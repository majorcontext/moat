package run

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/serialdev"
)

// DeviceReason explains why a requested device is unusable.
type DeviceReason int

const (
	// ReasonDeviceNotFound means nothing attached matches the config's vid/pid.
	ReasonDeviceNotFound DeviceReason = iota
	// ReasonDeviceAmbiguous means several attached devices match and no pin
	// exists to pick between them.
	ReasonDeviceAmbiguous
	// ReasonDevicePinMismatch means a matching device is attached but it is not
	// the one previously approved under this name.
	ReasonDevicePinMismatch
	// ReasonDeviceEnumerationFailed means the host device list could not be read.
	ReasonDeviceEnumerationFailed
)

// MissingDevice describes a device a run needs but cannot use.
type MissingDevice struct {
	Name       string // config name, e.g. "esp32"
	Reason     DeviceReason
	Detail     string // human-readable explanation, safe to print
	FixCommand string // e.g. "moat device forget esp32"
}

// NewPin describes a device this run would approve for the first time, so the
// CLI can surface the trust-on-first-use decision before the run starts.
type NewPin struct {
	Name string // config name, e.g. "esp32"
	// VID:PID of the device being approved.
	VID, PID string
	// Identity is how the device will be pinned: its serial number, or the
	// physical port when the device has none.
	Serial   string
	PortPath string
	// ByPortPath reports whether the pin identifies the device only by the
	// port it is plugged into — true when the device ships no serial number
	// (common on cheap CH340 and CP2102 clones). Such a pin approves
	// whatever is plugged into that port, which the consent notice must say
	// plainly.
	ByPortPath bool
}

// resolvedDevice pairs a config entry with the host device it resolved to.
type resolvedDevice struct {
	entry  config.DeviceEntry
	device serialdev.Device
	pin    serialdev.Pin
	// newPin is true when this run is the first to approve the device, so the
	// caller knows to record the pin only after the run is committed.
	newPin bool
}

// resolveDevices matches each configured device against the attached hardware
// and checks it against its pin.
//
// It is the single source of truth for both DetectMissingDevices (the CLI
// pre-flight) and ResolveDevices (the Create gate), so the two cannot drift:
// anything the detector reports is exactly what the gate rejects.
func resolveDevices(
	ctx context.Context,
	devs []config.DeviceEntry,
	enum serialdev.Enumerator,
	pins *serialdev.PinStore,
) ([]resolvedDevice, []MissingDevice) {
	if len(devs) == 0 {
		return nil, nil
	}

	attached, err := enum.List(ctx)
	if err != nil {
		// One enumeration failure explains every device; repeating it per entry
		// would bury the cause.
		missing := make([]MissingDevice, 0, len(devs))
		for _, d := range devs {
			missing = append(missing, MissingDevice{
				Name:   d.Name,
				Reason: ReasonDeviceEnumerationFailed,
				Detail: fmt.Sprintf("could not list host serial devices: %v", err),
			})
		}
		return nil, missing
	}

	var (
		resolved []resolvedDevice
		missing  []MissingDevice
	)
	for _, entry := range devs {
		vid, pid := entry.VIDPID()
		candidates, matchErr := serialdev.Match(attached, serialdev.Matcher{
			VID:       vid,
			PID:       pid,
			Interface: entry.Match.Interface,
		})

		pin, pinned, perr := pins.Get(entry.Name)
		if perr != nil {
			missing = append(missing, MissingDevice{
				Name:   entry.Name,
				Reason: ReasonDeviceEnumerationFailed,
				Detail: fmt.Sprintf("could not read device pins: %v", perr),
			})
			continue
		}

		switch {
		case errors.Is(matchErr, serialdev.ErrNoMatch):
			missing = append(missing, MissingDevice{
				Name:   entry.Name,
				Reason: ReasonDeviceNotFound,
				Detail: fmt.Sprintf("no attached device matches USB ID %s:%s\n"+
					"  Plug it in, or run `moat device list` to see what is attached",
					vid, pid),
			})
			continue

		case errors.Is(matchErr, serialdev.ErrAmbiguous):
			// A pin disambiguates: pick the device it names.
			if pinned {
				if dev, ok := pickPinned(candidates, pin); ok {
					resolved = append(resolved, resolvedDevice{entry: entry, device: dev, pin: pin})
					continue
				}
				missing = append(missing, MissingDevice{
					Name:       entry.Name,
					Reason:     ReasonDevicePinMismatch,
					Detail:     pinMismatchDetail(entry.Name, pin, candidates),
					FixCommand: "moat device forget " + entry.Name,
				})
				continue
			}
			missing = append(missing, MissingDevice{
				Name:   entry.Name,
				Reason: ReasonDeviceAmbiguous,
				Detail: fmt.Sprintf("%d attached devices match USB ID %s:%s — %s\n%s",
					len(candidates), vid, pid, describeDevices(candidates),
					ambiguousFix(entry, candidates)),
			})
			continue

		case matchErr != nil:
			missing = append(missing, MissingDevice{
				Name:   entry.Name,
				Reason: ReasonDeviceNotFound,
				Detail: matchErr.Error(),
			})
			continue
		}

		dev := candidates[0]
		if !pinned {
			// Trust on first use: this run approves the device.
			resolved = append(resolved, resolvedDevice{
				entry:  entry,
				device: dev,
				pin:    serialdev.PinFor(entry.Name, dev),
				newPin: true,
			})
			continue
		}
		if err := pin.Verify(dev); err != nil {
			missing = append(missing, MissingDevice{
				Name:       entry.Name,
				Reason:     ReasonDevicePinMismatch,
				Detail:     err.Error(),
				FixCommand: "moat device forget " + entry.Name,
			})
			continue
		}
		resolved = append(resolved, resolvedDevice{entry: entry, device: dev, pin: pin})
	}
	return resolved, missing
}

