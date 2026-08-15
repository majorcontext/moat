//go:build linux

package serialdev

import (
	"os"
	"path/filepath"
	"testing"
)

// fakeSysfs builds the subset of sysfs the enumerator reads:
//
//	/sys/class/tty/ttyUSB0/device -> /sys/devices/pci0000:00/usb1/1-2/1-2:1.0
//	/sys/devices/pci0000:00/usb1/1-2/{idVendor,idProduct,serial,product}
func fakeSysfs(t *testing.T, serial string) string {
	t.Helper()
	root := t.TempDir()
	usbDev := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2")
	iface := filepath.Join(usbDev, "1-2:1.0")
	if err := os.MkdirAll(iface, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, val string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(usbDev, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("idVendor", "303a")
	write("idProduct", "1001")
	write("product", "USB JTAG/serial debug unit")
	if serial != "" {
		write("serial", serial)
	}
	ttyDir := filepath.Join(root, "class", "tty", "ttyUSB0")
	if err := os.MkdirAll(ttyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(iface, filepath.Join(ttyDir, "device")); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestEnumerateSysfsReadsUSBIdentity(t *testing.T) {
	got, err := enumerateSysfs(fakeSysfs(t, "AAA"))
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1: %+v", len(got), got)
	}
	d := got[0]
	if d.Path != "/dev/ttyUSB0" || d.VID != "303a" || d.PID != "1001" || d.Serial != "AAA" {
		t.Fatalf("got %+v, want /dev/ttyUSB0 303a:1001 AAA", d)
	}
	if d.PortPath != "1-2" {
		t.Fatalf("PortPath = %q, want the USB port path 1-2", d.PortPath)
	}
	if d.Description != "USB JTAG/serial debug unit" {
		t.Fatalf("Description = %q, want the product string", d.Description)
	}
}

func TestEnumerateSysfsSerialLessDeviceStillEnumerates(t *testing.T) {
	got, err := enumerateSysfs(fakeSysfs(t, ""))
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want 1", len(got))
	}
	if got[0].Serial != "" {
		t.Fatalf("Serial = %q, want empty for a device without one", got[0].Serial)
	}
	if got[0].PortPath != "1-2" {
		t.Fatalf("PortPath = %q, want 1-2 — it is the only identity such a device has", got[0].PortPath)
	}
}

func TestEnumerateSysfsSkipsNonUSBTTYs(t *testing.T) {
	root := fakeSysfs(t, "AAA")
	// A console tty has no `device` symlink at all.
	if err := os.MkdirAll(filepath.Join(root, "class", "tty", "ttyS0"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	for _, d := range got {
		if d.Path == "/dev/ttyS0" {
			t.Fatal("non-USB tty must not be enumerated")
		}
	}
	if len(got) != 1 {
		t.Fatalf("got %d devices, want only the USB one", len(got))
	}
}

func TestEnumerateSysfsSkipsNonUSBPlatformDevices(t *testing.T) {
	// A platform serial port does have a `device` symlink, but no USB ancestor
	// with idVendor — the walk must not misattribute it.
	root := fakeSysfs(t, "AAA")
	plat := filepath.Join(root, "devices", "platform", "serial8250")
	if err := os.MkdirAll(plat, 0o755); err != nil {
		t.Fatal(err)
	}
	ttyDir := filepath.Join(root, "class", "tty", "ttyS1")
	if err := os.MkdirAll(ttyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(plat, filepath.Join(ttyDir, "device")); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/dev/ttyUSB0" {
		t.Fatalf("got %+v, want only the USB-backed device", got)
	}
}

func TestEnumerateSysfsSkipsDeviceWithoutVendorID(t *testing.T) {
	root := fakeSysfs(t, "AAA")
	if err := os.Remove(filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2", "idVendor")); err != nil {
		t.Fatal(err)
	}
	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none — a device with no vendor ID cannot be pinned", got)
	}
}

func TestEnumerateSysfsMissingTTYClassIsAnError(t *testing.T) {
	if _, err := enumerateSysfs(filepath.Join(t.TempDir(), "nonexistent")); err == nil {
		t.Fatal("a missing /sys/class/tty should be reported, not treated as no devices")
	}
}
