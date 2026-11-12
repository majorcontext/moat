package run

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSessionMountPersistence(t *testing.T) {
	root := t.TempDir()
	t.Setenv("MOAT_HOME", root)
	for _, shared := range []bool{false, true} {
		hostHome := t.TempDir()
		first, err := codexSessionMount(hostHome, "/projects/one", "/home/agent", shared)
		if err != nil {
			t.Fatal(err)
		}
		if first.Target != "/home/agent/.codex/sessions" || first.ReadOnly {
			t.Fatalf("bad mount: %+v", first)
		}
		transcript := filepath.Join(first.Source, "rollout.jsonl")
		if err := os.WriteFile(transcript, []byte("saved conversation"), 0o600); err != nil {
			t.Fatal(err)
		}
		again, err := codexSessionMount(hostHome, "/projects/one", "/other/home", shared)
		if err != nil || again.Source != first.Source {
			t.Fatalf("repeat: %+v, %v", again, err)
		}
		if data, err := os.ReadFile(transcript); err != nil || string(data) != "saved conversation" {
			t.Fatalf("transcript lost: %q, %v", data, err)
		}
		other, err := codexSessionMount(hostHome, "/projects/two", "/home/agent", shared)
		if err != nil {
			t.Fatal(err)
		}
		if (other.Source == first.Source) != shared {
			t.Fatalf("workspace isolation: %+v, %+v", first, other)
		}
	}
}

func TestCodexSessionMountErrors(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	for _, args := range [][3]string{{"/host", "", "/container"}, {"/host", "/workspace", ""}, {"/host", "/workspace", "relative"}} {
		if _, err := codexSessionMount(args[0], args[1], args[2], false); err == nil {
			t.Fatalf("accepted %q", args)
		}
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOAT_HOME", file)
	if _, err := codexSessionMount("/host", "/workspace", "/container", false); err == nil {
		t.Fatal("silently ignored persistence failure")
	}
}
