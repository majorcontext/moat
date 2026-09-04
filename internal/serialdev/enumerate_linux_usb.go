//go:build linux

package serialdev

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// ListUSB implements USBEnumerator by walking /sys/bus/usb/devices and keeping
// devices the serial broker cannot serve: USB devices with no tty at all.
//
// This is the "why doesn't my SDR show up" surface. The serial walk
// (enumerateSysfs) starts from /sys/class/tty, so a device with no serial
// interface — an RTL2832U SDR dongle, a keyboard — is invisible to it by
// construction. This walk starts from the USB side instead, then drops every
// device the serial walk already covers, plus hubs and ID-less devices: the
// point is showing identifiable hardware, not listing the bus.
func (e *sysfsEnumerator) ListUSB(_ context.Context) ([]Device, error) {
	return enumerateSysfsUSB(e.root)
}

// enumerateSysfsUSB walks /sys/bus/usb/devices and returns USB devices with no
// serial interface. root is injectable so tests can build a fake tree.
func enumerateSysfsUSB(root string) ([]Device, error) {
	usbBus := filepath.Join(root, "bus", "usb", "devices")
	entries, err := os.ReadDir(usbBus)
	if err != nil {
		return nil, fmt.Errorf("listing USB devices in %s: %w", usbBus, err)
	}

	// Serial-backed devices, keyed by USB port path, so a composite device with
	// both a tty and other interfaces (a phone in modem mode) is not double-
	// reported as serial-less.
	serialPaths := serialPortPaths(root)

	var out []Device
	for _, e := range entries {
		name := e.Name()
		// Interfaces ("1-2:1.0") and roothubs ("usb1") are not devices.
		if strings.Contains(name, ":") || strings.HasPrefix(name, "usb") {
			continue
		}
		dir := filepath.Join(usbBus, name)
		if !e.IsDir() && e.Type() == fs.ModeSymlink {
			// /sys/bus/usb/devices entries are symlinks into /sys/devices.
			if resolved, err := filepath.EvalSymlinks(dir); err == nil {
				dir = resolved
			}
		}
		vid := readAttr(dir, "idVendor")
		pid := readAttr(dir, "idProduct")
		if vid == "" || pid == "" {
			continue // unidentifiable
		}
		// bDeviceClass 9 = hub: host infrastructure, not a plugged-in device
		// anyone is looking for. Roothubs ("usbN") were already skipped by
		// name; this drops downstream hubs. The attribute is hex ("09" on a
		// real kernel — sysfs emits it with %02x), so parse rather than
		// string-compare; an absent or unreadable class is per-interface
		// (0), not a hub. Class-0 controller hubs — the same hardware the
		// macOS walk filters by name — fall through to the hubByName
		// backstop below.
		if isHubClass(readAttr(dir, "bDeviceClass")) {
			continue
		}
		description := readAttr(dir, "product")
		if hubByName(description) {
			continue
		}
		portPath := name
		if serialPaths[portPath] {
			continue // already covered by the serial walk
		}
		out = append(out, Device{
			VID:         strings.ToLower(vid),
			PID:         strings.ToLower(pid),
			Serial:      readAttr(dir, "serial"),
			PortPath:    portPath,
			Description: description,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PortPath < out[j].PortPath })
	return out, nil
}

// isHubClass reports whether a sysfs bDeviceClass value names a hub (USB class
// 9). Kernels emit it as two hex digits ("09"), but tolerate the unpadded
// form for hand-built trees. An empty or unparsable value is not a hub —
// classless (per-interface) devices are the common case, and dropping them
// on a read hiccup would hide real hardware.
func isHubClass(s string) bool {
	v, err := strconv.ParseUint(s, 16, 8)
	return err == nil && v == 9
}

// serialPortPaths returns the USB port paths of every serial-backed device,
// i.e. the same set enumerateSysfs reports, keyed for membership checks.
func serialPortPaths(root string) map[string]bool {
	ttys, err := enumerateSysfs(root)
	if err != nil {
		// The serial walk failing (no /sys/class/tty) also means no serial
		// devices to exclude; the USB walk reports everything identifiable.
		return nil
	}
	paths := make(map[string]bool, len(ttys))
	for _, d := range ttys {
		paths[d.PortPath] = true
	}
	return paths
}
