package cli

import (
	"strings"
	"testing"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/providers/codex"
	"github.com/majorcontext/moat/internal/providers/copilot"
	"github.com/majorcontext/moat/internal/providers/gemini"
	"github.com/majorcontext/moat/internal/run"
	"github.com/spf13/cobra"
)

// Drive the real agent commands and root profile selection, with no --grant or
// moat.yaml. Dry-run stops before container creation, after implicit grants have
// been assembled. Then check the preflight store used by real runs as well.
func TestImplicitGrantsInheritProfiles(t *testing.T) {
	for _, agent := range []struct {
		name     string
		grant    string
		key      credential.Provider
		register func(*cobra.Command)
	}{
		{"codex", "codex", credential.ProviderCodexSubscription, (&codex.Provider{}).RegisterCLI},
		{"codex", "openai", credential.ProviderOpenAI, (&codex.Provider{}).RegisterCLI},
		{"claude", "claude", credential.ProviderClaude, (&claude.OAuthProvider{}).RegisterCLI},
		{"gemini", "gemini", credential.ProviderGemini, (&gemini.Provider{}).RegisterCLI},
		{"copilot", "github", credential.ProviderGitHub, (&copilot.Provider{}).RegisterCLI},
	} {
		for _, parentName := range []string{"", "work"} {
			t.Run(agent.name+"/"+agent.grant+"/"+profileSourceLabel(parentName), func(t *testing.T) {
				t.Setenv("MOAT_HOME", t.TempDir())
				t.Setenv("MOAT_KEYRING_BACKEND", "file")
				t.Setenv("MOAT_PROFILE", "")
				oldProfile, oldActive := profile, credential.ActiveProfile
				oldDryRun, oldInternalDryRun := dryRun, intcli.DryRun
				t.Cleanup(func() {
					profile, credential.ActiveProfile = oldProfile, oldActive
					dryRun, intcli.DryRun = oldDryRun, oldInternalDryRun
				})

				key, err := credential.DefaultEncryptionKey()
				if err != nil {
					t.Fatal(err)
				}
				parent, err := credential.NewProfileStore(parentName, key)
				if err != nil {
					t.Fatal(err)
				}
				if err := parent.Save(credential.Credential{Provider: agent.key, Token: "parent-token"}); err != nil {
					t.Fatal(err)
				}
				if err := config.CreateProfile("child", config.ProfileConfig{Inherit: parentName, InheritDefault: parentName == ""}); err != nil {
					t.Fatal(err)
				}
				if err := config.CreateProfile("grandchild", config.ProfileConfig{Inherit: "child"}); err != nil {
					t.Fatal(err)
				}
				workspace := t.TempDir()

				checkCommand := func(wantGrant string) {
					t.Helper()
					root := &cobra.Command{Use: "moat", PersistentPreRunE: rootCmd.PersistentPreRunE}
					root.PersistentFlags().StringVar(&profile, "profile", "", "profile")
					root.PersistentFlags().BoolVar(&dryRun, "dry-run", false, "dry run")
					agent.register(root)
					args := []string{agent.name, workspace, "--dry-run"}
					if parentName == "" {
						args = append(args, "--profile", "grandchild")
					} else {
						t.Setenv("MOAT_PROFILE", "grandchild")
					}
					root.SetArgs(args)
					out := captureStdout(t, func() {
						if err := root.Execute(); err != nil {
							t.Fatal(err)
						}
					})
					if !strings.Contains(out, "Grants: ["+wantGrant+"]\n") {
						t.Fatalf("unexpected implicit grants:\n%s", out)
					}
				}

				checkCommand(agent.grant)
				store, err := run.OpenDefaultStore()
				if err != nil {
					t.Fatal(err)
				}
				if missing := run.DetectMissingGrants([]string{agent.grant}, nil, store); len(missing) != 0 {
					t.Fatalf("inherited implicit grant treated as missing: %v", missing)
				}
				if source, err := store.Source(agent.key); err != nil || source != parentName {
					t.Fatalf("source = %q, %v", source, err)
				}

				// A local grant overrides the inherited one without changing the
				// implicit grant name or modifying the parent credential.
				if err := store.Save(credential.Credential{Provider: agent.key, Token: "local-token"}); err != nil {
					t.Fatal(err)
				}
				checkCommand(agent.grant)
				cred, err := store.Get(agent.key)
				if err != nil || cred.Token != "local-token" {
					t.Fatalf("local override = %v, %v", cred, err)
				}
				original, err := parent.Get(agent.key)
				if err != nil || original.Token != "parent-token" {
					t.Fatalf("parent changed: %v, %v", original, err)
				}

				// Excluding the local grant must not rediscover its parent as an
				// implicit grant on the next invocation.
				if err := store.Delete(agent.key); err != nil {
					t.Fatal(err)
				}
				checkCommand("")
			})
		}
	}
}
