package run

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
		_, err := codexSessionMount(args[0], args[1], args[2], false)
		if err == nil {
			t.Fatalf("accepted %q", args)
		}
		for _, detail := range []string{
			fmt.Sprintf("workspace=%q", args[1]),
			fmt.Sprintf("hostDir=%q", codexSessionsHostDir(args[0], args[1], false)),
			fmt.Sprintf("containerHome=%q", args[2]),
		} {
			if !strings.Contains(err.Error(), detail) {
				t.Errorf("error %q missing %s", err, detail)
			}
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

func TestCodexSessionMountRequiresHostHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("MOAT_HOME", t.TempDir())
	for _, shared := range []bool{false, true} {
		mount, err := codexSessionMountForWorkspace("/workspace", "/home/agent", shared)
		if err == nil || !strings.Contains(err.Error(), "resolving host home directory") {
			t.Fatalf("shared=%v: expected home lookup failure, got mount=%+v, err=%v", shared, mount, err)
		}
		if mount.Source != "" {
			t.Fatalf("returned mount after home lookup failure: %+v", mount)
		}
	}
}
