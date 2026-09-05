package run

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialtest"
	"gopkg.in/yaml.v3"
)

func esp32Entry() config.DeviceEntry {
	return config.DeviceEntry{
		Name:  "esp32",
		Match: config.DeviceMatch{USB: "303a:1001"},
	}
}

func esp32Device(serial string) serialdev.Device {
	return serialdev.Device{
		Path: "/dev/ttyUSB0", VID: "303a", PID: "1001",
		Serial: serial, PortPath: "1-2",
	}
}

func newPins(t *testing.T) *serialdev.PinStore {
	t.Helper()
	s, err := serialdev.OpenPinStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatalf("OpenPinStore: %v", err)
	}
	return s
}

func TestDetectMissingDevicesFlagsAbsentDevice(t *testing.T) {
	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(), newPins(t))

	if len(missing) != 1 {
		t.Fatalf("got %d missing, want 1: %+v", len(missing), missing)
	}
	if missing[0].Reason != ReasonDeviceNotFound {
		t.Fatalf("Reason = %v, want ReasonDeviceNotFound", missing[0].Reason)
	}
	if !strings.Contains(missing[0].Detail, "moat device list") {
		t.Fatalf("detail %q should point at moat device list", missing[0].Detail)
	}
}

func TestDetectMissingDevicesPassesWhenPresent(t *testing.T) {
	// Companion of the absent case: a device that is attached must produce no
	// findings, or every run with hardware would be blocked.
	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), newPins(t))

	if len(missing) != 0 {
		t.Fatalf("got %+v, want no missing devices", missing)
	}
}

func TestDetectMissingDevicesWithNoDevicesConfiguredIsEmpty(t *testing.T) {
	if missing := DetectMissingDevices(context.Background(), nil,
		serialtest.NewFailingEnumerator(errors.New("should not be called")), newPins(t)); len(missing) != 0 {
		t.Fatalf("got %+v, want none — enumeration must not run when nothing is configured", missing)
	}
}

func TestDetectMissingDevicesFlagsPinMismatch(t *testing.T) {
	pins := newPins(t)
	if err := pins.Put(serialdev.PinFor("esp32", esp32Device("AAA"))); err != nil {
		t.Fatal(err)
	}
	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("BBB")), pins)

	if len(missing) != 1 {
		t.Fatalf("got %d missing, want 1: %+v", len(missing), missing)
	}
	if missing[0].Reason != ReasonDevicePinMismatch {
		t.Fatalf("Reason = %v, want ReasonDevicePinMismatch", missing[0].Reason)
	}
	if missing[0].FixCommand != "moat device forget esp32" {
		t.Fatalf("FixCommand = %q, want the forget command", missing[0].FixCommand)
	}
}

func TestDetectMissingDevicesPassesWhenPinMatches(t *testing.T) {
	pins := newPins(t)
	if err := pins.Put(serialdev.PinFor("esp32", esp32Device("AAA"))); err != nil {
		t.Fatal(err)
	}
	if missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), pins); len(missing) != 0 {
		t.Fatalf("got %+v, want none for the pinned device", missing)
	}
}

func TestDetectMissingDevicesFlagsAmbiguousMatch(t *testing.T) {
	other := esp32Device("BBB")
	other.Path = "/dev/ttyUSB1"
	other.PortPath = "1-3"

	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA"), other), newPins(t))

	if len(missing) != 1 || missing[0].Reason != ReasonDeviceAmbiguous {
		t.Fatalf("got %+v, want one ambiguous finding", missing)
	}
	// Both candidates must be named, or the user cannot tell which to unplug.
	if !strings.Contains(missing[0].Detail, "AAA") || !strings.Contains(missing[0].Detail, "BBB") {
		t.Fatalf("detail %q should name both candidates", missing[0].Detail)
	}
}

func TestAmbiguousMatchResolvesWhenPinned(t *testing.T) {
	// Companion of the ambiguous case: once a device is pinned, a second
	// matching device attached alongside it must not block the run.
	other := esp32Device("BBB")
	other.Path = "/dev/ttyUSB1"
	other.PortPath = "1-3"

	pins := newPins(t)
	if err := pins.Put(serialdev.PinFor("esp32", esp32Device("AAA"))); err != nil {
		t.Fatal(err)
	}
	specs, err := ResolveDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA"), other), pins)
	if err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	if len(specs) != 1 || specs[0].Serial != "AAA" {
		t.Fatalf("got %+v, want the pinned device", specs)
	}
}

