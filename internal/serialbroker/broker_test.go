package serialbroker_test

import (
	"bytes"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"strconv"
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

	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
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
	_, _, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err == nil {
		t.Fatal("a device already claimed by another run must not be listenable")
	}
}

func TestReclaimingARevokedDeviceSucceeds(t *testing.T) {
	// The mirror of TestListenTwiceForTheSameDeviceFails: a claim that dies
	// with its run must not hold the device hostage — the next run gets it.
	b, _, addr := newBroker(t)
	b.Revoke("run-a")
	_, addr2, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("after the owning run is revoked the device must be claimable: %v", err)
	}
	if addr2 != addr {
		// Not a hard requirement on the port number, but the idempotent
		// re-Listen below must return whatever the live listener answers on.
		t.Logf("reclaimed device moved from %s to %s", addr, addr2)
	}
	// And the old address must now serve the new run, not the dead one.
	c := dial(t, addr2)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
}

func TestSameRunRelistenIsIdempotent(t *testing.T) {
	// monitorProxyHealth re-registers an existing run with the daemon after a
	// transient failure; that must return the live listener, not a self-conflict.
	b, _, addr := newBroker(t)
	_, addr2, err := b.Listen("run-a", serialbroker.Approved{
		Name:   "esp32-renamed", // a different config name for the same device
		Device: testDevice(),
	}, "")
	if err != nil {
		t.Fatalf("re-listening a device this run already holds must succeed: %v", err)
	}
	if addr2 != addr {
		t.Fatalf("re-listen must return the existing address %s, got %s", addr, addr2)
	}
	// No second claim was registered, so revoking the run once must close
	// the device exactly once and leave it free for the next run.
	b.Revoke("run-a")
	if c, err := net.DialTimeout("tcp", addr, 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("listener must be closed after the run's one and only claim is revoked")
	}
	if _, _, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, ""); err != nil {
		t.Fatalf("device must be free after one revoke: %v", err)
	}
}

func TestTwoNamesForOneDeviceConflict(t *testing.T) {
	// The claim is keyed on the physical device, not the config name: two
	// entries resolving to the same node must not both get listeners, or
	// their sessions interleave bytes on the same line. The TIOCEXCL backstop
	// would only surface at connect time as a confusing open failure.
	b, _, _ := newBroker(t) // run-a holds /dev/ttyUSB0 as "esp32"
	_, _, err := b.Listen("run-a", serialbroker.Approved{
		Name:   "monitor", // same run, different name, same device
		Device: testDevice(),
	}, "")
	if err != nil {
		t.Fatalf("same run re-listening its own device under a new name must be idempotent: %v", err)
	}
	_, _, err = b.Listen("run-b", serialbroker.Approved{
		Name:   "flash", // different run, same device
		Device: testDevice(),
	}, "")
	if err == nil {
		t.Fatal("a second run claiming the same physical device under a different name must fail")
	}
	if !strings.Contains(err.Error(), "run-a") || !strings.Contains(err.Error(), "/dev/ttyUSB0") {
		t.Fatalf("conflict must name the device and the owning run, got: %v", err)
	}
}

func TestSameNameDifferentDevicesDoesNotConflict(t *testing.T) {
	// The mirror of TestTwoNamesForOneDeviceConflict: an identical config
	// name on a different physical device is a different claim. Generic
	// names collide across unrelated projects; only the device identity
	// should decide.
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
	})
	t.Cleanup(func() { b.Close() })
	devA := serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
	devB := serialdev.Device{Path: "/dev/ttyUSB1", VID: "303a", PID: "1001", Serial: "BBB"}
	if _, _, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: devA}, ""); err != nil {
		t.Fatalf("first listen: %v", err)
	}
	_, _, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: devB}, "")
	if err != nil {
		t.Fatalf("same name on a different physical device must not conflict: %v", err)
	}
}

