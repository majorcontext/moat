package moatinit

import (
	"reflect"
	"testing"
)

// TestWorkspaceVolumeEnabled covers the WS-01 gate: only the exact string
// "1" activates the populate.
func TestWorkspaceVolumeEnabled(t *testing.T) {
	if !workspaceVolumeEnabled("1") {
		t.Error(`workspaceVolumeEnabled("1") = false, want true`)
	}
	for _, v := range []string{"", "0", "true", " 1", "01", "yes"} {
		if workspaceVolumeEnabled(v) {
			t.Errorf("workspaceVolumeEnabled(%q) = true, want false", v)
		}
	}
}

// TestStagingDir covers WS-03: the :- expansion defaults on empty (and
// therefore unset), and passes explicit values through.
func TestStagingDir(t *testing.T) {
	if got := stagingDir(""); got != "/mnt/host-workspace" {
		t.Errorf(`stagingDir("") = %q, want /mnt/host-workspace`, got)
	}
	if got := stagingDir("/custom"); got != "/custom" {
		t.Errorf(`stagingDir("/custom") = %q, want /custom`, got)
	}
}

// TestExcludeFileContent covers WS-04/WS-06: the env value is written
// verbatim (no trailing newline appended), empty stays empty.
func TestExcludeFileContent(t *testing.T) {
	if got := excludeFileContent("./node_modules\n./dist/sub"); got != "./node_modules\n./dist/sub" {
		t.Errorf("excludeFileContent altered the value: %q", got)
	}
	if got := excludeFileContent(""); got != "" {
		t.Errorf(`excludeFileContent("") = %q, want empty`, got)
	}
}

// TestVolumeChownPaths covers the named-volume chown splitting: space
// separated, glob characters kept literal (set -f semantics).
func TestVolumeChownPaths(t *testing.T) {
	got := volumeChownPaths("/r/[x] /r/normal  /r/star*")
	want := []string{"/r/[x]", "/r/normal", "/r/star*"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("volumeChownPaths() = %v, want %v", got, want)
	}
	if paths := volumeChownPaths(""); len(paths) != 0 {
		t.Errorf("volumeChownPaths(\"\") = %v, want empty", paths)
	}
}
