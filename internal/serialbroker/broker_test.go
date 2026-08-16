package serialbroker_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/rfc2217"
	"github.com/majorcontext/moat/internal/serialbroker"
	"github.com/majorcontext/moat/internal/serialdev"
	"github.com/majorcontext/moat/internal/serialport"
	"github.com/majorcontext/moat/internal/serialtest"
)

func testDevice() serialdev.Device {
	return serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
}

// newBroker returns a broker with one approved device backed by a fake port,
// plus the address that device is listening on.
func newBroker(t *testing.T) (*serialbroker.Broker, *serialtest.FakePort, string) {
	t.Helper()
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return b, fp, addr
}

func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// readN reads exactly n bytes with a deadline, failing the test on timeout.
func readN(t *testing.T, c net.Conn, n int) []byte {
	t.Helper()
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, n)
	total := 0
	for total < n {
		got, err := c.Read(buf[total:])
		if err != nil {
			t.Fatalf("read: %v (got %d of %d bytes)", err, total, n)
		}
		total += got
	}
	return buf
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDataFlowsToTheDevice(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "ping" {
		t.Fatalf("device got %q, want ping", buf[:n])
	}
}

func TestDataFlowsFromTheDevice(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := fp.Peer().Write([]byte("pong")); err != nil {
		t.Fatalf("device write: %v", err)
	}
	if got := readN(t, c, 4); string(got) != "pong" {
		t.Fatalf("client got %q, want pong", got)
	}
}

func TestDeviceBytesContainingIACAreEscaped(t *testing.T) {
	// Firmware images are full of 0xFF. Unescaped, they corrupt the stream in a
	// way that presents as failing hardware.
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := fp.Peer().Write([]byte{0x01, 0xFF, 0x02}); err != nil {
		t.Fatalf("device write: %v", err)
	}
	got := readN(t, c, 4)
	want := []byte{0x01, 0xFF, 0xFF, 0x02}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x with IAC doubled", got, want)
	}
}

func TestClientIACIsUnescapedBeforeReachingTheDevice(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := c.Write([]byte{0x01, 0xFF, 0xFF, 0x02}); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	want := []byte{0x01, 0xFF, 0x02}
	if !bytes.Equal(buf[:n], want) {
		t.Fatalf("device got % x, want % x", buf[:n], want)
	}
}

func TestSetBaudRateReachesThePort(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(921600)); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "baud 921600", func() bool { return fp.LastSettings().Baud == 921600 })
}

func TestSetDataSizeReachesThePort(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetDataSize, []byte{7}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "7 data bits", func() bool { return fp.LastSettings().DataBits == 7 })
}

func TestDTRAssertAndDeassertBothReachThePort(t *testing.T) {
	// The ESP32 reset sequence is asserts *and* deasserts; testing only the
	// assert would pass while boards silently fail to enter the bootloader.
	_, fp, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROn}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "DTR asserted", func() bool { return fp.LastModem().DTR })

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROff}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "DTR deasserted", func() bool { return !fp.LastModem().DTR })
}

func TestRTSAssertAndDeassertBothReachThePort(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlRTSOn}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "RTS asserted", func() bool { return fp.LastModem().RTS })

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlRTSOff}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "RTS deasserted", func() bool { return !fp.LastModem().RTS })
}

func TestModemTransitionsArriveInOrder(t *testing.T) {
	// esptool's reset sequence depends on the order of transitions, not just
	// the final state. A relay that coalesced them would pass a final-state
	// assertion and still never reset the chip.
	_, fp, addr := newBroker(t)
	c := dial(t, addr)

	for _, ctrl := range []byte{
		rfc2217.ControlDTROff, rfc2217.ControlRTSOn,
		rfc2217.ControlDTROn, rfc2217.ControlRTSOff,
		rfc2217.ControlDTROff,
	} {
		if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{ctrl}); err != nil {
			t.Fatalf("WriteCommand(%s): %v", rfc2217.ControlName(ctrl), err)
		}
	}
	waitFor(t, "five modem transitions", func() bool { return len(fp.ModemSequence()) == 5 })

	want := []serialport.Modem{
		{DTR: false, RTS: false},
		{DTR: false, RTS: true},
		{DTR: true, RTS: true},
		{DTR: true, RTS: false},
		{DTR: false, RTS: false},
	}
	got := fp.ModemSequence()
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transition %d = %+v, want %+v (full sequence %+v)", i, got[i], want[i], got)
		}
	}
}

func TestBreakReachesThePort(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlBreakOn}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "break sent", func() bool { return fp.BreakCount() == 1 })
}

func TestControlCommandsDoNotLeakIntoTheDeviceStream(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROn}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	if _, err := c.Write([]byte("data")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "data" {
		t.Fatalf("device got %q, want only %q", buf[:n], "data")
	}
}