func TestCloseListenersReleasesOnlyTheGivenClaims(t *testing.T) {
	// A partially-failed registration rolls back only what it opened. The
	// run's earlier listeners — a live container's devices — must survive.
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
	})
	t.Cleanup(func() { b.Close() })
	devA := serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
	devB := serialdev.Device{Path: "/dev/ttyUSB1", VID: "1a86", PID: "7523", Serial: "BBB"}

	refA, addrA, err := b.Listen("run-a", serialbroker.Approved{Name: "a", Device: devA}, "")
	if err != nil {
		t.Fatalf("listen a: %v", err)
	}
	_, addrB, err := b.Listen("run-a", serialbroker.Approved{Name: "b", Device: devB}, "")
	if err != nil {
		t.Fatalf("listen b: %v", err)
	}

	// Roll back only B's claim — the shape of a second registration that
	// failed on a third device.
	b.CloseListeners([]*serialbroker.ListenerRef{refA})

	if c, err := net.DialTimeout("tcp", addrA, 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("rolled-back listener must be closed")
	}
	// A must be released for other runs to claim.
	if _, _, err := b.Listen("run-b", serialbroker.Approved{Name: "a", Device: devA}, ""); err != nil {
		t.Fatalf("rolled-back claim must be free: %v", err)
	}
	// B's listener must still be alive and serving the same run.
	c := dial(t, addrB)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("live sibling listener must survive the rollback: %v", err)
	}
}

func TestRevokeReleasesEveryClaimOfTheRun(t *testing.T) {
	// Revoke remains the full-run teardown (used by unregister and the
	// liveness reaper); every claim of the run goes, others' stay.
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
	})
	t.Cleanup(func() { b.Close() })
	devA := serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
	devB := serialdev.Device{Path: "/dev/ttyUSB1", VID: "1a86", PID: "7523", Serial: "BBB"}
	_, addrA, err := b.Listen("run-a", serialbroker.Approved{Name: "a", Device: devA}, "")
	if err != nil {
		t.Fatalf("listen a: %v", err)
	}
	_, addrB, err := b.Listen("run-b", serialbroker.Approved{Name: "b", Device: devB}, "")
	if err != nil {
		t.Fatalf("listen b: %v", err)
	}

	b.Revoke("run-a")

	if c, err := net.DialTimeout("tcp", addrA, 500*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("revoked run's listener must be closed")
	}
	c := dial(t, addrB)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("another run's listener must survive the revoke: %v", err)
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
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

func TestPurgeIsConfirmedToTheClient(t *testing.T) {
	// pyserial's Serial.open() sends PURGE_DATA as part of connecting —
	// reset_input_buffer() and reset_output_buffer() run before open returns —
	// and blocks until the server replies. A broker without this reply fails
	// every pyserial connect (esptool: "timeout while waiting for option
	// 'purge'"), even though the data path itself is fine. The reply must echo
	// the request's value byte exactly: pyserial's check_answer rejects a
	// mismatch.
	_, _, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{rfc2217.PurgeBothBuffers}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	// IAC SB COM-PORT (cmd+100) <value> IAC SE
	got := readN(t, c, 7)
	want := []byte{
		0xFF, 0xFA, rfc2217.OptionComPort, rfc2217.CmdPurgeData + 100,
		rfc2217.PurgeBothBuffers, 0xFF, 0xF0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want the server confirmation % x", got, want)
	}
}

func TestPurgeFlushesThePort(t *testing.T) {
	// The reply alone is not the feature: pyserial purges because it wants
	// stale bytes gone before it starts reading. The broker must forward the
	// flush to the device, not just acknowledge it.
	_, fp, addr := newBroker(t)
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{rfc2217.PurgeReceiveBuffer}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "buffers flushed", func() bool { return fp.PurgeCount() == 1 })
}

func TestPurgeIsAcknowledgedEvenWhenTheFlushFails(t *testing.T) {
	// A flush failure must not swallow the reply. The first version of PURGE
	// support returned early on a flush error, so a device whose flush failed
	// (Darwin EFAULT, a vanished USB device) left pyserial blocked on the ack
	// — presenting as a dead device when the data path was still fine.
	_, fp, addr := newBroker(t)
	fp.SetFlushError(errors.New("device detached"))
	c := dial(t, addr)

	if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{rfc2217.PurgeReceiveBuffer}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	got := readN(t, c, 7)
	want := []byte{
		0xFF, 0xFA, rfc2217.OptionComPort, rfc2217.CmdPurgeData + 100,
		rfc2217.PurgeReceiveBuffer, 0xFF, 0xF0,
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("got % x, want the ack even after a flush failure % x", got, want)
	}
}

