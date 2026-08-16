//go:build linux || darwin

package serialport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// readBaud reports the baud rate currently set on the port's line.
func readBaud(t *testing.T, p Port) uint32 {
	t.Helper()
	got, err := p.(*tty).currentSettings()
	if err != nil {
		t.Fatalf("reading settings: %v", err)
	}
	return got.Baud
}
