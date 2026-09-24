package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/audit"
)

// formatEntryData renders the event log `moat audit` prints. Device entries are
// the ones the serial audit trail exists for — they carry the USB identity and
// capture mode that prove which physical device was reached and whether payload
// capture was on — so they must render as those facts, not as raw JSON that the
// 80-char truncation cuts to exactly the fields that matter least.

func TestFormatDeviceEntryLeadsWithIdentityAndRecordMode(t *testing.T) {
	e := &audit.Entry{Type: audit.EntryDevice, Data: map[string]any{
		"name": "board", "action": "attach",
		"path": "/dev/ttyUSB0", "vid": "303a", "pid": "1001",
		"serial": "E072A1A23248", "record_mode": "full",
		"tx_bytes": float64(1024), "rx_bytes": float64(2048),
	}}
	got := formatEntryData(e)

	for _, want := range []string{"attach board", "303a:1001", "serial=E072A1A23248", "record=full", "tx=1024 rx=2048"} {
		if !strings.Contains(got, want) {
			t.Errorf("formatted entry %q missing %q", got, want)
		}
	}
}

func TestFormatDeviceEntryOmitsWhatItDoesNotHave(t *testing.T) {
	// A serial-less device has no serial number; an events-mode session has no
	// record_mode worth stating and no counters at attach time.
	e := &audit.Entry{Type: audit.EntryDevice, Data: map[string]any{
		"name": "probe", "action": "attach",
		"vid": "1a86", "pid": "7523", "record_mode": "events",
	}}
	got := formatEntryData(e)

	if !strings.Contains(got, "attach probe 1a86:7523") {
		t.Errorf("formatted entry = %q, want the identity inline", got)
	}
	for _, absent := range []string{"serial=", "tx=", "record=events"} {
		if strings.Contains(got, absent) {
			t.Errorf("formatted entry %q should not carry %q", got, absent)
		}
	}
}

func TestFormatDeviceEntryTruncatesLongDetailWithoutLosingIdentity(t *testing.T) {
	// The default branch truncates raw JSON at 80 chars, which for a device
	// entry cut off record_mode and serial — the punchline of this formatter.
	long := make([]byte, 200)
	for i := range long {
		long[i] = 'x'
	}
	e := &audit.Entry{Type: audit.EntryDevice, Data: map[string]any{
		"name": "board", "action": "error",
		"vid": "303a", "pid": "1001", "serial": "AAA", "record_mode": "events",
		"detail": string(long),
	}}
	got := formatEntryData(e)

	if !strings.Contains(got, "serial=AAA") || !strings.Contains(got, "303a:1001") {
		t.Errorf("long detail pushed the identity out of %q", got)
	}
	if !strings.Contains(got, "…") {
		t.Errorf("long detail was not marked truncated in %q", got)
	}
}
