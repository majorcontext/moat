//go:build linux

package serialdev

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
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
		out = append(out, Device{
			Path:        "/dev/" + name,
			VID:         strings.ToLower(vid),
			PID:         strings.ToLower(pid),
			Serial:      readAttr(usbDir, "serial"),
			PortPath:    portPath,
			Description: readAttr(usbDir, "product"),
			// The tty's interface directory is "1-2:1.0" — port:config.iface.
			// The trailing number distinguishes the UARTs of a multi-interface
			// bridge (FT2232H, CP2105), which share every other attribute.
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

// interfaceNumber extracts the USB interface number from a tty's sysfs
// directory, whose name is "<port>:<config>.<interface>" — "1-2:1.0" is port
// 1-2, configuration 1, interface 0. Dual-UART bridges (FT2232H, CP2105)
// expose "1-2:1.0" and "1-2:1.1" for their two ttys; the trailing number is
// the only attribute that tells them apart.
func interfaceNumber(ifaceDir string) string {
	name := filepath.Base(ifaceDir)
	if i := strings.LastIndex(name, "."); i >= 0 && i+1 < len(name) {
		if n, err := strconv.Atoi(name[i+1:]); err == nil {
			return strconv.Itoa(n)
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
