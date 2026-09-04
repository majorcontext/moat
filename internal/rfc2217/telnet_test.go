package rfc2217

import (
	"bytes"
	"io"
	"testing"
	"testing/iotest"
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

func TestReaderRejectsOversizedSubnegotiation(t *testing.T) {
	// The payload buffer grows with the stream until IAC SE arrives, so an
	// unbounded "subnegotiation" is an allocation the peer chooses. The
	// reader must cap it, and the bytes after the abort must not be eaten as
	// command bytes — the next valid frame has to parse.
	var wire []byte
	wire = append(wire, 'a', iac, sb, OptionComPort, CmdSetControl)
	for i := 0; i < MaxSubnegotiation+64; i++ {
		wire = append(wire, 'X')
	}
	// End the runaway, then send a real command and real data: the reader
	// must resynchronize rather than swallow the stream.
	wire = append(wire, iac, se)
	wire = append(wire, iac, sb, OptionComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00, iac, se)
	wire = append(wire, 'b')

	var gotCmd byte
	aborted := false
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(cmd byte, payload []byte) { gotCmd = cmd }
	r.SetOnBadCommand(func() { aborted = true })
	data, err := io.ReadAll(r)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAll: %v", err)
	}
	if !aborted {
		t.Fatal("oversized subnegotiation must trip the abort callback")
	}
	if gotCmd != CmdSetBaudRate {
		t.Fatalf("reader must resync on the next valid frame, got cmd %d", gotCmd)
	}
	if string(data) != "ab" {
		t.Fatalf("data = %q, want %q", data, "ab")
	}
	// And the memory actually held stays bounded by the cap, not the stream.
	if len(r.payload) > MaxSubnegotiation {
		t.Fatalf("payload retained %d bytes, cap is %d", len(r.payload), MaxSubnegotiation)
	}
}

func TestReaderAcceptsSubnegotiationAtTheCap(t *testing.T) {
	// Companion: a payload at exactly the cap still parses. The cap guards
	// against unbounded growth, not against large-but-bounded commands.
	payload := bytes.Repeat([]byte{0x7F}, MaxSubnegotiation-1) // room for the escaped 0xFF below
	wire := append([]byte{iac, sb, OptionComPort, CmdSetControl}, payload...)
	wire = append(wire, iac, iac, iac, se) // ...payload 0xFF, IAC SE

	var gotLen int
	r := NewReader(bytes.NewReader(wire))
	r.OnCommand = func(_ byte, payload []byte) { gotLen = len(payload) }
	if _, err := io.ReadAll(r); err != nil && err != io.EOF {
		t.Fatalf("ReadAll: %v", err)
	}
	if gotLen != MaxSubnegotiation {
		t.Fatalf("payload length = %d, want %d", gotLen, MaxSubnegotiation)
	}
}

func TestReaderSplitSubnegotiationAcrossReads(t *testing.T) {
	// A legitimately sized command split across source reads must parse as
	// one command — the cap cannot be allowed to break chunk-boundary state.
	wire := []byte{'a', iac, sb, OptionComPort, CmdSetBaudRate, 0x00, 0x01, 0xC2, 0x00, iac, se, 'b'}
	var gotCmd byte
	var gotPayload []byte
	r := NewReader(iotest.OneByteReader(bytes.NewReader(wire)))
	r.OnCommand = func(cmd byte, payload []byte) { gotCmd, gotPayload = cmd, append([]byte(nil), payload...) }
	data, err := io.ReadAll(r)
	if err != nil && err != io.EOF {
		t.Fatalf("ReadAll: %v", err)
	}
	if gotCmd != CmdSetBaudRate || !bytes.Equal(gotPayload, []byte{0x00, 0x01, 0xC2, 0x00}) {
		t.Fatalf("split command parsed as (%d, % x)", gotCmd, gotPayload)
	}
	if string(data) != "ab" {
		t.Fatalf("data = %q, want %q", data, "ab")
	}
}

