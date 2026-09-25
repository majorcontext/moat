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

func TestCloseListenersSkipsAPreexistingClaim(t *testing.T) {
	// A re-registration batch may re-Listen a device the run already holds
	// (idempotent) alongside a new one, then fail on a later device and roll the
	// batch back. Rolling back must not tear down the live listener the run
	// already had — only what this batch actually opened. Regression: the
	// idempotent path handed back an owning ref, so CloseListeners closed the
	// live device out from under the container.
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
	})
	t.Cleanup(func() { b.Close() })
	devA := serialdev.Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA"}
	devB := serialdev.Device{Path: "/dev/ttyUSB1", VID: "1a86", PID: "7523", Serial: "BBB"}

	// run-a already holds A (a live container's device).
	_, addrA, err := b.Listen("run-a", serialbroker.Approved{Name: "a", Device: devA}, "")
	if err != nil {
		t.Fatalf("initial listen a: %v", err)
	}
	// The batch: re-Listen A (idempotent, preexisting) and open a new B.
	refAPre, _, err := b.Listen("run-a", serialbroker.Approved{Name: "a", Device: devA}, "")
	if err != nil {
		t.Fatalf("re-listen a: %v", err)
	}
	refB, addrB, err := b.Listen("run-a", serialbroker.Approved{Name: "b", Device: devB}, "")
	if err != nil {
		t.Fatalf("listen b: %v", err)
	}
	// The batch fails on a later device and rolls back everything it collected.
	b.CloseListeners([]*serialbroker.ListenerRef{refAPre, refB})

	// A was not opened by this batch — it must survive.
	c := dial(t, addrA)
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatalf("device the batch only re-listed must survive rollback: %v", err)
	}
	// B was opened by the batch — it must be closed.
	if conn, err := net.DialTimeout("tcp", addrB, 500*time.Millisecond); err == nil {
		conn.Close()
		t.Fatal("the device this batch opened must be rolled back")
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
	// A recorder that cannot be opened must not strand the hardware, and must
	// not leave the audit trail claiming capture that did not happen.
	fp := serialtest.NewFakePort(t)
	var (
		mu     sync.Mutex
		events []serialbroker.Event
	)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		OpenRecorder: func(string, string) (io.WriteCloser, error) {
			return nil, errors.New("disk full")
		},
		Log: func(e serialbroker.Event) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
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
	defer c.Close()
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

	waitFor(t, "attach and error events", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(events) >= 2
	})
	mu.Lock()
	defer mu.Unlock()
	for _, e := range events {
		// record: full was configured, but no recorder could be opened, so no
		// event may claim it: the audit entry stamped from this field would
		// otherwise attest a capture file that does not exist.
		if e.Record == serialbroker.RecordFull {
			t.Fatalf("event %q claims record=full; capture degraded and must report events only: %+v", e.Kind, e)
		}
		if e.Kind == "error" && e.Detail == "" {
			t.Fatalf("degraded capture must leave an error event explaining why: %+v", e)
		}
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
	// The record mode is the audit trail's proof that payload capture was
	// enabled for the session; an empty value here reads downstream as
	// "events only" even when record: full was configured.
	if e.Record != "events" {
		t.Fatalf("Record = %q, want the events default", e.Record)
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

// readCommand reads one complete com-port reply frame (IAC SB 44 cmd payload
// IAC SE) and returns (cmd, payload).
func readCommand(t *testing.T, c net.Conn) (byte, []byte) {
	t.Helper()
	frame := readN(t, c, 7) // 4 header/trailer + up to 3 payload; extended below if payload is longer
	for frame[len(frame)-2] != iacByte || frame[len(frame)-1] != seByte {
		frame = append(frame, readN(t, c, 1)...)
		if len(frame) > 32 {
			t.Fatalf("reply frame did not terminate: % x", frame)
		}
	}
	return frame[3], frame[4 : len(frame)-2]
}

const (
	iacByte = 0xFF
	seByte  = 0xF0
)

func TestModemStatePollIsAnswered(t *testing.T) {
	// pyserial with poll_modem=1 sends NOTIFY_MODEMSTATE from every .cts/.dsr/
	// .ri/.cd read and raises "remote sends no NOTIFY_MODEMSTATE" if nothing
	// comes back — the trap the review flagged: the poll is a client-to-server
	// command (7), and the answer is the same value plus 100.
	_, fp, addr := newBroker(t)
	fp.SetModemStatus(serialport.ModemStatus{CTS: true, CD: true})

	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdNotifyModemState, nil); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.SrvNotifyModemState {
		t.Fatalf("reply cmd = %d, want %d (NOTIFY-MODEMSTATE)", cmd, rfc2217.SrvNotifyModemState)
	}
	want := rfc2217.ModemCTS | rfc2217.ModemCD
	if len(payload) != 1 || payload[0] != want {
		t.Fatalf("modem state = % x, want [%02x]", payload, want)
	}
}

func TestModemStatePollOnEmptyLinesIsAnsweredWithZero(t *testing.T) {
	// Companion: an adapter that wires no input lines still gets an answer —
	// zero, the all-clear encoding — because silence reads as a dead port.
	_, fp, addr := newBroker(t)
	fp.SetModemStatus(serialport.ModemStatus{})

	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdNotifyModemState, nil); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.SrvNotifyModemState {
		t.Fatalf("reply cmd = %d, want %d", cmd, rfc2217.SrvNotifyModemState)
	}
	if len(payload) != 1 || payload[0] != 0 {
		t.Fatalf("modem state = % x, want [00]", payload)
	}
}

