package serialdev

import (
	"errors"
	"testing"
)

func devs() []Device {
	return []Device{
		{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"},
		{Path: "/dev/ttyUSB1", VID: "303a", PID: "1001", Serial: "BBB", PortPath: "1-3"},
		{Path: "/dev/ttyACM0", VID: "2341", PID: "0043", Serial: "CCC", PortPath: "1-4"},
	}
}

func TestMatchSingle(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "2341", PID: "0043"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].Serial != "CCC" {
		t.Fatalf("got %+v, want the 2341:0043 device", got)
	}
}

func TestMatchAmbiguousReturnsAllCandidates(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "303a", PID: "1001"})
	if !errors.Is(err, ErrAmbiguous) {
		t.Fatalf("err = %v, want ErrAmbiguous", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d candidates, want 2 so the caller can list them", len(got))
	}
}

func TestMatchNone(t *testing.T) {
	if _, err := Match(devs(), Matcher{VID: "dead", PID: "beef"}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch", err)
	}
}

func TestMatchIsCaseInsensitiveOnHexIDs(t *testing.T) {
	got, err := Match(devs(), Matcher{VID: "303A", PID: "1001"})
	if !errors.Is(err, ErrAmbiguous) || len(got) != 2 {
		t.Fatalf("uppercase VID should match lowercase: got %+v err %v", got, err)
	}
}

func TestMatchEmptyDeviceListIsNoMatch(t *testing.T) {
	if _, err := Match(nil, Matcher{VID: "303a", PID: "1001"}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch for an empty device list", err)
	}
}

func TestMatchInterfaceSelectsOneUARTOfABridge(t *testing.T) {
	// An FT2232H-style bridge: one USB device, two ttys, same IDs and serial.
	bridge := []Device{
		{Path: "/dev/ttyUSB0", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0"},
		{Path: "/dev/ttyUSB1", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "1"},
	}
	got, err := Match(bridge, Matcher{VID: "0403", PID: "6010", Interface: "1"})
	if err != nil {
		t.Fatalf("Match: %v", err)
	}
	if len(got) != 1 || got[0].Path != "/dev/ttyUSB1" {
		t.Fatalf("got %+v, want only the interface-1 port", got)
	}
}

func TestMatchWithoutInterfaceStaysAmbiguousOnABridge(t *testing.T) {
	bridge := []Device{
		{Path: "/dev/ttyUSB0", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0"},
		{Path: "/dev/ttyUSB1", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "1"},
	}
	got, err := Match(bridge, Matcher{VID: "0403", PID: "6010"})
	if !errors.Is(err, ErrAmbiguous) || len(got) != 2 {
		t.Fatalf("no selector should not silently pick a port: got %+v err %v", got, err)
	}
}

func TestMatchInterfaceWithoutThatInterfaceIsNoMatch(t *testing.T) {
	// Companion to the selector test: asking for an interface the device does
	// not expose must fail as a no-match, not fall back to matching any.
	bridge := []Device{
		{Path: "/dev/ttyUSB0", VID: "0403", PID: "6010", Serial: "FT7ABCDE", PortPath: "1-2", Interface: "0"},
	}
	if _, err := Match(bridge, Matcher{VID: "0403", PID: "6010", Interface: "1"}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch when the bridge has no such interface", err)
	}
}

func TestMatchInterfaceSelectsNothingOnDevicesWithoutOne(t *testing.T) {
	// Single-UART devices enumerated before the interface field, or on a
	// platform that cannot see it: a stray selector must not match them.
	legacy := []Device{
		{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"},
	}
	if _, err := Match(legacy, Matcher{VID: "303a", PID: "1001", Interface: "1"}); !errors.Is(err, ErrNoMatch) {
		t.Fatalf("err = %v, want ErrNoMatch against a device with no interface number", err)
	}
}

func TestIdentityCarriesApprovalFields(t *testing.T) {
	d := Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	got := d.Identity()
	want := Identity{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	if got != want {
		t.Fatalf("Identity() = %+v, want %+v", got, want)
	}
}
