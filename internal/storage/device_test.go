package storage

import (
	"fmt"
	"strings"
	"sync"
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

// Concurrent device sessions append to one devices.jsonl: a run with two
// approved devices has a session goroutine per device, both emitting into the
// same file. A record written as two O_APPEND calls (payload, then newline)
// lets another writer land between them, producing a line that parses as
// neither event — and ReadDeviceEvents drops unparseable lines silently, so
// both disappear rather than failing loudly.
func TestWriteDeviceEventConcurrentWritersStayOnTheirOwnLines(t *testing.T) {
	store, err := NewRunStore(t.TempDir(), "run-a")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}

	const writers, each = 8, 25
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := store.WriteDeviceEvent(DeviceEvent{
					Device: fmt.Sprintf("dev%d", w),
					Kind:   "attach",
					Detail: strings.Repeat("x", 256), // long enough to span a write boundary
				}); err != nil {
					t.Errorf("WriteDeviceEvent: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	events, readErr := store.ReadDeviceEvents()
	if readErr != nil {
		t.Fatalf("ReadDeviceEvents: %v", readErr)
	}
	if len(events) != writers*each {
		t.Fatalf("read %d events, want %d — lines were interleaved and silently dropped",
			len(events), writers*each)
	}
	for _, ev := range events {
		if len(ev.Detail) != 256 {
			t.Fatalf("event detail is %d bytes, want 256: a record was spliced", len(ev.Detail))
		}
	}
}
