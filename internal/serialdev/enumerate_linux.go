//go:build linux

package serialdev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type sysfsEnumerator struct{ root string }

// NewEnumerator returns the host's serial device enumerator.
func NewEnumerator() Enumerator { return &sysfsEnumerator{root: "/sys"} }

// List implements Enumerator.
func (e *sysfsEnumerator) List(_ context.Context) ([]Device, error) {
	return enumerateSysfs(e.root)
}

// enumerateSysfs walks /sys/class/tty and returns only ttys backed by USB.
// root is injectable so tests can build a fake tree.
func enumerateSysfs(root string) ([]Device, error) {
	ttyClass := filepath.Join(root, "class", "tty")
	entries, err := os.ReadDir(ttyClass)
	if err != nil {
		return nil, fmt.Errorf("listing serial devices in %s: %w", ttyClass, err)
	}
	var out []Device
	for _, e := range entries {
		name := e.Name()
		target, err := filepath.EvalSymlinks(filepath.Join(ttyClass, name, "device"))
		if err != nil {
			// No backing device: pty, console, or a tty that vanished mid-walk.
			continue
		}
		usbDir, portPath, ok := usbParent(target)
		if !ok {
			continue // platform serial port, not USB
		}
		vid := readAttr(usbDir, "idVendor")
		pid := readAttr(usbDir, "idProduct")
		if vid == "" || pid == "" {
			// Without a USB ID the device cannot be matched or pinned, so
			// offering it would only produce a confusing failure later.
			continue
		}
		serial := readAttr(usbDir, "serial")
		if serial == "" && portPath == "" {
			// Neither identity: a pin for it would approve any device with
			// the same USB ID. (portPath is always set on Linux in practice —
			// usbParent returned ok — but the check is cheap insurance.)
			continue
		}
		out = append(out, Device{
			Path:        "/dev/" + name,
			VID:         strings.ToLower(vid),
			PID:         strings.ToLower(pid),
			Serial:      serial,
			PortPath:    portPath,
			Description: readAttr(usbDir, "product"),
			// The interface number distinguishes the UARTs of a multi-interface
			// bridge (FT2232H, CP2105), which share every other attribute. It is
			// read from the tty's enclosing interface directory — see
			// interfaceNumber for why that is not always `target` itself.
			Interface: interfaceNumber(target),
		})
	}
	return out, nil
}

// usbParent walks up from a tty's device directory to the USB device that owns
// it, returning that directory and its port path — the directory's base name,
// e.g. "1-2" for a root-port device or "1-2.3" behind a hub.
//
// The port path is the fallback identity for devices with no serial number, so
// it must name the physical port rather than the interface below it.
func usbParent(dir string) (string, string, bool) {
	for d := dir; d != "/" && d != "."; d = filepath.Dir(d) {
		if _, err := os.Stat(filepath.Join(d, "idVendor")); err == nil {
			return d, filepath.Base(d), true
		}
	}
	return "", "", false
}

// interfaceDirRe matches a USB interface directory name,
// "<port>:<config>.<interface>" — e.g. "1-2:1.0", "1-2.3:1.1", "3-1.4.2:2.0".
var interfaceDirRe = regexp.MustCompile(`^\d+-\d+(?:\.\d+)*:\d+\.\d+$`)

// interfaceNumber extracts the USB interface number for a tty, given the
// resolved target of its sysfs "device" symlink. The number distinguishes the
// UARTs of a dual-interface bridge (FT2232H, CP2105), which share VID/PID and
// serial. It cannot simply parse `target`, because the two USB serial drivers
// arrange sysfs differently:
//
//   - cdc_acm ttys (ttyACM*) link straight to the interface directory,
//     ".../1-2/1-2:1.0".
//   - usb-serial ttys (ttyUSB*, i.e. FTDI, CP210x) link to a usb_serial_port
//     device nested one level below it, ".../1-2/1-2:1.0/ttyUSB0".
//
// An earlier version read filepath.Base(target) and so returned "" for every
// ttyUSB device — leaving the two ports of an FT2232H indistinguishable, which
// is exactly the hardware the discriminator exists for. Walk up to the first
// ancestor that is an interface directory instead, and take the number after
// its final dot. Empty when there is no such ancestor.
func interfaceNumber(target string) string {
	for d := target; d != "/" && d != "."; d = filepath.Dir(d) {
		base := filepath.Base(d)
		if !interfaceDirRe.MatchString(base) {
			continue
		}
		if i := strings.LastIndex(base, "."); i >= 0 && i+1 < len(base) {
			if n, err := strconv.Atoi(base[i+1:]); err == nil {
				return strconv.Itoa(n)
			}
		}
	}
	return ""
}

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
