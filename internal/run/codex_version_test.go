package run

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
	codexprov "github.com/majorcontext/moat/internal/providers/codex"
)

// Regression: the subscription version gate ran against dep.Version, which is
// empty for an unpinned dependency because ResolveVersions only fills in
// runtime deps — codex-cli is an npm dep. Every `moat codex` run therefore
// aborted with `unsupported Codex CLI version ""` before creating a container.
// The gate must check the version the image will actually install.
func TestCodexDefaultDependencyPassesTheSubscriptionVersionGate(t *testing.T) {
	parsed, err := deps.ParseAll(codexprov.DefaultDependencies())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := deps.ResolveVersions(context.Background(), parsed)
	if err != nil {
		// Version resolution reaches the network for runtime deps; the codex-cli
		// entry this test cares about is unaffected either way.
		resolved = parsed
	}

	var found bool
	for _, dep := range resolved {
		if dep.Name != "codex-cli" {
			continue
		}
		found = true
		if err := codexprov.ValidateVersion(effectiveCodexVersion(dep)); err != nil {
			t.Fatalf("default codex-cli dependency rejected by the version gate: %v", err)
		}
	}
	if !found {
		t.Fatal("codex-cli missing from the codex agent's default dependencies")
	}
}

// Companion: a project that pins a version outside the verified range must
// still be rejected, or the gate is not doing anything.
func TestCodexPinnedUnsupportedVersionIsRejected(t *testing.T) {
	parsed, err := deps.ParseAll([]string{"codex-cli@0.160.0"})
	if err != nil {
		t.Fatal(err)
	}
	if err := codexprov.ValidateVersion(effectiveCodexVersion(parsed[0])); err == nil {
		t.Fatal("codex-cli@0.160.0 should be rejected for subscription auth")
	}
}

// The registry default is what GenerateDockerfile installs for an unpinned dep,
// so the adapter's supported range has to include it. If someone bumps the
// registry past the verified range this fails instead of shipping a broken run.
func TestCodexRegistryDefaultIsWithinSupportedRange(t *testing.T) {
	spec, ok := deps.GetSpec("codex-cli")
	if !ok {
		t.Fatal("codex-cli missing from the dependency registry")
	}
	if err := codexprov.ValidateVersion(spec.Default); err != nil {
		t.Fatalf("registry default %q is outside the supported adapter range: %v", spec.Default, err)
	}
}

func TestResolveBashEnv(t *testing.T) {
	const claudeEnv = "BASH_ENV=$HOME/.claude/anthropic-env.sh"
	const codexEnv = "BASH_ENV=$HOME/.codex/" + codexprov.OpenAIShellEnvFileName

	t.Run("codex wins because its file sources claude's", func(t *testing.T) {
		got := resolveBashEnv([]string{"PATH=/bin", claudeEnv, "HOME=/root", codexEnv})
		var seen []string
		for _, item := range got {
			if strings.HasPrefix(item, "BASH_ENV=") {
				seen = append(seen, item)
			}
		}
		if len(seen) != 1 || seen[0] != codexEnv {
			t.Fatalf("BASH_ENV entries = %v, want exactly [%s]", seen, codexEnv)
		}
		for _, want := range []string{"PATH=/bin", "HOME=/root"} {
			if !containsStr(got, want) {
				t.Errorf("resolveBashEnv dropped %q: %v", want, got)
			}
		}
	})

	// Order must not decide the winner: the same two entries reversed still
	// have to resolve to the codex file.
	t.Run("claude appended last still loses", func(t *testing.T) {
		got := resolveBashEnv([]string{codexEnv, claudeEnv})
		if len(got) != 1 || got[0] != codexEnv {
			t.Fatalf("resolveBashEnv = %v, want [%s]", got, codexEnv)
		}
	})

	t.Run("a single entry is left alone", func(t *testing.T) {
		for _, only := range []string{claudeEnv, codexEnv} {
			got := resolveBashEnv([]string{"PATH=/bin", only})
			if len(got) != 2 || got[1] != only {
				t.Fatalf("resolveBashEnv(%q) = %v, want it untouched", only, got)
			}
		}
	})

	t.Run("no entries is a no-op", func(t *testing.T) {
		got := resolveBashEnv([]string{"PATH=/bin"})
		if len(got) != 1 {
			t.Fatalf("resolveBashEnv = %v, want unchanged", got)
		}
	})
}

func containsStr(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}

// A missing subscription falls back to the API key; a subscription that exists
// but cannot be opened must surface that error instead, or a key rotation
// silently moves the user onto API billing.
func TestLoadCredentialForGrant_CodexFallbackOnlyWhenAbsent(t *testing.T) {
	t.Run("absent subscription falls back to the API key", func(t *testing.T) {
		store := newGrantsTestStore(t)
		if err := store.Save(credential.Credential{Provider: credential.ProviderOpenAI, Token: "sk-test"}); err != nil {
			t.Fatal(err)
		}
		cred, key, err := loadCredentialForGrant(store, "codex", "codex")
		if err != nil {
			t.Fatalf("loadCredentialForGrant = %v, want the API-key fallback", err)
		}
		if key != credential.ProviderOpenAI || cred.Token != "sk-test" {
			t.Fatalf("got key=%q token=%q, want the stored OpenAI key", key, cred.Token)
		}
	})

	t.Run("present subscription is preferred", func(t *testing.T) {
		store := newGrantsTestStore(t)
		for _, c := range []credential.Credential{
			{Provider: credential.ProviderCodexSubscription, Token: "subscription"},
			{Provider: credential.ProviderOpenAI, Token: "sk-test"},
		} {
			if err := store.Save(c); err != nil {
				t.Fatal(err)
			}
		}
		_, key, err := loadCredentialForGrant(store, "codex", "codex")
		if err != nil || key != credential.ProviderCodexSubscription {
			t.Fatalf("got key=%q err=%v, want the subscription", key, err)
		}
	})

	t.Run("unreadable subscription surfaces the error", func(t *testing.T) {
		store := newGrantsTestStore(t)
		if err := store.Save(credential.Credential{Provider: credential.ProviderOpenAI, Token: "sk-test"}); err != nil {
			t.Fatal(err)
		}
		_, _, err := loadCredentialForGrant(errStore{store: store, err: credential.ErrDecrypt}, "codex", "codex")
		if err == nil {
			t.Fatal("a decrypt failure must not silently fall back to API-key billing")
		}
		if !errors.Is(err, credential.ErrDecrypt) {
			t.Fatalf("err = %v, want it to carry ErrDecrypt so the CLI can classify it", err)
		}
	})
}

// errStore fails the subscription lookup with a chosen error and passes every
// other provider through, modeling a credential that exists but cannot be read.
type errStore struct {
	store credential.Store
	err   error
}

func (e errStore) Get(p credential.Provider) (*credential.Credential, error) {
	if p == credential.ProviderCodexSubscription {
		return nil, e.err
	}
	return e.store.Get(p)
}
func (e errStore) Save(c credential.Credential) error     { return e.store.Save(c) }
func (e errStore) Delete(p credential.Provider) error     { return e.store.Delete(p) }
func (e errStore) List() ([]credential.Credential, error) { return e.store.List() }