func TestModemStatePollAnswersZeroWhenTheDeviceCannotReport(t *testing.T) {
	// A device whose TIOCMGET fails (some adapters do not wire the lines)
	// must not stall the client: the answer is zero and an error event is
	// recorded.
	_, fp, addr := newBroker(t)
	fp.SetModemStatusError(errors.New("TIOCMGET: inappropriate ioctl"))

	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdNotifyModemState, nil); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.SrvNotifyModemState {
		t.Fatalf("reply cmd = %d, want %d", cmd, rfc2217.SrvNotifyModemState)
	}
	if len(payload) != 1 || payload[0] != 0 {
		t.Fatalf("modem state = % x, want [00]", payload)
	}
}

func TestSetControlQueriesAnswerWithTheCurrentValue(t *testing.T) {
	// The zero-ish SET-CONTROL values are queries, not changes: a client that
	// sends them expects the current setting back, not an echo of its own
	// query byte. Assert DTR/RTS first so the "current" values are known.
	_, fp, addr := newBroker(t)

	c := dial(t, addr)
	for _, ctrl := range []byte{rfc2217.ControlDTROff, rfc2217.ControlRTSOn} {
		if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{ctrl}); err != nil {
			t.Fatalf("sending %s: %v", rfc2217.ControlName(ctrl), err)
		}
		readCommand(t, c)
	}
	if fp.LastModem().DTR || !fp.LastModem().RTS {
		t.Fatalf("setup: modem = %+v, want DTR off, RTS on", fp.LastModem())
	}

	cases := []struct {
		query byte
		want  byte
	}{
		{rfc2217.ControlQueryDTR, rfc2217.ControlDTROff},
		{rfc2217.ControlQueryRTS, rfc2217.ControlRTSOn},
		{rfc2217.ControlQueryFlow, rfc2217.ControlFlowNone},
		{rfc2217.ControlQueryBreak, rfc2217.ControlBreakOff},
	}
	for _, tc := range cases {
		if err := rfc2217.WriteCommand(c, rfc2217.CmdSetControl, []byte{tc.query}); err != nil {
			t.Fatalf("sending %s: %v", rfc2217.ControlName(tc.query), err)
		}
		cmd, payload := readCommand(t, c)
		if cmd != rfc2217.SrvSetControl {
			t.Fatalf("%s: reply cmd = %d, want %d", rfc2217.ControlName(tc.query), cmd, rfc2217.SrvSetControl)
		}
		if len(payload) != 1 || payload[0] != tc.want {
			t.Fatalf("%s: reply = % x, want [%02x]", rfc2217.ControlName(tc.query), payload, tc.want)
		}
	}
	// A query must not touch the lines.
	if fp.LastModem().DTR || !fp.LastModem().RTS {
		t.Fatalf("queries changed the modem lines: %+v", fp.LastModem())
	}
}

