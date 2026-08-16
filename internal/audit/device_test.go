package audit

import (
	"path/filepath"
	"testing"
)

func TestAppendDeviceChainsAndCarriesIdentity(t *testing.T) {
	s, err := OpenStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	first, err := s.AppendDevice(DeviceData{
		Name: "esp32", Path: "/dev/ttyUSB0",
		VID: "303a", PID: "1001", Serial: "AAA", Action: "attach",
	})
	if err != nil {
		t.Fatalf("AppendDevice: %v", err)
	}
	if first.Type != EntryDevice {
		t.Fatalf("Type = %q, want %q", first.Type, EntryDevice)
	}

	second, err := s.AppendDevice(DeviceData{Name: "esp32", Action: "detach", TxBytes: 10, RxBytes: 20})
	if err != nil {
		t.Fatalf("AppendDevice: %v", err)
	}
	if second.PrevHash == "" {
		t.Fatal("second entry must chain to the first")
	}
	if second.Sequence <= first.Sequence {
		t.Fatalf("sequence did not advance: %d then %d", first.Sequence, second.Sequence)
	}
}

func TestAppendDeviceRecordsFullCaptureMode(t *testing.T) {
	// The chain must show when payloads were captured, since that changes what
	// is on disk.
	s, err := OpenStore(filepath.Join(t.TempDir(), "audit.db"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer s.Close()

	entry, err := s.AppendDevice(DeviceData{Name: "esp32", Action: "attach", RecordMode: "full"})
	if err != nil {
		t.Fatalf("AppendDevice: %v", err)
	}
	got, err := s.Get(entry.Sequence)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	data, ok := got.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data = %T, want a decoded object", got.Data)
	}
	if data["record_mode"] != "full" {
		t.Fatalf("record_mode = %v, want full", data["record_mode"])
	}
}
