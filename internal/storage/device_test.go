package storage

import (
	"testing"
	"time"
)

func TestDeviceEventsRoundTrip(t *testing.T) {
	s, err := NewRunStore(t.TempDir(), "run-a")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	want := DeviceEvent{
		Timestamp: time.Now().UTC().Truncate(time.Second),
		Device:    "esp32",
		Kind:      "detach",
		Detail:    "/dev/ttyUSB0",
		TxBytes:   1024,
		RxBytes:   2048,
	}
	if err := s.WriteDeviceEvent(want); err != nil {
		t.Fatalf("WriteDeviceEvent: %v", err)
	}
	got, err := s.ReadDeviceEvents()
	if err != nil {
		t.Fatalf("ReadDeviceEvents: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Device != want.Device || got[0].Kind != want.Kind {
		t.Fatalf("got %+v, want %+v", got[0], want)
	}
	if got[0].TxBytes != want.TxBytes || got[0].RxBytes != want.RxBytes {
		t.Fatalf("byte counters lost: got tx=%d rx=%d", got[0].TxBytes, got[0].RxBytes)
	}
}

func TestDeviceEventsAppend(t *testing.T) {
	s, err := NewRunStore(t.TempDir(), "run-a")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	for _, kind := range []string{"attach", "modem", "detach"} {
		if err := s.WriteDeviceEvent(DeviceEvent{Device: "esp32", Kind: kind}); err != nil {
			t.Fatalf("WriteDeviceEvent(%s): %v", kind, err)
		}
	}
	got, err := s.ReadDeviceEvents()
	if err != nil {
		t.Fatalf("ReadDeviceEvents: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Kind != "attach" || got[2].Kind != "detach" {
		t.Fatalf("events out of order: %+v", got)
	}
}

func TestReadDeviceEventsWithNoFileIsEmpty(t *testing.T) {
	// Companion: a run with no devices must not error when read.
	s, err := NewRunStore(t.TempDir(), "run-a")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	got, err := s.ReadDeviceEvents()
	if err != nil {
		t.Fatalf("ReadDeviceEvents: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}