func TestModemStateMaskIsAcknowledged(t *testing.T) {
	// pyserial sends SET_MODEMSTATE_MASK to subscribe to change
	// notifications. The broker answers polls instead of pushing changes,
	// but the mask still needs its echo or the client's wait hangs — same
	// class as the PURGE ack.
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetModemStateMask, []byte{0x1F}); err != nil {
		t.Fatalf("WriteCommand: %v", err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.SrvSetModemStateMask {
		t.Fatalf("reply cmd = %d, want %d", cmd, rfc2217.SrvSetModemStateMask)
	}
	if len(payload) != 1 || payload[0] != 0x1F {
		t.Fatalf("mask echo = % x, want [1f]", payload)
	}
}

// TestParityReachesThePort pins the seam the parity unit tests cannot see:
// parityFromWire's result must land in the Port's ApplySettings, not merely in
// the broker's reply. The fake records every settings change it was given, so
// an odd↔even swap anywhere in that plumbing shows up here.
// TestPySerialOpenSequence sends parity none only.
func TestParityReachesThePort(t *testing.T) {
	cases := []struct {
		wire byte
		want uint8
	}{
		{2, serialport.ParityOdd},
		{3, serialport.ParityEven},
		{1, serialport.ParityNone},
	}
	for _, c := range cases {
		_, fp, addr := newBroker(t)
		conn := dial(t, addr)
		if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := rfc2217.WriteCommand(conn, rfc2217.CmdSetParity, []byte{c.wire}); err != nil {
			t.Fatalf("sending parity %d: %v", c.wire, err)
		}
		readCommand(t, conn) // confirmation reply
		settings := fp.SettingsSequence()
		if len(settings) == 0 || settings[len(settings)-1].Parity != c.want {
			t.Errorf("parity %d: port settings = %+v, want last entry parity %d", c.wire, settings, c.want)
		}
		conn.Close()
	}
}

// TestConflictEventNamesTheDevice covers the `conflict` audit event, which the
// refused-connection test cannot see: it asserts only that the socket closes.
// The event is how a claim conflict surfaces in devices.jsonl and the audit
// chain — the run's owner learns the device was busy, not merely unreachable.
func TestConflictEventNamesTheDevice(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log:      func(e serialbroker.Event) { events <- e },
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
	buf := make([]byte, 8)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}

	second := dial(t, addr)
	defer second.Close()
	for {
		select {
		case e := <-events:
			if e.Kind == "conflict" {
				if e.Device != "esp32" {
					t.Errorf("conflict event device = %q, want esp32", e.Device)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no conflict event fired for the refused connection")
		}
	}
}

// TestSetModemFailureEmitsAnErrorEventAndKeepsTheSessionAlive exercises the
// broker's error-event path: a device that cannot drive its control lines
// (unplug mid-command, EIO) must surface as an `error` event rather than die
// silently, and the session must keep answering — the client is told what
// failed through the event stream while the data path continues.
func TestSetModemFailureEmitsAnErrorEventAndKeepsTheSessionAlive(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log:      func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := dial(t, addr)
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	fp.SetModemError(errors.New("input/output error"))
	if err := rfc2217.WriteCommand(conn, rfc2217.CmdSetControl, []byte{rfc2217.ControlDTROn}); err != nil {
		t.Fatal(err)
	}

	// The command is still answered (echo of the request byte): SET-CONTROL
	// replies are mandatory, and swallowing one hangs pyserial's open().
	cmd, payload := readCommand(t, conn)
	if cmd != rfc2217.CmdSetControl+100 {
		t.Fatalf("reply cmd = %d, want %d", cmd, rfc2217.CmdSetControl+100)
	}
	if len(payload) != 1 || payload[0] != rfc2217.ControlDTROn {
		t.Fatalf("reply payload = %v, want the echoed DTR-on", payload)
	}

	// And the failure surfaced as an error event.
	sawError := false
	for !sawError {
		select {
		case e := <-events:
			if e.Kind == "error" && strings.Contains(e.Detail, "setting modem lines") {
				sawError = true
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no error event for the failed SetModem")
		}
	}

	// The session survives: a subsequent command is answered normally.
	fp.SetModemError(nil)
	if err := rfc2217.WriteCommand(conn, rfc2217.CmdSetControl, []byte{rfc2217.ControlRTSOn}); err != nil {
		t.Fatal(err)
	}
	cmd, payload = readCommand(t, conn)
	if cmd != rfc2217.CmdSetControl+100 || len(payload) != 1 || payload[0] != rfc2217.ControlRTSOn {
		t.Fatalf("reply after recovery = (%d, %v)", cmd, payload)
	}
}

// TestApplySettingsFailureEmitsAnErrorEvent covers the other command-path
// error branch: a baud change the device rejects (device unplugged mid-open,
// or a rate it cannot clock) must reach the event stream, not vanish.
func TestApplySettingsFailureEmitsAnErrorEvent(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log:      func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := dial(t, addr)
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	fp.SetApplySettingsError(errors.New("input/output error"))
	if err := rfc2217.WriteCommand(conn, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(115200)); err != nil {
		t.Fatal(err)
	}
	readCommand(t, conn) // the reply still arrives (echo of the current baud)

	for {
		select {
		case e := <-events:
			if e.Kind == "error" && strings.Contains(e.Detail, "applying line settings") {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no error event for the failed ApplySettings")
		}
	}
}

// TestDeviceReadFailureEndsTheSessionAndDetaches covers the unplug lifecycle
// step: a device read error (EIO on unplug) must tear the session down and
// emit the detach event that releases the claim — a session that stayed
// wedged would hold the device forever.
func TestDeviceReadFailureEndsTheSessionAndDetaches(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log:      func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := dial(t, addr)
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}

	fp.SetReadError(errors.New("input/output error"))
	// The device pump sits blocked in the read that the error must fail; a
	// real unplug fails the in-flight read itself, and the fake returns the
	// scripted error only on the next call. Unblock the in-flight read with a
	// byte from the device side so the pump loops into it.
	if _, err := fp.Peer().Write([]byte("y")); err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case e := <-events:
			if e.Kind == "detach" {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("session did not detach after the device read failed")
		}
	}
}

// TestOpenFailureEmitsAnErrorEvent covers the session-open error branch: a
// device that disappears between approval and connection must surface as an
// error event, not a silently refused connection.
func TestOpenFailureEmitsAnErrorEvent(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			return nil, errors.New("no such device")
		},
		Log: func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := dial(t, addr)
	defer conn.Close()

	for {
		select {
		case e := <-events:
			if e.Kind == "error" && strings.Contains(e.Detail, "no such device") {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no error event for the failed open")
		}
	}
}

// TestCloseIsIdempotentAndListenAfterCloseFails pins two lifecycle edges:
// Close twice must not panic (the daemon calls it from several teardown
// paths), and a Listen after Close must fail with a named error rather than
// binding a listener nobody will tear down.
func TestCloseIsIdempotentAndListenAfterCloseFails(t *testing.T) {
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return serialtest.NewFakePort(t), nil },
	})
	if _, _, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, ""); err != nil {
		t.Fatalf("Listen: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := b.Close(); err != nil {
		t.Fatalf("second Close must be a no-op, got %v", err)
	}
	if _, _, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, ""); err == nil {
		t.Fatal("Listen after Close must fail")
	} else if !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Listen after Close error %q should name the closed broker", err)
	}
}

