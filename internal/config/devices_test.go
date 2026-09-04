package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadDevicesYAML writes a moat.yaml and loads it.
func loadDevicesYAML(t *testing.T, yaml string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	return Load(dir)
}

const validDevice = `name: x
devices:
  - name: esp32
    match: {usb: "303a:1001"}
`

func TestDevicesParseValidEntry(t *testing.T) {
	cfg, err := loadDevicesYAML(t, validDevice+"    baud: 115200\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Devices) != 1 {
		t.Fatalf("got %d devices, want 1", len(cfg.Devices))
	}
	d := cfg.Devices[0]
	if d.Name != "esp32" {
		t.Fatalf("Name = %q, want esp32", d.Name)
	}
	if d.Match.USB != "303a:1001" {
		t.Fatalf("Match = %+v, want 303a:1001", d.Match)
	}
	if d.Baud != 115200 {
		t.Fatalf("Baud = %d, want 115200", d.Baud)
	}
	vid, pid := d.VIDPID()
	if vid != "303a" || pid != "1001" {
		t.Fatalf("VIDPID() = %s:%s, want 303a:1001", vid, pid)
	}
}

func TestDevicesVIDPIDLowercases(t *testing.T) {
	// Vendors print IDs in both cases; resolution compares lowercased.
	cfg, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: esp32\n    match: {usb: \"303A:100B\"}\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	vid, pid := cfg.Devices[0].VIDPID()
	if vid != "303a" || pid != "100b" {
		t.Fatalf("VIDPID() = %s:%s, want 303a:100b", vid, pid)
	}
}

func TestDevicesAbsentIsEmptyNotAnError(t *testing.T) {
	cfg, err := loadDevicesYAML(t, "name: x\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Devices) != 0 {
		t.Fatalf("got %d devices, want none", len(cfg.Devices))
	}
}

func TestDevicesRecordDefaultsToEvents(t *testing.T) {
	cfg, err := loadDevicesYAML(t, validDevice)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Devices[0].RecordMode(); got != RecordEvents {
		t.Fatalf("RecordMode() = %q, want %q when record is omitted", got, RecordEvents)
	}
}

func TestDevicesRecordFullIsPreserved(t *testing.T) {
	// Companion of the default case: an explicit value must survive, or full
	// capture would silently never happen.
	cfg, err := loadDevicesYAML(t, validDevice+"    record: full\n")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Devices[0].RecordMode(); got != RecordFull {
		t.Fatalf("RecordMode() = %q, want %q", got, RecordFull)
	}
}

func TestDevicesRejectInvalidRecordMode(t *testing.T) {
	_, err := loadDevicesYAML(t, validDevice+"    record: everything\n")
	if err == nil {
		t.Fatal("record: everything should be rejected")
	}
	if !strings.Contains(err.Error(), "events") || !strings.Contains(err.Error(), "full") {
		t.Fatalf("error %q should name the allowed values", err)
	}
}

func TestDevicesRejectMissingName(t *testing.T) {
	_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - match: {usb: \"303a:1001\"}\n")
	if err == nil {
		t.Fatal("a device with no name should be rejected")
	}
}

func TestDevicesRejectNameWithPathSeparator(t *testing.T) {
	// The name becomes a path under /dev/moat/serial/, so a separator would let
	// config escape that directory.
	_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: ../../etc/passwd\n    match: {usb: \"303a:1001\"}\n")
	if err == nil {
		t.Fatal("a device name containing a path separator should be rejected")
	}
}

func TestDevicesRejectNameWithUppercaseOrSpaces(t *testing.T) {
	for _, name := range []string{"ESP32", "my device", "esp 32", "-leading"} {
		_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: \""+name+"\"\n    match: {usb: \"303a:1001\"}\n")
		if err == nil {
			t.Fatalf("device name %q should be rejected", name)
		}
	}
}