// TestPySerialOpenSequence models pyserial's Serial.open() against the broker:
// the exact command sequence serial/rfc2217.py sends when connecting, with
// every step blocking on its server reply the way the real client does.
//
// This is the test that would have caught the missing PURGE case: esptool
// failed with "timeout while waiting for option 'purge'" on real hardware
// while every existing broker test passed, because none of them spoke the
// client's full connect dance. Each step uses a short deadline so a missing
// reply fails the test in seconds instead of hanging it.
func TestPySerialOpenSequence(t *testing.T) {
	_, fp, addr := newBroker(t)
	c := dial(t, addr)
	if err := c.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// 1. Telnet negotiation: the client offers WILL for BINARY, SGA, and
	// COM-PORT-OPTION, and DO for ECHO, SGA, BINARY, and COM-PORT-OPTION.
	// The mandatory ones are "we-BINARY" (client WILL BINARY) and
	// "we-RFC2217" (client WILL COM-PORT) — open() blocks until both are
	// answered affirmatively.
	for _, offer := range [][2]byte{
		{rfc2217.Do, rfc2217.OptionEcho},
		{rfc2217.Will, rfc2217.OptionSGA},
		{rfc2217.Do, rfc2217.OptionSGA},
		{rfc2217.Do, rfc2217.OptionBinary},
		{rfc2217.Do, rfc2217.OptionComPort},
		{rfc2217.Will, rfc2217.OptionBinary},
		{rfc2217.Will, rfc2217.OptionComPort},
	} {
		if err := rfc2217.WriteNegotiate(c, offer[0], offer[1]); err != nil {
			t.Fatalf("offering %v: %v", offer, err)
		}
	}
	// The server answers each offer with 3 bytes; 7 offers → 21 bytes. The
	// client checks only that the mandatory ones came back positive, but we
	// assert the count so a regression that stalls negotiation fails here
	// rather than later in a settings step.
	if got := readN(t, c, 21); len(got) != 21 {
		t.Fatalf("negotiation replies: got %d bytes, want 21", len(got))
	}

	// 2. Line settings: baud, data size, parity, stop size, each sent then
	// waited on (open() raises "Remote does not accept parameter change"
	// after 3s otherwise). Replies are read with a length that matches the
	// payload: 10 for baud (4-byte value), 7 for the single-byte settings.
	settings := []struct {
		cmd     byte
		payload []byte
	}{
		{rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(921600)},
		{rfc2217.CmdSetDataSize, []byte{8}},
		{rfc2217.CmdSetParity, []byte{1}},
		{rfc2217.CmdSetStopSize, []byte{1}},
	}
	for _, st := range settings {
		if err := rfc2217.WriteCommand(c, st.cmd, st.payload); err != nil {
			t.Fatalf("sending setting %d: %v", st.cmd, err)
		}
		n := 7
		if st.cmd == rfc2217.CmdSetBaudRate {
			n = 10
		}
		got := readN(t, c, n)
		if got[3] != st.cmd+100 {
			t.Fatalf("setting %d: reply cmd = %d, want %d", st.cmd, got[3], st.cmd+100)
		}
	}

	// 3. Flow control: SET_CONTROL with USE_NO_FLOW_CONTROL.
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{rfc2217.ControlFlowNone}); err != nil {
		t.Fatalf("sending flow control: %v", err)
	}
	readN(t, c, 7)

	// 4. DTR and RTS: the client asserts both by default (their _dtr_state
	// and _rts_state default to True).
	for _, ctrl := range []byte{rfc2217.ControlDTROn, rfc2217.ControlRTSOn} {
		if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{ctrl}); err != nil {
			t.Fatalf("sending %s: %v", rfc2217.ControlName(ctrl), err)
		}
		readN(t, c, 7)
	}

	// 5. The purges: reset_input_buffer() then reset_output_buffer(), both
	// called from open(). This is where esptool hung on real hardware.
	for _, v := range []byte{rfc2217.PurgeReceiveBuffer, rfc2217.PurgeTransmitBuffer} {
		if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{v}); err != nil {
			t.Fatalf("sending purge %d: %v", v, err)
		}
		readN(t, c, 7)
	}

	// The device saw the whole sequence.
	waitFor(t, "baud to reach the device", func() bool { return fp.LastSettings().Baud == 921600 })
	waitFor(t, "both purges to reach the device", func() bool { return fp.PurgeCount() == 2 })
	if !fp.LastModem().DTR || !fp.LastModem().RTS {
		t.Fatalf("final modem state = %+v, want DTR and RTS asserted", fp.LastModem())
	}
}

