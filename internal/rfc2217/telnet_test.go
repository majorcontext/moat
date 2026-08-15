package rfc2217

import (
	"bytes"
	"io"
	"testing"
)

func TestEscapeIACDoublesFFBytes(t *testing.T) {
	got := EscapeIAC(nil, []byte{0x01, 0xFF, 0x02})
	want := []byte{0x01, 0xFF, 0xFF, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestEscapeIACLeavesOtherBytesAlone(t *testing.T) {
	src := []byte{0x00, 0x7F, 0xFE}
	if got := EscapeIAC(nil, src); !bytes.Equal(got, src) {
		t.Fatalf("got % x, want % x unchanged", got, src)
	}
}

func TestEscapeIACHandlesRunsOfIAC(t *testing.T) {
	got := EscapeIAC(nil, []byte{0xFF, 0xFF})
	want := []byte{0xFF, 0xFF, 0xFF, 0xFF}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestReaderUnescapesDoubledIAC(t *testing.T) {
	// Wire bytes 01 FF FF 02 represent the payload 01 FF 02.
	r := NewReader(bytes.NewReader([]byte{0x01, 0xFF, 0xFF, 0x02}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x01, 0xFF, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
}

func TestReaderRoundTripsEscapedData(t *testing.T) {
	// The property that actually matters: escape then un-escape is identity,
	// including for firmware-shaped input full of 0xFF.
	src := []byte{0x00, 0xFF, 0xFF, 0x10, 0xFF, 0xE9, 0xFF}
	r := NewReader(bytes.NewReader(EscapeIAC(nil, src)))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if !bytes.Equal(got, src) {
		t.Fatalf("round trip gave % x, want % x", got, src)
	}
}

func TestReaderDispatchesCommandAndKeepsSurroundingData(t *testing.T) {
	// data "hi", then SET-CONTROL DTR-ON, then data "yo"
	wire := []byte{
		'h', 'i',
		iac, sb, OptionComPort, CmdSetControl, ControlDTROn, iac, se,
		'y', 'o',
	}
	var gotCmd byte
	var gotPayload []byte
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(cmd byte, payload []byte) {
		gotCmd = cmd
		gotPayload = append([]byte(nil), payload...)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "hiyo" {
		t.Fatalf("data = %q, want %q — command bytes must not leak into the stream", data, "hiyo")
	}
	if gotCmd != CmdSetControl {
		t.Fatalf("cmd = %d, want CmdSetControl", gotCmd)
	}
	if len(gotPayload) != 1 || gotPayload[0] != ControlDTROn {
		t.Fatalf("payload = % x, want [%02x]", gotPayload, ControlDTROn)
	}
}

func TestReaderHandlesEscapedIACInsideSubnegotiation(t *testing.T) {
	// A baud value containing 0xFF must survive: 0x00FF0000 is sent escaped.
	wire := []byte{iac, sb, OptionComPort, CmdSetBaudRate, 0x00, 0xFF, 0xFF, 0x00, 0x00, iac, se}
	var got []byte
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(_ byte, p []byte) { got = append([]byte(nil), p...) }
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []byte{0x00, 0xFF, 0x00, 0x00}
	if !bytes.Equal(got, want) {
		t.Fatalf("payload = % x, want % x un-escaped", got, want)
	}
}

func TestReaderIgnoresOtherTelnetOptions(t *testing.T) {
	// A subnegotiation for an option we do not implement must be consumed
	// whole, not passed through as data.
	wire := []byte{'a', iac, sb, 0x18 /* TERMINAL-TYPE */, 0x01, iac, se, 'b'}
	called := false
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(byte, []byte) { called = true }
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "ab" {
		t.Fatalf("data = %q, want %q", data, "ab")
	}
	if called {
		t.Fatal("OnCommand fired for a non-com-port option")
	}
}

func TestReaderIgnoresTwoByteTelnetCommands(t *testing.T) {
	// IAC DO/DONT/WILL/WONT <option> are three bytes and carry no payload.
	wire := []byte{'a', iac, 0xFD /* DO */, 0x2C, 'b'}
	r := NewReader(bytes.NewReader(wire))
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "ab" {
		t.Fatalf("data = %q, want %q", data, "ab")
	}
}

func TestReaderSurvivesTruncatedSubnegotiation(t *testing.T) {
	// A peer that dies mid-command must produce EOF, not a hang or a panic.
	wire := []byte{'a', iac, sb, OptionComPort, CmdSetControl}
	r := NewReader(bytes.NewReader(wire))
	data, err := io.ReadAll(r)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "a" {
		t.Fatalf("data = %q, want %q", data, "a")
	}
}

func TestWriteCommandFramesCorrectly(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommand(&buf, CmdSetBaudRate, []byte{0x00, 0x01, 0xC2, 0x00}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	want := []byte{iac, sb, OptionComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00, iac, se}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteCommandEscapesIACInPayload(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommand(&buf, CmdSetBaudRate, []byte{0x00, 0xFF, 0x00, 0x00}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	want := []byte{iac, sb, OptionComPort, CmdSetBaudRate, 0x00, 0xFF, 0xFF, 0x00, 0x00, iac, se}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestWriteCommandThenReaderRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteCommand(&buf, CmdSetBaudRate, EncodeBaud(921600)); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	var gotCmd byte
	var gotBaud uint32
	r := NewReader(&buf)
	r.OnCommand = func(cmd byte, p []byte) {
		gotCmd = cmd
		gotBaud = DecodeBaud(p)
	}
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if gotCmd != CmdSetBaudRate || gotBaud != 921600 {
		t.Fatalf("got cmd %d baud %d, want CmdSetBaudRate 921600", gotCmd, gotBaud)
	}
}
