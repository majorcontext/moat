// cmd/moat/cli/device_preflight_test.go
package cli

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialtest"
)

// newTestPreflight builds a pre-flight against a temp pin store and a fake
// enumerator, so tests neither touch the user's real pins nor need hardware.
func newTestPreflight(t *testing.T, attached ...serialdev.Device) (devicePreflight, string) {
	t.Helper()
	dir := t.TempDir()
	return devicePreflight{
		enum:    serialtest.NewFakeEnumerator(attached...),
		pinPath: filepath.Join(dir, "devices.json"),
		out:     nil, // set by the caller
	}, dir
}

func TestPreflightDevicesReportsEveryMissingDevice(t *testing.T) {
	// Both devices are absent, so both findings must appear at once — the
	// point of the pre-flight is that the user fixes everything in one pass
	// instead of failing one device at a time inside create.
	pf, _ := newTestPreflight(t)
	devices := []config.DeviceEntry{
		{Name: "esp32", Match: config.DeviceMatch{USB: "303a:1001"}},
		{Name: "probe", Match: config.DeviceMatch{USB: "0403:6010"}},
	}
	var out bytes.Buffer
	pf.out = &out
	if preflightDevices(context.Background(), devices, pf) {
		t.Fatal("preflightDevices reported success for absent devices")
	}
	for _, name := range []string{"esp32", "probe"} {
		if !strings.Contains(out.String(), name) {
			t.Errorf("output should name %s:\n%s", name, out.String())
		}
	}
}

func TestPreflightDevicesPrintsFixCommandForMismatch(t *testing.T) {
	// Companion of the absent case at the other failure mode: a present but
	// different device is a pin mismatch, and its remedy is `moat device
	// forget`, which must be printed rather than left inside the detail text.
	pf, dir := newTestPreflight(t, serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "BBB", PortPath: "1-2",
	})
	pins, err := serialdev.OpenPinStore(filepath.Join(dir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pins.Put(serialdev.PinFor("esp32", serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2",
	})); err != nil {
		t.Fatal(err)
	}

	devices := []config.DeviceEntry{{Name: "esp32", Match: config.DeviceMatch{USB: "303a:1001"}}}
	var out bytes.Buffer
	pf.out = &out
	if preflightDevices(context.Background(), devices, pf) {
		t.Fatal("preflightDevices reported success for a pin mismatch")
	}
	if !strings.Contains(out.String(), "moat device forget esp32") {
		t.Errorf("output should carry the fix command:\n%s", out.String())
	}
}

func TestPreflightDevicesPrintsConsentNoticeForNewPin(t *testing.T) {
	// First use approves the device: the notice must name the device, say
	// what the run can do to the hardware, and state the TOFU contract.
	pf, _ := newTestPreflight(t, serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2",
	})
	devices := []config.DeviceEntry{{Name: "esp32", Match: config.DeviceMatch{USB: "303a:1001"}}}
	var out bytes.Buffer
	pf.out = &out
	if !preflightDevices(context.Background(), devices, pf) {
		t.Fatalf("preflightDevices failed on an approvable device:\n%s", out.String())
	}
	for _, want := range []string{"esp32", "303a:1001", "serial AAA"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("consent notice should mention %q:\n%s", want, out.String())
		}
	}
}

func TestPreflightDevicesWarnsWhenPinIsByPortPath(t *testing.T) {
	// The serial-less clone is the case the design says must be stated
	// plainly: the pin approves whatever is plugged into that port, not this
	// particular unit. The notice carries that warning verbatim.
	pf, _ := newTestPreflight(t, serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "1a86", PID: "7523", PortPath: "1-3",
	})
	devices := []config.DeviceEntry{{Name: "ch340", Match: config.DeviceMatch{USB: "1a86:7523"}}}
	var out bytes.Buffer
	pf.out = &out
	if !preflightDevices(context.Background(), devices, pf) {
		t.Fatalf("preflightDevices failed on an approvable device:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "pins whatever is plugged into port") {
		t.Errorf("a port-path pin must be called out in the notice:\n%s", out.String())
	}
}

func TestPreflightDevicesSilentWhenAlreadyPinned(t *testing.T) {
	// Companion of the consent cases: a device pinned by an earlier run is
	// old news — printing a first-use notice for it would train the user to
	// ignore the notice.
	pf, dir := newTestPreflight(t, serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2",
	})
	pins, err := serialdev.OpenPinStore(filepath.Join(dir, "devices.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := pins.Put(serialdev.PinFor("esp32", serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2",
	})); err != nil {
		t.Fatal(err)
	}

	devices := []config.DeviceEntry{{Name: "esp32", Match: config.DeviceMatch{USB: "303a:1001"}}}
	var out bytes.Buffer
	pf.out = &out
	if !preflightDevices(context.Background(), devices, pf) {
		t.Fatalf("preflightDevices failed on a pinned device:\n%s", out.String())
	}
	if out.Len() != 0 {
		t.Errorf("expected no output for an already-pinned device, got:\n%s", out.String())
	}
}

func TestPreflightDevicesSilentWithNoDevicesConfigured(t *testing.T) {
	// Companion: runs without devices must not touch the pin store at all —
	// serial is opt-in.
	pf, _ := newTestPreflight(t)
	var out bytes.Buffer
	pf.out = &out
	if !preflightDevices(context.Background(), nil, pf) {
		t.Fatal("preflightDevices failed with no devices configured")
	}
	if out.Len() != 0 {
		t.Errorf("expected no output with no devices configured, got:\n%s", out.String())
	}
}

func TestDetectNewPinsMatchesWhatResolveDevicesPins(t *testing.T) {
	// Drift guard for the new detector: what DetectNewPins reports as a new
	// approval must be exactly what ResolveDevices records, or the consent
	// notice could describe a pin the run never takes — or approve silently
	// a device the notice never showed.
	dir := t.TempDir()
	devices := []config.DeviceEntry{
		{Name: "esp32", Match: config.DeviceMatch{USB: "303a:1001"}},
		{Name: "ch340", Match: config.DeviceMatch{USB: "1a86:7523"}},
	}
	attached := []serialdev.Device{
		{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"},
		{Path: "/dev/ttyUSB1", VID: "1a86", PID: "7523", PortPath: "1-3"},
	}

	store := func() *serialdev.PinStore {
		s, err := serialdev.OpenPinStore(filepath.Join(dir, "devices.json"))
		if err != nil {
			t.Fatal(err)
		}
		return s
	}

	detected := run.DetectNewPins(context.Background(), devices,
		serialtest.NewFakeEnumerator(attached...), store())

	// ResolveDevices is what actually records pins; run it against the same
	// store and compare what landed.
	if _, err := run.ResolveDevices(context.Background(), devices,
		serialtest.NewFakeEnumerator(attached...), store()); err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	recorded, err := store().List()
	if err != nil {
		t.Fatal(err)
	}

	if len(detected) != len(recorded) {
		t.Fatalf("DetectNewPins reported %d new pins, ResolveDevices recorded %d", len(detected), len(recorded))
	}
	byName := map[string]bool{}
	for _, p := range recorded {
		byName[p.Name] = true
	}
	for _, p := range detected {
		if !byName[p.Name] {
			t.Errorf("DetectNewPins reported %q but ResolveDevices pinned nothing under that name", p.Name)
		}
		if p.Name == "ch340" && !p.ByPortPath {
			t.Errorf("ch340 has no serial number; DetectNewPins must report ByPortPath")
		}
	}
}