// TestRevokeClosesTheRunsSessions covers Revoke against an active session:
// the container's connection must be closed and the device released for
// another run.
func TestRevokeClosesTheRunsSessions(t *testing.T) {
	var mu sync.Mutex
	ports := []*serialtest.FakePort{}
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			mu.Lock()
			defer mu.Unlock()
			p := serialtest.NewFakePort(t)
			ports = append(ports, p)
			return p, nil
		},
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	conn := dial(t, addr)
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "session to open the device", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(ports) == 1
	})

	b.Revoke("run-a")

	// The connection the run held is closed...
	if err := conn.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Read(make([]byte, 1)); err == nil {
		t.Fatal("the revoked run's connection must be closed")
	}
	// ...and the device is free for another run.
	if _, addr2, err := b.Listen("run-b", serialbroker.Approved{Name: "esp32", Device: testDevice()}, ""); err != nil {
		t.Fatalf("Listen by another run after Revoke: %v", err)
	} else {
		_ = addr2
	}
}

// TestZeroBaudQueryAnswersTheCurrentRate covers the SET-BAUDRATE query form:
// a zero value asks for the current rate rather than setting one, and the
// reply must carry the current baud — pyserial sends the query in some
// connect paths and a reply of 0 reads as an unusable port.
func TestZeroBaudQueryAnswersTheCurrentRate(t *testing.T) {
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// Set a known rate first, so the query has something to report.
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(115200)); err != nil {
		t.Fatal(err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.CmdSetBaudRate+100 {
		t.Fatalf("set reply cmd = %d, want %d", cmd, rfc2217.CmdSetBaudRate+100)
	}
	if got := rfc2217.DecodeBaud(payload); got != 115200 {
		t.Fatalf("set reply baud = %d, want 115200", got)
	}

	// Now the query: value 0 must answer with the current rate, not 0.
	if err := rfc2217.WriteCommand(c, rfc2217.CmdSetBaudRate, rfc2217.EncodeBaud(0)); err != nil {
		t.Fatal(err)
	}
	cmd, payload = readCommand(t, c)
	if cmd != rfc2217.CmdSetBaudRate+100 {
		t.Fatalf("query reply cmd = %d, want %d", cmd, rfc2217.CmdSetBaudRate+100)
	}
	if got := rfc2217.DecodeBaud(payload); got != 115200 {
		t.Fatalf("query reply baud = %d, want the current 115200", got)
	}
}

// TestUndefinedPurgeValueIsAcknowledgedNotDropped covers the hostile-input
// branch: an undefined PURGE value still gets the echo reply, because a client
// that blocks on the ack would otherwise hang on a value it sent by mistake.
func TestUndefinedPurgeValueIsAcknowledgedNotDropped(t *testing.T) {
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := rfc2217.WriteCommand(c, rfc2217.CmdPurgeData, []byte{0x7B}); err != nil {
		t.Fatal(err)
	}
	cmd, payload := readCommand(t, c)
	if cmd != rfc2217.CmdPurgeData+100 {
		t.Fatalf("reply cmd = %d, want %d", cmd, rfc2217.CmdPurgeData+100)
	}
	if len(payload) != 1 || payload[0] != 0x7B {
		t.Fatalf("reply payload = % x, want the echoed 7b", payload)
	}
}

// TestUnsupportedWillOfferIsAnsweredDont covers the negotiation branch: a
// client offering WILL <unsupported> must get DONT — silence stalls a client
// that waits for an answer, and WILL-ing it would promise behavior the broker
// does not implement.
func TestUnsupportedWillOfferIsAnsweredDont(t *testing.T) {
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if err := c.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}

	if err := rfc2217.WriteNegotiate(c, rfc2217.Will, 0x62 /* not an option we support */); err != nil {
		t.Fatal(err)
	}
	// IAC DONT <option>: 3 bytes.
	got := readN(t, c, 3)
	if got[0] != 0xFF || got[1] != rfc2217.Dont || got[2] != 0x62 {
		t.Fatalf("reply = % x, want IAC DONT 62", got)
	}
}