func TestInterfaceSelectorPicksOneUARTOfABridge(t *testing.T) {
	// First use of a dual-UART bridge: without a pin to disambiguate, only the
	// interface selector can pick a port. The chosen port must be the one the
	// selector names, not whichever enumerated first.
	bridge := []config.DeviceEntry{{
		Name:  "port-a",
		Match: config.DeviceMatch{USB: "0403:6010", Interface: "1"},
	}}
	enum := serialtest.NewFakeEnumerator(
		serialdev.Device{Path: "/dev/ttyUSB0", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0"},
		serialdev.Device{Path: "/dev/ttyUSB1", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "1"},
	)
	specs, err := ResolveDevices(context.Background(), bridge, enum, newPins(t))
	if err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	if len(specs) != 1 || specs[0].Path != "/dev/ttyUSB1" {
		t.Fatalf("got %+v, want the interface-1 port", specs)
	}
	if specs[0].Interface != "1" {
		t.Fatalf("spec.Interface = %q, want \"1\" so the broker binds this port again", specs[0].Interface)
	}
}

func TestAmbiguousBridgeSuggestsTheInterfaceSelector(t *testing.T) {
	// Two ports of one bridge with no selector: the fix is a config change,
	// not unplugging — the error must say so.
	bridge := []config.DeviceEntry{{
		Name:  "port-a",
		Match: config.DeviceMatch{USB: "0403:6010"},
	}}
	enum := serialtest.NewFakeEnumerator(
		serialdev.Device{Path: "/dev/ttyUSB0", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0"},
		serialdev.Device{Path: "/dev/ttyUSB1", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "1"},
	)
	missing := DetectMissingDevices(context.Background(), bridge, enum, newPins(t))
	if len(missing) != 1 || missing[0].Reason != ReasonDeviceAmbiguous {
		t.Fatalf("got %+v, want one ambiguous finding", missing)
	}
	if !strings.Contains(missing[0].Detail, "interface:") {
		t.Fatalf("detail %q should suggest the interface selector", missing[0].Detail)
	}
	// The snippet must name the user's own device entry.
	if !strings.Contains(missing[0].Detail, "port-a") {
		t.Fatalf("detail %q should show the configured device name", missing[0].Detail)
	}
}

func TestAmbiguousDevicesStillSuggestUnplugging(t *testing.T) {
	// Companion: two separate boards with the same USB ID are not a bridge —
	// the interface selector would be wrong advice there.
	other := esp32Device("BBB")
	other.Path = "/dev/ttyUSB1"
	other.PortPath = "1-3"

	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA"), other), newPins(t))
	if len(missing) != 1 || missing[0].Reason != ReasonDeviceAmbiguous {
		t.Fatalf("got %+v, want one ambiguous finding", missing)
	}
	if !strings.Contains(missing[0].Detail, "Unplug") {
		t.Fatalf("detail %q should still suggest unplugging", missing[0].Detail)
	}
	if strings.Contains(missing[0].Detail, "interface:") {
		t.Fatalf("detail %q should not suggest the interface selector for separate boards", missing[0].Detail)
	}
}

func TestDetectMissingDevicesReportsEnumerationFailure(t *testing.T) {
	missing := DetectMissingDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFailingEnumerator(errors.New("ioreg exploded")), newPins(t))

	if len(missing) != 1 || missing[0].Reason != ReasonDeviceEnumerationFailed {
		t.Fatalf("got %+v, want an enumeration failure", missing)
	}
	if !strings.Contains(missing[0].Detail, "ioreg exploded") {
		t.Fatalf("detail %q should carry the underlying cause", missing[0].Detail)
	}
}

func TestResolveDevicesPinsOnFirstUse(t *testing.T) {
	pins := newPins(t)
	specs, err := ResolveDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), pins)
	if err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Path != "/dev/ttyUSB0" || specs[0].Serial != "AAA" {
		t.Fatalf("spec = %+v, want the attached device's identity", specs[0])
	}

	pin, ok, err := pins.Get("esp32")
	if err != nil || !ok {
		t.Fatalf("pin not recorded: ok=%v err=%v", ok, err)
	}
	if pin.Serial != "AAA" {
		t.Fatalf("pin = %+v, want serial AAA", pin)
	}
}

