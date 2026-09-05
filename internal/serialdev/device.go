// Package serialdev enumerates host serial devices and records which ones a
// run has been approved to use.
//
// Approval is trust-on-first-use: the first run to use a device name records
// that device's identity (see Pin), and later runs must present the same
// device. Enforcement lives here rather than in the kernel because the devices
// cgroup can only filter on major:minor, which cannot distinguish two boards of
// the same model. See docs/plans/2026-08-15-serial-devices-design.md.
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

	// Interface distinguishes the UARTs of a multi-interface bridge (FT2232H,
	// CP2105, ESP-Prog): one USB device, several ttys, everything else about
	// them identical. It is the interface's number within the device — "0",
	// "1" — and empty when the platform cannot see it.
	Interface string

	// isHub marks a USB hub (class 9). Set by the macOS parser from
	// bDeviceClass; on Linux the hub roothubs are skipped by name instead
	// (they start with "usb"). Only USBEnumerator reads it — a hub is host
	// infrastructure, never a device anyone is trying to match.
	isHub bool
}

// Identity is the subset of a Device used for approval decisions.
type Identity struct {
	VID       string
	PID       string
	Serial    string
	PortPath  string
	Interface string
}

// hubByName reports whether a product string names a hub. Over-filtering is
// acceptable here — hardware whose own name says "hub" is not something
// anyone is matching in devices: — while under-filtering put two of the
// host's controller hubs into the user-facing list (on macOS, confirmed;
// on Linux, class-0 controller hubs exist behind the same ports).
//
// Both enumerators use it as the backstop for hubs that report no usable
// class: macOS's internal 0424:7240/7260 hubs, and Linux's per-interface
// (class-0) hubs that the bDeviceClass check in enumerate_linux_usb.go
// cannot see.
func hubByName(description string) bool {
	return strings.Contains(strings.ToLower(description), "hub")
}

// Identity returns the device's approval identity.
func (d Device) Identity() Identity {
	return Identity{VID: d.VID, PID: d.PID, Serial: d.Serial, PortPath: d.PortPath, Interface: d.Interface}
}

// sameInterface reports whether a recorded interface and an attached one name
// the same UART, normalizing the empty form onto "0". Only called for pins
// that carry an interface value at all — a recorded empty value means "predates
// the discriminator", not "interface 0", because CDC modems legitimately put
// their single tty on interface 1.
func sameInterface(recorded, attached string) bool {
	return normalizeInterface(recorded) == normalizeInterface(attached)
}

// normalizeInterface maps the empty form onto the first interface.
func normalizeInterface(s string) string {
	if s == "" {
		return "0"
	}
	return s
}

// Matcher selects devices by USB vendor and product ID.
type Matcher struct {
	VID string
	PID string
	// Interface narrows a match to one UART of a multi-interface bridge, where
	// several ttys share a USB ID. Empty matches any interface, which keeps
	// single-UART devices as they were.
	Interface string
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
		if strings.ToLower(d.VID) != vid || strings.ToLower(d.PID) != pid {
			continue
		}
		// An interface selector narrows to one UART of a multi-interface
		// bridge. A device whose interface the platform could not determine
		// ("") must not satisfy a specific selector: treating unknown as
		// interface 0 (as pin verification does for pins that predate the
		// field) would let the selector land on the wrong port, or on a device
		// that never exposed interfaces at all.
		if m.Interface != "" && d.Interface != m.Interface {
			continue
		}
		out = append(out, d)
	}
	switch len(out) {
	case 0:
		if m.Interface != "" {
			return nil, fmt.Errorf("%w for %s:%s interface %s", ErrNoMatch, vid, pid, m.Interface)
		}
		return nil, fmt.Errorf("%w for %s:%s", ErrNoMatch, vid, pid)
	case 1:
		return out, nil
	default:
		return out, fmt.Errorf("%w for %s:%s", ErrAmbiguous, vid, pid)
	}
}