// TestWontDontAreNotAnswered covers the loop hazard the source comment calls
// out: answering a WONT or DONT would make the peer answer back, and the pair
// would ping-pong forever. The broker must stay silent.
func TestWontDontAreNotAnswered(t *testing.T) {
	_, _, addr := newBroker(t)
	c := dial(t, addr)
	if err := c.SetDeadline(time.Now().Add(300 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	for _, verb := range []byte{rfc2217.Wont, rfc2217.Dont} {
		if err := rfc2217.WriteNegotiate(c, verb, rfc2217.OptionBinary); err != nil {
			t.Fatal(err)
		}
	}
	// The deadline turns into a read timeout, not a hang: any bytes here
	// would mean the broker answered a WONT/DONT.
	buf := make([]byte, 8)
	n, err := c.Read(buf)
	if n > 0 {
		t.Fatalf("broker answered a WONT/DONT with % x — that is the negotiation loop", buf[:n])
	}
	if err == nil {
		t.Fatal("read returned without bytes and without error")
	}
}

// TestConnectionAfterListenerCloseIsDropped covers the race at the end of a
// run: a client that connects between close() and the listener's socket being
// torn down must simply have its connection closed, never served a device.
func TestConnectionAfterListenerCloseIsDropped(t *testing.T) {
	var mu sync.Mutex
	opens := 0
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) {
			mu.Lock()
			defer mu.Unlock()
			opens++
			return serialtest.NewFakePort(t), nil
		},
	})
	ref, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	// Close through the refs API the run manager uses to release a run's
	// listeners, then dial the address the container was given.
	b.CloseListeners([]*serialbroker.ListenerRef{ref})
	late, derr := net.DialTimeout("tcp", addr, 2*time.Second)
	if derr == nil {
		defer late.Close()
		// The socket may still be in the kernel's teardown backlog; a
		// connection that lands must be closed, never served.
		if err := late.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
			t.Fatal(err)
		}
		if _, err := late.Read(make([]byte, 1)); err == nil {
			t.Fatal("the late connection must be closed, not served")
		}
	}
	// Either way — refused, or accepted then closed — no session may open
	// the device. Give a would-be session time to (wrongly) open the port.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if opens != 0 {
		t.Fatalf("%d sessions opened after the listener closed; the device must never be served", opens)
	}
}

