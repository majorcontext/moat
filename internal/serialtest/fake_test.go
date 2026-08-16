package serialtest_test

import (
	"errors"
	"testing"

	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialport"
	"github.com/majorcontext/moat/internal/serialtest"
)

func TestFakePortSatisfiesThePortInterface(t *testing.T) {
	var _ serialport.Port = serialtest.NewFakePort(t)
}

func TestFakePortCarriesDataToThePeer(t *testing.T) {
	p := serialtest.NewFakePort(t)
	if _, err := p.Write([]byte("to device")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := p.Peer().Read(buf)
	if err != nil {
		t.Fatalf("peer Read: %v", err)
	}
	if string(buf[:n]) != "to device" {
		t.Fatalf("peer got %q, want %q", buf[:n], "to device")
	}
}

func TestFakePortCarriesDataFromThePeer(t *testing.T) {
	p := serialtest.NewFakePort(t)
	if _, err := p.Peer().Write([]byte("from device")); err != nil {
		t.Fatalf("peer Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := p.Read(buf)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(buf[:n]) != "from device" {
		t.Fatalf("port got %q, want %q", buf[:n], "from device")
	}
}

func TestFakePortCarriesBinaryDataUnaltered(t *testing.T) {
	// Firmware payloads are binary; the pty must be in raw mode or 0x0A and
	// 0x0D would be translated.
	p := serialtest.NewFakePort(t)
	want := []byte{0x00, 0xFF, 0x0A, 0x0D, 0x1B}
	if _, err := p.Write(want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 64)
	n, err := p.Peer().Read(buf)
	if err != nil {
		t.Fatalf("peer Read: %v", err)
	}
	if string(buf[:n]) != string(want) {
		t.Fatalf("peer got % x, want % x", buf[:n], want)
	}
}

func TestFakePortRecordsSettings(t *testing.T) {
	p := serialtest.NewFakePort(t)
	want := serialport.Settings{Baud: 921600, DataBits: 8, StopBits: 1}
	if err := p.ApplySettings(want); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	if got := p.LastSettings(); got != want {
		t.Fatalf("LastSettings = %+v, want %+v", got, want)
	}
}

func TestFakePortSettingsMergeLeavesZeroFieldsAlone(t *testing.T) {
	// Mirrors the real port's contract, so a test passing against the fake is
	// not passing for a reason the real device would not reproduce.
	p := serialtest.NewFakePort(t)
	if err := p.ApplySettings(serialport.Settings{Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	if err := p.ApplySettings(serialport.Settings{FlowControl: serialport.FlowRTSCTS}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	got := p.LastSettings()
	if got.Baud != 115200 || got.DataBits != 8 {
		t.Fatalf("LastSettings = %+v, want baud and framing preserved", got)
	}
	if got.FlowControl != serialport.FlowRTSCTS {
		t.Fatalf("FlowControl = %d, want FlowRTSCTS", got.FlowControl)
	}
}

func TestFakePortRecordsModemAssertAndDeassert(t *testing.T) {
	p := serialtest.NewFakePort(t)
	if err := p.SetModem(serialport.Modem{DTR: true}); err != nil {
		t.Fatalf("SetModem: %v", err)
	}
	if got := p.LastModem(); !got.DTR || got.RTS {
		t.Fatalf("LastModem = %+v, want DTR set and RTS clear", got)
	}
	if err := p.SetModem(serialport.Modem{DTR: false, RTS: true}); err != nil {
		t.Fatalf("SetModem: %v", err)
	}
	if got := p.LastModem(); got.DTR || !got.RTS {
		t.Fatalf("LastModem = %+v, want DTR clear and RTS set", got)
	}
}

func TestFakePortPreservesModemSequence(t *testing.T) {
	// The ESP32 reset sequence is a series of transitions; the fake has to
	// record all of them so a coalescing relay can be caught.
	p := serialtest.NewFakePort(t)
	steps := []serialport.Modem{
		{DTR: false, RTS: true},
		{DTR: true, RTS: false},
		{DTR: false, RTS: false},
	}
	for _, s := range steps {
		if err := p.SetModem(s); err != nil {
			t.Fatalf("SetModem(%+v): %v", s, err)
		}
	}
	got := p.ModemSequence()
	if len(got) != len(steps) {
		t.Fatalf("ModemSequence has %d entries, want %d", len(got), len(steps))
	}
	for i := range steps {
		if got[i] != steps[i] {
			t.Fatalf("step %d = %+v, want %+v", i, got[i], steps[i])
		}
	}
}

func TestFakePortCountsBreaks(t *testing.T) {
	p := serialtest.NewFakePort(t)
	if err := p.SendBreak(); err != nil {
		t.Fatalf("SendBreak: %v", err)
	}
	if err := p.SendBreak(); err != nil {
		t.Fatalf("SendBreak: %v", err)
	}
	if got := p.BreakCount(); got != 2 {
		t.Fatalf("BreakCount = %d, want 2", got)
	}
}

func TestFakePortCloseIsIdempotent(t *testing.T) {
	// Both the code under test and t.Cleanup will close it.
	p := serialtest.NewFakePort(t)
	if err := p.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestFakeEnumeratorReturnsItsDevices(t *testing.T) {
	want := serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
	got, err := serialtest.NewFakeEnumerator(want).List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 || got[0] != want {
		t.Fatalf("List = %+v, want [%+v]", got, want)
	}
}

func TestFakeEnumeratorWithNoDevicesIsEmptyNotAnError(t *testing.T) {
	got, err := serialtest.NewFakeEnumerator().List(t.Context())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List = %+v, want empty", got)
	}
}

func TestFailingEnumeratorReturnsItsError(t *testing.T) {
	sentinel := errors.New("ioreg exploded")
	_, err := serialtest.NewFailingEnumerator(sentinel).List(t.Context())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the sentinel", err)
	}
}