func TestPurgeOfEveryDefinedValueIsAnswered(t *testing.T) {
	// pyserial sends PURGE_RECEIVE_BUFFER from reset_input_buffer and
	// PURGE_TRANSMIT_BUFFER from reset_output_buffer, both inside open().
	// Answering only one of them leaves the other failing on the same
	// connect-time timeout.
	//
	// The device serves one connection at a time, so each iteration waits for
	// the previous session to release it — the same pattern the
	// reusability test uses.
	var (
		mu       sync.Mutex
		opens    int
		detached int
		lastOpen int
	)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			mu.Lock()
			defer mu.Unlock()
			opens++
			return serialtest.NewFakePort(t), nil
		},
		Log: func(e serialbroker.Event) {
			if e.Kind != "detach" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			detached++
		},
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	for _, v := range []byte{
		rfc2217.PurgeReceiveBuffer,
		rfc2217.PurgeTransmitBuffer,
		rfc2217.PurgeBothBuffers,
	} {
		mu.Lock()
		lastOpen = opens
		mu.Unlock()

		c := dial(t, addr)
		if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{v}); err != nil {
			t.Fatalf("WriteCommand(%d): %v", v, err)
		}
		got := readN(t, c, 7)
		want := []byte{
			0xFF, 0xFA, rfc2217.OptionComPort, rfc2217.CmdPurgeData + 100,
			v, 0xFF, 0xF0,
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("value %d: got % x, want the server confirmation % x", v, got, want)
		}
		c.Close()
		// Wait until the session has released the device before dialing again.
		waitFor(t, "session to detach", func() bool {
			mu.Lock()
			defer mu.Unlock()
			return detached >= 1 && opens > lastOpen
		})
		mu.Lock()
		detached = 0
		mu.Unlock()
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Record: record,
	}, "")
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Record: serialbroker.RecordFull,
	}, "")
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
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
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

func TestOversizedSubnegotiationDropsTheSession(t *testing.T) {
	// The session must not wedge on a hostile or broken client: the reader
	// aborts a past-cap subnegotiation, the connection is dropped, and the
	// device becomes usable again — a wedged client holding the only session
	// slot is a denial of the hardware.
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	// One runaway subnegotiation: IAC SB COM-PORT <cmd> then payload past the cap.
	wire := []byte{0xFF, 0xFA, 44, 0x05}
	for i := 0; i < rfc2217.MaxSubnegotiation+256; i++ {
		wire = append(wire, 'X')
	}
	if _, err := c.Write(wire); err != nil {
		t.Fatal(err)
	}

	// The connection must close, not hang.
	if err := c.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(make([]byte, 16)); err == nil {
		t.Fatal("connection should be dropped after an oversized subnegotiation")
	}

	// And the device must accept a new session — the claim is per-listener,
	// but a fresh client must not be refused as busy.
	c2 := dial(t, addr)
	if _, err := c2.Write([]byte("y")); err != nil {
		t.Fatalf("device must accept a new session after the drop: %v", err)
	}
}

