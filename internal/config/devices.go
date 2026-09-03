package config

import (
	"fmt"
	"regexp"
	"strings"
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

// usbIDRe matches a "vid:pid" USB ID pair as `moat device list` prints it —
// 4 hex digits, colon, 4 hex digits, in either case.
var usbIDRe = regexp.MustCompile(`^[0-9a-fA-F]{4}:[0-9a-fA-F]{4}$`)

// DeviceMatch selects a host device by its USB ID. The value is the exact
// `vid:pid` pair the device list prints, so a row can be copied into
// moat.yaml without splitting it.
type DeviceMatch struct {
	USB string `yaml:"usb"`
}

// DeviceEntry requests access to one serial device.
type DeviceEntry struct {
	// Name is the device's name in moat.yaml. It determines how the container
	// addresses the device: MOAT_SERIAL_<NAME>_URL carries its rfc2217://
	// endpoint.
	Name string `yaml:"name"`

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

// VIDPID returns the match's vendor and product IDs, lowercased, for device
// resolution. Callers have validated the format already.
func (d DeviceEntry) VIDPID() (string, string) {
	vid, pid, _ := strings.Cut(d.Match.USB, ":")
	return strings.ToLower(vid), strings.ToLower(pid)
}

// validateDevices checks the devices block.
func validateDevices(devs []DeviceEntry) error {
	seen := make(map[string]bool, len(devs))
	for i, d := range devs {
		if d.Name == "" {
			return fmt.Errorf("devices[%d]: name is required — it names the device, e.g. `name: esp32`", i)
		}
		if !deviceNameRe.MatchString(d.Name) {
			return fmt.Errorf("devices[%d]: invalid device name %q — use lowercase letters, digits, '-' and '_', "+
				"starting with a letter or digit (the name becomes MOAT_SERIAL_<NAME>_URL)", i, d.Name)
		}
		if seen[d.Name] {
			return fmt.Errorf("devices[%d]: duplicate device name %q", i, d.Name)
		}
		seen[d.Name] = true

		if !usbIDRe.MatchString(d.Match.USB) {
			return fmt.Errorf("devices[%s]: match.usb must be a vid:pid pair, e.g. \"303a:1001\" — got %q\n"+
				"  Run `moat device list` to see the USB IDs of attached devices", d.Name, d.Match.USB)
		}

		if d.Baud < 0 {
			return fmt.Errorf("devices[%s]: baud must be positive, got %d", d.Name, d.Baud)
		}

		switch d.Record {
		case "", RecordEvents, RecordFull:
		default:
			return fmt.Errorf("devices[%s]: invalid record mode %q: must be %q (default) or %q",
				d.Name, d.Record, RecordEvents, RecordFull)
		}
	}
	return nil
}
