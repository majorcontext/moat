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

func TestIdentityCarriesApprovalFields(t *testing.T) {
	d := Device{Path: "/dev/ttyUSB0", VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	got := d.Identity()
	want := Identity{VID: "303a", PID: "1001", Serial: "AAA", PortPath: "1-2"}
	if got != want {
		t.Fatalf("Identity() = %+v, want %+v", got, want)
	}
}
