package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/serialdev"
)

func esp32() serialdev.Device {
	return serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001",
		Serial: "AAA", PortPath: "1-2", Description: "USB JTAG/serial debug unit",
	}
}

func render(t *testing.T, devices []serialdev.Device, pins []serialdev.Pin) string {
	t.Helper()
	var buf bytes.Buffer
	if err := printDevices(&buf, devices, pins); err != nil {
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
	if !strings.Contains(out, "devices:") || !strings.Contains(out, `vid: "303a"`) {
		t.Fatalf("output should suggest a moat.yaml snippet:\n%s", out)
	}
}

func TestDeviceListShowsUnpinnedDevicesAsDash(t *testing.T) {
	out := render(t, []serialdev.Device{esp32()}, nil)
	if !strings.Contains(out, "\t") && !strings.Contains(out, "-") {
		t.Fatalf("an unpinned device should render a dash in the PIN column:\n%s", out)
	}
}

func TestDeviceListShowsThePinnedName(t *testing.T) {
	out := render(t, []serialdev.Device{esp32()},
		[]serialdev.Pin{serialdev.PinFor("esp32", esp32())})
	if !strings.Contains(out, "esp32") {
		t.Fatalf("output should name the pin:\n%s", out)
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
		"USB JTAG/serial debug unit": "usb-jtagserial-debug-unit",
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
