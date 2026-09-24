package serialbroker

import (
	"testing"

	"github.com/majorcontext/moat/internal/serialport"
)

// Parity is framing: an odd↔even swap or a silently accepted mark/space value
// corrupts every byte on the line while looking like noise on real hardware.
// These tests exercise every parity in both directions — TestPySerialOpenSequence
// sends none only.

func TestParityFromWireAcceptsNoneOddEvenAndRejectsTheRest(t *testing.T) {
	valid := []struct {
		wire byte
		want uint8
	}{
		{1, serialport.ParityNone},
		{2, serialport.ParityOdd},
		{3, serialport.ParityEven},
	}
	for _, v := range valid {
		got, ok := parityFromWire(v.wire)
		if !ok {
			t.Errorf("parityFromWire(%d): rejected a defined RFC2217 value", v.wire)
		}
		if got != v.want {
			t.Errorf("parityFromWire(%d) = %d, want %d", v.wire, got, v.want)
		}
	}
	// Rejects: undefined values, and mark (4) / space (5) parity, which moat
	// does not represent — treating them as "none" would corrupt framing.
	for _, wire := range []byte{0, 4, 5, 6, 255} {
		if got, ok := parityFromWire(wire); ok {
			t.Errorf("parityFromWire(%d) = (%d, true), want rejected", wire, got)
		}
	}
}

func TestParityToWireInvertsFromWire(t *testing.T) {
	for _, p := range []uint8{serialport.ParityNone, serialport.ParityOdd, serialport.ParityEven} {
		wire, ok := parityFromWire(parityToWire(p))
		if !ok {
			t.Errorf("parityToWire(%d) produced an undefined wire value %d", p, parityToWire(p))
		}
		if wire != p {
			t.Errorf("parityFromWire(parityToWire(%d)) = %d, want identity", p, wire)
		}
	}
}

func TestFormatSettingsLabelsEveryParity(t *testing.T) {
	cases := []struct {
		parity uint8
		want   string
	}{
		{serialport.ParityNone, "115200 8N1"},
		{serialport.ParityOdd, "115200 8O1"},
		{serialport.ParityEven, "115200 8E1"},
	}
	for _, c := range cases {
		got := formatSettings(serialport.Settings{Baud: 115200, DataBits: 8, StopBits: 1, Parity: c.parity})
		if got != c.want {
			t.Errorf("formatSettings(parity %d) = %q, want %q", c.parity, got, c.want)
		}
	}
}
