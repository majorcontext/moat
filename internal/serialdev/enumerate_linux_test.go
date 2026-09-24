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
	if d.Interface != "0" {
		t.Fatalf("Interface = %q, want 0 — the fixture's tty lives on interface 1-2:1.0", d.Interface)
	}
}

func TestEnumerateSysfsSeparatesDualInterfaceBridges(t *testing.T) {
	// One USB device, two ttys, one per interface (FT2232H shape: "1-2:1.0"
	// and "1-2:1.1"). Without the interface discriminator the two Devices are
	// byte-identical, Match reports them ambiguous, and the user is told to
	// "unplug all but one" — of a single plug.
	root := fakeSysfs(t, "FT7ABCDE")
	usbDev := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2")
	iface1 := filepath.Join(usbDev, "1-2:1.1")
	if err := os.MkdirAll(iface1, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "class", "tty", "ttyUSB1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(iface1, filepath.Join(root, "class", "tty", "ttyUSB1", "device")); err != nil {
		t.Fatal(err)
	}

	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d devices, want one per tty: %+v", len(got), got)
	}
	ids := map[string]string{}
	for _, d := range got {
		ids[d.Interface] = d.Path
	}
	if ids["0"] != "/dev/ttyUSB0" || ids["1"] != "/dev/ttyUSB1" {
		t.Fatalf("interface->path = %v, want 0->/dev/ttyUSB0 1->/dev/ttyUSB1", ids)
	}
	// The two identities must differ — that is what lets them pin separately.
	if got[0].Identity() == got[1].Identity() {
		t.Fatalf("both ttys share identity %+v; the interface discriminator is missing", got[0].Identity())
	}
}

func TestEnumerateSysfsReadsInterfaceForNestedUSBSerialPorts(t *testing.T) {
	// The real usb-serial (FTDI/CP210x) layout the previous code got wrong: the
	// tty's `device` link points at a usb_serial_port device nested one level
	// below the interface directory (".../1-2:1.0/ttyUSB0"), not at the
	// interface directory itself the way cdc_acm does. Reading filepath.Base of
	// the target yielded "ttyUSB0" → interface "" → both ports of an FT2232H
	// indistinguishable. Build that exact shape for a dual-UART bridge and
	// assert each port carries its interface number.
	root := t.TempDir()
	usbDev := filepath.Join(root, "devices", "pci0000:00", "usb1", "1-2")
	if err := os.MkdirAll(usbDev, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, val := range map[string]string{"idVendor": "0403", "idProduct": "6010", "serial": "FT7ABCDE", "product": "Dual RS232-HS"} {
		if err := os.WriteFile(filepath.Join(usbDev, name), []byte(val+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []struct{ iface, tty string }{{"1-2:1.0", "ttyUSB0"}, {"1-2:1.1", "ttyUSB1"}} {
		portDir := filepath.Join(usbDev, p.iface, p.tty) // the nested usb_serial_port device
		if err := os.MkdirAll(portDir, 0o755); err != nil {
			t.Fatal(err)
		}
		ttyDir := filepath.Join(root, "class", "tty", p.tty)
		if err := os.MkdirAll(ttyDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(portDir, filepath.Join(ttyDir, "device")); err != nil {
			t.Fatal(err)
		}
	}

	got, err := enumerateSysfs(root)
	if err != nil {
		t.Fatalf("enumerateSysfs: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d devices, want one per tty: %+v", len(got), got)
	}
	ifaces := map[string]string{}
	for _, d := range got {
		ifaces[d.Path] = d.Interface
		if d.PortPath != "1-2" {
			t.Fatalf("%s PortPath = %q, want 1-2 (the USB device, not the nested port dir)", d.Path, d.PortPath)
		}
	}
	if ifaces["/dev/ttyUSB0"] != "0" || ifaces["/dev/ttyUSB1"] != "1" {
		t.Fatalf("path->interface = %v, want ttyUSB0->0 ttyUSB1->1", ifaces)
	}
	if got[0].Identity() == got[1].Identity() {
		t.Fatalf("both ttys share identity %+v; the nested-layout interface read is broken", got[0].Identity())
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