func TestDevicesAcceptConventionalNames(t *testing.T) {
	// Companion of the rejection cases: ordinary names must still load.
	for _, name := range []string{"esp32", "esp32-s3", "board_1", "a"} {
		if _, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: \""+name+"\"\n    match: {usb: \"303a:1001\"}\n"); err != nil {
			t.Fatalf("device name %q should be accepted: %v", name, err)
		}
	}
}

func TestDevicesRejectDuplicateNames(t *testing.T) {
	yaml := "name: x\ndevices:\n" +
		"  - name: esp32\n    match: {usb: \"303a:1001\"}\n" +
		"  - name: esp32\n    match: {usb: \"1a86:7523\"}\n"
	_, err := loadDevicesYAML(t, yaml)
	if err == nil {
		t.Fatal("duplicate device names should be rejected")
	}
	if !strings.Contains(err.Error(), "esp32") {
		t.Fatalf("error %q should name the duplicate", err)
	}
}

func TestDevicesRejectBadUSBIDs(t *testing.T) {
	cases := map[string]string{
		"missing colon": `{usb: "303a1001"}`,
		"short vid":     `{usb: "30a:1001"}`,
		"long pid":      `{usb: "303a:10011"}`,
		"non-hex vid":   `{usb: "30zz:1001"}`,
		"empty vid":     `{usb: ":1001"}`,
		"missing pid":   `{usb: "303a:"}`,
		"0x prefix":     `{usb: "0x303a:0x1001"}`,
		"vid only":      `{usb: "303a"}`,
	}
	for what, match := range cases {
		_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: esp32\n    match: "+match+"\n")
		if err == nil {
			t.Fatalf("%s should be rejected", what)
		}
	}
}

func TestDevicesAcceptUppercaseHexIDs(t *testing.T) {
	// Vendors print IDs in both cases; matching lowercases them anyway.
	if _, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: esp32\n    match: {usb: \"303A:100B\"}\n"); err != nil {
		t.Fatalf("uppercase hex should be accepted: %v", err)
	}
}

func TestDevicesRejectNegativeBaud(t *testing.T) {
	_, err := loadDevicesYAML(t, validDevice+"    baud: -1\n")
	if err == nil {
		t.Fatal("a negative baud rate should be rejected")
	}
}

func TestDevicesBaudIsOptional(t *testing.T) {
	cfg, err := loadDevicesYAML(t, validDevice)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Devices[0].Baud != 0 {
		t.Fatalf("Baud = %d, want 0 meaning the tool decides", cfg.Devices[0].Baud)
	}
}

func TestDevicesAcceptInterfaceSelector(t *testing.T) {
	// The selector for the second UART of a dual-UART bridge. The match is
	// written in block form because the selector nests under it.
	const yaml = `name: x
devices:
  - name: esp32
    match:
      usb: "303a:1001"
      interface: "1"
`
	cfg, err := loadDevicesYAML(t, yaml)
	if err != nil {
		t.Fatalf("interface selector should be accepted: %v", err)
	}
	if got := cfg.Devices[0].Match.Interface; got != "1" {
		t.Fatalf("Match.Interface = %q, want \"1\"", got)
	}
}

func TestDevicesInterfaceSelectorDefaultsToEmpty(t *testing.T) {
	// Companion: without the selector nothing changes for single-UART
	// configs — empty means "no selector", not interface 0.
	cfg, err := loadDevicesYAML(t, validDevice)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Devices[0].Match.Interface; got != "" {
		t.Fatalf("Match.Interface = %q, want empty", got)
	}
}

func TestDevicesRejectBadInterfaceSelectors(t *testing.T) {
	cases := map[string]string{
		"non-numeric":    "uart1",
		"negative":       "-1",
		"hex":            "0x1",
		"padded":         " 1",
		"trailing space": "1 ",
	}
	for what, iface := range cases {
		_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - name: esp32\n    match: {usb: \"303a:1001\", interface: \""+iface+"\"}\n")
		if err == nil {
			t.Fatalf("%s interface selector should be rejected", what)
		}
	}
}