// TestClientWriteFailureEndsTheSession covers the device->container pump's
// write branch: a client that stops reading (or vanishes) mid-stream must end
// the session, not wedge the device forever — the detach event releases the
// claim for the next client.
func TestClientWriteFailureEndsTheSession(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log:      func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	conn := dial(t, addr)
	// Establish the session, then break the client side mid-stream: a read
	// with a short deadline proves bytes flow first.
	if _, err := conn.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := fp.Peer().Read(buf); err != nil {
		t.Fatalf("device read: %v", err)
	}

	// Fill the client's socket until writes block: close the peer so the
	// session's conn.Write fails on the next device byte.
	conn.Close()

	// Device bytes still arrive; the pump's write fails and must end the
	// session — detach fires, which is what releases the claim.
	if _, err := fp.Peer().Write([]byte("device-bytes")); err != nil {
		t.Fatal(err)
	}
	for {
		select {
		case e := <-events:
			if e.Kind == "detach" {
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("session did not detach after the client write failed")
		}
	}
}

// TestFullRecordModeAnnouncesItselfOnAttach is the broker half of the audit
// seam: the attach event must carry Record=full, which the daemon's fan-out
// stamps into the audit entry as record_mode. An empty Record reads
// downstream as "events only" even when payload capture was configured.
func TestFullRecordModeAnnouncesItselfOnAttach(t *testing.T) {
	events := make(chan serialbroker.Event, 8)
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		OpenRecorder: func(string, string) (io.WriteCloser, error) {
			return &capture{}, nil
		},
		Log: func(e serialbroker.Event) { events <- e },
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{
		Name: "esp32", Device: testDevice(), Record: serialbroker.RecordFull,
	}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := dial(t, addr)
	defer c.Close()
	if _, err := c.Write([]byte("x")); err != nil {
		t.Fatal(err)
	}

	for {
		select {
		case e := <-events:
			if e.Kind == "attach" {
				if e.Record != serialbroker.RecordFull {
					t.Fatalf("attach Record = %q, want %q", e.Record, serialbroker.RecordFull)
				}
				return
			}
		case <-time.After(3 * time.Second):
			t.Fatal("no attach event")
		}
	}
}

// A connection holds the device from the moment it is accepted, and TCP
// keepalives only notice a peer that vanished — one that stays connected and
// says nothing is indistinguishable from a healthy idle console. Without a
// handshake budget, connecting first and staying silent denies the container
// its device for the life of the daemon.
func TestSilentPeerIsEvictedAndReleasesTheDevice(t *testing.T) {
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort:         func(string) (serialport.Port, error) { return fp, nil },
		HandshakeTimeout: 150 * time.Millisecond,
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	silent := dial(t, addr)
	// Say nothing at all. The budget must expire and free the slot.
	assertServerClosed(t, silent, "silent peer")

	// The device must be usable again — that is the point of evicting it.
	next := dial(t, addr)
	if _, err := next.Write([]byte("hello")); err != nil {
		t.Fatalf("second client could not use the released device: %v", err)
	}
}

// Companion, and the one that matters: clearing the budget on the first byte
// would let a peer send a single stray byte and then hold the device forever,
// which is the same denial with one byte of extra effort. The budget clears
// only on a completed unit, so one byte then silence is still evicted.
func TestOneStrayByteDoesNotDisarmTheHandshakeBudget(t *testing.T) {
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort:         func(string) (serialport.Port, error) { return fp, nil },
		HandshakeTimeout: 150 * time.Millisecond,
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	// A bare IAC: the start of a command, never completed.
	if _, err := c.Write([]byte{255}); err != nil {
		t.Fatalf("write: %v", err)
	}
	assertServerClosed(t, c, "one incomplete byte then silence")
}

// The companion the stray-byte test misses: a COMPLETE negotiation the server
// never answers. WONT/DONT fall into handleNegotiate's default branch, produce
// no reply and no state, and cost the peer three bytes — so if they settled the
// session, the eviction guard would be trivially disarmed by a client that then
// says nothing and holds the device until the daemon restarts.
func TestUnansweredNegotiationDoesNotDisarmTheHandshakeBudget(t *testing.T) {
	for _, tc := range []struct {
		name string
		verb byte
	}{
		{"WONT", rfc2217.Wont},
		{"DONT", rfc2217.Dont},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fp := serialtest.NewFakePort(t)
			b := serialbroker.New(serialbroker.Options{
				OpenPort:         func(string) (serialport.Port, error) { return fp, nil },
				HandshakeTimeout: 150 * time.Millisecond,
			})
			t.Cleanup(func() { b.Close() })
			_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
			if err != nil {
				t.Fatalf("Listen: %v", err)
			}

			c := dial(t, addr)
			if err := rfc2217.WriteNegotiate(c, tc.verb, rfc2217.OptionComPort); err != nil {
				t.Fatalf("WriteNegotiate: %v", err)
			}
			assertServerClosed(t, c, "a negotiation the server does not answer, then silence")
		})
	}
}

