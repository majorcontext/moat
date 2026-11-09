package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestProfileInspectionIncludesImplicitAndDefaultWithoutSecrets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MOAT_HOME", home)
	oldActive, oldJSON := credential.ActiveProfile, jsonOut
	t.Cleanup(func() { credential.ActiveProfile, jsonOut = oldActive, oldJSON })
	credential.ActiveProfile, jsonOut = "work", true
	data := "profiles:\n  work:\n    inherit_default: true\n    env:\n      API_KEY: super-secret\n  default: {}\n"
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(credential.StoreDirForProfile("implicit"), 0o700); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		cmd := newProfileCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs(args)
		err := cmd.Execute()
		return out.String(), err
	}
	out, err := run("list")
	if err != nil {
		t.Fatal(err)
	}
	var rows []profileInfo
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || !rows[0].Default || rows[1].Name != "default" || rows[1].Default || rows[2].Name != "implicit" || !rows[3].Active || rows[3].Parent != "(default)" {
		t.Fatal(rows)
	}
	if strings.Contains(out, "super-secret") {
		t.Fatal("leaked environment value")
	}
	out, err = run("show")
	if err != nil {
		t.Fatal(err)
	}
	var info profileInfo
	if err := json.Unmarshal([]byte(out), &info); err != nil {
		t.Fatal(err)
	}
	if info.Name != "work" || len(info.Environment) != 1 || info.Environment[0] != "API_KEY" || len(info.Chain) != 2 {
		t.Fatal(info)
	}
	jsonOut = false
	out, err = run("show", "work")
	if err != nil || strings.Contains(out, "super-secret") || !strings.Contains(out, "API_KEY") {
		t.Fatalf("show: %q %v", out, err)
	}
	if _, err := run("show", "missing"); err == nil {
		t.Fatal("show accepted missing profile")
	}
	if _, err := os.Stat(credential.StoreDirForProfile("missing")); !os.IsNotExist(err) {
		t.Fatal("inspection created a credential store")
	}
}

func TestProfileLifecycleCommandsConfirmationAndDryRun(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	oldDry, oldJSON := dryRun, jsonOut
	t.Cleanup(func() { dryRun, jsonOut = oldDry, oldJSON })
	dryRun, jsonOut = false, false
	if err := config.CreateProfile("work", config.ProfileConfig{}); err != nil {
		t.Fatal(err)
	}
	run := func(input string, args ...string) error {
		cmd := newProfileCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetIn(strings.NewReader(input))
		cmd.SetArgs(args)
		return cmd.Execute()
	}
	if err := run("n\n", "remove", "work"); err == nil {
		t.Fatal("removed without confirmation")
	}
	dryRun = true
	if err := run("", "remove", "work", "--force"); err != nil {
		t.Fatal(err)
	}
	dryRun = false
	if err := run("", "reset", "work", "--force"); err != nil {
		t.Fatal(err)
	}
	if err := run("yes\n", "remove", "work"); err != nil {
		t.Fatal(err)
	}
	if err := run("", "remove", "work", "--force"); err == nil {
		t.Fatal("removed missing profile")
	}
}

func TestProfileListReportsBrokenInheritanceAndDryRunValidates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("MOAT_HOME", home)
	oldJSON, oldDry := jsonOut, dryRun
	t.Cleanup(func() { jsonOut, dryRun = oldJSON, oldDry })
	jsonOut, dryRun = true, false
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("profiles:\n  broken:\n    inherit: missing\n  good: {}\n  child:\n    inherit: good\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	cmd := newProfileCommand()
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"list"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	var rows []profileInfo
	if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.Name == "broken" {
			found = strings.Contains(row.Error, "missing")
		}
	}
	if !found {
		t.Fatal(rows)
	}
	dryRun = true
	for _, name := range []string{"absent", "good"} {
		cmd := newProfileCommand()
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		cmd.SetArgs([]string{"remove", name, "--force"})
		if err := cmd.Execute(); err == nil {
			t.Fatalf("dry run accepted %s", name)
		}
	}
}

func TestProfileClearDecliningPreservesActiveProfile(t *testing.T) {
	for _, action := range []string{"remove", "reset"} {
		for label, active := range map[string]bool{"active": true, "inactive": false} {
			t.Run(action+"/"+label, func(t *testing.T) {
				home := t.TempDir()
				t.Setenv("MOAT_HOME", home)
				oldActive, oldDry := credential.ActiveProfile, dryRun
				t.Cleanup(func() { credential.ActiveProfile, dryRun = oldActive, oldDry })
				credential.ActiveProfile, dryRun = "work", false
				if !active {
					credential.ActiveProfile = "other"
				}
				if err := config.CreateProfile("work", config.ProfileConfig{Env: map[string]string{"MODEL": "keep"}}); err != nil {
					t.Fatal(err)
				}
				store := credential.StoreDirForProfile("work")
				if err := os.MkdirAll(store, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(store, "github.enc")
				if err := os.WriteFile(path, []byte("preserved"), 0o600); err != nil {
					t.Fatal(err)
				}
				cmd := newProfileCommand()
				var prompt bytes.Buffer
				cmd.SetOut(&bytes.Buffer{})
				cmd.SetErr(&prompt)
				cmd.SetIn(strings.NewReader("n\n"))
				cmd.SetArgs([]string{action, "work"})
				if err := cmd.Execute(); err == nil {
					t.Fatal("accepted declined confirmation")
				}
				if strings.Contains(prompt.String(), "active profile") != active {
					t.Fatal(prompt.String())
				}
				data, err := os.ReadFile(path)
				if err != nil || string(data) != "preserved" {
					t.Fatalf("changed grants: %q %v", data, err)
				}
				cfg, err := config.LoadProfileConfig()
				if err != nil || cfg.Profiles["work"].Env["MODEL"] != "keep" {
					t.Fatalf("changed settings: %v %v", cfg, err)
				}
			})
		}
	}
}

func TestProfileClearJSONDryRunAndImplicitRemoval(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	oldJSON, oldDry := jsonOut, dryRun
	t.Cleanup(func() { jsonOut, dryRun = oldJSON, oldDry })
	jsonOut, dryRun = true, true
	store := credential.StoreDirForProfile("implicit")
	if err := os.MkdirAll(store, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"remove", "reset"} {
		cmd := newProfileCommand()
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetArgs([]string{action, "implicit"})
		if err := cmd.Execute(); err != nil {
			t.Fatal(err)
		}
		var result struct {
			Name   string
			Action string
			DryRun bool `json:"dry_run"`
		}
		if err := json.Unmarshal(out.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Name != "implicit" || result.Action != action || !result.DryRun {
			t.Fatal(result)
		}
		if _, err := os.Stat(store); err != nil {
			t.Fatal("dry run removed store", err)
		}
	}
	dryRun = false
	cmd := newProfileCommand()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetArgs([]string{"remove", "implicit", "--force"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("implicit store remains: %v", err)
	}
	profiles, err := credential.ListProfiles()
	if err != nil || len(profiles) != 0 {
		t.Fatalf("removed profile remains listed: %v %v", profiles, err)
	}
}

func TestProfileListSkipsRecoveryDirectories(t *testing.T) {
	t.Setenv("MOAT_HOME", t.TempDir())
	visible := credential.StoreDirForProfile("visible")
	if err := os.MkdirAll(visible, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := os.MkdirTemp(filepath.Dir(visible), ".profile-clear-"); err != nil {
		t.Fatal(err)
	}
	profiles, err := credential.ListProfiles()
	if err != nil || len(profiles) != 1 || profiles[0] != "visible" {
		t.Fatalf("profiles = %v, %v", profiles, err)
	}
}