func TestResolveDevicesDoesNotRepinAnAlreadyPinnedDevice(t *testing.T) {
	pins := newPins(t)
	first := serialdev.PinFor("esp32", esp32Device("AAA"))
	if err := pins.Put(first); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), pins); err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	got, _, err := pins.Get("esp32")
	if err != nil {
		t.Fatal(err)
	}
	if !got.FirstSeen.Equal(first.FirstSeen) {
		t.Fatalf("FirstSeen changed from %v to %v; the original approval time must stand",
			first.FirstSeen, got.FirstSeen)
	}
}

func TestResolveDevicesCarriesRecordMode(t *testing.T) {
	entry := esp32Entry()
	entry.Record = config.RecordFull
	specs, err := ResolveDevices(context.Background(),
		[]config.DeviceEntry{entry},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), newPins(t))
	if err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	if specs[0].Record != config.RecordFull {
		t.Fatalf("Record = %q, want %q", specs[0].Record, config.RecordFull)
	}
}

func TestResolveDevicesDefaultsRecordMode(t *testing.T) {
	specs, err := ResolveDevices(context.Background(),
		[]config.DeviceEntry{esp32Entry()},
		serialtest.NewFakeEnumerator(esp32Device("AAA")), newPins(t))
	if err != nil {
		t.Fatalf("ResolveDevices: %v", err)
	}
	if specs[0].Record != config.RecordEvents {
		t.Fatalf("Record = %q, want %q", specs[0].Record, config.RecordEvents)
	}
}

func TestResolveDevicesDoesNotPinWhenAnotherDeviceFails(t *testing.T) {
	// A run that cannot start must not leave a pin behind: the user would then
	// be silently locked to a device they never successfully used.
	pins := newPins(t)
	entries := []config.DeviceEntry{
		esp32Entry(),
		{Name: "probe", Match: config.DeviceMatch{USB: "1a86:7523"}},
	}
	if _, err := ResolveDevices(context.Background(), entries,
		serialtest.NewFakeEnumerator(esp32Device("AAA")), pins); err == nil {
		t.Fatal("resolution should fail when one device is absent")
	}
	if _, ok, _ := pins.Get("esp32"); ok {
		t.Fatal("no pin should be recorded when the run cannot start")
	}
}

// TestDetectMissingDevicesMatchesResolve is the drift guard between the CLI
// pre-flight and the Create gate. It asserts both directions: everything the
// detector flags is rejected by ResolveDevices, and everything it passes is
// accepted. Letting these diverge would produce a run that passes pre-flight
// and then fails during create, or vice versa.
func TestDetectMissingDevicesMatchesResolve(t *testing.T) {
	other := esp32Device("BBB")
	other.Path = "/dev/ttyUSB1"
	other.PortPath = "1-3"

	noSerial := serialdev.Device{Path: "/dev/ttyUSB2", VID: "1a86", PID: "7523", PortPath: "1-4"}
	noSerialMoved := noSerial
	noSerialMoved.PortPath = "1-9"

	cases := []struct {
		name     string
		entries  []config.DeviceEntry
		attached []serialdev.Device
		pin      *serialdev.Pin
	}{
		{"absent", []config.DeviceEntry{esp32Entry()}, nil, nil},
		{"present unpinned", []config.DeviceEntry{esp32Entry()}, []serialdev.Device{esp32Device("AAA")}, nil},
		{
			"pin matches",
			[]config.DeviceEntry{esp32Entry()},
			[]serialdev.Device{esp32Device("AAA")},
			ptr(serialdev.PinFor("esp32", esp32Device("AAA"))),
		},
		{
			"pin mismatch",
			[]config.DeviceEntry{esp32Entry()},
			[]serialdev.Device{esp32Device("BBB")},
			ptr(serialdev.PinFor("esp32", esp32Device("AAA"))),
		},
		{"ambiguous", []config.DeviceEntry{esp32Entry()}, []serialdev.Device{esp32Device("AAA"), other}, nil},
		{
			"ambiguous but pinned",
			[]config.DeviceEntry{esp32Entry()},
			[]serialdev.Device{esp32Device("AAA"), other},
			ptr(serialdev.PinFor("esp32", esp32Device("AAA"))),
		},
		{
			"serial-less same port",
			[]config.DeviceEntry{{Name: "ch340", Match: config.DeviceMatch{USB: "1a86:7523"}}},
			[]serialdev.Device{noSerial},
			ptr(serialdev.PinFor("ch340", noSerial)),
		},
		{
			"serial-less moved port",
			[]config.DeviceEntry{{Name: "ch340", Match: config.DeviceMatch{USB: "1a86:7523"}}},
			[]serialdev.Device{noSerialMoved},
			ptr(serialdev.PinFor("ch340", noSerial)),
		},
		{"nothing configured", nil, []serialdev.Device{esp32Device("AAA")}, nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mkPins := func() *serialdev.PinStore {
				p := newPins(t)
				if tc.pin != nil {
					if err := p.Put(*tc.pin); err != nil {
						t.Fatal(err)
					}
				}
				return p
			}
			enum := serialtest.NewFakeEnumerator(tc.attached...)

			missing := DetectMissingDevices(context.Background(), tc.entries, enum, mkPins())
			_, err := ResolveDevices(context.Background(), tc.entries, enum, mkPins())

			switch {
			case len(missing) > 0 && err == nil:
				t.Fatalf("detector flagged %+v but ResolveDevices accepted it", missing)
			case len(missing) == 0 && err != nil:
				t.Fatalf("detector found nothing but ResolveDevices rejected it: %v", err)
			}
		})
	}
}