// Companion to the pair above: a negotiation the server DOES answer settles the
// session, so a real client that negotiates and then waits for the device to
// say something first is not evicted mid-handshake.
func TestAnsweredNegotiationSettlesTheSession(t *testing.T) {
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort:         func(string) (serialport.Port, error) { return fp, nil },
		HandshakeTimeout: 150 * time.Millisecond,
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	if err := rfc2217.WriteNegotiate(c, rfc2217.Will, rfc2217.OptionComPort); err != nil {
		t.Fatalf("WriteNegotiate: %v", err)
	}
	// Drain the DO reply so the settle has certainly happened, then sit well
	// past the budget.
	if err := c.SetReadDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	reply := make([]byte, 3)
	if _, err := io.ReadFull(c, reply); err != nil {
		t.Fatalf("reading the negotiation reply: %v", err)
	}
	time.Sleep(500 * time.Millisecond)

	// Observe the DEVICE side: a client-side Write lands in the local buffer
	// and succeeds even after the peer closed, so it cannot tell a live
	// session from an evicted one.
	if _, err := c.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("write after the budget: %v", err)
	}
	done := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 4)
		n, _ := io.ReadFull(fp.Peer(), buf)
		done <- buf[:n]
	}()
	select {
	case got := <-done:
		if string(got) != "AT\r\n" {
			t.Fatalf("device saw %q, want %q", got, "AT\r\n")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing reached the device: an answered negotiation did not settle the session")
	}
}

