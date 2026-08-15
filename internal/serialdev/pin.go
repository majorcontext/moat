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
)

// ErrPinMismatch means the attached device is not the one previously approved
// under this name. It is always fatal rather than a warning: a swapped device
// is the exact case pinning exists to catch.
var ErrPinMismatch = errors.New("serial device does not match its pin")

// Pin records the identity a device name was first approved with.
type Pin struct {
	Name      string    `json:"name"`
	VID       string    `json:"vid"`
	PID       string    `json:"pid"`
	Serial    string    `json:"serial,omitempty"`
	PortPath  string    `json:"port_path,omitempty"`
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
	if p.PortPath != d.PortPath {
		return fmt.Errorf("%w: %q has no serial number, so it was pinned to port %s, "+
			"but a matching device is now on port %s\n"+
			"  Plug it back into the original port, or run: moat device forget %s",
			ErrPinMismatch, p.Name, p.PortPath, d.PortPath, p.Name)
	}
	return nil
}

// PinStore persists pins as JSON. It is safe for concurrent use.
type PinStore struct {
	path string
	mu   sync.Mutex
	pins map[string]Pin
}

// DefaultPinPath is where pins live under the moat home directory.
func DefaultPinPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "devices.json"
	}
	return filepath.Join(home, ".moat", "devices.json")
}

// OpenPinStore loads the store at path, creating an empty one if absent.
//
// A malformed file is an error rather than an empty store: silently discarding
// pins would silently disable the approval check.
func OpenPinStore(path string) (*PinStore, error) {
	s := &PinStore{path: path, pins: map[string]Pin{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("reading device pins from %s: %w", path, err)
	}
	if len(data) == 0 {
		return s, nil
	}
	var pins []Pin
	if err := json.Unmarshal(data, &pins); err != nil {
		return nil, fmt.Errorf("parsing device pins in %s: %w\n"+
			"  Fix the file by hand, or delete it to re-approve devices from scratch", path, err)
	}
	for _, p := range pins {
		s.pins[p.Name] = p
	}
	return s, nil
}

// Get returns the pin recorded for name, if any.
func (s *PinStore) Get(name string) (Pin, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.pins[name]
	return p, ok, nil
}

// List returns all pins, for `moat device list`.
func (s *PinStore) List() ([]Pin, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked(), nil
}

// Put records a pin and flushes the store.
func (s *PinStore) Put(p Pin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins[p.Name] = p
	return s.flushLocked()
}

// Forget removes a pin so the next run re-approves the device. Forgetting an
// unknown name is not an error.
func (s *PinStore) Forget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
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
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	// Write-then-rename so a crash cannot leave a half-written pin file, which
	// would fail to parse on the next run and block every device.
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing device pins: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("replacing device pins at %s: %w", s.path, err)
	}
	return nil
}
