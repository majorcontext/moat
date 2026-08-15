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