// The other companion: a real client must NOT be evicted. Payload reaching the
// device settles the session, after which a serial console may legitimately sit
// quiet far longer than the budget.
func TestActiveSessionSurvivesPastTheHandshakeBudget(t *testing.T) {
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort:         func(string) (serialport.Port, error) { return fp, nil },
		HandshakeTimeout: 150 * time.Millisecond,
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	// Observe the DEVICE side, not the client's own Write: a TCP write lands in
	// the local buffer and succeeds even after the peer has closed, so a
	// client-side error check cannot tell an evicted session from a live one.
	readFromDevice := func(what string) {
		t.Helper()
		if _, err := c.Write([]byte("AT\r\n")); err != nil {
			t.Fatalf("%s: write: %v", what, err)
		}
		done := make(chan []byte, 1)
		go func() {
			buf := make([]byte, 4)
			n, _ := io.ReadFull(fp.Peer(), buf)
			done <- buf[:n]
		}()
		select {
		case got := <-done:
			if string(got) != "AT\r\n" {
				t.Fatalf("%s: device saw %q, want %q", what, got, "AT\r\n")
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: nothing reached the device; the session was evicted", what)
		}
	}

	readFromDevice("first write settles the session")
	// Wait out several budgets, then confirm bytes still reach the device.
	time.Sleep(500 * time.Millisecond)
	readFromDevice("after the budget would have expired")
}

// assertServerClosed fails unless the peer actually closed the connection. A
// bare "Read returned an error" is not enough: our own read deadline also
// returns an error, so a test asserting err != nil passes whether the server
// evicted us or we simply stopped waiting — which silently inverts the thing
// these tests exist to prove.
func assertServerClosed(t *testing.T, c net.Conn, what string) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err := c.Read(make([]byte, 1))
	if err == nil {
		t.Fatalf("%s: connection still open; it holds the device", what)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		t.Fatalf("%s: connection was never closed (our own read timed out); the budget did not fire", what)
	}
}

// Revoke is the synchronization point for a run's device events. The daemon's
// liveness reaper revokes a dead run and then closes its stores; if Revoke
// returned while a session was still shutting down, the detach record — the
// one carrying the session's byte counts — would arrive after the sink it
// writes to was gone. Worse, that late event re-opens the run's audit store,
// leaving a second handle on a database whose chain has a sequence primary
// key.
func TestRevokeWaitsForTheDetachEvent(t *testing.T) {
	var mu sync.Mutex
	var kinds []string
	fp := serialtest.NewFakePort(t)
	b := serialbroker.New(serialbroker.Options{
		OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		Log: func(e serialbroker.Event) {
			mu.Lock()
			defer mu.Unlock()
			kinds = append(kinds, e.Kind)
		},
	})
	t.Cleanup(func() { b.Close() })
	_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	c := dial(t, addr)
	// Drive real traffic so the session is fully up before the revoke.
	if _, err := c.Write([]byte("AT\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(fp.Peer(), buf); err != nil {
		t.Fatalf("device never saw the write: %v", err)
	}

	b.Revoke("run-a")

	// Read immediately — no sleep. A Revoke that does not wait leaves the
	// detach to a goroutine that has not run yet.
	mu.Lock()
	got := append([]string(nil), kinds...)
	mu.Unlock()
	var sawDetach bool
	for _, k := range got {
		if k == "detach" {
			sawDetach = true
		}
	}
	if !sawDetach {
		t.Fatalf("Revoke returned before the detach event was emitted: %v", got)
	}
}

// A connection accepted just before the broker closes must not race the
// listener's shutdown. serve() registers each accepted connection with a
// WaitGroup that close() waits on, and a WaitGroup requires every Add from
// zero to happen-before Wait. Dial and close back to back, many times, so
// close() regularly runs before the connection's handler has touched any
// shared state — the window where nothing else orders the two. Run with -race.
func TestCloseRightAfterAcceptDoesNotRace(t *testing.T) {
	for i := 0; i < 50; i++ {
		fp := serialtest.NewFakePort(t)
		b := serialbroker.New(serialbroker.Options{
			OpenPort: func(string) (serialport.Port, error) { return fp, nil },
		})
		_, addr, err := b.Listen("run-a", serialbroker.Approved{Name: "esp32", Device: testDevice()}, "")
		if err != nil {
			t.Fatalf("Listen: %v", err)
		}
		c, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		b.Close()
		c.Close()
	}
}
