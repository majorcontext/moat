package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/serialdev"
)

func esp32() serialdev.Device {
	return serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001",
		Serial: "AAA", PortPath: "1-2", Description: "USB JTAG/serial debug unit",
	}
}

func render(t *testing.T, devices []serialdev.Device, pins []serialdev.Pin) string {
	return renderUSB(t, devices, nil, pins)
}

func renderUSB(t *testing.T, devices, usbDevices []serialdev.Device, pins []serialdev.Pin) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printDevices(&buf, devices, usbDevices, pins); err != nil {
		t.Fatalf("printDevices: %v", err)
	}
	return buf.String()
}

func TestDeviceListShowsIdentityAndSuggestsConfig(t *testing.T) {
	out := render(t, []serialdev.Device{esp32()}, nil)

	for _, want := range []string{"/dev/ttyUSB0", "303a:1001", "AAA", "USB JTAG/serial debug unit"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	// The IDs are only useful if the user knows where to put them.
	if !strings.Contains(out, "devices:") || !strings.Contains(out, `usb: "303a:1001"`) {
		t.Fatalf("output should suggest a moat.yaml snippet:\n%s", out)
	}
}

func TestDeviceListShowsUnpinnedDevicesAsDash(t *testing.T) {
	// The PIN column (5th) must render "-" for an unpinned device: that dash
	// is what tells the user the next run will prompt for approval. The
	// tabwriter pads columns, so the assertion matches on the padded row.
	out := render(t, []serialdev.Device{esp32()}, nil)
	lines := strings.Split(out, "\n")
	var row string
	for _, l := range lines {
		if strings.HasPrefix(l, "/dev/ttyUSB0") {
			row = l
		}
	}
	if row == "" {
		t.Fatalf("no row for the device in:\n%s", out)
	}
	fields := strings.Fields(row)
	// DEVICE USB-ID IFACE SERIAL-NUMBER PIN DESCRIPTION; the description is
	// free text and may itself contain spaces, so index from the left for the
	// leading columns and check the PIN field directly.
	if len(fields) < 5 || fields[4] != "-" {
		t.Fatalf("PIN column = %v, want -:\n%s", fields, row)
	}
}

func TestDeviceListShowsThePinnedName(t *testing.T) {
	out := render(t, []serialdev.Device{esp32()},
		[]serialdev.Pin{serialdev.PinFor("esp32", esp32())})
	if !strings.Contains(out, "esp32") {
		t.Fatalf("output should name the pin:\n%s", out)
	}
}

func TestDeviceListShowsTheInterfaceNumber(t *testing.T) {
	// A dual-UART bridge lists one row per port with identical USB IDs; the
	// interface number is the column that tells them apart in moat.yaml.
	portA := esp32()
	portA.Interface = "0"
	portB := esp32()
	portB.Path = "/dev/ttyUSB1"
	portB.Interface = "1"
	out := render(t, []serialdev.Device{portA, portB}, nil)
	if !strings.Contains(out, "IFACE") {
		t.Fatalf("output should carry an IFACE column:\n%s", out)
	}
	// Both rows must show their interface: the selector is only usable if the
	// number is visible on each row.
	for _, row := range []string{
		"/dev/ttyUSB0  303a:1001  0",
		"/dev/ttyUSB1  303a:1001  1",
	} {
		if !strings.Contains(out, row) {
			t.Fatalf("output missing %q:\n%s", row, out)
		}
	}
}

func TestDeviceListShowsDashWithoutInterface(t *testing.T) {
	// Companion: single-UART devices (or platforms that cannot see the
	// interface) must render a dash, not a confusing 0.
	out := render(t, []serialdev.Device{esp32()}, nil)
	if !strings.Contains(out, "/dev/ttyUSB0  303a:1001  -") {
		t.Fatalf("a device with no interface number should show a dash in the IFACE column:\n%s", out)
	}
}

func TestDeviceListSeparatesBridgePortsWithTheirOwnPins(t *testing.T) {
	// Both ports of one bridge are pinned under different names: neither row
	// may report a MISMATCH just because the sibling pin fails to verify
	// against it.
	portA := esp32()
	portA.Interface = "0"
	portB := esp32()
	portB.Path = "/dev/ttyUSB1"
	portB.Interface = "1"
	pins := []serialdev.Pin{
		serialdev.PinFor("port-a", portA),
		serialdev.PinFor("port-b", portB),
	}
	out := render(t, []serialdev.Device{portA, portB}, pins)
	if strings.Contains(out, "MISMATCH") {
		t.Fatalf("sibling pins on one bridge must not flag a mismatch:\n%s", out)
	}
	if !strings.Contains(out, "port-a") || !strings.Contains(out, "port-b") {
		t.Fatalf("both rows should show their pin names:\n%s", out)
	}
}

func TestDeviceListFlagsAMismatchedDevice(t *testing.T) {
	// Same model, different unit: this is exactly what a run would reject, so
	// it must be visible before the run fails.
	attached := esp32()
	attached.Serial = "BBB"
	out := render(t, []serialdev.Device{attached},
		[]serialdev.Pin{serialdev.PinFor("esp32", esp32())})

	if !strings.Contains(out, "MISMATCH") {
		t.Fatalf("output should flag the mismatch:\n%s", out)
	}
	if !strings.Contains(out, "esp32") {
		t.Fatalf("output should name which pin mismatched:\n%s", out)
	}
}

func TestDeviceListExplainsPortPinningForSerialLessDevices(t *testing.T) {
	d := serialdev.Device{Path: "/dev/ttyUSB1", VID: "1a86", PID: "7523", PortPath: "1-3"}
	out := render(t, []serialdev.Device{d}, nil)
	if !strings.Contains(out, "port 1-3") {
		t.Fatalf("a device with no serial should say it pins by port:\n%s", out)
	}
}

func TestDeviceListWithNoDevicesExplainsWhy(t *testing.T) {
	// An empty table with no explanation reads like a bug.
	out := render(t, nil, nil)
	if !strings.Contains(out, "No serial devices attached") {
		t.Fatalf("empty state should say so:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "plug in") {
		t.Fatalf("empty state should tell the user what to do:\n%s", out)
	}
}

func TestDeviceListReportsPinsWhoseDeviceIsAbsent(t *testing.T) {
	// A stale pin is the cause of a later run failure; surface it here.
	out := render(t, nil, []serialdev.Pin{serialdev.PinFor("esp32", esp32())})
	if !strings.Contains(out, "not attached") {
		t.Fatalf("output should list pins with no attached device:\n%s", out)
	}
	if !strings.Contains(out, "moat device forget") {
		t.Fatalf("output should say how to clear one:\n%s", out)
	}
}

func TestDeviceListDoesNotReportAttachedPinsAsOrphans(t *testing.T) {
	// Companion of the orphan case: a pin whose device is present must not be
	// reported as missing.
	out := render(t, []serialdev.Device{esp32()},
		[]serialdev.Pin{serialdev.PinFor("esp32", esp32())})
	if strings.Contains(out, "not attached") {
		t.Fatalf("an attached pinned device must not be listed as absent:\n%s", out)
	}
}

func TestSuggestedNameFromDescription(t *testing.T) {
	cases := map[string]string{
		"USB JTAG/serial debug unit": "usb-jtag-serial-debug-unit",
		"CP2102 USB to UART Bridge":  "cp2102-usb-to-uart-bridge",
		"":                           "mydevice",
		"!!!":                        "mydevice",
	}
	for desc, want := range cases {
		d := esp32()
		d.Description = desc
		if got := suggestedName(d); got != want {
			t.Fatalf("suggestedName(%q) = %q, want %q", desc, got, want)
		}
	}
}

func TestSuggestedNameIsAValidDeviceName(t *testing.T) {
	// The suggestion is pasted straight into moat.yaml, so it has to satisfy
	// the same validation the config applies.
	d := esp32()
	d.Description = "  Weird --- Name__  "
	got := suggestedName(d)
	for _, r := range got {
		valid := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
		if !valid {
			t.Fatalf("suggestedName produced %q, which contains the invalid rune %q", got, r)
		}
	}
	if strings.HasPrefix(got, "-") || strings.HasSuffix(got, "-") {
		t.Fatalf("suggestedName produced %q, which cannot start or end with a dash", got)
	}
}

func nooelec() serialdev.Device {
	// Path empty: the whole point of a USB device with no serial interface.
	return serialdev.Device{
		VID: "0bda", PID: "2838", Serial: "00000001",
		PortPath: "1-3", Description: "RTL2832U",
	}
}

func TestDeviceListShowsNonSerialUSBDevices(t *testing.T) {
	out := renderUSB(t, []serialdev.Device{esp32()}, []serialdev.Device{nooelec()}, nil)
	for _, want := range []string{"0bda:2838", "RTL2832U", "00000001"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "no serial interface") {
		t.Fatalf("the section must say these cannot go through devices::\n%s", out)
	}
	// The section must NOT prescribe a fix. These rows are keyboards, cameras
	// and LAN adapters as often as SDRs, and no single remedy covers them —
	// an earlier version told the user to allow a host port with
	// `network: host:`, which is unnecessary under a permissive policy and
	// insufficient under a strict one, and is meaningless for a keyboard.
	for _, overfit := range []string{"network: host:", "rtl_tcp", "sample server"} {
		if strings.Contains(out, overfit) {
			t.Fatalf("the section prescribes %q, which does not apply to most of what it lists:\n%s", overfit, out)
		}
	}
	// The USB device must not be presented as usable by the serial broker.
	if !strings.Contains(out, "not usable with devices:") {
		t.Fatalf("the section must say devices: does not apply:\n%s", out)
	}
}

func TestDeviceListUSBOnlyDoesNotClaimNothingIsAttached(t *testing.T) {
	// The case that motivated the section: an SDR plugged in, `moat device
	// list` showing "No serial devices attached" and "plug in a device" —
	// implying moat cannot see hardware the user is looking at.
	out := renderUSB(t, nil, []serialdev.Device{nooelec()}, nil)
	if !strings.Contains(out, "No serial devices attached") {
		t.Fatalf("serial section must still say there are none:\n%s", out)
	}
	if !strings.Contains(out, "no USB device below has a serial interface") {
		t.Fatalf("empty state must acknowledge the attached USB device:\n%s", out)
	}
	if !strings.Contains(out, "0bda:2838") {
		t.Fatalf("the attached SDR must still be listed:\n%s", out)
	}
	// Telling someone to plug hardware in when they already have is the
	// original complaint; with a USB device present the copy must not do it.
	if strings.Contains(out, "Plug in a") {
		t.Fatalf("empty state must not tell a user with a USB device attached to plug one in:\n%s", out)
	}
}

func TestDeviceListNoUSBDevicesPrintsNoUSBSection(t *testing.T) {
	// Companion: with nothing non-serial attached, the section must not appear
	// (and especially not with the old "plug in" advice when a serial device
	// IS attached).
	out := renderUSB(t, []serialdev.Device{esp32()}, nil, nil)
	if strings.Contains(out, "Other USB devices") {
		t.Fatalf("USB section should not appear with no USB devices:\n%s", out)
	}
	out = renderUSB(t, nil, nil, nil)
	if strings.Contains(out, "Other USB devices") {
		t.Fatalf("USB section should not appear with no USB devices:\n%s", out)
	}
}

// runForget executes `moat device forget` with MOAT_HOME pointing at a temp
// home, returning stdout. The command reads its pin store through
// DefaultPinPath(), which MOAT_HOME relocates — that indirection is what makes
// the command testable without touching a real device pin.
func runForget(t *testing.T, pins []serialdev.Pin, name string) (string, error) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("MOAT_HOME", home)
	if len(pins) > 0 {
		store, err := serialdev.OpenPinStore(filepath.Join(home, "devices.json"))
		if err != nil {
			t.Fatalf("OpenPinStore: %v", err)
		}
		for _, p := range pins {
			if err := store.Put(p); err != nil {
				t.Fatalf("Save: %v", err)
			}
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
	}

	var out bytes.Buffer
	cmd := &cobra.Command{Use: "forget"}
	cmd.SetOut(&out)
	err := forgetDevice(cmd, []string{name})
	return out.String(), err
}

func TestDeviceForgetRemovesThePin(t *testing.T) {
	out, err := runForget(t, []serialdev.Pin{serialdev.PinFor("esp32", esp32())}, "esp32")
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if !strings.Contains(out, "Forgot device esp32") {
		t.Fatalf("output should confirm the forget:\n%s", out)
	}
	// The companion assertion: the pin is really gone, not merely reported so.
	home := os.Getenv("MOAT_HOME")
	store, err := serialdev.OpenPinStore(filepath.Join(home, "devices.json"))
	if err != nil {
		t.Fatalf("reopening the pin store: %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup
	if _, ok, err := store.Get("esp32"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if ok {
		t.Fatal("the pin must be gone after forgetting it")
	}
}

func TestDeviceForgetOfAnUnknownNameIsAnActionableError(t *testing.T) {
	_, err := runForget(t, nil, "esp32")
	if err == nil {
		t.Fatal("forgetting an unpinned name must fail")
	}
	if !strings.Contains(err.Error(), `no device named "esp32" is pinned`) {
		t.Fatalf("error %q should name the missing pin and point at device list", err)
	}
	if !strings.Contains(err.Error(), "moat device list") {
		t.Fatalf("error %q should point at `moat device list`", err)
	}
}
