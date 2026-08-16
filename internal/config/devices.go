package config

import (
	"fmt"
	"regexp"
)

// Record modes for a device session.
const (
	// RecordEvents records attach/detach, pin decisions, line settings, and
	// byte counters. It is the default.
	RecordEvents = "events"
	// RecordFull additionally captures the payload bytes. Serial carries
	// firmware images and device credentials, so it is opt-in.
	RecordFull = "full"
)

// deviceNameRe constrains device names. The name is interpolated into an
// environment variable name and, once the console bridge lands, into a path
// under /dev/moat/serial/, so anything that could escape that directory or
// confuse a shell is rejected.
var deviceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// usbIDRe matches a 4-digit hex USB vendor or product ID, in either case.
var usbIDRe = regexp.MustCompile(`^[0-9a-fA-F]{4}$`)

// DeviceMatch selects a host device by USB vendor and product ID.
type DeviceMatch struct {
	VID string `yaml:"vid"`
	PID string `yaml:"pid"`
}

// DeviceEntry requests access to one serial device.
type DeviceEntry struct {
	// Serial is the device name. It determines how the container addresses the
	// device: MOAT_SERIAL_<NAME>_URL carries its rfc2217:// endpoint.
	Serial string `yaml:"serial"`

	// Match selects which attached device this entry refers to. The specific
	// device is pinned on first use; see internal/serialdev.
	Match DeviceMatch `yaml:"match"`

	// Baud optionally sets the initial line rate. Tools normally set their own.
	Baud int `yaml:"baud,omitempty"`

	// Record is "events" (default) or "full".
	Record string `yaml:"record,omitempty"`
}

// RecordMode returns the effective record mode, applying the default.
func (d DeviceEntry) RecordMode() string {
	if d.Record == "" {
		return RecordEvents
	}
	return d.Record
}

// validateDevices checks the devices block.
func validateDevices(devs []DeviceEntry) error {
	seen := make(map[string]bool, len(devs))
	for i, d := range devs {
		if d.Serial == "" {
			return fmt.Errorf("devices[%d]: serial is required — it names the device, e.g. `serial: esp32`", i)
		}
		if !deviceNameRe.MatchString(d.Serial) {
			return fmt.Errorf("devices[%d]: invalid device name %q — use lowercase letters, digits, '-' and '_', "+
				"starting with a letter or digit (the name becomes MOAT_SERIAL_<NAME>_URL)", i, d.Serial)
		}
		if seen[d.Serial] {
			return fmt.Errorf("devices[%d]: duplicate device name %q", i, d.Serial)
		}
		seen[d.Serial] = true

		if !usbIDRe.MatchString(d.Match.VID) {
			return fmt.Errorf("devices[%s]: match.vid must be 4 hex digits, e.g. \"303a\" — got %q\n"+
				"  Run `moat device list` to see the IDs of attached devices", d.Serial, d.Match.VID)
		}
		if !usbIDRe.MatchString(d.Match.PID) {
			return fmt.Errorf("devices[%s]: match.pid must be 4 hex digits, e.g. \"1001\" — got %q\n"+
				"  Run `moat device list` to see the IDs of attached devices", d.Serial, d.Match.PID)
		}

		if d.Baud < 0 {
			return fmt.Errorf("devices[%s]: baud must be positive, got %d", d.Serial, d.Baud)
		}

		switch d.Record {
		case "", RecordEvents, RecordFull:
		default:
			return fmt.Errorf("devices[%s]: invalid record mode %q: must be %q (default) or %q",
				d.Serial, d.Record, RecordEvents, RecordFull)
		}
	}
	return nil
}
