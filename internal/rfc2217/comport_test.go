package rfc2217

import "testing"

func TestBaudRateRoundTrip(t *testing.T) {
	for _, baud := range []uint32{9600, 115200, 921600, 1500000} {
		if got := DecodeBaud(EncodeBaud(baud)); got != baud {
			t.Fatalf("round trip of %d gave %d", baud, got)
		}
	}
}

func TestEncodeBaudIsFourByteBigEndian(t *testing.T) {
	got := EncodeBaud(115200) // 0x0001C200
	want := []byte{0x00, 0x01, 0xC2, 0x00}
	if len(got) != 4 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] || got[3] != want[3] {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestDecodeBaudRejectsShortPayload(t *testing.T) {
	// A malformed peer must not panic the broker.
	if got := DecodeBaud([]byte{0x00, 0x01}); got != 0 {
		t.Fatalf("DecodeBaud(short) = %d, want 0", got)
	}
}

func TestControlValuesAreDistinct(t *testing.T) {
	// Companion coverage: every signal needs both an on and an off encoding,
	// because the ESP32 reset sequence is asserts *and* deasserts. A collision
	// here would make a deassert read as an assert.
	values := map[byte]string{
		ControlDTROn:    "DTR on",
		ControlDTROff:   "DTR off",
		ControlRTSOn:    "RTS on",
		ControlRTSOff:   "RTS off",
		ControlBreakOn:  "break on",
		ControlBreakOff: "break off",
	}
	if len(values) != 6 {
		t.Fatalf("control values collide: %v", values)
	}
}

func TestControlNameLabelsEveryDefinedValue(t *testing.T) {
	// ControlName is the `detail` field of every modem/break audit entry and
	// devices.jsonl row, so the label must be the right one — returning
	// "dtr-on" for RTS-off passes an emptiness check and corrupts the trail.
	//
	// Every value the protocol defines gets its expected label, and the
	// companion (unknown value) lives below.
	labels := map[byte]string{
		ControlQueryFlow:     "flow-query",
		ControlFlowNone:      "flow-none",
		ControlFlowXONXOFF:   "flow-xonxoff",
		ControlFlowRTSCTS:    "flow-rtscts",
		ControlQueryBreak:    "break-query",
		ControlBreakOn:       "break-on",
		ControlBreakOff:      "break-off",
		ControlQueryDTR:      "dtr-query",
		ControlDTROn:         "dtr-on",
		ControlDTROff:        "dtr-off",
		ControlQueryRTS:      "rts-query",
		ControlRTSOn:         "rts-on",
		ControlRTSOff:        "rts-off",
		ControlQueryFlowIn:   "flow-in-query",
		ControlFlowNoneIn:    "flow-in-none",
		ControlFlowXONXOFFIn: "flow-in-xonxoff",
		ControlFlowRTSCTSIn:  "flow-in-rtscts",
		ControlFlowDCD:       "flow-dcd",
		ControlFlowDTRIn:     "flow-in-dtr",
		ControlFlowDSR:       "flow-dsr",
	}
	for v, want := range labels {
		if got := ControlName(v); got != want {
			t.Errorf("ControlName(%d) = %q, want %q", v, got, want)
		}
	}
	// A duplicate label would blur two different signals in the audit trail.
	seen := map[string]byte{}
	for v, want := range labels {
		if prev, dup := seen[want]; dup {
			t.Errorf("ControlName(%d) and ControlName(%d) share the label %q", prev, v, want)
		}
		seen[want] = v
	}
}

func TestControlNameOnUnknownValueIsNotEmpty(t *testing.T) {
	if got := ControlName(200); got == "" {
		t.Fatal("unknown control values still need a printable label")
	}
}

func TestComPortCommandConstantsHaveStableWireValues(t *testing.T) {
	// Pin every com-port command constant to its RFC 2217 literal wire value,
	// and assert each server reply is exactly its client command plus 100.
	// Every other test compares a wire byte against the same symbolic constant
	// under test, so without this a reintroduction of the client/server
	// namespace-mixing bug — which once put a NOTIFY out as 207 on the wire —
	// would pass the whole suite. settings_test.go pins parity the same way.
	cmds := []struct {
		name string
		cmd  byte
		want byte
		srv  byte // server reply constant, or 0 when the command has none (Signature)
	}{
		{"Signature", CmdSignature, 0, 0},
		{"SetBaudRate", CmdSetBaudRate, 1, SrvSetBaudRate},
		{"SetDataSize", CmdSetDataSize, 2, SrvSetDataSize},
		{"SetParity", CmdSetParity, 3, SrvSetParity},
		{"SetStopSize", CmdSetStopSize, 4, SrvSetStopSize},
		{"SetControl", CmdSetControl, 5, SrvSetControl},
		{"NotifyLineState", CmdNotifyLineState, 6, SrvNotifyLineState},
		{"NotifyModemState", CmdNotifyModemState, 7, SrvNotifyModemState},
		{"FlowControlSuspend", CmdFlowControlSuspend, 8, SrvFlowControlSuspend},
		{"FlowControlResume", CmdFlowControlResume, 9, SrvFlowControlResume},
		{"SetLineStateMask", CmdSetLineStateMask, 10, SrvSetLineStateMask},
		{"SetModemStateMask", CmdSetModemStateMask, 11, SrvSetModemStateMask},
		{"PurgeData", CmdPurgeData, 12, SrvPurgeData},
	}
	for _, c := range cmds {
		if c.cmd != c.want {
			t.Errorf("Cmd%s = %d, want %d", c.name, c.cmd, c.want)
		}
		if c.name == "Signature" {
			continue // no server-reply constant in this namespace
		}
		if c.srv != c.want+100 {
			t.Errorf("Srv%s = %d, want Cmd%s(%d)+100 = %d", c.name, c.srv, c.name, c.cmd, c.want+100)
		}
	}
}
