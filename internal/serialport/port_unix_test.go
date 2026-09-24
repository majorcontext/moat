//go:build linux || darwin

package serialport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/creack/pty"
)

// openPTY returns the path of a fresh pty slave plus its master. The slave is a
// real tty, so it exercises the same code path Open takes for a USB serial
// device; the master stands in for the device at the other end of the wire.
//
// Nothing else may open the slave once Open has it: Open takes an exclusive
// claim (TIOCEXCL), so a second open fails with EBUSY. That is the behavior we
// want in production, which is why tests reach the far end via the master.
func openPTY(t *testing.T) (string, *os.File) {
	t.Helper()
	master, slave, err := pty.Open()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	path := slave.Name()
	// Keep only the path; Open reopens it and takes the exclusive claim.
	if err := slave.Close(); err != nil {
		t.Fatalf("closing slave: %v", err)
	}
	t.Cleanup(func() { master.Close() })
	return path, master
}

func TestOpenAcceptsATTY(t *testing.T) {
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open on a tty: %v", err)
	}
	defer p.Close()
	if p.Name() == "" {
		t.Fatal("Name() must report the device path")
	}
}

func TestOpenRejectsNonTTY(t *testing.T) {
	// The tty check is the device-class allowlist: storage and HID devices are
	// excluded because they are not ttys, so this rejection is load-bearing.
	path := filepath.Join(t.TempDir(), "regular-file")
	if err := os.WriteFile(path, []byte("not a device"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Open(path)
	if err == nil {
		t.Fatal("Open on a regular file must fail")
	}
	if !strings.Contains(err.Error(), "not a serial device") {
		t.Fatalf("error %q should explain that only tty character devices are exposed", err)
	}
}

func TestOpenRejectsMissingPath(t *testing.T) {
	_, err := Open(filepath.Join(t.TempDir(), "nonexistent"))
	if err == nil {
		t.Fatal("Open on a missing path must fail")
	}
	if !strings.Contains(err.Error(), "opening serial device") {
		t.Fatalf("error %q should name the operation that failed", err)
	}
}

func TestOpenRejectsDirectory(t *testing.T) {
	_, err := Open(t.TempDir())
	if err == nil {
		t.Fatal("Open on a directory must fail")
	}
}

func TestApplySettingsSetsBaud(t *testing.T) {
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if err := p.ApplySettings(Settings{Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	// Read the setting back through a second fd to confirm it reached the line.
	if got := readBaud(t, p); got != 115200 {
		t.Fatalf("baud on the line is %d, want 115200", got)
	}
}

func TestApplySettingsLeavesZeroFieldsAlone(t *testing.T) {
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if err := p.ApplySettings(Settings{Baud: 115200, DataBits: 8, StopBits: 1}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	// A settings frame carrying only flow control must not reset the baud rate.
	if err := p.ApplySettings(Settings{FlowControl: FlowNone}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	if got := readBaud(t, p); got != 115200 {
		t.Fatalf("baud is %d after a partial update, want 115200 preserved", got)
	}
}

func TestApplySettingsAcceptsCommonBaudRates(t *testing.T) {
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()
	// 115200 is the ESP32 boot console; 921600 is what esptool prefers to flash.
	for _, baud := range []uint32{9600, 115200, 921600} {
		if err := p.ApplySettings(Settings{Baud: baud}); err != nil {
			t.Fatalf("ApplySettings(%d): %v", baud, err)
		}
	}
}

func TestWriteAndReadPassThrough(t *testing.T) {
	path, peer := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if _, err := p.Write([]byte("hello")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, 32)
	n, err := peer.Read(buf)
	if err != nil {
		t.Fatalf("peer Read: %v", err)
	}
	if string(buf[:n]) != "hello" {
		t.Fatalf("peer got %q, want hello", buf[:n])
	}
}

func TestFlushBuffersDiscardsPendingInput(t *testing.T) {
	// RFC2217 PURGE_DATA maps to this flush, and pyserial blocks its entire
	// connect sequence on it — a flush that errors (rather than clearing) fails
	// every pyserial connect. This test exercises the real ioctl against a real
	// tty, which is where a wrong argument-passing convention shows up: Darwin's
	// TIOCFLUSH expects the queue selector by pointer (_IOW), so passing it by
	// value gives EFAULT there even though the Linux form passes both ways here.
	//
	// The stale-bytes assertion lives in the broker tests (pty queue topology
	// differs by platform, so which side's queue holds the bytes is not portable
	// to assert here); what this pins down is that the ioctl itself succeeds
	// with the correct argument convention on each platform.
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if err := p.FlushBuffers(); err != nil {
		t.Fatalf("FlushBuffers: %v", err)
	}
	// Idempotent: a second flush of an already-empty queue is fine.
	if err := p.FlushBuffers(); err != nil {
		t.Fatalf("second FlushBuffers: %v", err)
	}
}

func TestCloseUnblocksPendingRead(t *testing.T) {
	// The broker tears sessions down by closing the port while a read is in
	// flight; if that blocked forever, Revoke would hang.
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 1)
		_, _ = p.Read(buf)
	}()
	// Give the goroutine a moment to block in Read before closing.
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	<-done
}

func TestSetModemClearsBeforeItSets(t *testing.T) {
	// A pty has no modem lines, so SetModem's TIOCMBIC/TIOCMBIS fail (ENOTTY
	// on Linux and macOS). The ioctl argument convention itself cannot be
	// exercised against a pty — that is the hardware e2e's job
	// (MOAT_SERIAL_TEST_DEVICE: esptool's reset sequence asserts and releases
	// DTR/RTS through this method, and a wrong argument form fails there the
	// way the darwin TIOCFLUSH bug did). What the pty pins here is the branch
	// structure: clears run before sets, so a transition never momentarily
	// asserts both lines — the ESP32 reset sequence depends on the exact
	// transitions — and a failure names the ioctl that failed and the port.
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	// Set-only frame: no clear to run, so the SET ioctl's error surfaces.
	err = p.SetModem(Modem{DTR: true, RTS: true})
	if err == nil {
		// This platform's pty accepts modem-line ioctls; the order the test
		// asserts is only observable when the ioctls can fail.
		t.Skipf("pty on this platform accepts modem-line ioctls; branch order not observable")
	}
	if !strings.Contains(err.Error(), "setting modem lines on "+path) {
		t.Fatalf("SetModem(DTR,RTS on): error %q should name the set ioctl and the port", err)
	}

	// Clear-only frame: only the CLEAR ioctl runs.
	err = p.SetModem(Modem{DTR: false, RTS: false})
	if err == nil || !strings.Contains(err.Error(), "clearing modem lines on "+path) {
		t.Fatalf("SetModem(DTR,RTS off): error %v should name the clear ioctl and the port", err)
	}

	// Mixed frame: the clear fires first — the error is the CLEAR ioctl's,
	// not the set's.
	err = p.SetModem(Modem{DTR: true, RTS: false})
	if err == nil || !strings.Contains(err.Error(), "clearing modem lines on "+path) {
		t.Fatalf("SetModem(DTR on, RTS off): error %v should surface the clear (which runs first), not the set", err)
	}
}

func TestModemStatusReportsAFailureToReadTheLines(t *testing.T) {
	// A pty has no modem lines, so TIOCMGET fails; the broker answers an
	// RFC2217 modem-state poll with zero in that case (covered in the broker
	// tests). This pins that the port reports the failure as an error naming
	// the port, rather than as a nil error with an all-low status that would
	// read as "carrier and CTS both dropped" to a client.
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	_, err = p.ModemStatus()
	if err == nil {
		t.Skipf("pty on this platform answers modem-line reads; failure path not observable")
	}
	if !strings.Contains(err.Error(), "reading modem lines on "+path) {
		t.Fatalf("ModemStatus(): error %q should name the port", err)
	}
}

func TestSendBreakOnATTY(t *testing.T) {
	// The real ioctl, against a real tty — the same shape as the flush test
	// below. Linux's TCSBRK drains then breaks; on a pty it returns
	// immediately. Darwin has no single-ioctl break, so sendBreak asserts
	// TIOCSBRK, holds, and clears with TIOCCBRK. What must hold on both: the
	// call completes (no hang — a pty has nothing to drain), and the ioctl
	// form is accepted, which is where a wrong argument convention would
	// surface as EFAULT.
	path, _ := openPTY(t)
	p, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer p.Close()

	if err := p.SendBreak(); err != nil {
		t.Fatalf("SendBreak on %s: %v", path, err)
	}
	// Idempotent: a second break is fine.
	if err := p.SendBreak(); err != nil {
		t.Fatalf("second SendBreak: %v", err)
	}
}

// readBaud reports the baud rate currently set on the port's line.
func readBaud(t *testing.T, p Port) uint32 {
	t.Helper()
	got, err := p.(*tty).currentSettings()
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	return got.Baud
}

// applyFraming hand-computes PARENB/PARODD/CSTOPB/CS5..CS8/CRTSCTS, and until
// now nothing read those bits back: currentSettings decoded only Baud, and the
// broker-level tests use serialtest.FakePort, whose ApplySettings just records
// the struct. An odd/even transposition or a wrong CSIZE mask would pass the
// whole suite and surface only as corruption on real hardware.
//
// The coverage is split deliberately. A pty cannot model character size or
// parity — it is 8-bit clean, so CSIZE and PARENB are normalized away and a
// round-trip through one reports CS8/no-parity no matter what was written.
// Asserting those against a pty would be a test that passes against a value the
// system cannot produce, which is the exact failure this branch has shipped
// twice. So the bits a pty does honor are checked through a real tty, and the
// bits it erases are checked directly on the termios applyFraming produces.
func TestApplyFramingSetsTermiosBits(t *testing.T) {
	bit := func(s Settings) unix.Termios {
		var tio unix.Termios
		applyFraming(&tio, s)
		return tio
	}

	t.Run("data bits select the CSIZE value", func(t *testing.T) {
		// uint64 on both sides: Termios.Cflag is uint32 on Linux and uint64 on
		// Darwin, so a width-specific comparison compiles on one and not the
		// other — and `go vet` on Linux never sees the Darwin failure.
		for bits, want := range map[uint8]uint64{5: unix.CS5, 6: unix.CS6, 7: unix.CS7, 8: unix.CS8} {
			tio := bit(Settings{DataBits: bits})
			if got := uint64(tio.Cflag & unix.CSIZE); got != want {
				t.Errorf("DataBits %d -> CSIZE %#x, want %#x", bits, got, want)
			}
		}
	})

	t.Run("odd and even parity are not transposed", func(t *testing.T) {
		odd := bit(Settings{Parity: ParityOdd})
		if odd.Cflag&unix.PARENB == 0 || odd.Cflag&unix.PARODD == 0 {
			t.Errorf("ParityOdd -> Cflag %#x, want PARENB|PARODD set", odd.Cflag)
		}
		even := bit(Settings{Parity: ParityEven})
		if even.Cflag&unix.PARENB == 0 {
			t.Errorf("ParityEven -> Cflag %#x, want PARENB set", even.Cflag)
		}
		if even.Cflag&unix.PARODD != 0 {
			t.Errorf("ParityEven -> Cflag %#x, want PARODD clear", even.Cflag)
		}
		none := bit(Settings{Parity: ParityNone})
		if none.Cflag&(unix.PARENB|unix.PARODD) != 0 {
			t.Errorf("ParityNone -> Cflag %#x, want PARENB and PARODD clear", none.Cflag)
		}
	})

	t.Run("switching away from a setting clears its bits", func(t *testing.T) {
		// Cflag is read-modify-write from the live termios, so a stale PARODD
		// or CSTOPB left behind by a previous session would silently ride along.
		tio := bit(Settings{Parity: ParityOdd, StopBits: 2, FlowControl: FlowRTSCTS})
		applyFraming(&tio, Settings{Parity: ParityNone, StopBits: 1, FlowControl: FlowNone})
		if tio.Cflag&(unix.PARENB|unix.PARODD) != 0 {
			t.Errorf("parity bits survived a switch to ParityNone: Cflag %#x", tio.Cflag)
		}
		if tio.Cflag&unix.CSTOPB != 0 {
			t.Errorf("CSTOPB survived a switch to one stop bit: Cflag %#x", tio.Cflag)
		}
		if tio.Cflag&unix.CRTSCTS != 0 {
			t.Errorf("CRTSCTS survived a switch to FlowNone: Cflag %#x", tio.Cflag)
		}
	})
}

// The companion to the direct-bit test above: the settings a pty does honor
// must survive the whole Open -> ApplySettings -> ioctl path, not just the
// in-memory struct.
func TestApplySettingsRoundTripsWhatAPtyHonors(t *testing.T) {
	cases := []struct {
		name string
		in   Settings
	}{
		{"one stop bit, no flow", Settings{Baud: 115200, DataBits: 8, StopBits: 1, FlowControl: FlowNone}},
		{"two stop bits", Settings{Baud: 19200, DataBits: 8, StopBits: 2, FlowControl: FlowNone}},
		{"hardware flow control", Settings{Baud: 115200, DataBits: 8, StopBits: 1, FlowControl: FlowRTSCTS}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := openPTY(t)
			p, err := Open(path)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			defer p.Close()

			if err := p.ApplySettings(tc.in); err != nil {
				t.Fatalf("ApplySettings(%+v): %v", tc.in, err)
			}
			got, err := p.(*tty).currentSettings()
			if err != nil {
				t.Fatalf("currentSettings: %v", err)
			}
			if got.StopBits != tc.in.StopBits {
				t.Errorf("StopBits on the line = %d, want %d", got.StopBits, tc.in.StopBits)
			}
			if got.FlowControl != tc.in.FlowControl {
				t.Errorf("FlowControl on the line = %d, want %d", got.FlowControl, tc.in.FlowControl)
			}
			if got.Baud != tc.in.Baud {
				t.Errorf("Baud on the line = %d, want %d", got.Baud, tc.in.Baud)
			}
		})
	}
}
