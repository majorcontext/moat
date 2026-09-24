# Serial Device Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let an agent inside a moat container talk to an approved serial device on the host (ESP32, Arduino, UART adapters) through a broker that enforces device identity, without privileged mode or disabling the gVisor sandbox.

**Architecture:** A host-side broker owns the real tty and serves a framed protocol over a TCP listener on the proxy daemon, authenticated with the run's existing auth token. A small binary inside the container creates a pty pair, exposes the slave at a stable path, and forwards data plus termios/modem-control changes to the broker. Device approval is trust-on-first-use: the first run pins VID/PID/serial, later runs must match.

**Tech Stack:** Go 1.25, `golang.org/x/sys/unix` for termios/ioctl, existing `internal/daemon` (proxy daemon + run registration), `internal/audit` (hash-chained audit store), `internal/storage` (per-run jsonl).

**Spec:** `docs/plans/2026-08-15-serial-devices-design.md`

## Global Constraints

- **Daemon API is additive-only.** New fields on `daemon.RegisterRequest`/`RunContext` must be `omitempty`; no renames, no removals. New endpoints must 404 gracefully on old daemons. See the package doc in `internal/daemon/api.go`.
- **New daemon behaviour needs a capability gate.** Add a `Cap*` constant next to `CapKeepPolicy`/`CapHostGatewayV2` in `internal/daemon/api.go` and check it CLI-side before relying on the serial endpoint.
- **Detector ↔ validator parity** (codebase invariant #2). `run.DetectMissingDevices` must classify exactly what the `Create` gate rejects, with a drift-guard test asserting *both* directions.
- **Companion-case tests** (codebase invariant #1). Every one-directional assertion needs its mirror: match succeeds *and* mismatch fails; missing serial falls back *and* changed port path fails.
- **No hardware in automated tests.** Every test uses a pty-backed fake device. Hardware tests skip unless `MOAT_SERIAL_TEST_DEVICE` is set.
- **`ui` vs `log`:** user-facing warnings/errors via `internal/ui`; diagnostics via `internal/log`. Never `ui` styling inside `tabwriter`.
- **Commits:** Conventional Commits, no `Co-Authored-By` lines.
- Run `make lint` and `make test-unit` before each commit.

## File Structure

| Path | Responsibility |
|---|---|
| `internal/serialdev/device.go` | `Device`, `Identity`, matching |
| `internal/serialdev/enumerate_linux.go` | sysfs enumeration |
| `internal/serialdev/enumerate_darwin.go` | IOKit/ioreg enumeration |
| `internal/serialdev/pin.go` | TOFU pin store (`~/.moat/devices.json`) |
| `internal/rfc2217/telnet.go`, `internal/rfc2217/comport.go` | RFC2217 telnet com-port-control codec |
| `internal/serialbroker/port.go` | `Port` interface, real tty implementation |
| `internal/serialbroker/broker.go` | claims, sessions, listener, token auth |
| `internal/serialtest/fake.go` | pty-backed fake `Port` + fake `Enumerator` |
| `cmd/moat-serial/main.go` | container-side pty bridge |
| `internal/serialbin/` | `go:embed` of prebuilt bridge binaries |
| `internal/config/devices.go` | `devices:` parsing + validation |
| `internal/run/devices.go` | pre-flight detection + wiring |
| `cmd/moat/cli/device.go` | `moat device list` / `forget` |

---

### Task 0: Spike — verify pty packet mode ✅ DONE (2026-08-15) — RESULT: pty approach rejected

**Outcome, measured on Linux 6.12:**

```
RESULT packet-mode-accepted=yes
RESULT baud-change-notifies=no
RESULT ixon-change-notifies=no
RESULT slave-tcgets-readable=yes ... match=true
RESULT slave-tiocmget=no err: inappropriate ioctl for device
```

`TIOCPKT` is accepted but Linux never fires `TIOCPKT_IOCTL` on termios changes, and a pty
has no modem control lines at all (`TIOCMGET` → `ENOTTY`). DTR/RTS — i.e. ESP32 auto-reset
— cannot be carried by a pty by any mechanism.

**Consequence, already applied to the design doc and to Tasks 4, 6, and 8 below:** the
broker speaks **RFC2217** (telnet com-port-control), which carries baud, DTR/RTS, and
break. Two front ends over that one protocol:

- `rfc2217://<gateway>:<port>` — full control lines, flashes boards
- `/dev/moat/serial/<name>` — console pty, served by an in-container RFC2217 *client*;
  forwards data and polled baud changes, cannot forward DTR/RTS

The steps below are retained only as the record of what was probed. Do not re-run them.

<details>
<summary>Original spike steps</summary>

#### Original: verify pty packet mode under gVisor and Apple container

The entire design assumes the container-side bridge can observe termios changes made by tools on the pty slave. If `TIOCPKT` is unsupported in a sandboxed guest, the fallback (polling the slave's termios) must be chosen now, not after six tasks are built on it. This repo has already lost work to an unverified in-container kernel-feature assumption (in-container Landlock silently no-ops on both gVisor and Apple).

**Files:**
- Create: `/tmp/ptyprobe/main.go` (throwaway — do not commit)

- [ ] **Step 1: Write the probe**

```go
package main

import (
	"fmt"
	"os"
	"golang.org/x/sys/unix"
)

func main() {
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		fmt.Println("FAIL open ptmx:", err)
		os.Exit(1)
	}
	defer m.Close()

	// Enable packet mode on the master.
	one := 1
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCPKT, one); err != nil {
		fmt.Println("FAIL TIOCPKT unsupported:", err)
		os.Exit(1)
	}
	fmt.Println("OK TIOCPKT accepted")

	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		fmt.Println("FAIL TIOCGPTN:", err)
		os.Exit(1)
	}
	if err := unix.IoctlSetPointerInt(int(m.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		fmt.Println("FAIL unlock:", err)
		os.Exit(1)
	}

	s, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", n), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		fmt.Println("FAIL open slave:", err)
		os.Exit(1)
	}
	defer s.Close()

	// Change baud on the slave; the master should see a TIOCPKT_IOCTL notification.
	t, err := unix.IoctlGetTermios(int(s.Fd()), unix.TCGETS)
	if err != nil {
		fmt.Println("FAIL TCGETS:", err)
		os.Exit(1)
	}
	t.Cflag = (t.Cflag &^ uint32(unix.CBAUD)) | uint32(unix.B115200)
	if err := unix.IoctlSetTermios(int(s.Fd()), unix.TCSETS, t); err != nil {
		fmt.Println("FAIL TCSETS:", err)
		os.Exit(1)
	}

	buf := make([]byte, 128)
	nr, err := m.Read(buf)
	if err != nil {
		fmt.Println("FAIL master read:", err)
		os.Exit(1)
	}
	if nr > 0 && buf[0] == unix.TIOCPKT_IOCTL {
		fmt.Println("OK TIOCPKT_IOCTL delivered — packet mode works")
		return
	}
	fmt.Printf("FAIL expected TIOCPKT_IOCTL (%d), got %d\n", unix.TIOCPKT_IOCTL, buf[0])
	os.Exit(1)
}
```

- [ ] **Step 2: Run it on the host (baseline)**

Run: `cd /tmp/ptyprobe && go mod init ptyprobe && go get golang.org/x/sys/unix && go run .`
Expected: `OK TIOCPKT_IOCTL delivered`

- [ ] **Step 3: Run it under gVisor**

Run: `GOOS=linux go build -o ptyprobe . && docker run --rm --runtime=runsc -v /tmp/ptyprobe/ptyprobe:/probe debian:bookworm-slim /probe`
Expected: `OK TIOCPKT_IOCTL delivered`

- [ ] **Step 4: Run it inside an Apple container (macOS host only)**

Run: `moat run --runtime apple -- /probe` with the binary mounted, or `container run` directly.
Expected: `OK TIOCPKT_IOCTL delivered`

- [ ] **Step 5: Record the outcome in the design doc**

If any target fails, add a "Termios observation" section to
`docs/plans/2026-08-15-serial-devices-design.md` selecting the polling fallback:
the bridge opens the slave itself and polls `TCGETS` every 20ms, diffing against the
last-known termios and emitting a `TERMIOS` frame on change. Task 8 then implements
polling instead of packet mode. **Do not commit the probe.**

</details>

---

### Task 1: Device identity and matching

**Files:**
- Create: `internal/serialdev/device.go`
- Test: `internal/serialdev/device_test.go`

**Interfaces:**
- Produces: `Device{Path, VID, PID, Serial, PortPath, Description string}`, `Matcher{VID, PID string}`, `func (d Device) Identity() Identity`, `Identity{VID, PID, Serial, PortPath string}`, `func Match(devs []Device, m Matcher) ([]Device, error)`, `ErrNoMatch`, `ErrAmbiguous`.

- [ ] **Step 1: Write the failing test**

```go
package serialdev

import (
	"errors"
	"testing"
)

func devs() []Device {
	return []Device{
		{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"},
		{Path: "/dev/ttyUSB1", VID: "303a", PID: "1001", Serial: "BBB", PortPath: "1-3"},
		{Path: "/dev/ttyACM0", VID: "2341", PID: "0043", Serial: "CCC", PortPath: "1-4"},
	}
}

func TestMatchSingle(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "2341", PID: "0043"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].Serial != "CCC" {
		t.Fatalf("got %+v, want the 2341:0043 device", got)
	}
}

func TestMatchAmbiguousReturnsAllCandidates(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "303a", PID: "1001"})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 so the caller can list them", len(got))
	}
}

func TestMatchNone(t *testing.T) {
	if _, err := Match(devs(), Matcher{VID: "dead", PID: "beef"}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}

func TestMatchIsCaseInsensitiveOnHexIDs(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "303A", PID: "1001"})
	if !errors.Is(err, ErrAmbiguous) || len(got) != 2 {
		t.Fatalf("uppercase VID should match lowercase: got %+v err %v", got, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serialdev/ -run TestMatch -v`
Expected: FAIL — package does not compile, `Match` undefined.

- [ ] **Step 3: Implement**

```go
// Package serialdev enumerates host serial devices and records which ones a
// run has been approved to use.
package serialdev

import (
	"errors"
	"fmt"
	"strings"
)

var (
	// ErrNoMatch means no attached device matched the config's vid/pid.
	ErrNoMatch = errors.New("no matching serial device")
	// ErrAmbiguous means several devices matched. Match still returns the
	// candidates so callers can name them in the error shown to the user.
	ErrAmbiguous = errors.New("multiple matching serial devices")
)

// Device is a serial port attached to the host.
type Device struct {
	Path        string // host device node, e.g. /dev/ttyUSB0 or /dev/cu.usbserial-14200
	VID         string // 4-hex-digit USB vendor ID, lowercase
	PID         string // 4-hex-digit USB product ID, lowercase
	Serial      string // USB iSerial, empty on devices that ship without one
	PortPath    string // physical topology: sysfs port path (Linux) or IOKit locationID (macOS)
	Description string // human label for `moat device list`
}

// Identity is the subset of a Device used for approval decisions.
type Identity struct {
	VID      string
	PID      string
	Serial   string
	PortPath string
}

// Identity returns the device's approval identity.
func (d Device) Identity() Identity {
	return Identity{VID: d.VID, PID: d.PID, Serial: d.Serial, PortPath: d.PortPath}
}

// Matcher selects devices by USB vendor and product ID.
type Matcher struct {
	VID string
	PID string
}

// Match returns devices matching m. Hex IDs compare case-insensitively so a
// config written as "303A" matches a device enumerated as "303a".
//
// On ambiguity it returns every candidate alongside ErrAmbiguous — the caller
// needs them to tell the user which devices collided.
func Match(devs []Device, m Matcher) ([]Device, error) {
	vid, pid := strings.ToLower(m.VID), strings.ToLower(m.PID)
	var out []Device
	for _, d := range devs {
		if strings.ToLower(d.VID) == vid && strings.ToLower(d.PID) == pid {
			out = append(out, d)
		}
	}
	switch len(out) {
	case 0:
		return nil, fmt.Errorf("%w for %s:%s", ErrNoMatch, vid, pid)
	case 1:
		return out, nil
	default:
		return out, fmt.Errorf("%w for %s:%s", ErrAmbiguous, vid, pid)
	}
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/serialdev/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/serialdev/
git commit -m "feat(serial): add device identity and vid/pid matching"
```

---

### Task 2: TOFU pin store

**Files:**
- Create: `internal/serialdev/pin.go`
- Test: `internal/serialdev/pin_test.go`

**Interfaces:**
- Consumes: `Device`, `Identity` from Task 1.
- Produces: `Pin{Name, VID, PID, Serial, PortPath string, FirstSeen time.Time}`, `PinStore`, `func OpenPinStore(path string) (*PinStore, error)`, `func (s *PinStore) Get(name string) (Pin, bool, error)`, `func (s *PinStore) Put(p Pin) error`, `func (s *PinStore) Forget(name string) error`, `func (s *PinStore) List() ([]Pin, error)`, `func (p Pin) Verify(d Device) error`, `func PinFor(name string, d Device) Pin`, `ErrPinMismatch`, `func DefaultPinPath() string`.

Verification rules, which the tests below pin down in both directions:
- Pin has a serial → the device's serial must match; port path is ignored (replugging into another port is fine).
- Pin has no serial (device shipped without one) → the port path must match, because "the thing plugged into that port" is the only identity available.
- VID/PID must always match.

- [ ] **Step 1: Write the failing test**

```go
package serialdev

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestPinVerifyMatchingSerialSucceeds(t *testing.T) {
	d := Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	p := PinFor("esp32", d)
	// Same device, different physical port: serial identity wins.
	moved := Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-9"}
	if err := p.Verify(moved); err != nil {
		t.Fatalf("replugged device with same serial should verify: %v", err)
	}
}

func TestPinVerifyDifferentSerialFails(t *testing.T) {
	p := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})
	swapped := Device{VID: "303a", PID: "1001", Serial: "BBB", PortPath: "1-2"}
	err := p.Verify(swapped)
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
	// The message must name both serials — it is the user's only clue.
	if got := err.Error(); !contains(got, "AAA") || !contains(got, "BBB") {
		t.Fatalf("error %q must name both the pinned and observed serial", got)
	}
}

func TestPinVerifyNoSerialFallsBackToPortPath(t *testing.T) {
	d := Device{VID: "1a86", PID: "7523", Serial: "", PortPath: "1-2"}
	p := PinFor("ch340", d)
	if p.Serial != "" {
		t.Fatal("pin should not invent a serial")
	}
	if err := p.Verify(d); err != nil {
		t.Fatalf("same port should verify: %v", err)
	}
}

func TestPinVerifyNoSerialDifferentPortFails(t *testing.T) {
	p := PinFor("ch340", Device{VID: "1a86", PID: "7523", PortPath: "1-2"})
	moved := Device{VID: "1a86", PID: "7523", PortPath: "1-7"}
	if err := p.Verify(moved); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("serial-less device moved to another port must fail: %v", err)
	}
}

func TestPinVerifyDifferentVIDPIDFails(t *testing.T) {
	p := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA"})
	if err := p.Verify(Device{VID: "2341", PID: "0043", Serial: "AAA"}); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("different vid/pid must fail even with a matching serial: %v", err)
	}
}

func TestPinStoreRoundTripAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenPinStore(path)
	if err != nil {
		t.Fatalf("OpenPinStore: %v", err)
	}
	if _, ok, err := s.Get("esp32"); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v, want ok=false", ok, err)
	}
	want := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})
	if err := s.Put(want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	reopened, err := OpenPinStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := reopened.Get("esp32")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if got.Serial != "AAA" || got.VID != "303a" {
		t.Fatalf("got %+v, want the pin written before reopen", got)
	}
	if got.FirstSeen.IsZero() {
		t.Fatal("FirstSeen must survive the round trip")
	}

	if err := reopened.Forget("esp32"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, ok, _ := reopened.Get("esp32"); ok {
		t.Fatal("pin still present after Forget")
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	}())
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serialdev/ -run TestPin -v`
Expected: FAIL — `PinFor`, `OpenPinStore`, `ErrPinMismatch` undefined.

- [ ] **Step 3: Implement**

```go
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
// under this name. It is always fatal: a swapped device is the exact case
// pinning exists to catch.
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
// physical port, which happens when the device ships without a serial number.
// Callers must say so in the consent prompt: it pins whatever is plugged into
// that port, not that particular device.
func (p Pin) PinnedByPortPath() bool { return p.Serial == "" }

// Verify checks that d is the device this pin was created for.
func (p Pin) Verify(d Device) error {
	if !strings.EqualFold(p.VID, d.VID) || !strings.EqualFold(p.PID, d.PID) {
		return fmt.Errorf("%w: %q was pinned to USB ID %s:%s but the attached device is %s:%s",
			ErrPinMismatch, p.Name, p.VID, p.PID, strings.ToLower(d.VID), strings.ToLower(d.PID))
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
	// No serial to compare: the physical port is the only identity available.
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

// Get returns the pin for name.
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
	out := make([]Pin, 0, len(s.pins))
	for _, p := range s.pins {
		out = append(out, p)
	}
	return out, nil
}

// Put records a pin and flushes the store.
func (s *PinStore) Put(p Pin) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pins[p.Name] = p
	return s.flushLocked()
}

// Forget removes a pin so the next run re-approves the device.
func (s *PinStore) Forget(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pins, name)
	return s.flushLocked()
}

func (s *PinStore) flushLocked() error {
	out := make([]Pin, 0, len(s.pins))
	for _, p := range s.pins {
		out = append(out, p)
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding device pins: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(s.path), err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("writing device pins: %w", err)
	}
	return os.Rename(tmp, s.path)
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/serialdev/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/serialdev/
git commit -m "feat(serial): add trust-on-first-use device pin store"
```

---

### Task 3: Host device enumeration

**Files:**
- Create: `internal/serialdev/enumerate.go`, `internal/serialdev/enumerate_linux.go`, `internal/serialdev/enumerate_darwin.go`
- Test: `internal/serialdev/enumerate_linux_test.go`

**Interfaces:**
- Produces: `type Enumerator interface { List(ctx context.Context) ([]Device, error) }`, `func NewEnumerator() Enumerator`, `func enumerateSysfs(root string) ([]Device, error)` (Linux, root injectable for tests).

Linux reads `/sys/class/tty/*/device` and walks up to the USB interface's parent to read `idVendor`, `idProduct`, `serial`. macOS shells out to `ioreg -r -c IOSerialBSDClient -a` and parses the plist for `IODialinDevice`, `idVendor`, `idProduct`, `USB Serial Number`, `locationID`. Only the Linux path is unit-tested with a fake sysfs tree; macOS parsing is tested against a captured `ioreg` fixture.

- [ ] **Step 1: Write the failing test**

```go
//go:build linux

package serialdev

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs builds the subset of sysfs the enumerator reads:
//   /sys/class/tty/ttyUSB0/device -> ../../../1-2:1.0
//   /sys/devices/.../1-2/{idVendor,idProduct,serial}
func fakeSysfs(t *testing.T, serial string) string {
	t.Helper()
	root := t.TempDir()
	usbDev := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2")
	iface := filepath.Join(usbDev, "1-2:1.0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		if err := os.WriteFile(filepath.Join(usbDev, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("idVendor", "303a")
	write("idProduct", "1001")
	if serial != "" {
		write("serial", serial)
	}
	ttyDir := filepath.Join(root, "class", "tty", "ttyUSB0")
	if err := os.MkdirAll(ttyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(iface, filepath.Join(ttyDir, "device")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEnumerateSysfsReadsUSBIdentity(t *testing.T) {
	got, err := enumerateSysfs(fakeSysfs(t, "AAA"))
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.Path != "/dev/ttyUSB0" || d.VID != "303a" || d.PID != "1001" || d.Serial != "AAA" {
		t.Fatalf("got %+v, want /dev/ttyUSB0 303a:1001 AAA", d)
	}
	if d.PortPath != "1-2" {
		t.Fatalf("PortPath = %q, want the USB port path 1-2", d.PortPath)
	}
}

func TestEnumerateSysfsSerialLessDeviceStillEnumerates(t *testing.T) {
	got, err := enumerateSysfs(fakeSysfs(t, ""))
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1", len(got))
	}
	if got[0].Serial != "" {
		t.Fatalf("Serial = %q, want empty for a device without one", got[0].Serial)
	}
	if got[0].PortPath != "1-2" {
		t.Fatalf("PortPath = %q, want 1-2 — it is the only identity such a device has", got[0].PortPath)
	}
}

func TestEnumerateSysfsSkipsNonUSBTTYs(t *testing.T) {
	root := fakeSysfs(t, "AAA")
	// A console tty with no USB parent must not appear.
	if err := os.MkdirAll(filepath.Join(root, "class", "tty", "ttyS0"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	for _, d := range got {
		if d.Path == "/dev/ttyS0" {
			t.Fatal("non-USB tty must not be enumerated")
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serialdev/ -run TestEnumerateSysfs -v`
Expected: FAIL — `enumerateSysfs` undefined.

- [ ] **Step 3: Implement `enumerate.go` and `enumerate_linux.go`**

```go
// enumerate.go
package serialdev

import "context"

// Enumerator lists serial devices attached to the host. Tests substitute a fake.
type Enumerator interface {
	List(ctx context.Context) ([]Device, error)
}
```

```go
//go:build linux

// enumerate_linux.go
package serialdev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type sysfsEnumerator struct{ root string }

// NewEnumerator returns the host's serial device enumerator.
func NewEnumerator() Enumerator { return &sysfsEnumerator{root: "/sys"} }

func (e *sysfsEnumerator) List(_ context.Context) ([]Device, error) {
	return enumerateSysfs(e.root)
}

// enumerateSysfs walks /sys/class/tty and returns only ttys backed by USB.
// root is injectable so tests can build a fake tree.
func enumerateSysfs(root string) ([]Device, error) {
	entries, err := os.ReadDir(filepath.Join(root, "class", "tty"))
	if err != nil {
		return nil, fmt.Errorf("listing serial devices: %w", err)
	}
	var out []Device
	for _, e := range entries {
		name := e.Name()
		devLink := filepath.Join(root, "class", "tty", name, "device")
		target, err := filepath.EvalSymlinks(devLink)
		if err != nil {
			continue // not a physical device (ptys, console)
		}
		usbDir, portPath, ok := usbParent(target)
		if !ok {
			continue // not USB-backed
		}
		vid := readAttr(usbDir, "idVendor")
		pid := readAttr(usbDir, "idProduct")
		if vid == "" || pid == "" {
			continue
		}
		out = append(out, Device{
			Path:        "/dev/" + name,
			VID:         strings.ToLower(vid),
			PID:         strings.ToLower(pid),
			Serial:      readAttr(usbDir, "serial"),
			PortPath:    portPath,
			Description: strings.TrimSpace(readAttr(usbDir, "product")),
		})
	}
	return out, nil
}

// usbParent walks up from a tty's device directory to the USB device that owns
// it, returning that directory and its port path (the directory's base name,
// e.g. "1-2" or "1-2.3").
func usbParent(dir string) (string, string, bool) {
	for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "idVendor")); err == nil {
			return d, filepath.Base(d), true
		}
	}
	return "", "", false
}

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/serialdev/ -v`
Expected: PASS

- [ ] **Step 5: Implement the macOS enumerator**

Create `enumerate_darwin.go` with `//go:build darwin`. Run `ioreg -r -c IOSerialBSDClient -a` (XML plist), decode with `howett.net/plist` if already vendored, otherwise parse the `-a`-free `ioreg -r -c IOSerialBSDClient` key/value output with a regexp per key. Map: `IODialinDevice` → `Path`, `idVendor`/`idProduct` (decimal in ioreg) → 4-digit lowercase hex `VID`/`PID`, `USB Serial Number` → `Serial`, `locationID` → `PortPath` formatted as `0x%08x`.

Capture a real `ioreg` dump to `internal/serialdev/testdata/ioreg.txt` and add `TestParseIoregExtractsIdentity` plus its companion `TestParseIoregSerialLessDevice` asserting `Serial == ""` and a non-empty `PortPath`.

- [ ] **Step 6: Run tests and commit**

```bash
go test ./internal/serialdev/ -v && make lint
git add internal/serialdev/
git commit -m "feat(serial): enumerate host USB serial devices on linux and macos"
```

---

### Task 4: RFC2217 codec

**Files:**
- Create: `internal/rfc2217/telnet.go`, `internal/rfc2217/comport.go`
- Test: `internal/rfc2217/telnet_test.go`, `internal/rfc2217/comport_test.go`

RFC2217 layers a com-port-control option on telnet. Two things have to be right, and both
are easy to get subtly wrong:

1. **Telnet escaping.** `0xFF` (IAC) in the data stream must be doubled, and un-doubled on
   receive. Miss this and binary firmware uploads corrupt at exactly the byte that appears
   most often in erased flash — a bug that looks like flaky hardware.
2. **Command framing.** `IAC SB COM-PORT-OPTION <cmd> <payload> IAC SE`, where the
   client-to-server option is 44 (`0x2C`) and server-to-client replies add 100 to the
   command byte.

**Interfaces:**
- Produces: `func EscapeIAC(dst, src []byte) []byte`, `func NewReader(io.Reader) *Reader`
  with `func (r *Reader) Read([]byte) (int, error)` (un-escapes data, dispatches commands
  via `r.OnCommand func(cmd byte, payload []byte)`), `func WriteCommand(w io.Writer, cmd byte, payload []byte) error`;
  command constants `CmdSetBaudRate=1, CmdSetDataSize=2, CmdSetParity=3, CmdSetStopSize=4,
  CmdSetControl=5, CmdNotifyModemState=107, CmdFlowControlSuspend=8, CmdFlowControlResume=9`;
  control values `ControlBreakOn=5, ControlBreakOff=6, ControlDTROn=8, ControlDTROff=9,
  ControlRTSOn=11, ControlRTSOff=12`; `const OptionComPort = 44`.

- [ ] **Step 1: Write the failing test**

```go
package rfc2217

import (
	"bytes"
	"io"
	"testing"
)

func TestEscapeIACDoublesFFBytes(t *testing.T) {
	got := EscapeIAC(nil, []byte{0x01, 0xFF, 0x02})
	want := []byte{0x01, 0xFF, 0xFF, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEscapeIACLeavesOtherBytesAlone(t *testing.T) {
	src := []byte{0x00, 0x7F, 0xFE}
	if got := EscapeIAC(nil, src); !bytes.Equal(got, src) {
		t.Fatalf("got % x, want % x unchanged", got, src)
	}
}

func TestReaderUnescapesDoubledIAC(t *testing.T) {
	// Wire bytes 01 FF FF 02 represent the payload 01 FF 02.
	r := NewReader(bytes.NewReader([]byte{0x01, 0xFF, 0xFF, 0x02}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x01, 0xFF, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestReaderDispatchesCommandAndKeepsSurroundingData(t *testing.T) {
	// data "hi", then SET-CONTROL DTR-ON, then data "yo"
	wire := []byte{'h', 'i',
		0xFF, 0xFA, OptionComPort, CmdSetControl, ControlDTROn, 0xFF, 0xF0,
		'y', 'o'}
	var gotCmd byte
	var gotPayload []byte
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(cmd byte, payload []byte) {
		gotCmd = cmd
		gotPayload = append([]byte(nil), payload...)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "hiyo" {
		t.Fatalf("data = %q, want %q — command bytes must not leak into the stream", data, "hiyo")
	}
	if gotCmd != CmdSetControl {
		t.Fatalf("cmd = %d, want CmdSetControl", gotCmd)
	}
	if len(gotPayload) != 1 || gotPayload[0] != ControlDTROn {
		t.Fatalf("payload = % x, want [%02x]", gotPayload, ControlDTROn)
	}
}

func TestReaderHandlesEscapedIACInsideSubnegotiation(t *testing.T) {
	// A baud rate of 0xFF-containing value must survive: 115200 = 0x0001C200,
	// but 16711680 = 0x00FF0000 exercises the escape path inside a command.
	payload := []byte{0x00, 0xFF, 0xFF, 0x00, 0x00} // escaped 0x00FF0000
	wire := append([]byte{0xFF, 0xFA, OptionComPort, CmdSetBaudRate}, payload...)
	wire = append(wire, 0xFF, 0xF0)
	var got []byte
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(_ byte, p []byte) { got = append([]byte(nil), p...) }
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x00, 0xFF, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload = % x, want % x un-escaped", got, want)
	}
}

func TestWriteCommandFramesCorrectly(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommand(&buf, CmdSetBaudRate, []byte{0x00, 0x01, 0xC2, 0x00}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	want := []byte{0xFF, 0xFA, OptionComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00, 0xFF, 0xF0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteCommandEscapesIACInPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommand(&buf, CmdSetBaudRate, []byte{0x00, 0xFF, 0x00, 0x00}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	want := []byte{0xFF, 0xFA, OptionComPort, CmdSetBaudRate, 0x00, 0xFF, 0xFF, 0x00, 0x00, 0xFF, 0xF0}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestBaudRateRoundTrip(t *testing.T) {
	for _, baud := range []uint32{9600, 115200, 921600, 1500000} {
		if got := DecodeBaud(EncodeBaud(baud)); got != baud {
			t.Fatalf("round trip of %d gave %d", baud, got)
		}
	}
}

func TestControlValuesCoverDTRAndRTSBothWays(t *testing.T) {
	// Companion coverage: every signal must have both its on and off encoding,
	// because ESP32 auto-reset is a sequence of asserts *and* deasserts.
	cases := map[byte]string{
		ControlDTROn: "DTR on", ControlDTROff: "DTR off",
		ControlRTSOn: "RTS on", ControlRTSOff: "RTS off",
		ControlBreakOn: "break on", ControlBreakOff: "break off",
	}
	seen := map[byte]bool{}
	for v, name := range cases {
		if seen[v] {
			t.Fatalf("duplicate control value %d (%s)", v, name)
		}
		seen[v] = true
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/rfc2217/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement**

`telnet.go` holds `EscapeIAC`, `Reader`, and `WriteCommand`. The `Reader` is a small state
machine over the wire bytes: `stateData` → on `0xFF` go to `stateIAC`; in `stateIAC`, a
second `0xFF` emits one literal `0xFF` and returns to `stateData`, `0xFA` (SB) begins a
subnegotiation, `0xF0` (SE) ends one; inside a subnegotiation, `0xFF 0xFF` un-escapes to a
single `0xFF` in the payload. Commands for options other than `OptionComPort` are consumed
and ignored.

`comport.go` holds the command and control constants plus `EncodeBaud`/`DecodeBaud`
(4-byte big-endian).

```go
// Package rfc2217 implements the telnet com-port-control option (RFC 2217),
// which carries baud rate, data framing, DTR/RTS, and break over TCP.
//
// moat uses it because a pty cannot express control lines: TIOCMGET on a pty
// returns ENOTTY, so DTR/RTS — and therefore ESP32 auto-reset — have no pty
// representation. See docs/plans/2026-08-15-serial-devices-design.md.
package rfc2217

const (
	iac  = 0xFF // interpret as command
	sb   = 0xFA // subnegotiation begin
	se   = 0xF0 // subnegotiation end

	// OptionComPort is the client-to-server com-port-control option. Replies
	// from the server use the same command byte plus 100.
	OptionComPort = 44
)
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/rfc2217/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/rfc2217/
git commit -m "feat(serial): add RFC2217 telnet com-port-control codec"
```

---

### Task 5: Port abstraction and pty-backed fake

**Files:**
- Create: `internal/serialbroker/port.go`, `internal/serialbroker/port_unix.go`, `internal/serialtest/fake.go`
- Test: `internal/serialtest/fake_test.go`

**Interfaces:**
- Consumes: `internal/rfc2217` (Task 4).
- Also produces the shared line-setting types used by the broker and bridge: `type Settings struct { Baud uint32; DataBits, StopBits, Parity, FlowControl uint8 }`, `type Modem struct { DTR, RTS bool }`.
- Produces: `type Port interface { io.ReadWriteCloser; ApplySettings(Settings) error; SetModem(Modem) error; SendBreak() error; Name() string }`; `func OpenPort(path string) (Port, error)`; `serialtest.FakePort` implementing `Port` with `Peer() io.ReadWriter`, `LastSettings() Settings`, `LastModem() Modem`, `BreakCount() int`; `func serialtest.NewFakePort(t *testing.T) *FakePort`; `func serialtest.NewFakeEnumerator(devs ...serialdev.Device) serialdev.Enumerator`.

`OpenPort` must reject anything that is not a tty — that check *is* the device-class allowlist (storage, HID, and smartcards do not present as ttys), so there is no separate deny list to drift.

- [ ] **Step 1: Write the failing test**

```go
package serialtest_test

import (
	"testing"

	"github.com/majorcontext/moat/internal/serialbroker"
	"github.com/majorcontext/moat/internal/serialtest"
)

func TestFakePortCarriesDataBothWays(t *testing.T) {
	p := serialtest.NewFakePort(t)
	defer p.Close()

	if _, err := p.Write([]byte("to device")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := p.Peer().Read(buf)
	if err != nil {
		t.Fatalf("peer Read: %v", err)
	}
	if string(buf[:n]) != "to device" {
		t.Fatalf("peer got %q, want %q", buf[:n], "to device")
	}

	if _, err := p.Peer().Write([]byte("from device")); err != nil {
		t.Fatalf("peer Write: %v", err)
	}
	n, err = p.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "from device" {
		t.Fatalf("port got %q, want %q", buf[:n], "from device")
	}
}

func TestFakePortRecordsTermiosAndModem(t *testing.T) {
	p := serialtest.NewFakePort(t)
	defer p.Close()

	want := serialbroker.Settings{Baud: 921600, DataBits: 8, StopBits: 1}
	if err := p.ApplySettings(want); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	if got := p.LastSettings(); got != want {
		t.Fatalf("LastSettings = %+v, want %+v", got, want)
	}

	if err := p.SetModem(serialbroker.Modem{DTR: true}); err != nil {
		t.Fatalf("SetModem: %v", err)
	}
	if got := p.LastModem(); !got.DTR || got.RTS {
		t.Fatalf("LastModem = %+v, want DTR set and RTS clear", got)
	}

	if err := p.SendBreak(); err != nil {
		t.Fatalf("SendBreak: %v", err)
	}
	if p.BreakCount() != 1 {
		t.Fatalf("BreakCount = %d, want 1", p.BreakCount())
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serialtest/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement `port.go`**

```go
// Package serialbroker owns host serial devices on behalf of runs.
package serialbroker

import (
	"io"

	"github.com/majorcontext/moat/internal/serialbroker"
)

// Port is an open serial device. Tests substitute serialtest.FakePort.
type Port interface {
	io.ReadWriteCloser

	// ApplySettings sets line settings (baud, framing, flow control).
	ApplySettings(Settings) error
	// SetModem drives the DTR and RTS output lines.
	SetModem(Modem) error
	// SendBreak transmits a break condition.
	SendBreak() error
	// Name is the host device path, for logs and errors.
	Name() string
}
```

- [ ] **Step 4: Implement `port_unix.go`**

Build-tagged `//go:build linux || darwin`. `OpenPort` opens the path with `O_RDWR|O_NOCTTY|O_NONBLOCK`, then:

1. Rejects non-ttys: call `unix.IoctlGetTermios(fd, tcgets)`; on failure return
   `fmt.Errorf("%s is not a serial device: moat only exposes tty character devices", path)`.
2. Takes an exclusive claim with `unix.IoctlSetPointerInt(fd, unix.TIOCEXCL, 0)`.
3. Puts the line in raw mode (`cfmakeraw` equivalent: clear `ICANON|ECHO|ECHOE|ISIG`, `IXON|ICRNL`, `OPOST`; set `CS8`, `CREAD|CLOCAL`).

`ApplySettings` maps `Baud` through a `map[uint32]uint32` of `unix.B115200` and friends, returning
`fmt.Errorf("unsupported baud rate %d", baud)` for unknown rates. `SetModem` uses `TIOCMBIS`/`TIOCMBIC`
with `unix.TIOCM_DTR`/`unix.TIOCM_RTS`. `SendBreak` uses `TCSBRK`. Use `tcgets`/`tcsets` constants that
differ between Linux (`unix.TCGETS`) and Darwin (`unix.TIOCGETA`) via a small per-OS file.

- [ ] **Step 5: Implement `serialtest/fake.go`**

A `FakePort` backed by a real pty pair, so tests exercise the same read/write path as a device:
`os.OpenFile("/dev/ptmx")`, unlock, open the slave, return the master as the `Port` and the slave as
`Peer()`. `ApplySettings`/`SetModem`/`SendBreak` record their arguments under a mutex rather than
touching the pty, and the recorded values are what tests assert on. `NewFakePort(t)` registers
`t.Cleanup` to close both ends. Also add `NewFakeEnumerator(devs ...serialdev.Device)` returning a
`serialdev.Enumerator` that yields a fixed slice.

- [ ] **Step 6: Run tests and commit**

```bash
go test ./internal/serialtest/ ./internal/serialbroker/ -v && make lint
git add internal/serialbroker/ internal/serialtest/
git commit -m "feat(serial): add port abstraction with pty-backed test fake"
```

---

### Task 6: Broker — RFC2217 server, claims, sessions

**Files:**
- Create: `internal/serialbroker/broker.go`, `internal/serialbroker/session.go`
- Test: `internal/serialbroker/broker_test.go`

**Interfaces:**
- Consumes: `Port`, `Settings`, `Modem` (Task 5), `internal/rfc2217` (Task 4), `serialdev.Device`, `serialtest.FakePort`.
- Produces: `type Broker struct{...}`; `func New(opts Options) *Broker`;
  `Options{OpenPort func(path string) (Port, error), Log func(Event)}`;
  `func (b *Broker) Listen(runID string, a Approved) (addr string, err error)`;
  `type Approved struct{ Name string; Device serialdev.Device; Record string }`;
  `func (b *Broker) Revoke(runID string)`; `func (b *Broker) Close() error`;
  `type Event struct{ RunID, Device, Kind, Detail string; TxBytes, RxBytes int64 }`.

Each approved device gets its **own listener**, returned to the caller as `host:port`.
That is the authentication model: RFC2217 has no auth, so reachability is the control, and
only the owning run's container is permitted to reach the port (Task 11 adds it to
`AllowedHostPorts`). Record this limitation in the docs rather than implying a token check.

Connection handling: accept, open the port, then run two pumps. Container→device un-escapes
IAC (via `rfc2217.NewReader`) and writes data to the port, while `OnCommand` maps
`CmdSetBaudRate`/`CmdSetDataSize`/`CmdSetParity`/`CmdSetStopSize` onto `Port.ApplySettings`
and `CmdSetControl` onto `Port.SetModem`/`Port.SendBreak`. Device→container escapes IAC and
writes data back. Only one connection per device at a time; a second is closed immediately.

**ESP32 reset timing.** Espressif ships a custom PortManager rather than using the stock
pyserial server because network latency breaks the reset sequence. Apply DTR/RTS changes
synchronously on the connection goroutine before acknowledging, and never coalesce or
reorder them — the sequence, not just the final state, is what resets the chip.

- [ ] **Step 1: Write the failing test**

```go
package serialbroker

import (
	"bytes"
	"net"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/rfc2217"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialtest"
)

func newTestBroker(t *testing.T) (*Broker, *serialtest.FakePort, string) {
	t.Helper()
	fp := serialtest.NewFakePort(t)
	b := New(Options{OpenPort: func(string) (Port, error) { return fp, nil }})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", Approved{
		Name:   "esp32",
		Device: serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"},
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return b, fp, addr
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDataFlowsToDevice(t *testing.T) {
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("device got %q, want ping", buf[:n])
	}
}

func TestDataFlowsFromDevice(t *testing.T) {
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)
	if _, err := fp.Peer().Write([]byte("pong")); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 32)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if string(buf[:n]) != "pong" {
		t.Fatalf("client got %q, want pong", buf[:n])
	}
}

func TestDeviceBytesContainingIACAreEscaped(t *testing.T) {
	// Firmware images are full of 0xFF. Unescaped, they corrupt the stream.
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)
	if _, err := fp.Peer().Write([]byte{0x01, 0xFF, 0x02}); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 32)
	n, err := c.Read(buf)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	want := []byte{0x01, 0xFF, 0xFF, 0x02}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("got % x, want % x with IAC doubled", buf[:n], want)
	}
}

func TestSetBaudRateReachesThePort(t *testing.T) {
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(921600)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fp.LastSettings().Baud == 921600 })
}

func TestDTRAssertAndDeassertBothReachThePort(t *testing.T) {
	// The ESP32 reset sequence is asserts *and* deasserts; testing only the
	// assert would pass while boards silently fail to enter the bootloader.
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROn}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fp.LastModem().DTR })

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROff}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !fp.LastModem().DTR })
}

func TestRTSAssertAndDeassertBothReachThePort(t *testing.T) {
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlRTSOn}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return fp.LastModem().RTS })

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlRTSOff}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return !fp.LastModem().RTS })
}

func TestControlCommandsDoNotLeakIntoTheDeviceStream(t *testing.T) {
	_, fp, addr := newTestBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROn}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("data")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "data" {
		t.Fatalf("device got %q, want only %q", buf[:n], "data")
	}
}

func TestSecondConnectionIsRefusedWhileOneIsActive(t *testing.T) {
	_, _, addr := newTestBroker(t)
	first := dial(t, addr)
	if _, err := first.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	second := dial(t, addr)
	second.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("second connection should be closed while the first holds the device")
	}
}

func TestRevokeClosesSessionAndListener(t *testing.T) {
	b, _, addr := newTestBroker(t)
	c := dial(t, addr)
	b.Revoke("run-a")

	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("session should close on Revoke")
	}
	if _, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		t.Fatal("listener should be closed after Revoke")
	}
}

func TestEventsAreEmittedForAttachAndDetach(t *testing.T) {
	var kinds []string
	fp := serialtest.NewFakePort(t)
	b := New(Options{
		OpenPort: func(string) (Port, error) { return fp, nil },
		Log:      func(e Event) { kinds = append(kinds, e.Kind) },
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", Approved{Name: "esp32", Device: serialdev.Device{Path: "/dev/ttyUSB0"}})
	if err != nil {
		t.Fatal(err)
	}
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return contains(kinds, "attach") })
	c.Close()
	waitFor(t, func() bool { return contains(kinds, "detach") })
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/serialbroker/ -v`
Expected: FAIL — `New`, `Options`, `Listen` undefined.

- [ ] **Step 3: Implement the broker and session pumps.**

- [ ] **Step 4: Run tests with the race detector**

Run: `go test ./internal/serialbroker/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/serialbroker/
git commit -m "feat(serial): serve RFC2217 per device with exclusive claims"
```

---

### Task 7: `devices:` config surface

**Files:**
- Create: `internal/config/devices.go`, `internal/config/devices_test.go`
- Modify: `internal/config/config.go:34-57` (add the field), `internal/config/config.go:586` (`Load` calls the validator)

**Interfaces:**
- Produces: `type DeviceEntry struct { Serial string `yaml:"serial"`; Match DeviceMatch `yaml:"match"`; Baud int `yaml:"baud,omitempty"`; Record string `yaml:"record,omitempty"` }`, `type DeviceMatch struct { VID, PID string }`, `func validateDevices(devs []DeviceEntry) error`, `func (d DeviceEntry) RecordMode() string`.

Validation rules: name required and must match `^[a-z0-9][a-z0-9_-]*$` (it becomes a path under `/dev/moat/serial/`); names unique; `vid`/`pid` required and exactly 4 hex digits; `baud` optional and positive; `record` empty (defaults to `events`) or one of `events`/`full` — matching the documented contract exactly, per invariant #4.

- [ ] **Step 1: Write the failing test** covering, at minimum: a valid entry parses; empty name rejected; name with a `/` rejected; duplicate names rejected; 3-digit VID rejected; non-hex PID rejected; `record: bogus` rejected; `record` omitted defaults to `events`; `record: full` preserved. The last two are the companion pair — assert both the default *and* the explicit value.

```go
func TestDevicesRecordDefaultsToEvents(t *testing.T) {
	cfg := loadYAML(t, "name: x\ndevices:\n  - serial: esp32\n    match: {vid: \"303a\", pid: \"1001\"}\n")
	if got := cfg.Devices[0].RecordMode(); got != "events" {
		t.Fatalf("RecordMode() = %q, want events when record is omitted", got)
	}
}

func TestDevicesRecordFullIsPreserved(t *testing.T) {
	cfg := loadYAML(t, "name: x\ndevices:\n  - serial: esp32\n    match: {vid: \"303a\", pid: \"1001\"}\n    record: full\n")
	if got := cfg.Devices[0].RecordMode(); got != "full" {
		t.Fatalf("RecordMode() = %q, want full", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestDevices -v`
Expected: FAIL — `Devices` field undefined.

- [ ] **Step 3: Implement** the types, add `Devices []DeviceEntry \`yaml:"devices,omitempty"\`` to `Config` next to `Mounts`, and call `validateDevices(cfg.Devices)` from `Load` alongside the existing sandbox/base_image validation.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/config/ -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/config/
git commit -m "feat(config): add devices: block for serial device access"
```

---

### Task 8: Container-side pty bridge (RFC2217 client)

**Files:**
- Create: `cmd/moat-serial/main.go`, `cmd/moat-serial/bridge.go`, `cmd/moat-serial/bridge_test.go`

**Interfaces:**
- Consumes: `internal/rfc2217` (Task 4).
- Produces: `func runBridge(ctx context.Context, cfg bridgeConfig) error`, `bridgeConfig{Addr, LinkPath, CompatPath string, UID, GID int, PollInterval time.Duration}`.

Flow: dial `Addr` (plain TCP — the RFC2217 endpoint has no handshake); open `/dev/ptmx`, `TIOCSPTLCK` unlock, `TIOCGPTN` for the slave number; `chown` the slave to `UID:GID`; symlink `LinkPath` and `CompatPath` to the slave; drop privileges; then pump bytes both ways, escaping and un-escaping IAC via `internal/rfc2217`.

**This front end is console-only, by construction.** Task 0 measured that a pty has no modem control lines (`TIOCMGET` → `ENOTTY`), so the bridge cannot observe or forward DTR/RTS no matter how it is written. It forwards baud changes by holding an fd on the slave and polling `TCGETS` every `PollInterval` (default 20ms), emitting `CmdSetBaudRate` only when the value changes. Tools that need control lines use the `rfc2217://` URL instead; the guide must say so plainly, because a user who tries to flash through the pty gets a confusing failure rather than an explanatory one.

- [ ] **Step 1: Write the failing test**

The test runs the bridge against an in-process fake RFC2217 server (a `net.Listener` using `internal/rfc2217`) and asserts the observable contract:

```go
func TestBridgeCreatesLinkAtConfiguredPath(t *testing.T)
func TestBridgeForwardsDataFromPtyToServer(t *testing.T)
func TestBridgeForwardsDataFromServerToPty(t *testing.T)
func TestBridgeEscapesIACFromPty(t *testing.T)          // 0xFF written to the pty arrives doubled
func TestBridgeUnescapesIACFromServer(t *testing.T)     // companion of the above
func TestBridgeForwardsBaudChangeAfterPoll(t *testing.T) // TCSETS 921600 on the link -> CmdSetBaudRate(921600)
func TestBridgeDoesNotResendUnchangedBaud(t *testing.T)  // companion: polling must not spam the server
func TestBridgeFailsClosedWhenServerUnreachable(t *testing.T) // returns an error and creates no link or symlink
```

The two IAC tests are the ones that bite in production: an unescaped `0xFF` corrupts exactly the byte that dominates erased flash, and the symptom looks like failing hardware rather than a protocol bug.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/moat-serial/ -v`
Expected: FAIL — package does not exist.

- [ ] **Step 3: Implement the bridge.**

- [ ] **Step 4: Run tests**

Run: `go test ./cmd/moat-serial/ -race -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add cmd/moat-serial/
git commit -m "feat(serial): add container-side pty bridge"
```

---

### Task 9: Bridge binary delivery

**Files:**
- Create: `internal/serialbin/serialbin.go`, `internal/serialbin/gen/gen.go`, `internal/serialbin/embed/.gitignore`, `internal/serialbin/checksums.txt`
- Modify: `Makefile`, `internal/deps/dockerfile.go:624-630`

**Interfaces:**
- Produces: `func serialbin.Binary(goarch string) ([]byte, error)`, `func serialbin.Available() bool`.

Mirror `internal/initbin/` on `feat/moat-init-go-rewrite`: a `gen` program cross-compiles `cmd/moat-serial` for `linux/amd64` and `linux/arm64` into `embed/`, records SHA-256 sums in `checksums.txt`, and `serialbin.go` exposes them via `go:embed`. Add a `make gen-serialbin` target and wire it into the release build. `dockerfile.go` writes the arch-appropriate binary into `ContextFiles["moat-serial"]` and emits `COPY moat-serial /usr/local/bin/moat-serial` alongside the existing `moat-init.sh` COPY, only when the run has devices.

**Note for the executor:** if `feat/moat-init-go-rewrite` has landed by the time you reach this task, do not create `internal/serialbin` at all. Add the bridge as a `serial-bridge` subcommand of `cmd/moat-init` and reuse `internal/initbin`. Duplicating the embed infrastructure is the wrong outcome.

- [ ] **Step 1: Write the failing test**

```go
func TestBinaryReturnsEmbeddedArch(t *testing.T)      // amd64 and arm64 both non-empty
func TestBinaryRejectsUnknownArch(t *testing.T)       // returns an error naming the arch
func TestChecksumsMatchEmbeddedBinaries(t *testing.T) // guards against a stale embed
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/serialbin/ -v`

- [ ] **Step 3: Implement `gen`, the Makefile target, and `serialbin.go`.**

- [ ] **Step 4: Run** `make gen-serialbin && go test ./internal/serialbin/ ./internal/deps/ -v`

- [ ] **Step 5: Commit**

```bash
git add internal/serialbin/ Makefile internal/deps/
git commit -m "build(serial): embed prebuilt bridge binaries for container delivery"
```

---

### Task 10: Daemon integration

**Files:**
- Modify: `internal/daemon/api.go` (add `SerialDevices []SerialDeviceSpec` to `RegisterRequest`, add `CapSerialDevices`, add `SerialAddrs map[string]string` to `RegisterResponse`), `internal/daemon/runcontext.go:72` (add `SerialDevices`), `internal/daemon/server.go:58-65` (start the broker listener), `internal/daemon/client.go`
- Test: `internal/daemon/serial_test.go`

**Interfaces:**
- Produces: `type SerialDeviceSpec struct { Name, Path, VID, PID, Serial, PortPath, Record string }`, `const CapSerialDevices = "serial-devices"`, `RegisterResponse.SerialAddrs map[string]string` (device name → `host:port`).

The daemon opens one listener per approved device on the gateway-reachable interface and returns the addresses in `RegisterResponse.SerialAddrs`. `handleUnregisterRun` calls `broker.Revoke(runID)`, which closes both the sessions and the listeners.

Back-compat rules apply in full: all new fields `omitempty`; an old daemon ignoring `SerialDevices` must not silently produce a run with no device access, so the CLI checks `CapSerialDevices` in `HealthResponse.Capabilities` and fails with "this run needs serial device support; restart the proxy daemon with `moat proxy restart`" when absent.

- [ ] **Step 1: Write the failing test**

```go
func TestRegisterWithSerialDevicesReturnsAddrPerDevice(t *testing.T)
func TestRegisterWithoutSerialDevicesOpensNoListener(t *testing.T) // companion: no cost when unused
func TestUnregisterRevokesBrokerClaims(t *testing.T)
func TestHealthAdvertisesSerialCapability(t *testing.T)
func TestRegisterRequestRoundTripsUnknownFieldsFromNewerCLI(t *testing.T) // back-compat guard
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/daemon/ -run Serial -v`

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run** `go test ./internal/daemon/ -race -v`

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/
git commit -m "feat(daemon): serve the serial broker and scope devices per run"
```

---

### Task 11: Run wiring and pre-flight detection

**Files:**
- Create: `internal/run/devices.go`, `internal/run/devices_test.go`
- Modify: `internal/run/manager_create.go` (resolve devices, pass to `RegisterRequest`, add the broker port to `AllowedHostPorts`, inject env), `internal/deps/scripts/moat-init.sh` (launch the bridge)

**Interfaces:**
- Produces: `func DetectMissingDevices(devs []config.DeviceEntry, enum serialdev.Enumerator, pins *serialdev.PinStore) []MissingDevice`, `type MissingDevice struct { Name, Reason, FixCommand string }`, reasons `ReasonDeviceNotFound`, `ReasonDeviceAmbiguous`, `ReasonPinMismatch`; `func ResolveDevices(...) ([]daemon.SerialDeviceSpec, error)`.

Per invariant #2 the detector and the `Create` gate must agree exactly, with a drift-guard test asserting **both** directions — every case the detector flags is rejected by the validator, and every case the validator rejects is flagged by the detector. Model it on `TestDetectMissingGrantsMatchesValidators`.

Env injected into the container: `MOAT_SERIAL_<NAME>_URL` (`rfc2217://<gateway>:<port>`) per device, plus `MOAT_SERIAL_DEVICES` (comma-separated names). No token is injected — the RFC2217 endpoint is scoped by reachability, and each device's port is added to `AllowedHostPorts` so only this run's container may reach it. `moat-init.sh` launches one bridge per name during its root phase, before dropping privileges, so it can create `/dev/moat/serial/<name>` and the `/dev/ttyUSB0` compat symlink.

- [ ] **Step 1: Write the failing test**

```go
func TestDetectMissingDevicesFlagsAbsentDevice(t *testing.T)
func TestDetectMissingDevicesPassesWhenPresentAndPinned(t *testing.T)   // companion
func TestDetectMissingDevicesFlagsPinMismatch(t *testing.T)
func TestDetectMissingDevicesFlagsAmbiguousMatch(t *testing.T)
func TestDetectMissingDevicesPinsOnFirstUse(t *testing.T)               // no pin -> pin written, no error
func TestDetectMissingDevicesMatchesValidators(t *testing.T)            // drift guard, both directions
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/run/ -run Device -v`

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run** `make test-unit`

- [ ] **Step 5: Commit**

```bash
git add internal/run/ internal/deps/
git commit -m "feat(run): resolve, pin, and attach serial devices at run start"
```

---

### Task 12: Observability

**Files:**
- Modify: `internal/audit/entry.go` (add `EntryTypeDevice` and `DeviceData`), `internal/audit/store.go` (add `AppendDevice`), `internal/storage/storage.go` (add `WriteDeviceEvent`/`ReadDeviceEvents` writing `devices.jsonl`), `internal/daemon/server.go` (wire `serialbroker.Options.Log`)
- Test: `internal/audit/store_test.go`, `internal/storage/storage_test.go`

`DeviceData{Name, Path, VID, PID, Serial, Action, Detail string, TxBytes, RxBytes int64, RecordMode string}` with actions `pin`, `attach`, `detach`, `mismatch`, `conflict`, `unplug`. `record: full` additionally tees payload bytes into the run store; the audit entry for the session records `RecordMode: "full"` so the chain shows capture was on.

- [ ] **Step 1: Write the failing test**

```go
func TestAppendDeviceEntryChainsCorrectly(t *testing.T)
func TestDeviceEventsRoundTripThroughRunStore(t *testing.T)
func TestFullRecordModeIsMarkedInTheAuditEntry(t *testing.T)
func TestEventsModeDoesNotWritePayloadBytes(t *testing.T)  // companion: default must not capture
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./internal/audit/ ./internal/storage/ -run Device -v`

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run** `make test-unit`

- [ ] **Step 5: Commit**

```bash
git add internal/audit/ internal/storage/ internal/daemon/
git commit -m "feat(serial): record device attach/detach events in the audit chain"
```

---

### Task 13: `moat device` CLI

**Files:**
- Create: `cmd/moat/cli/device.go`, `cmd/moat/cli/device_test.go`

`moat device list` prints attached serial devices with VID:PID, serial, port path, and pin state (`pinned`, `unpinned`, or `MISMATCH`). `moat device forget <name>` clears a pin and confirms. Use `tabwriter` for the table with no `ui` styling inside it (invariant), and `ui.Bold`/`ui.Green` only outside the writer.

- [ ] **Step 1: Write the failing test**

```go
func TestDeviceListShowsPinState(t *testing.T)
func TestDeviceListWithNoDevicesExplainsWhy(t *testing.T)  // actionable empty state, not a blank table
func TestDeviceForgetRemovesPin(t *testing.T)
func TestDeviceForgetUnknownNameErrors(t *testing.T)
```

- [ ] **Step 2: Run test to verify it fails** — `go test ./cmd/moat/cli/ -run TestDevice -v`

- [ ] **Step 3: Implement.**

- [ ] **Step 4: Run** `make test-unit && make lint`

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/
git commit -m "feat(cli): add moat device list and moat device forget"
```

---

### Task 14: Documentation, example, and hardware test recipe

**Files:**
- Create: `docs/content/guides/18-serial-devices.md`, `examples/serial/moat.yaml`, `examples/serial/README.md`, `internal/e2e/serial_test.go`
- Modify: `docs/content/reference/02-moat-yaml.md`, `docs/content/reference/01-cli.md`, `CHANGELOG.md`

The guide must state plainly that an agent with a serial line can reflash and brick the device, and that device-level consent — not sandboxing — is the mitigation. Follow `docs/STYLE-GUIDE.md`: no marketing language, working example first.

`internal/e2e/serial_test.go` is build-tagged `e2e` and calls `t.Skip` unless `MOAT_SERIAL_TEST_DEVICE` is set, since it needs a real board.

- [ ] **Step 1: Write the example**

```yaml
# examples/serial/moat.yaml
name: esp32-dev
agent: claude
dependencies: [python]
devices:
  - serial: esp32
    match: { vid: "303a", pid: "1001" }
    baud: 115200
```

- [ ] **Step 2: Write the hardware test recipe in `examples/serial/README.md`**

```bash
# Requires a real ESP32 plugged into the host.
moat device list                      # confirm the board appears, note its serial
# Flashing goes through the RFC2217 URL — it needs DTR/RTS, which a pty cannot carry.
moat run -- sh -c 'esptool.py --port "$MOAT_SERIAL_ESP32_URL" chip_id'
moat run -- sh -c 'esptool.py --port "$MOAT_SERIAL_ESP32_URL" write_flash 0x0 firmware.bin'

# The pty is for console use only.
moat run -- picocom -b 115200 /dev/moat/serial/esp32
moat audit <run-id> | grep device     # attach/detach events in the chain

# Pin enforcement: swap in a second board with the same VID:PID.
# Expected: the run fails before the container is created, naming both serials.
moat device forget esp32              # then the new board is approved on next run
```

- [ ] **Step 3: Write the docs and CHANGELOG entry**

CHANGELOG goes under `### Added` with the feature name bolded and a real PR link — CI fails on an unfilled `#NNN` placeholder.

- [ ] **Step 4: Verify every documented command against the implementation**

Run each command in the guide. Docs that do not match behaviour erode trust; check output formats rather than assuming them.

- [ ] **Step 5: Commit**

```bash
git add docs/ examples/ internal/e2e/ CHANGELOG.md
git commit -m "docs(serial): document serial device access and add an esp32 example"
```

---

## Self-Review

**Spec coverage.** Broker on the daemon → Tasks 6, 10. TCP transport → Tasks 6, 10, 11. Protocol with termios/modem → Tasks 4, 8. Container pty bridge → Tasks 8, 9. Config surface → Task 7. Deny by default, TOFU pinning, serial-less fallback, exclusive claim, tty-only class enforcement → Tasks 2, 5, 6, 11. Observability with `events` default and `full` opt-in → Tasks 7, 12. Lifecycle pre-flight, unplug, stop → Tasks 6, 10, 11. Mocked tests → Tasks 5, 6, 8. Hardware recipe → Task 14. Docs → Task 14. The one addition beyond the spec is Task 0, which verifies the pty packet-mode assumption the spec's protocol depends on.

**Type consistency.** `Device`/`Identity`/`Matcher` (Task 1) are consumed unchanged by `Pin.Verify` (2), `Enumerator` (3), `Approved` (6), and `DetectMissingDevices` (11). `serialbroker.Settings`/`Modem` (5) are used by the broker (6) and the bridge (8) with identical shapes, and `internal/rfc2217` (4) is the only wire format either speaks. `Port` (5) is the type `Options.OpenPort` returns (6). `SerialDeviceSpec` (10) is what `ResolveDevices` produces (11). `RecordMode()` (7) feeds `Approved.Record` (6) and `DeviceData.RecordMode` (12).

**Known risk.** Task 0 gates Task 8. If packet mode is unavailable under gVisor or Apple's guest kernel, Task 8 switches to termios polling; nothing else in the plan changes.