func TestReaderConsumesZeroNilReads(t *testing.T) {
	// A source that returns (0, nil) — permitted by io.Reader for a
	// non-blocking source — must not busy-spin the read loop.
	src := &stallingReader{chunks: [][]byte{{'a', 'b'}, {'c'}}}
	r := NewReader(src)
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("data = %q, want %q", data, "abc")
	}
}

// stallingReader returns (0, nil) once between each chunk, like a source
// whose fd is momentarily empty.
type stallingReader struct {
	chunks [][]byte
	stall  bool
}

func (s *stallingReader) Read(p []byte) (int, error) {
	if s.stall {
		s.stall = false
		return 0, nil
	}
	if len(s.chunks) == 0 {
		return 0, io.EOF
	}
	s.stall = true
	n := copy(p, s.chunks[0])
	s.chunks = s.chunks[1:]
	return n, nil
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

func TestReaderReportsNegotiationVerbs(t *testing.T) {
	// A peer that offers options and hears nothing back will stall, so the
	// reader has to surface every verb, not just the ones we act on.
	wire := []byte{
		iac, Do, OptionComPort,
		iac, Will, OptionBinary,
		iac, Wont, OptionEcho,
		iac, Dont, OptionSGA,
	}
	type call struct{ verb, option byte }
	var got []call
	r := NewReader(bytes.NewReader(wire))
	r.OnNegotiate = func(verb, option byte) { got = append(got, call{verb, option}) }
	if _, err := io.ReadAll(r); err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	want := []call{
		{Do, OptionComPort},
		{Will, OptionBinary},
		{Wont, OptionEcho},
		{Dont, OptionSGA},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d negotiations, want %d: %+v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("negotiation %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestNegotiationDoesNotLeakIntoData(t *testing.T) {
	r := NewReader(bytes.NewReader([]byte{'a', iac, Do, OptionComPort, 'b'}))
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "ab" {
		t.Fatalf("data = %q, want %q", got, "ab")
	}
}

func TestWriteNegotiate(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteNegotiate(&buf, Will, OptionComPort); err != nil {
		t.Fatalf("WriteNegotiate: %v", err)
	}
	want := []byte{iac, Will, OptionComPort}
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("got % x, want % x", buf.Bytes(), want)
	}
}

func TestReaderIgnoresIACNoArgumentCommands(t *testing.T) {
	// IAC <NOP/DM/...> is two bytes with no option argument; the reader must
	// return to data state and keep the surrounding bytes, rather than
	// consuming the next byte as a phantom option.
	for _, cmd := range []byte{0xF1 /* NOP */, 0xF2 /* DM */, 0xF6 /* AYT */, 0x06 /* TIMING-MARK */} {
		wire := []byte{'a', iac, cmd, 'b'}
		r := NewReader(bytes.NewReader(wire))
		data, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("cmd %#x: ReadAll: %v", cmd, err)
		}
		if string(data) != "ab" {
			t.Fatalf("cmd %#x: data = %q, want %q", cmd, data, "ab")
		}
	}
}

func TestReaderDropsASubnegotiationWithAMidPayloadIACCommand(t *testing.T) {
	// IAC <other> inside a subnegotiation is malformed: the peer should have
	// doubled the IAC or sent IAC SE. The partial command is dropped and the
	// reader resynchronizes on data, rather than guessing at a meaning.
	wire := []byte{
		'a', iac, sb, OptionComPort, CmdSetControl, 0x08,
		iac, 0xEE /* neither IAC nor SE */, 'b',
	}
	r := NewReader(bytes.NewReader(wire))
	var cmds int
	r.OnCommand = func(byte, []byte) { cmds++ }
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if cmds != 0 {
		t.Fatalf("%d commands dispatched, want the malformed one dropped", cmds)
	}
	if string(data) != "ab" {
		t.Fatalf("data = %q, want %q — the reader must resynchronize on data", data, "ab")
	}
}