func TestSerialEnvVarName(t *testing.T) {
	cases := map[string]string{
		"esp32":    "MOAT_SERIAL_ESP32_URL",
		"esp32-s3": "MOAT_SERIAL_ESP32_S3_URL",
		"board_1":  "MOAT_SERIAL_BOARD_1_URL",
	}
	for in, want := range cases {
		if got := SerialEnvVarName(in); got != want {
			t.Fatalf("SerialEnvVarName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSerialEnvBuildsURLsAndDeviceList(t *testing.T) {
	env := SerialEnv("host.docker.internal", map[string]string{
		"esp32": "0.0.0.0:54321",
		"probe": "0.0.0.0:54322",
	})
	want := map[string]bool{
		"MOAT_SERIAL_ESP32_URL=rfc2217://host.docker.internal:54321": true,
		"MOAT_SERIAL_PROBE_URL=rfc2217://host.docker.internal:54322": true,
		"MOAT_SERIAL_DEVICES=esp32,probe":                            true,
	}
	if len(env) != len(want) {
		t.Fatalf("got %v, want %d entries", env, len(want))
	}
	for _, e := range env {
		if !want[e] {
			t.Fatalf("unexpected entry %q in %v", e, env)
		}
	}
}

func TestSerialEnvWithNoDevicesIsEmpty(t *testing.T) {
	// Companion: runs without devices must not gain a stray MOAT_SERIAL_DEVICES.
	if env := SerialEnv("host.docker.internal", nil); len(env) != 0 {
		t.Fatalf("got %v, want no environment entries", env)
	}
}

func ptr[T any](v T) *T { return &v }

func TestSerialPinsFromAddrs(t *testing.T) {
	// The container's MOAT_SERIAL_*_URL froze these ports at create; a daemon
	// restart must re-register the run pinned to them, or its devices go dead.
	pins := serialPinsFromAddrs(map[string]string{
		"esp32": "172.17.0.1:41234",
		"probe": "172.17.0.1:41235",
	})
	if len(pins) != 2 || pins["esp32"] != 41234 || pins["probe"] != 41235 {
		t.Fatalf("got %v, want esp32=41234 probe=41235", pins)
	}
}

func TestSerialPinsFromAddrsSkipsUnusableEntries(t *testing.T) {
	// Companion: an address without a parsable port is skipped, not fatal —
	// the other devices still come back. An empty map is nil, so a
	// re-registration without devices stays unpinned.
	if got := serialPinsFromAddrs(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
	if got := serialPinsFromAddrs(map[string]string{"bad": "no-port"}); len(got) != 0 {
		t.Fatalf("got %v, want no pins from an unusable address", got)
	}
}

// newSeamedManager returns a Manager whose serial seams are injected, the way
// NewManagerWithOptions wires test fakes. The zero-value seams (nil enum, empty
// pin path) are what production Create runs with.
func newSeamedManager(enum serialdev.Enumerator, pinPath string) *Manager {
	return &Manager{serialEnum: enum, serialPinPath: pinPath}
}

// TestCreateGateFailsOnAbsentDevice covers the Create gate's absent-device
// path: resolveDevicesForCreate must return the same actionable error the
// pre-flight detector produces, so the run fails before the container is
// created. This is the gate half of the pre-flight ↔ gate pair; the detector
// half lives in the DetectMissingDevices tests above.
func TestCreateGateFailsOnAbsentDevice(t *testing.T) {
	pinPath := filepath.Join(t.TempDir(), "devices.json")
	m := newSeamedManager(serialtest.NewFakeEnumerator(), pinPath)

	_, err := m.resolveDevicesForCreate(context.Background(), []config.DeviceEntry{esp32Entry()})
	if err == nil {
		t.Fatal("a run whose device is absent must fail before container creation")
	}
	// Same message the pre-flight shows, so the two never drift.
	if !strings.Contains(err.Error(), "no attached device matches USB ID 303a:1001") {
		t.Fatalf("error %q should name the missing device like the pre-flight does", err)
	}
	if !strings.Contains(err.Error(), "moat device list") {
		t.Fatalf("error %q should point at `moat device list`", err)
	}
}

// TestCreateGatePinsAndResolvesAnAttachedDevice is the companion: a device
// that is attached resolves to a spec the daemon can serve, and the first-use
// pin is recorded — the gate is where approval becomes durable.
func TestCreateGatePinsAndResolvesAnAttachedDevice(t *testing.T) {
	pinPath := filepath.Join(t.TempDir(), "devices.json")
	m := newSeamedManager(serialtest.NewFakeEnumerator(esp32Device("AAA")), pinPath)

	specs, err := m.resolveDevicesForCreate(context.Background(), []config.DeviceEntry{esp32Entry()})
	if err != nil {
		t.Fatalf("resolveDevicesForCreate: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("got %d specs, want 1", len(specs))
	}
	if specs[0].Name != "esp32" || specs[0].VID != "303a" || specs[0].Serial != "AAA" {
		t.Fatalf("spec = %+v, want the attached device's identity", specs[0])
	}
	if specs[0].Record != "events" {
		t.Fatalf("record mode = %q, want the events default", specs[0].Record)
	}

	// The pin is recorded — a second gate pass must verify, not re-approve.
	store, err := serialdev.OpenPinStore(pinPath)
	if err != nil {
		t.Fatalf("reopening the pin store: %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup
	if _, ok, err := store.Get("esp32"); err != nil {
		t.Fatalf("Get: %v", err)
	} else if !ok {
		t.Fatal("the gate must record the first-use pin")
	}
	if _, err := m.resolveDevicesForCreate(context.Background(), []config.DeviceEntry{esp32Entry()}); err != nil {
		t.Fatalf("second pass over the pinned device must verify: %v", err)
	}
}

func TestAmbiguousFixEmitsParseableDevicesYAML(t *testing.T) {
	// A dual-UART bridge gets the interface-selector suggestion, and the
	// devices block it prints must parse as real moat.yaml. It used to emit map
	// form ("jtag:") instead of a list entry ("- name: jtag"), so a user who
	// copied the suggestion got a parse error.
	entry := config.DeviceEntry{Name: "jtag", Match: config.DeviceMatch{USB: "0403:6010"}}
	bridge := []serialdev.Device{
		{VID: "0403", PID: "6010", Serial: "FT7", PortPath: "1-2", Interface: "0", Path: "/dev/ttyUSB0"},
		{VID: "0403", PID: "6010", Serial: "FT7", PortPath: "1-2", Interface: "1", Path: "/dev/ttyUSB1"},
	}
	msg := ambiguousFix(entry, bridge)
	i := strings.Index(msg, "devices:")
	if i < 0 {
		t.Fatalf("bridge ambiguity should suggest an interface selector, got: %s", msg)
	}
	// Dedent the block (printed indented four spaces under the prose).
	var b strings.Builder
	for _, ln := range strings.Split(msg[i:], "\n") {
		b.WriteString(strings.TrimPrefix(ln, "    "))
		b.WriteByte('\n')
	}
	var cfg config.Config
	if err := yaml.Unmarshal([]byte(b.String()), &cfg); err != nil {
		t.Fatalf("suggested devices YAML does not parse: %v\n%s", err, b.String())
	}
	if len(cfg.Devices) != 1 || cfg.Devices[0].Name != "jtag" || cfg.Devices[0].Match.Interface != "0" {
		t.Fatalf("parsed %+v, want one entry name=jtag interface=0", cfg.Devices)
	}

	// Companion: a non-bridge ambiguity gets the physical-unplug guidance and
	// prints no YAML to copy.
	nonBridge := []serialdev.Device{
		{VID: "1a86", PID: "7523", Path: "/dev/ttyUSB0"},
		{VID: "1a86", PID: "7523", Path: "/dev/ttyUSB1"},
	}
	if got := ambiguousFix(entry, nonBridge); !strings.Contains(got, "Unplug all but one") {
		t.Fatalf("non-bridge ambiguity should suggest unplugging, got: %s", got)
	}
}
