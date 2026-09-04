package serialdev

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// ErrPinMismatch means the attached device is not the one previously approved
// under this name. It is always fatal rather than a warning: a swapped device
// is the exact case pinning exists to catch.
var ErrPinMismatch = errors.New("serial device does not match its pin")

// Pin records the identity a device name was first approved with.
type Pin struct {
	Name     string `json:"name"`
	VID      string `json:"vid"`
	PID      string `json:"pid"`
	Serial   string `json:"serial,omitempty"`
	PortPath string `json:"port_path,omitempty"`
	// Interface is the UART index of a multi-interface bridge (FT2232H,
	// CP2105). Empty on pins written before the field existed, which skips
	// the interface check rather than demanding "0": CDC modems expose their
	// single tty on interface 1.
	Interface string    `json:"interface,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
}

// PinFor builds the pin recorded on first approval of d under name.
func PinFor(name string, d Device) Pin {
	return Pin{
		Name:      name,
		VID:       strings.ToLower(d.VID),
		PID:       strings.ToLower(d.PID),
		Serial:    d.Serial,
		PortPath:  d.PortPath,
		Interface: d.Interface,
		FirstSeen: time.Now().UTC(),
	}
}

// PinnedByPortPath reports whether this pin identifies its device only by
// physical port, which happens when the device ships without a serial number
// (common on cheap CH340 and CP2102 clones).
//
// Callers must surface this in the consent prompt: such a pin approves whatever
// is plugged into that port, not that particular device.
func (p Pin) PinnedByPortPath() bool { return p.Serial == "" }

// Verify checks that d is the device this pin was created for.
func (p Pin) Verify(d Device) error {
	if !strings.EqualFold(p.VID, d.VID) || !strings.EqualFold(p.PID, d.PID) {
		return fmt.Errorf("%w: %q was pinned to USB ID %s:%s but the attached device is %s:%s\n"+
			"  If you intended to swap devices, run: moat device forget %s",
			ErrPinMismatch, p.Name, p.VID, p.PID,
			strings.ToLower(d.VID), strings.ToLower(d.PID), p.Name)
	}
	// The interface distinguishes the UARTs of a multi-interface bridge — one
	// plug, two ports, everything else identical. Only pins recorded with a
	// discriminator enforce it: a pin with no interface value predates the
	// field, and CDC modems legitimately expose their tty on interface 1, so
	// treating an empty value as "0" would fail hardware the pin was created
	// against.
	if p.Interface != "" && !sameInterface(p.Interface, d.Interface) {
		return fmt.Errorf("%w: %q was pinned to interface %s but the attached device is interface %s\n"+
			"  Two ports on one bridge must be declared as separate devices, each pinning its own interface\n"+
			"  If you intended to move it, run: moat device forget %s",
			ErrPinMismatch, p.Name, normalizeInterface(p.Interface), normalizeInterface(d.Interface), p.Name)
	}
	if p.Serial != "" {
		if p.Serial != d.Serial {
			observed := d.Serial
			if observed == "" {
				observed = "(none)"
			}
			return fmt.Errorf("%w: %q was pinned to serial %s but the attached device reports %s\n"+
				"  If you intended to swap devices, run: moat device forget %s",
				ErrPinMismatch, p.Name, p.Serial, observed, p.Name)
		}
		return nil
	}
	// No serial to compare against: the physical port is the only identity the
	// device has, so moving it to another port is indistinguishable from a swap.
	// A pin with neither serial nor port pins nothing at all — it would approve
	// any device with the right USB ID, so it fails closed instead.
	if p.PortPath == "" {
		return fmt.Errorf("%w: %q has neither a serial number nor a port to verify against — "+
			"the pin is incomplete and cannot approve any device\n"+
			"  Run: moat device forget %s, then run again to re-pin the device",
			ErrPinMismatch, p.Name, p.Name)
	}
	if p.PortPath != d.PortPath {
		return fmt.Errorf("%w: %q has no serial number, so it was pinned to port %s, "+
			"but a matching device is now on port %s\n"+
			"  Plug it back into the original port, or run: moat device forget %s",
			ErrPinMismatch, p.Name, p.PortPath, d.PortPath, p.Name)
	}
	return nil
}

// PinStore persists pins as JSON. It is safe for concurrent use within a
// process, and across processes via an advisory lock on a sidecar lock file:
// two moat processes approving devices concurrently would otherwise race —
// each reads the file at open, each writes its own map back, and the second
// write drops the first's pins, silently re-arming trust-on-first-use for
// every device the loser had pinned.
type PinStore struct {
	path string
	mu   sync.Mutex
	pins map[string]Pin
	lf   *os.File // held for the store's lifetime; flock'd per mutation
}

// DefaultPinPath is where pins live under the moat home directory. MOAT_HOME
// overrides the location the same way it does for every other moat state.
func DefaultPinPath() string {
	if override := os.Getenv("MOAT_HOME"); override != "" {
		return filepath.Join(override, "devices.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "devices.json"
	}
	return filepath.Join(home, ".moat", "devices.json")
}

// lock takes the store's mutex and the cross-process flock, reloading pins
// from disk so the caller mutates the latest state rather than a snapshot
// taken at OpenPinStore time. The returned func releases both.
func (s *PinStore) lock() (func(), error) {
	s.mu.Lock()
	var unlock func()
	if s.lf != nil {
		u, err := lockFile(s.lf)
		if err != nil {
			s.mu.Unlock()
			return nil, fmt.Errorf("locking device pins at %s: %w", s.path+".lock", err)
		}
		unlock = u
	}
	if err := s.reloadLocked(); err != nil {
		if unlock != nil {
			unlock()
		}
		s.mu.Unlock()
		return nil, err
	}
	return func() {
		if unlock != nil {
			unlock()
		}
		s.mu.Unlock()
	}, nil
}

// reloadLocked re-reads the pin file into the map. Callers hold the lock.
func (s *PinStore) reloadLocked() error {
	pins, err := readPins(s.path)
	if err != nil {
		return err
	}
	s.pins = pins
	return nil
}

// readPins parses the pin file at path, tolerating absence and emptiness.
func readPins(path string) (map[string]Pin, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]Pin{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading device pins from %s: %w", path, err)
	}
	if len(data) == 0 {
		return map[string]Pin{}, nil
	}
	var pins []Pin
	if err := json.Unmarshal(data, &pins); err != nil {
		return nil, fmt.Errorf("parsing device pins in %s: %w\n"+
			"  Fix the file by hand, or delete it to re-approve devices from scratch", path, err)
	}
	m := make(map[string]Pin, len(pins))
	for _, p := range pins {
		m[p.Name] = p
	}
	return m, nil
}

// OpenPinStore loads the store at path, creating an empty one if absent.
//
// A malformed file is an error rather than an empty store: silently discarding
// pins would silently disable the approval check.
//
// The store keeps the file's lock sidecar open so mutations can flock it; a
// store whose lock file cannot be opened still works single-process (lf is
// nil, lockFile is skipped) rather than failing every run.
func OpenPinStore(path string) (*PinStore, error) {
	s := &PinStore{path: path}
	pins, err := readPins(path)
	if err != nil {
		return nil, err
	}
	s.pins = pins

	// The lock file is separate from the data so a crash mid-write of either
	// never corrupts the other, and so its permissions say "lock" not "pins".
	dir := filepath.Dir(path)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, mkErr)
	}
	lf, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		// Not fatal: single-process use (the overwhelmingly common case —
		// one CLI, one daemon) still works, and erroring here would block
		// every run on a directory the user can fix.
		log.Debug("opening device pin lock file failed; continuing without cross-process lock", "path", path+".lock", "err", err)
		return s, nil
	}
	s.lf = lf
	return s, nil
}

// Close releases the lock file. The store is unusable after Close.
func (s *PinStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lf != nil {
		err := s.lf.Close()
		s.lf = nil
		return err
	}
	return nil
}

// Get returns the pin recorded for name, if any.
func (s *PinStore) Get(name string) (Pin, bool, error) {
	unlock, err := s.lock()
	if err != nil {
		return Pin{}, false, err
	}
	defer unlock()
	p, ok := s.pins[name]
	return p, ok, nil
}

// List returns all pins, for `moat device list`.
func (s *PinStore) List() ([]Pin, error) {
	unlock, err := s.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	return s.snapshotLocked(), nil
}

// Put records a pin and flushes the store.
func (s *PinStore) Put(p Pin) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	s.pins[p.Name] = p
	return s.flushLocked()
}

// Forget removes a pin so the next run re-approves the device. Forgetting an
// unknown name is not an error.
func (s *PinStore) Forget(name string) error {
	unlock, err := s.lock()
	if err != nil {
		return err
	}
	defer unlock()
	delete(s.pins, name)
	return s.flushLocked()
}

func (s *PinStore) snapshotLocked() []Pin {
	out := make([]Pin, 0, len(s.pins))
	for _, p := range s.pins {
		out = append(out, p)
	}
	return out
}

func (s *PinStore) flushLocked() error {
	data, err := json.MarshalIndent(s.snapshotLocked(), "", "  ")
	if err != nil {
		return fmt.Errorf("encoding device pins: %w", err)
	}
	dir := filepath.Dir(s.path)
	if mkErr := os.MkdirAll(dir, 0o700); mkErr != nil {
		return fmt.Errorf("creating %s: %w", dir, mkErr)
	}
	// Write-then-rename so a crash cannot leave a half-written pin file, which
	// would fail to parse on the next run and block every device.
	// O_EXCL plus noFollow (unix): a pre-symlinked .tmp must not be followed
	// to its victim (os.WriteFile would), and a stale .tmp from a crashed run
	// must not be silently reused.
	tmp := s.path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY|noFollow, 0o600)
	if err != nil {
		return fmt.Errorf("writing device pins: %w", err)
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("writing device pins: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("writing device pins: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("replacing device pins at %s: %w", s.path, err)
	}
	return nil
}
