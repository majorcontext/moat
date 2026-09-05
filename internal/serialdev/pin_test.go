package serialdev

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinVerifyMatchingSerialSucceeds(t *testing.T) {
	d := Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	p := PinFor("esp32", d)
	// Same device, different physical port: serial identity wins.
	moved := Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-9"}
	if err := p.Verify(moved); err != nil {
		t.Fatalf("replugged device with same serial should verify: %v", err)
	}
}

func TestPinVerifyDifferentSerialFails(t *testing.T) {
	p := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})
	swapped := Device{VID: "303a", PID: "1001", Serial: "BBB", PortPath: "1-2"}
	err := p.Verify(swapped)
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
	// The message must name both serials — it is the user's only clue.
	got := err.Error()
	if !strings.Contains(got, "AAA") || !strings.Contains(got, "BBB") {
		t.Fatalf("error %q must name both the pinned and observed serial", got)
	}
	if !strings.Contains(got, "moat device forget esp32") {
		t.Fatalf("error %q must tell the user how to re-approve", got)
	}
}

func TestPinVerifyReportsMissingSerialReadably(t *testing.T) {
	// A device that previously had a serial but now reports none must not
	// produce a message ending in a dangling empty string.
	p := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA"})
	err := p.Verify(Device{VID: "303a", PID: "1001", Serial: ""})
	if !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
	if !strings.Contains(err.Error(), "(none)") {
		t.Fatalf("error %q should render an absent serial as (none)", err)
	}
}

func TestPinVerifyNoSerialFallsBackToPortPath(t *testing.T) {
	d := Device{VID: "1a86", PID: "7523", Serial: "", PortPath: "1-2"}
	p := PinFor("ch340", d)
	if p.Serial != "" {
		t.Fatal("pin should not invent a serial")
	}
	if !p.PinnedByPortPath() {
		t.Fatal("a serial-less pin must report that it is pinned by port path")
	}
	if err := p.Verify(d); err != nil {
		t.Fatalf("same port should verify: %v", err)
	}
}

func TestPinVerifyNoSerialDifferentPortFails(t *testing.T) {
	p := PinFor("ch340", Device{VID: "1a86", PID: "7523", PortPath: "1-2"})
	moved := Device{VID: "1a86", PID: "7523", PortPath: "1-7"}
	if err := p.Verify(moved); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("serial-less device moved to another port must fail: %v", err)
	}
}

func TestPinVerifyDifferentVIDPIDFails(t *testing.T) {
	p := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA"})
	if err := p.Verify(Device{VID: "2341", PID: "0043", Serial: "AAA"}); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("different vid/pid must fail even with a matching serial: %v", err)
	}
}

func TestPinForNormalizesHexIDs(t *testing.T) {
	p := PinFor("esp32", Device{VID: "303A", PID: "100B", Serial: "AAA"})
	if p.VID != "303a" || p.PID != "100b" {
		t.Fatalf("pin stored %s:%s, want lowercase 303a:100b", p.VID, p.PID)
	}
	// And a device enumerated in the other case still verifies.
	if err := p.Verify(Device{VID: "303a", PID: "100b", Serial: "AAA"}); err != nil {
		t.Fatalf("case should not affect verification: %v", err)
	}
}

func TestPinStoreRoundTripAndForget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	s, err := OpenPinStore(path)
	if err != nil {
		t.Fatalf("OpenPinStore: %v", err)
	}
	if _, ok, err := s.Get("esp32"); err != nil || ok {
		t.Fatalf("empty store: ok=%v err=%v, want ok=false", ok, err)
	}
	want := PinFor("esp32", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})
	if err := s.Put(want); err != nil {
		t.Fatalf("Put: %v", err)
	}

	reopened, err := OpenPinStore(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	got, ok, err := reopened.Get("esp32")
	if err != nil || !ok {
		t.Fatalf("Get after reopen: ok=%v err=%v", ok, err)
	}
	if got.Serial != "AAA" || got.VID != "303a" {
		t.Fatalf("got %+v, want the pin written before reopen", got)
	}
	if got.FirstSeen.IsZero() {
		t.Fatal("FirstSeen must survive the round trip")
	}

	if err := reopened.Forget("esp32"); err != nil {
		t.Fatalf("Forget: %v", err)
	}
	if _, ok, _ := reopened.Get("esp32"); ok {
		t.Fatal("pin still present after Forget")
	}
}

func TestPinStoreForgetIsIdempotent(t *testing.T) {
	s, err := OpenPinStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatalf("OpenPinStore: %v", err)
	}
	if err := s.Forget("never-pinned"); err != nil {
		t.Fatalf("forgetting an unknown name should not error: %v", err)
	}
}

