package sandbox

import (
	"reflect"
	"testing"
)

func TestBuildPolicyDefaults(t *testing.T) {
	p := BuildPolicy(nil, nil)
	want := []string{"/dev", "/proc", "/run", "/tmp", "/var/tmp"}
	if !reflect.DeepEqual(p.AllowWrite, want) {
		t.Errorf("AllowWrite = %v, want %v", p.AllowWrite, want)
	}
}

func TestBuildPolicyIncludesMountsAndExtras(t *testing.T) {
	p := BuildPolicy(
		[]string{"/workspace", "/var/run/docker.sock"},
		[]string{"/data"},
	)
	for _, path := range []string{"/workspace", "/var/run/docker.sock", "/data", "/tmp"} {
		if !contains(p.AllowWrite, path) {
			t.Errorf("AllowWrite = %v, missing %q", p.AllowWrite, path)
		}
	}
}

func TestBuildPolicyExcludesNothingItWasNotGiven(t *testing.T) {
	// Companion to the inclusion test: the caller filters read-only mounts,
	// so a policy built without them must not contain them. This pins the
	// contract that BuildPolicy adds no mount paths on its own.
	p := BuildPolicy(nil, nil)
	for _, path := range []string{"/workspace", "/", "/etc", "/home"} {
		if contains(p.AllowWrite, path) {
			t.Errorf("AllowWrite = %v, unexpectedly contains %q", p.AllowWrite, path)
		}
	}
}

func TestBuildPolicyCleansAndDedupes(t *testing.T) {
	p := BuildPolicy([]string{"/workspace/", "/workspace", "/tmp"}, []string{"/data/../data"})
	count := 0
	for _, path := range p.AllowWrite {
		if path == "/workspace" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("AllowWrite = %v, want exactly one /workspace entry", p.AllowWrite)
	}
	if contains(p.AllowWrite, "/data/../data") || !contains(p.AllowWrite, "/data") {
		t.Errorf("AllowWrite = %v, want cleaned /data", p.AllowWrite)
	}
	// Empty strings are dropped, not turned into ".".
	p = BuildPolicy([]string{""}, nil)
	if contains(p.AllowWrite, ".") || contains(p.AllowWrite, "") {
		t.Errorf("AllowWrite = %v, empty input should be dropped", p.AllowWrite)
	}
}

func TestBuildPolicyDeterministic(t *testing.T) {
	a := BuildPolicy([]string{"/b", "/a"}, []string{"/c"})
	b := BuildPolicy([]string{"/a", "/b"}, []string{"/c"})
	if !reflect.DeepEqual(a, b) {
		t.Errorf("policies differ by input order: %v vs %v", a, b)
	}
}

func TestPolicyEncodeParseRoundTrip(t *testing.T) {
	p := BuildPolicy([]string{"/workspace"}, []string{"/data"})
	s, err := p.Encode()
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	got, err := ParsePolicy(s)
	if err != nil {
		t.Fatalf("ParsePolicy: %v", err)
	}
	if !reflect.DeepEqual(got, p) {
		t.Errorf("round trip = %+v, want %+v", got, p)
	}
}

func TestParsePolicyRejectsGarbage(t *testing.T) {
	// Companion to the round-trip test: malformed transport must error, not
	// yield an empty (allow-nothing... or worse, misparsed) policy silently.
	if _, err := ParsePolicy("not json"); err == nil {
		t.Error("ParsePolicy(garbage) succeeded, want error")
	}
	if _, err := ParsePolicy(""); err == nil {
		t.Error("ParsePolicy(empty) succeeded, want error")
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
