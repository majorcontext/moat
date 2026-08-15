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