func TestListenBindsTheRequestedAddressNotWildcard(t *testing.T) {
	// RFC2217 has no authentication, so where the listener binds is the
	// reachability control. A listener that ignored the requested address and
	// bound every interface would expose the device to the network.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		t.Fatalf("listener bound %q — every other requested address must be refused, not widened to wildcard", addr)
	}
}

func TestListenWithAnUnbindableAddressRefusesTheDevice(t *testing.T) {
	// Companion of the happy path: an address that cannot be bound must fail
	// the claim outright. Falling back to wildcard (or to the broker default)
	// would silently expose the device on an interface nobody asked for.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		// The broker default is deliberately unbindable here so a fallback
		// to it produces the same refusal this test asserts.
		BindAddr: "203.0.113.1", // TEST-NET-3: never assigned to this host
	})
	t.Cleanup(func() { b.Close() })

	if _, _, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, ""); err == nil {
		t.Fatal("a request for an unbindable address must refuse the device, not bind somewhere else")
	}
	// The failed claim must leave the device free for the next run.
	if _, _, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1"); err != nil {
		t.Fatalf("device must be free after a failed bind: %v", err)
	}
}

func TestListenAtRebindsTheSamePort(t *testing.T) {
	// The container's MOAT_SERIAL_*_URL froze the port at create. A restore
	// that rebinds a different number leaves the run's device dead while the
	// daemon looks healthy — so ListenAt must return the exact port asked for.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	// Simulate the daemon restart: old broker closed, new broker, same
	// pinned port.
	if err := b.Close(); err != nil {
		t.Fatalf("closing the old broker: %v", err)
	}
	b2 := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b2.Close() })
	_, addr2, err := b2.ListenAt("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1", port)
	if err != nil {
		t.Fatalf("ListenAt on the freed port: %v", err)
	}
	if addr2 != addr {
		t.Fatalf("rebound %q, want the exact original address %q", addr2, addr)
	}
}

func TestListenAtRefusesATakenPort(t *testing.T) {
	// Companion of the happy path: a pinned port that is taken must fail with
	// a named error, never fall back to an OS-assigned port — the container
	// could not reach the fallback, and the failure would be invisible.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		t.Fatal(err)
	}

	// A different run holds the device on this port in a second broker.
	b2 := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b2.Close() })
	_, _, err = b2.ListenAt("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1", port)
	if err == nil {
		t.Fatal("binding a taken port must fail, not fall back to an ephemeral port")
	}
	// The failed claim must leave the device free.
	if _, _, err := b2.ListenAt("run-c", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "127.0.0.1", 0); err != nil {
		// run-c requested port 0 = OS-assigned; must succeed.
		t.Fatalf("device must be free after a failed pin: %v", err)
	}
}

func TestConfiguredBaudIsAppliedOnConnect(t *testing.T) {
	// moat.yaml's `baud:` is the line rate the session opens at — a client
	// that never sends SET-BAUDRATE (a plain reader) still gets it.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Baud: 115200,
	}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := dial(t, addr)
	_ = c
	waitFor(t, "baud 115200 applied at open", func() bool { return fp.LastSettings().Baud == 115200 })
}

func TestClientSetBaudOverridesTheConfiguredBaud(t *testing.T) {
	// Companion: the initial rate is a default, not a lock — a client that
	// negotiates its own rate (esptool always does) still wins.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Baud: 115200,
	}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := dial(t, addr)
	waitFor(t, "initial baud", func() bool { return fp.LastSettings().Baud == 115200 })

	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(921600)); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	waitFor(t, "client baud 921600", func() bool { return fp.LastSettings().Baud == 921600 })
}

func TestZeroBaudLeavesThePortUnchanged(t *testing.T) {
	// Companion: no `baud:` in moat.yaml must not force a rate on the port —
	// tools that set their own would see a spurious initial SET-BAUDRATE.
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
	})
	t.Cleanup(func() { b.Close() })

	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	dial(t, addr)
	time.Sleep(100 * time.Millisecond)
	if got := fp.LastSettings().Baud; got != 0 {
		t.Fatalf("LastSettings().Baud = %d, want 0 — no configured baud must leave the port alone", got)
	}
}
