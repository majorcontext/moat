package config

import (
	"strings"
	"testing"
)

func TestWorkspaceModeValidate(t *testing.T) {
	tests := []struct {
		name    string
		mode    WorkspaceMode
		wantErr bool
	}{
		{"empty defaults ok", "", false},
		{"bind", "bind", false},
		{"volume", "volume", false},
		{"invalid", "vol", true},
		{"garbage", "host", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wc := WorkspaceConfig{Mode: tt.mode}
			err := wc.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) err=%v wantErr=%v", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestResolveWorkspaceMode(t *testing.T) {
	cases := []struct {
		yaml     WorkspaceMode
		override string
		want     WorkspaceMode
	}{
		{"", "", WorkspaceModeBind},
		{"volume", "", WorkspaceModeVolume},
		{"bind", "volume", WorkspaceModeVolume},
		{"volume", "bind", WorkspaceModeBind},
		{"", "bind", WorkspaceModeBind},
	}
	for _, c := range cases {
		got, err := ResolveWorkspaceMode(WorkspaceConfig{Mode: c.yaml}, c.override)
		if err != nil {
			t.Fatalf("ResolveWorkspaceMode(%q,%q) unexpected err: %v", c.yaml, c.override, err)
		}
		if got != c.want {
			t.Fatalf("ResolveWorkspaceMode(%q,%q)=%q want %q", c.yaml, c.override, got, c.want)
		}
	}
}

func TestResolveWorkspaceModeInvalidOverride(t *testing.T) {
	_, err := ResolveWorkspaceMode(WorkspaceConfig{}, "vol")
	if err == nil {
		t.Fatal("expected error for invalid override")
	}
	if !strings.Contains(err.Error(), "vol") {
		t.Errorf("error should mention offending value 'vol', got: %v", err)
	}
}

func TestResolveWorkspaceModeInvalidYaml(t *testing.T) {
	// Bad yaml mode with no CLI override must also error (the doc promises
	// ResolveWorkspaceMode validates w.Mode, so it's safe to call without Load).
	if _, err := ResolveWorkspaceMode(WorkspaceConfig{Mode: "vol"}, ""); err == nil {
		t.Fatal("expected error for invalid yaml workspace mode")
	}
}

func TestIsVolumeMode(t *testing.T) {
	if !IsVolumeMode("volume") {
		t.Error(`IsVolumeMode("volume") = false, want true`)
	}
	// Companion: every non-volume form (the bind default, empty, and anything
	// else) must read as not-volume, so guards keyed on it don't misfire.
	for _, mode := range []string{"", "bind", "Volume", "vol", "VOLUME"} {
		if IsVolumeMode(mode) {
			t.Errorf("IsVolumeMode(%q) = true, want false", mode)
		}
	}
}
