package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPiSessionMount(t *testing.T) {
	moatHome := t.TempDir()
	t.Setenv("MOAT_HOME", moatHome)
	first, err := piSessionMount("/projects/my-project", "/home/moatuser")
	if err != nil {
		t.Fatal(err)
	}
	if first.Target != "/home/moatuser/.pi/agent/sessions" || first.ReadOnly {
		t.Fatalf("expected writable sessions-only mount, got %+v", first)
	}
	if !strings.HasPrefix(first.Source, filepath.Join(moatHome, "pi", "sessions")+string(filepath.Separator)) {
		t.Fatalf("sessions escaped the Moat Pi directory: %s", first.Source)
	}
	if info, err := os.Stat(first.Source); err != nil || !info.IsDir() {
		t.Fatalf("host sessions directory not created: %v", err)
	}
	// Existing transcripts survive repeated runs, and runtime home is respected.
	file := filepath.Join(first.Source, "existing.jsonl")
	if err := os.WriteFile(file, []byte("existing transcript"), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := piSessionMount("/projects/my-project", "/custom/home")
	if err != nil || again.Source != first.Source || again.Target != "/custom/home/.pi/agent/sessions" {
		t.Fatalf("repeat/custom home mount: %+v, %v", again, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "existing transcript" {
		t.Fatalf("existing transcript changed: %q, %v", data, err)
	}
	other, err := piSessionMount("/projects/my_project", "/home/moatuser")
	if err != nil || other.Source == first.Source {
		t.Fatalf("punctuation-distinct workspaces share sessions: %+v, %v", other, err)
	}
}

func TestPiSessionMountErrors(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	for _, args := range [][2]string{{"", "/home/moatuser"}, {"/workspace", ""}, {"/workspace", "relative/home"}} {
		if _, err := piSessionMount(args[0], args[1]); err == nil {
			t.Fatalf("accepted invalid paths %q", args)
		}
	}
	// Failure to persist an explicitly requested transcript must not be silent.
	file := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOAT_HOME", file)
	if _, err := piSessionMount("/workspace", "/home/moatuser"); err == nil {
		t.Fatal("expected host directory creation error")
	}
}