// pickPinned finds the candidate matching the pin.
func pickPinned(candidates []serialdev.Device, pin serialdev.Pin) (serialdev.Device, bool) {
	for _, d := range candidates {
		if pin.Verify(d) == nil {
			return d, true
		}
	}
	return serialdev.Device{}, false
}

func pinMismatchDetail(name string, pin serialdev.Pin, candidates []serialdev.Device) string {
	target := pin.Serial
	if target == "" {
		target = "port " + pin.PortPath
	}
	return fmt.Sprintf("%q is pinned to %s, but none of the attached matching devices is it — %s\n"+
		"  If you intended to swap devices, run: moat device forget %s",
		name, target, describeDevices(candidates), name)
}

// describeDevices renders candidates for an error message.
func describeDevices(devs []serialdev.Device) string {
	parts := make([]string, 0, len(devs))
	for _, d := range devs {
		id := d.Serial
		if id == "" {
			id = "no serial, port " + d.PortPath
		}
		if d.Interface != "" {
			id += ", interface " + d.Interface
		}
		parts = append(parts, fmt.Sprintf("%s (%s)", d.Path, id))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}

// ambiguousFix suggests how to disambiguate multiple matching devices. Two
// ports on one USB bridge (same IDs, same serial, different interfaces) can be
// told apart with the interface selector; anything else needs physical
// unplugging.
func ambiguousFix(entry config.DeviceEntry, candidates []serialdev.Device) string {
	if len(candidates) < 2 || !sameBridge(candidates) {
		return "  Unplug all but one so moat can pin the right device"
	}
	vid, pid := entry.VIDPID()
	return fmt.Sprintf("  These look like ports of one multi-interface bridge. "+
		"Declare each as its own device with an interface selector:\n"+
		"    devices:\n"+
		"      %s:\n"+
		"        match:\n"+
		"          usb: \"%s:%s\"\n"+
		"          interface: \"0\"   # or \"1\", per `moat device list`",
		entry.Name, vid, pid)
}

// sameBridge reports whether every candidate is a UART of one USB device: same
// IDs, same serial, same port path, and each carries an interface number.
func sameBridge(candidates []serialdev.Device) bool {
	first := candidates[0]
	if first.Interface == "" {
		return false
	}
	for _, d := range candidates[1:] {
		if d.Interface == "" || d.VID != first.VID || d.PID != first.PID ||
			d.Serial != first.Serial || d.PortPath != first.PortPath {
			return false
		}
	}
	return true
}

// DetectMissingDevices reports configured devices the run cannot use, without
// formatting an error, so the CLI can show them all before creating anything.
//
// It shares resolveDevices with ResolveDevices, which is what keeps the
// pre-flight and the Create gate in agreement.
func DetectMissingDevices(
	ctx context.Context,
	devs []config.DeviceEntry,
	enum serialdev.Enumerator,
	pins *serialdev.PinStore,
) []MissingDevice {
	_, missing := resolveDevices(ctx, devs, enum, pins)
	return missing
}

// DetectNewPins reports the devices this run would approve on first use, so
// the CLI can surface the trust-on-first-use decision — approving a device
// gives the run full control of that hardware, and a serial-less clone pins
// whatever is plugged into the port.
//
// It shares resolveDevices with the Create gate, so what it reports is
// exactly what ResolveDevices would pin. It writes nothing: the pin is
// recorded only if the run proceeds.
func DetectNewPins(
	ctx context.Context,
	devs []config.DeviceEntry,
	enum serialdev.Enumerator,
	pins *serialdev.PinStore,
) []NewPin {
	resolved, _ := resolveDevices(ctx, devs, enum, pins)
	var newPins []NewPin
	for _, r := range resolved {
		if !r.newPin {
			continue
		}
		newPins = append(newPins, NewPin{
			Name:       r.entry.Name,
			VID:        r.device.VID,
			PID:        r.device.PID,
			Serial:     r.device.Serial,
			PortPath:   r.device.PortPath,
			ByPortPath: r.pin.PinnedByPortPath(),
		})
	}
	return newPins
}

// ResolveDevices resolves every configured device and returns the specs to send
// to the daemon. Any unusable device is an error: a run that silently started
// without its hardware would fail later in a way that looks like broken
// equipment.
//
// Pins for newly approved devices are recorded here, so a device is pinned the
// first time a run actually claims it rather than the first time it is listed.
func ResolveDevices(
	ctx context.Context,
	devs []config.DeviceEntry,
	enum serialdev.Enumerator,
	pins *serialdev.PinStore,
) ([]daemon.SerialDeviceSpec, error) {
	resolved, missing := resolveDevices(ctx, devs, enum, pins)
	if len(missing) > 0 {
		return nil, fmt.Errorf("%s", FormatMissingDevices(missing))
	}

	specs := make([]daemon.SerialDeviceSpec, 0, len(resolved))
	for _, r := range resolved {
		if r.newPin {
			if err := pins.Put(r.pin); err != nil {
				return nil, fmt.Errorf("recording device pin for %q: %w", r.entry.Name, err)
			}
		}
		specs = append(specs, daemon.SerialDeviceSpec{
			Name:      r.entry.Name,
			Path:      r.device.Path,
			VID:       r.device.VID,
			PID:       r.device.PID,
			Serial:    r.device.Serial,
			PortPath:  r.device.PortPath,
			Interface: r.device.Interface,
			Record:    r.entry.RecordMode(),
			Baud:      r.entry.Baud,
		})
	}
	return specs, nil
}

// splitPort splits a host:port address.
func splitPort(addr string) (string, string, error) {
	return net.SplitHostPort(addr)
}

// FormatMissingDevices renders missing devices as a single actionable message.
func FormatMissingDevices(missing []MissingDevice) string {
	var b strings.Builder
	b.WriteString("cannot use the serial devices this run requires:\n")
	for _, m := range missing {
		fmt.Fprintf(&b, "  %s: %s\n", m.Name, m.Detail)
	}
	return strings.TrimRight(b.String(), "\n")
}

// SerialEnvVarName returns the environment variable carrying a device's URL,
// e.g. "esp32-s3" -> "MOAT_SERIAL_ESP32_S3_URL".
func SerialEnvVarName(device string) string {
	upper := strings.ToUpper(device)
	upper = strings.ReplaceAll(upper, "-", "_")
	return "MOAT_SERIAL_" + upper + "_URL"
}

// SerialEnv builds the environment a container needs to reach its devices.
//
// Each device is addressed by an rfc2217:// URL because control lines (DTR/RTS)
// have no pty representation, so a device path could enumerate the hardware and
// still never flash it.
func SerialEnv(hostAddr string, addrs map[string]string) []string {
	if len(addrs) == 0 {
		return nil
	}
	names := make([]string, 0, len(addrs))
	for name := range addrs {
		names = append(names, name)
	}
	sort.Strings(names)

	env := make([]string, 0, len(names)+1)
	for _, name := range names {
		_, port, err := splitPort(addrs[name])
		if err != nil {
			continue
		}
		env = append(env, fmt.Sprintf("%s=rfc2217://%s:%s", SerialEnvVarName(name), hostAddr, port))
	}
	env = append(env, "MOAT_SERIAL_DEVICES="+strings.Join(names, ","))
	return env
}

// serialPinsFromAddrs extracts device-name -> port from the daemon's serial
// addresses. The result goes into RegisterRequest.SerialPins so a
// re-registration after a daemon restart re-binds the exact ports the
// container's frozen MOAT_SERIAL_*_URLs point at — a fresh ephemeral port
// would be unreachable from inside the container.
func serialPinsFromAddrs(addrs map[string]string) map[string]int {
	if len(addrs) == 0 {
		return nil
	}
	pins := make(map[string]int, len(addrs))
	for name, addr := range addrs {
		_, portStr, err := splitPort(addr)
		if err != nil {
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		pins[name] = port
	}
	return pins
}