func TestPinStoreListReturnsAllPins(t *testing.T) {
	s, err := OpenPinStore(filepath.Join(t.TempDir(), "devices.json"))
	if err != nil {
		t.Fatalf("OpenPinStore: %v", err)
	}
	if err := s.Put(PinFor("a", Device{VID: "303a", PID: "1001", Serial: "AAA"})); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(PinFor("b", Device{VID: "1a86", PID: "7523", PortPath: "1-2"})); err != nil {
		t.Fatal(err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d pins, want 2", len(got))
	}
}

func TestOpenPinStoreOnCorruptFileExplainsTheFix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := OpenPinStore(path)
	if err == nil {
		t.Fatal("corrupt pin file should be an error, not silently ignored")
	}
	if !strings.Contains(err.Error(), path) {
		t.Fatalf("error %q must name the offending file", err)
	}
}

func TestOpenPinStoreOnEmptyFileIsEmptyNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "devices.json")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := OpenPinStore(path)
	if err != nil {
		t.Fatalf("empty file should open cleanly: %v", err)
	}
	got, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List returned %d pins, want 0", len(got))
	}
}

func TestPinInterfaceDistinguishesDualUARTPorts(t *testing.T) {
	// One bridge, two UARTs: the same VID/PID/serial/port, different ttys.
	// Each port pins independently under its own config name.
	portA := Device{VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0", Path: "/dev/ttyUSB0"}
	portB := Device{VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "1", Path: "/dev/ttyUSB1"}

	pinA := PinFor("jtag", portA)
	pinB := PinFor("uart", portB)
	if err := pinA.Verify(portA); err != nil {
		t.Fatalf("port A must verify against its own pin: %v", err)
	}
	if err := pinB.Verify(portB); err != nil {
		t.Fatalf("port B must verify against its own pin: %v", err)
	}
	// The cross checks fail: a config pinned to one UART must not silently
	// land on the other after a device forget.
	if err := pinA.Verify(portB); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("pin A vs port B: err = %v, want ErrPinMismatch", err)
	}
	if err := pinB.Verify(portA); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("pin B vs port A: err = %v, want ErrPinMismatch", err)
	}
}

func TestPinWithoutInterfaceStillVerifiesAnyInterface(t *testing.T) {
	// Companion: pins written before the interface field existed have no
	// value. They must keep verifying the hardware they were created against
	// — a CDC modem's single tty lives on interface 1, so demanding "0"
	// would break existing pins for hardware the user already approved.
	old := PinFor("board", Device{VID: "12346", PID: "4097", Serial: "AAA", PortPath: "0x08320000"})
	old.Interface = "" // as written by an older binary

	modemOnIface1 := Device{VID: "12346", PID: "4097", Serial: "AAA", PortPath: "0x08320000", Interface: "1"}
	if err := old.Verify(modemOnIface1); err != nil {
		t.Fatalf("pre-discriminator pin must keep verifying: %v", err)
	}
	singleOnIface0 := Device{VID: "12346", PID: "4097", Serial: "AAA", PortPath: "0x08320000", Interface: "0"}
	if err := old.Verify(singleOnIface0); err != nil {
		t.Fatalf("pre-discriminator pin must keep verifying: %v", err)
	}
}

func TestPinWithoutSerialOrPortFailsClosed(t *testing.T) {
	// A pin with neither identity approves any device with the right USB ID:
	// the port comparison sees "" != "" as a match. Such a pin must fail
	// verification instead — the device it was meant to name cannot be told
	// apart from a swapped one, so no device is approved.
	pin := Pin{Name: "board", VID: "1a86", PID: "7523"} // no Serial, no PortPath

	sameModel := Device{VID: "1a86", PID: "7523", PortPath: "1-3"}
	if err := pin.Verify(sameModel); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch — the pin pins nothing and must not pass", err)
	}
	otherPort := Device{VID: "1a86", PID: "7523", PortPath: "1-4"}
	if err := pin.Verify(otherPort); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("err = %v, want ErrPinMismatch", err)
	}
}

func TestSerialLessPinWithPortStillVerifies(t *testing.T) {
	// Companion: the port-pinned path itself must keep working — a clone
	// with no serial number, pinned by port, verifies on that port and
	// fails on another.
	pin := PinFor("dongle", Device{VID: "1a86", PID: "7523", PortPath: "1-3"})
	if err := pin.Verify(Device{VID: "1a86", PID: "7523", PortPath: "1-3"}); err != nil {
		t.Fatalf("same port must verify: %v", err)
	}
	if err := pin.Verify(Device{VID: "1a86", PID: "7523", PortPath: "1-4"}); !errors.Is(err, ErrPinMismatch) {
		t.Fatalf("different port must fail: %v", err)
	}
}

