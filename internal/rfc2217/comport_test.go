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

func TestControlNameCoversEverySignal(t *testing.T) {
	for _, v := range []byte{ControlBreakOn, ControlBreakOff, ControlDTROn, ControlDTROff, ControlRTSOn, ControlRTSOff} {
		if ControlName(v) == "" {
			t.Fatalf("ControlName(%d) is empty; logs and audit entries need a label", v)
		}
	}
}

func TestControlNameOnUnknownValueIsNotEmpty(t *testing.T) {
	if got := ControlName(200); got == "" {
		t.Fatal("unknown control values still need a printable label")
	}
}
