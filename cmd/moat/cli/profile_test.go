package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

func TestProfileCreateCommand(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	// An ambient active profile never changes the explicitly selected parent.
	t.Setenv("MOAT_PROFILE", "unrelated")
	run := func(args ...string) error {
		cmd := newProfileCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run("create", "work", "--inherit-default"); err != nil {
		t.Fatal(err)
	}
	if err := run("create", "client", "--inherit", "work"); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Profiles["work"].InheritDefault || cfg.Profiles["client"].Inherit != "work" {
		t.Fatal(cfg.Profiles)
	}
	for _, args := range [][]string{
		{"create", "client"},
		{"create", "missing-parent", "--inherit", "absent"},
		{"create", "both", "--inherit", "work", "--inherit-default"},
		{"create", "empty", "--inherit", ""},
		{"create", "../escape"},
		{"create"},
	} {
		if err := run(args...); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}

func TestInheritedGrantListAndRevoke(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	oldProfile, oldJSON := credential.ActiveProfile, jsonOut
	t.Cleanup(func() { credential.ActiveProfile = oldProfile; jsonOut = oldJSON })
	credential.ActiveProfile = "client"
	jsonOut = true
	if err := config.CreateProfile("client", config.ProfileConfig{InheritDefault: true}); err != nil {
		t.Fatal(err)
	}
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	parent, err := credential.NewProfileStore("", key)
	if err != nil {
		t.Fatal(err)
	}
	child, err := credential.NewProfileStore("client", key)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []credential.Provider{"github", "oauth:linear"} {
		if err := parent.Save(credential.Credential{Provider: p, Token: "parent-secret"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := child.Save(credential.Credential{Provider: "oauth:linear", Token: "child-secret"}); err != nil {
		t.Fatal(err)
	}
	list := func() []struct{ Provider, Source string } {
		t.Helper()
		out := captureStdout(t, func() {
			if err := runGrantList(&cobra.Command{}, nil); err != nil {
				t.Fatal(err)
			}
		})
		if strings.Contains(out, "secret") {
			t.Fatal("leaked token in listing")
		}
		var rows []struct{ Provider, Source string }
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			t.Fatalf("invalid JSON %q: %v", out, err)
		}
		return rows
	}
	rows := list()
	if len(rows) != 2 || rows[0].Source != "(default)" || rows[1].Source != "client" {
		t.Fatalf("listing = %v", rows)
	}
	for _, p := range []string{"github", "oauth:linear"} {
		out := captureStdout(t, func() {
			if err := runRevoke(&cobra.Command{}, []string{p}); err != nil {
				t.Fatal(err)
			}
		})
		if !strings.Contains(out, "Excluded from inheritance, including future parent grants") {
			t.Fatalf("missing exclusion explanation: %s", out)
		}
		if _, err := child.Get(credential.Provider(p)); !errors.Is(err, credential.ErrNotFound) {
			t.Fatalf("not excluded: %v", err)
		}
		if _, err := parent.Get(credential.Provider(p)); err != nil {
			t.Fatal("modified parent", err)
		}
	}
	if rows := list(); len(rows) != 0 {
		t.Fatal(rows)
	}
}

func TestRevokeLocalGrantExplainsOnlyInheritedExclusions(t *testing.T) {
	for _, inherits := range []bool{false, true} {
		t.Run(fmt.Sprint(inherits), func(t *testing.T) {
			t.Setenv("MOAT_HOME", t.TempDir())
			t.Setenv("MOAT_KEYRING_BACKEND", "file")
			oldProfile := credential.ActiveProfile
			t.Cleanup(func() { credential.ActiveProfile = oldProfile })
			credential.ActiveProfile = "client"
			if err := config.CreateProfile("client", config.ProfileConfig{InheritDefault: inherits}); err != nil {
				t.Fatal(err)
			}
			key, err := credential.DefaultEncryptionKey()
			if err != nil {
				t.Fatal(err)
			}
			store, err := credential.NewProfileStore("client", key)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Save(credential.Credential{Provider: "github", Token: "local"}); err != nil {
				t.Fatal(err)
			}
			out := captureStdout(t, func() {
				if err := runRevoke(&cobra.Command{}, []string{"github"}); err != nil {
					t.Fatal(err)
				}
			})
			if got := strings.Contains(out, "Excluded from inheritance"); got != inherits {
				t.Fatalf("unexpected exclusion explanation: %s", out)
			}
		})
	}
}

func TestGrantListReportsUnreadableInheritedCredential(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	t.Setenv("MOAT_KEYRING_BACKEND", "file")
	oldProfile, oldJSON := credential.ActiveProfile, jsonOut
	t.Cleanup(func() { credential.ActiveProfile = oldProfile; jsonOut = oldJSON })
	credential.ActiveProfile = "client"
	if err := config.CreateProfile("client", config.ProfileConfig{InheritDefault: true}); err != nil {
		t.Fatal(err)
	}
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := credential.NewProfileStore("", key); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(credential.StoreDirForProfile(""), "github.enc"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, asJSON := range []bool{false, true} {
		jsonOut = asJSON
		out := captureStdout(t, func() {
			err := runGrantList(&cobra.Command{}, nil)
			if err == nil || !strings.Contains(err.Error(), "reading credential github") {
				t.Fatalf("missing credential error: %v", err)
			}
		})
		if out != "" {
			t.Fatalf("printed incomplete listing: %q", out)
		}
	}
}