func TestPinStoreConcurrentProcessesBothSurvive(t *testing.T) {
	// Two stores on one path model two moat processes (a CLI run and a
	// re-registration, say). Without the cross-process lock each Put writes
	// its own map back and the second drops the first's pin — silently
	// re-arming trust-on-first-use for the lost device.
	path := filepath.Join(t.TempDir(), "devices.json")
	a, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck
	b, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close() //nolint:errcheck

	if err := a.Put(PinFor("board-a", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(PinFor("board-b", Device{VID: "1a86", PID: "7523", Serial: "BBB", PortPath: "1-3"})); err != nil {
		t.Fatal(err)
	}

	// A fresh store reads what both writers left — the loser's pin must not
	// have been dropped.
	c, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck
	for _, name := range []string{"board-a", "board-b"} {
		if _, ok, _ := c.Get(name); !ok {
			t.Fatalf("pin %s was lost — the other process's write dropped it", name)
		}
	}
}

func TestPinStorePutSeesAnotherProcesssWrite(t *testing.T) {
	// Companion: a store held open across another process's Put must not
	// write a stale snapshot back over it — reload-under-lock is the other
	// half of the flock fix.
	path := filepath.Join(t.TempDir(), "devices.json")
	a, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close() //nolint:errcheck
	b, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close() //nolint:errcheck

	if err := b.Put(PinFor("board-b", Device{VID: "1a86", PID: "7523", Serial: "BBB", PortPath: "1-3"})); err != nil {
		t.Fatal(err)
	}
	// a was opened before b wrote; a's next write must carry b's pin forward.
	if err := a.Put(PinFor("board-a", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})); err != nil {
		t.Fatal(err)
	}

	c, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close() //nolint:errcheck
	if _, ok, _ := c.Get("board-b"); !ok {
		t.Fatalf("a's stale snapshot overwrote b's pin")
	}
	if _, ok, _ := c.Get("board-a"); !ok {
		t.Fatalf("a's own pin missing after its Put")
	}
}

func TestPinStoreTempFileIsNotASymlinkAndKeepsItsMode(t *testing.T) {
	// A pre-symlinked devices.json.tmp must not be followed to its victim,
	// and the resulting devices.json must be 0600 — the pin file names
	// hardware approval; group/world read has no audience.
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")

	victim := filepath.Join(dir, "victim.txt")
	if err := os.WriteFile(victim, []byte("do not touch"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path+".tmp"); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	s, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck
	// The planted symlink is discarded (os.Remove unlinks it without
	// following), so the write succeeds against a fresh regular file.
	if err := s.Put(PinFor("board", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})); err != nil {
		t.Fatalf("Put over a symlinked temp file: %v", err)
	}

	// The victim must be untouched — the symlink was never followed.
	got, rerr := os.ReadFile(victim)
	if rerr != nil || string(got) != "do not touch" {
		t.Fatalf("victim file was overwritten through the symlink: %q", got)
	}

	// devices.json is a real 0600 file, and the pin round-trips.
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat pins: %v", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		t.Fatal("pin file is a symlink")
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("pin file mode = %o, want 600", perm)
	}
	if _, ok, gerr := s.Get("board"); gerr != nil || !ok {
		t.Fatalf("pin was not recorded: ok=%v err=%v", ok, gerr)
	}
}

func TestPinStoreRecoversFromStaleTempFile(t *testing.T) {
	// A .tmp left behind by a crash between create and rename must not wedge
	// every future write. Before the fix, O_EXCL turned the leftover into a
	// permanent "file exists" failure for Put — and for Forget, the command a
	// pin mismatch tells the user to run.
	dir := t.TempDir()
	path := filepath.Join(dir, "devices.json")
	if err := os.WriteFile(path+".tmp", []byte("junk from a crashed run"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := OpenPinStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck
	if err := s.Put(PinFor("board", Device{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"})); err != nil {
		t.Fatalf("Put with a stale temp file present: %v", err)
	}
	if _, ok, gerr := s.Get("board"); gerr != nil || !ok {
		t.Fatalf("pin missing after recovering from a stale temp file: ok=%v err=%v", ok, gerr)
	}
	// Companion: Forget — the documented remedy — must work too.
	if err := s.Forget("board"); err != nil {
		t.Fatalf("Forget with a stale temp file present: %v", err)
	}
}
