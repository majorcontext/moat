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
  - serial: esp32
    match: {vid: "303a", pid: "1001"}
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
	if d.Serial != "esp32" {
		t.Fatalf("Serial = %q, want esp32", d.Serial)
	}
	if d.Match.VID != "303a" || d.Match.PID != "1001" {
		t.Fatalf("Match = %+v, want 303a:1001", d.Match)
	}
	if d.Baud != 115200 {
		t.Fatalf("Baud = %d, want 115200", d.Baud)
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
	_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - match: {vid: \"303a\", pid: \"1001\"}\n")
	if err == nil {
		t.Fatal("a device with no name should be rejected")
	}
}

func TestDevicesRejectNameWithPathSeparator(t *testing.T) {
	// The name becomes a path under /dev/moat/serial/, so a separator would let
	// config escape that directory.
	_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - serial: ../../etc/passwd\n    match: {vid: \"303a\", pid: \"1001\"}\n")
	if err == nil {
		t.Fatal("a device name containing a path separator should be rejected")
	}
}

func TestDevicesRejectNameWithUppercaseOrSpaces(t *testing.T) {
	for _, name := range []string{"ESP32", "my device", "esp 32", "-leading"} {
		_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - serial: \""+name+"\"\n    match: {vid: \"303a\", pid: \"1001\"}\n")
		if err == nil {
			t.Fatalf("device name %q should be rejected", name)
		}
	}
}

func TestDevicesAcceptConventionalNames(t *testing.T) {
	// Companion of the rejection cases: ordinary names must still load.
	for _, name := range []string{"esp32", "esp32-s3", "board_1", "a"} {
		if _, err := loadDevicesYAML(t, "name: x\ndevices:\n  - serial: \""+name+"\"\n    match: {vid: \"303a\", pid: \"1001\"}\n"); err != nil {
			t.Fatalf("device name %q should be accepted: %v", name, err)
		}
	}
}

func TestDevicesRejectDuplicateNames(t *testing.T) {
	yaml := "name: x\ndevices:\n" +
		"  - serial: esp32\n    match: {vid: \"303a\", pid: \"1001\"}\n" +
		"  - serial: esp32\n    match: {vid: \"1a86\", pid: \"7523\"}\n"
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
		"short vid":   `{vid: "30a", pid: "1001"}`,
		"long pid":    `{vid: "303a", pid: "10011"}`,
		"non-hex vid": `{vid: "30zz", pid: "1001"}`,
		"empty vid":   `{vid: "", pid: "1001"}`,
		"missing pid": `{vid: "303a"}`,
		"0x prefix":   `{vid: "0x303a", pid: "1001"}`,
	}
	for what, match := range cases {
		_, err := loadDevicesYAML(t, "name: x\ndevices:\n  - serial: esp32\n    match: "+match+"\n")
		if err == nil {
			t.Fatalf("%s should be rejected", what)
		}
	}
}

func TestDevicesAcceptUppercaseHexIDs(t *testing.T) {
	// Vendors print IDs in both cases; matching lowercases them anyway.
	if _, err := loadDevicesYAML(t, "name: x\ndevices:\n  - serial: esp32\n    match: {vid: \"303A\", pid: \"100B\"}\n"); err != nil {
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
