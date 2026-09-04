package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/serialbroker"
	"github.com/majorcontext/moat/internal/storage"
)

// The fan-out is the seam between the broker and the run's on-disk records:
// every event lands in devices.jsonl, and only session boundaries also land in
// the audit chain. The design's "the audit chain records that full capture
// was enabled" lives in the Record → RecordMode mapping, so these tests assert
// both ends of that seam.

func TestSerialFanoutWritesEveryEventToDevicesJSONL(t *testing.T) {
	baseDir := t.TempDir()
	f := newSerialEventFanout(baseDir)

	f.handle(serialbroker.Event{
		RunID: "run_aabbccdd", Device: "esp32", Kind: "modem",
		Detail: "dtr-on", TxBytes: 4, RxBytes: 9,
	})
	f.handle(serialbroker.Event{
		RunID: "run_aabbccdd", Device: "esp32", Kind: "settings",
		Detail: "115200 8N1",
	})

	store, err := storage.NewRunStore(baseDir, "run_aabbccdd")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	events, err := store.ReadDeviceEvents()
	if err != nil {
		t.Fatalf("ReadDeviceEvents: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("got %d device events, want 2 — every event must be recorded", len(events))
	}
	if events[0].Device != "esp32" || events[0].Kind != "modem" || events[0].Detail != "dtr-on" {
		t.Fatalf("event = %+v, want the modem event", events[0])
	}
	if events[0].TxBytes != 4 || events[0].RxBytes != 9 {
		t.Fatalf("event counters = %d/%d, want 4/9", events[0].TxBytes, events[0].RxBytes)
	}
}

func TestSerialFanoutAuditsSessionBoundariesOnly(t *testing.T) {
	baseDir := t.TempDir()
	f := newSerialEventFanout(baseDir)

	// Chatter: recorded in devices.jsonl (asserted above) but never audited.
	f.handle(serialbroker.Event{RunID: "run_aabbccdd", Device: "esp32", Kind: "modem", Detail: "rts-on"})
	// Session boundaries: attach with the device identity and the record
	// mode, then detach with byte counters.
	f.handle(serialbroker.Event{
		RunID: "run_aabbccdd", Device: "esp32", Kind: "attach",
		DevicePath: "/dev/ttyUSB0", VID: "303a", PID: "1001", DeviceSerial: "AAA",
		Record: "full",
	})
	f.handle(serialbroker.Event{
		RunID: "run_aabbccdd", Device: "esp32", Kind: "detach",
		TxBytes: 1024, RxBytes: 2048,
	})
	f.handle(serialbroker.Event{RunID: "run_aabbccdd", Device: "esp32", Kind: "error", Detail: "reading modem lines: input/output error"})

	entries := readAuditEntries(t, filepath.Join(baseDir, "run_aabbccdd", "audit.db"))
	if len(entries) != 3 {
		t.Fatalf("got %d audit entries, want 3 (attach, detach, error — not the rts-on chatter)", len(entries))
	}
	var sawAttach bool
	for _, e := range entries {
		if e.Type != audit.EntryDevice {
			t.Fatalf("entry type = %v, want EntryDevice", e.Type)
		}
		data, ok := e.Data.(map[string]any)
		if !ok {
			t.Fatalf("entry data = %T, want the decoded device object", e.Data)
		}
		if data["action"] == "attach" {
			sawAttach = true
			// The record-mode mapping is the audit trail's proof that full
			// capture was enabled for the session.
			if data["record_mode"] != "full" {
				t.Fatalf("attach record_mode = %v, want full", data["record_mode"])
			}
			if data["serial"] != "AAA" || data["vid"] != "303a" || data["pid"] != "1001" {
				t.Fatalf("attach identity = %v, want the device's USB identity", data)
			}
			if data["path"] != "/dev/ttyUSB0" {
				t.Fatalf("attach path = %v, want the device path", data["path"])
			}
		}
	}
	if !sawAttach {
		t.Fatal("no attach entry among the audited boundaries")
	}
}

// TestSerialFanoutIgnoresEventsWithoutARun is the companion: the daemon
// serves many runs and a malformed event must not create a bogus run
// directory.
func TestSerialFanoutIgnoresEventsWithoutARun(t *testing.T) {
	baseDir := t.TempDir()
	f := newSerialEventFanout(baseDir)

	f.handle(serialbroker.Event{RunID: "", Device: "esp32", Kind: "attach"})

	entries, err := os.ReadDir(baseDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("an event with no run id created %d entries under the base dir", len(entries))
	}
}

// readAuditEntries reads an audit store's entries through its own reader,
// keeping the test on the fan-out's half of the seam.
func readAuditEntries(t *testing.T, dbPath string) []audit.Entry {
	t.Helper()
	as, err := audit.OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	defer as.Close() //nolint:errcheck // test cleanup

	count, err := as.Count()
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count == 0 {
		return nil
	}
	entries, err := as.Range(1, count)
	if err != nil {
		t.Fatalf("Range: %v", err)
	}
	out := make([]audit.Entry, 0, len(entries))
	for _, e := range entries {
		out = append(out, *e)
	}
	return out
}