func TestSecondConnectionIsRefusedWhileOneIsActive(t *testing.T) {
	_, fp, addr := newBroker(t)
	first := dial(t, addr)
	if _, err := first.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	// Wait until the first connection owns the device.
	buf := make([]byte, 8)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}

	second := dial(t, addr)
	if err := second.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Read(make([]byte, 1)); err == nil {
		t.Fatal("a second connection must be closed while the first holds the device")
	}
}

func TestDeviceIsReusableAfterTheFirstClientDisconnects(t *testing.T) {
	// Companion of the refusal test: the claim must be released, or a run could
	// only ever connect once.
	//
	// Each connection gets a fresh port, matching serialport.Open — the broker
	// closes the port when a session ends, so a shared fake would be dead on the
	// second connection for reasons no real device would reproduce.
	var (
		mu       sync.Mutex
		ports    []*serialtest.FakePort
		detached bool
	)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			mu.Lock()
			defer mu.Unlock()
			p := serialtest.NewFakePort(t)
			ports = append(ports, p)
			return p, nil
		},
		Log: func(e serialbroker.Event) {
			if e.Kind != "detach" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			detached = true
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	first := dial(t, addr)
	if _, err := first.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first session to open the device", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ports) == 1
	})

	first.Close()
	// Wait for the claim to be released before reconnecting; a connection made
	// while the first still holds the device would be refused by design.
	waitFor(t, "first session to detach", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return detached
	})

	second := dial(t, addr)
	waitFor(t, "second session to open the device", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ports) == 2
	})
	if _, err := second.Write([]byte("y")); err != nil {
		t.Fatalf("write on the reconnected session: %v", err)
	}

	mu.Lock()
	peer := ports[1].Peer()
	mu.Unlock()
	buf := make([]byte, 8)
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatalf("device read after reconnect: %v", err)
	}
	if string(buf[:n]) != "y" {
		t.Fatalf("device got %q, want y", buf[:n])
	}
}

func TestRevokeClosesSessionAndListener(t *testing.T) {
	b, _, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	b.Revoke("run-a")

	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("session should close on Revoke")
	}
	if c2, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		c2.Close()
		t.Fatal("listener should be closed after Revoke")
	}
}

func TestRevokeOfAnUnknownRunIsHarmless(t *testing.T) {
	b, _, addr := newBroker(t)
	b.Revoke("some-other-run")
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("revoking another run must not disturb this one: %v", err)
	}
}

func TestListenTwiceForTheSameDeviceFails(t *testing.T) {
	b, _, _ := newBroker(t)
	_, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err == nil {
		t.Fatal("a device already claimed by another run must not be listenable")
	}
}

func TestListenReportsPortOpenFailureAtConnectTime(t *testing.T) {
	// The device may vanish between approval and connection; the client must
	// see the connection close rather than hang.
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			return nil, errFakeMissing
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := dial(t, addr)
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 1)); err == nil {
		t.Fatal("connection should close when the device cannot be opened")
	}
}

func TestEventsRecordAttachAndDetach(t *testing.T) {
	fp := serialtest.NewFakePort(t)
	var (
		mu     sync.Mutex
		kinds  []string
		counts serialbroker.Event
	)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log: func(e serialbroker.Event) {
			mu.Lock()
			defer mu.Unlock()
			kinds = append(kinds, e.Kind)
			if e.Kind == "detach" {
				counts = e
			}
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}
	waitFor(t, "attach event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return containsString(kinds, "attach")
	})

	c.Close()
	waitFor(t, "detach event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return containsString(kinds, "detach")
	})

	mu.Lock()
	defer mu.Unlock()
	if counts.RxBytes != 5 {
		t.Fatalf("detach event RxBytes = %d, want 5 bytes received from the container", counts.RxBytes)
	}
	if counts.Device != "esp32" || counts.RunID != "run-a" {
		t.Fatalf("detach event = %+v, want it to name the device and run", counts)
	}
}

// errFakeMissing stands in for a device that vanished between approval and
// connection.
var errFakeMissing = errors.New("no such device")

func containsString(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestTelnetOptionOffersAreAnswered(t *testing.T) {
	// pyserial-based clients (esptool among them) negotiate before sending any
	// com-port command and block waiting for the reply. Silence here presents
	// as a hung device rather than a protocol problem.
	_, _, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteNegotiate(c, rfc2217.Do, rfc2217.OptionComPort); err != nil {
		t.Fatalf("WriteNegotiate: %v", err)
	}
	got := readN(t, c, 3)
	want := []byte{0xFF, rfc2217.Will, rfc2217.OptionComPort}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want IAC WILL COM-PORT-OPTION (% x)", got, want)
	}
}

