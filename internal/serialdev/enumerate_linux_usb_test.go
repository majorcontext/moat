//go:build linux

package serialdev

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfsUSB extends fakeSysfs with a second USB device that has no tty —
// an SDR dongle — plus the pieces the USB walk reads: the bus/devices
// directory of symlinks and a hub to be skipped.
func fakeSysfsUSB(t *testing.T) string {
	t.Helper()
	root := fakeSysfs(t, "AAA") // gives 1-2 with a tty (idVendor 303a etc.)

	// A USB device with no serial interface: the RTL2832U.
	sdr := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-3")
	if err := os.MkdirAll(filepath.Join(sdr, "1-3:1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(dir, name, val string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(sdr, "idVendor", "0bda")
	write(sdr, "idProduct", "2838")
	write(sdr, "serial", "00000001")
	write(sdr, "product", "RTL2832U")
	// bDeviceClass 0 = per-interface; anything other than 9 (hub) is fine.
	write(sdr, "bDeviceClass", "0")

	// A hub: identical shape, but class 9.
	hub := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-1")
	if err := os.MkdirAll(filepath.Join(hub, "1-1:1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(hub, "idVendor", "1d6b")
	write(hub, "idProduct", "0002")
	write(hub, "bDeviceClass", "9")
	write(hub, "product", "USB 2.0 Hub")

	// The USB walk reads /sys/bus/usb/devices, whose entries are symlinks
	// into /sys/devices. The real bus directory also holds the roothub
	// ("usb1") and interfaces ("1-2:1.0") — both must be skipped by name.
	busDir := filepath.Join(root, "bus", "usb", "devices")
	if err := os.MkdirAll(busDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, entry := range []struct{ link, target string }{
		{"usb1", filepath.Join(root, "devices", "pci0000:00", "usb1")},
		{"1-1", hub},
		{"1-2", filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2")},
		{"1-2:1.0", filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2", "1-2:1.0")},
		{"1-3", sdr},
		{"1-3:1.0", filepath.Join(sdr, "1-3:1.0")},
	} {
		if err := os.Symlink(entry.target, filepath.Join(busDir, entry.link)); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func findByPortPath(devs []Device, portPath string) (Device, bool) {
	for _, d := range devs {
		if d.PortPath == portPath {
			return d, true
		}
	}
	return Device{}, false
}

func TestEnumerateSysfsUSBFindsNonSerialDevices(t *testing.T) {
	got, err := enumerateSysfsUSB(fakeSysfsUSB(t))
	if err != nil {
		t.Fatalf("enumerateSysfsUSB: %v", err)
	}
	d, ok := findByPortPath(got, "1-3")
	if !ok {
		t.Fatalf("SDR not found in %+v", got)
	}
	if d.Path != "" {
		t.Fatalf("Path = %q, want empty — the device has no tty", d.Path)
	}
	if d.VID != "0bda" || d.PID != "2838" {
		t.Fatalf("got %s:%s, want 0bda:2838 (RTL2832U)", d.VID, d.PID)
	}
	if d.Serial != "00000001" {
		t.Fatalf("Serial = %q, want 00000001", d.Serial)
	}
	if d.Description != "RTL2832U" {
		t.Fatalf("Description = %q, want RTL2832U", d.Description)
	}
}

func TestEnumerateSysfsUSBSkipsSerialBackedDevices(t *testing.T) {
	// Companion: the device with a tty (1-2) is the serial walk's output and
	// must not double-report here.
	got, err := enumerateSysfsUSB(fakeSysfsUSB(t))
	if err != nil {
		t.Fatalf("enumerateSysfsUSB: %v", err)
	}
	if _, ok := findByPortPath(got, "1-2"); ok {
		t.Fatal("a serial-backed device must not appear in the USB list")
	}
}

func TestEnumerateSysfsUSBSkipsHubsAndRootHubs(t *testing.T) {
	got, err := enumerateSysfsUSB(fakeSysfsUSB(t))
	if err != nil {
		t.Fatalf("enumerateSysfsUSB: %v", err)
	}
	if _, ok := findByPortPath(got, "1-1"); ok {
		t.Fatal("a hub (bDeviceClass 9) must be skipped")
	}
	if _, ok := findByPortPath(got, "usb1"); ok {
		t.Fatal("the roothub (usbN) must be skipped")
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want only the SDR: %+v", len(got), got)
	}
}

func TestEnumerateSysfsUSBMissingBusIsAnError(t *testing.T) {
	if _, err := enumerateSysfsUSB(filepath.Join(t.TempDir(), "nonexistent")); err == nil {
		t.Fatal("a missing /sys/bus/usb/devices should be reported, not treated as no devices")
	}
}
