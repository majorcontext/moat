//go:build linux

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

func readAttr(dir, name string) string {
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}