func TestBinaryModeIsAccepted(t *testing.T) {
	// Serial payloads are binary; a client asking for binary mode must get a
	// yes, or it is entitled to mangle high-bit bytes.
	_, _, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteNegotiate(c, rfc2217.Will, rfc2217.OptionBinary); err != nil {
		t.Fatalf("WriteNegotiate: %v", err)
	}
	got := readN(t, c, 3)
	want := []byte{0xFF, rfc2217.Do, rfc2217.OptionBinary}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want IAC DO BINARY (% x)", got, want)
	}
}

func TestUnsupportedOptionIsRefused(t *testing.T) {
	// Companion of the accept cases: an option we do not implement must get an
	// explicit no, not silence.
	_, _, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteNegotiate(c, rfc2217.Do, rfc2217.OptionEcho); err != nil {
		t.Fatalf("WriteNegotiate: %v", err)
	}
	got := readN(t, c, 3)
	want := []byte{0xFF, rfc2217.Wont, rfc2217.OptionEcho}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want IAC WONT ECHO (% x)", got, want)
	}
}

func TestSetBaudRateIsConfirmedToTheClient(t *testing.T) {
	// RFC2217 clients wait for the server to echo the accepted value; without
	// the reply, esptool blocks on every baud change.
	_, _, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(921600)); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	// IAC SB COM-PORT (cmd+100) <4 bytes> IAC SE
	got := readN(t, c, 10)
	want := []byte{
		0xFF, 0xFA, rfc2217.OptionComPort, rfc2217.CmdSetBaudRate + 100,
		0x00, 0x0E, 0x10, 0x00, 0xFF, 0xF0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want the server confirmation % x", got, want)
	}
}

// capture is an in-memory recorder standing in for the run's capture file.
type capture struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	closed bool
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *capture) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return nil
}

func (c *capture) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func newRecordingBroker(t *testing.T, record string) (*serialtest.FakePort, *capture, string) {
	t.Helper()
	fp := serialtest.NewFakePort(t)
	cap := &capture{}
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		OpenRecorder: func(string, string) (io.WriteCloser, error) {
			return cap, nil
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Record: record,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	return fp, cap, addr
}

func TestFullRecordModeCapturesBothDirections(t *testing.T) {
	fp, cap, addr := newRecordingBroker(t, serialbroker.RecordFull)
	c := dial(t, addr)

	if _, err := c.Write([]byte("to-device")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}
	if _, err := fp.Peer().Write([]byte("from-device")); err != nil {
		t.Fatal(err)
	}
	readN(t, c, len("from-device"))

	waitFor(t, "both directions captured", func() bool {
		got := cap.String()
		return strings.Contains(got, hex.EncodeToString([]byte("to-device"))) &&
			strings.Contains(got, hex.EncodeToString([]byte("from-device")))
	})
	if got := cap.String(); !strings.Contains(got, " tx ") || !strings.Contains(got, " rx ") {
		t.Fatalf("capture %q should label each direction", got)
	}
}

func TestEventsModeCapturesNoPayload(t *testing.T) {
	// Companion of the capture test, and the more important half: the default
	// must not write firmware images or device credentials to disk.
	fp, cap, addr := newRecordingBroker(t, "events")
	c := dial(t, addr)

	if _, err := c.Write([]byte("secret-payload")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}
	if got := cap.String(); got != "" {
		t.Fatalf("capture should be empty in events mode, got %q", got)
	}
}

func TestEmptyRecordModeCapturesNoPayload(t *testing.T) {
	// An omitted record field must behave like "events", not like "full".
	fp, cap, addr := newRecordingBroker(t, "")
	c := dial(t, addr)

	if _, err := c.Write([]byte("secret-payload")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}
	if got := cap.String(); got != "" {
		t.Fatalf("capture should be empty when record is unset, got %q", got)
	}
}

func TestFullRecordModeWithoutARecorderStillRuns(t *testing.T) {
	// A recorder that cannot be opened must not strand the hardware.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		OpenRecorder: func(string, string) (io.WriteCloser, error) {
			return nil, errors.New("disk full")
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Record: serialbroker.RecordFull,
	})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	if _, err := c.Write([]byte("still-works")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 32)
	n, err := fp.Peer().Read(buf)
	if err != nil {
		t.Fatalf("device read: %v", err)
	}
	if string(buf[:n]) != "still-works" {
		t.Fatalf("device got %q, want still-works", buf[:n])
	}
}

func TestEventsCarryDeviceIdentity(t *testing.T) {
	// Audit entries must name the physical device, not just the config name.
	fp := serialtest.NewFakePort(t)
	var (
		mu     sync.Mutex
		events []serialbroker.Event
	)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log: func(e serialbroker.Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		},
	})
	t.Cleanup(func() { b.Close() })
	addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()})
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "attach event", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	e := events[0]
	if e.VID != "303a" || e.PID != "1001" || e.DeviceSerial != "AAA" {
		t.Fatalf("event %+v should carry the device's USB identity", e)
	}
	if e.DevicePath != "/dev/ttyUSB0" {
		t.Fatalf("DevicePath = %q, want the host device node", e.DevicePath)
	}
}
