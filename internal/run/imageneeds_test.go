package run

import (
	"fmt"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
)

// mockStore is a minimal credential.Store for testing.
type mockStore struct {
	creds map[credential.Provider]*credential.Credential
}

func newMockStore() *mockStore {
	return &mockStore{creds: make(map[credential.Provider]*credential.Credential)}
}

func (s *mockStore) Save(cred credential.Credential) error {
	s.creds[cred.Provider] = &cred
	return nil
}

func (s *mockStore) Get(provider credential.Provider) (*credential.Credential, error) {
	if c, ok := s.creds[provider]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("credential not found: %s", provider)
}

func (s *mockStore) Delete(provider credential.Provider) error {
	delete(s.creds, provider)
	return nil
}

func (s *mockStore) List() ([]credential.Credential, error) {
	out := make([]credential.Credential, 0, len(s.creds))
	for _, c := range s.creds {
		out = append(out, *c)
	}
	return out, nil
}

func TestResolveImageNeedsClaudeGrant(t *testing.T) {
	needs := resolveImageNeedsWithStore([]string{"claude"}, nil, nil)
	if !contains(needs.initProviders, "claude") {
		t.Error("claude grant should add claude to initProviders")
	}
}

func TestResolveImageNeedsAnthropicOAuth(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-oat-fake-oauth-token",
		CreatedAt: time.Now(),
	})

	needs := resolveImageNeedsWithStore([]string{"anthropic"}, nil, store)
	if !contains(needs.initProviders, "claude") {
		t.Error("anthropic grant with OAuth token should add claude to initProviders")
	}
}

func TestResolveImageNeedsAnthropicAPIKey(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderAnthropic,
		Token:     "sk-ant-api-fake-api-key",
		CreatedAt: time.Now(),
	})

	needs := resolveImageNeedsWithStore([]string{"anthropic"}, nil, store)
	if contains(needs.initProviders, "claude") {
		t.Error("anthropic grant with API key should NOT add claude to initProviders")
	}
}

func TestResolveImageNeedsAnthropicNoStore(t *testing.T) {
	// Without a store, anthropic grants are skipped (no error, just no init).
	needs := resolveImageNeedsWithStore([]string{"anthropic"}, nil, nil)
	if contains(needs.initProviders, "claude") {
		t.Error("anthropic grant without store should NOT add claude to initProviders")
	}
}

func TestResolveImageNeedsOpenAI(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderOpenAI,
		Token:     "sk-openai-test",
		CreatedAt: time.Now(),
	})

	needs := resolveImageNeedsWithStore([]string{"openai"}, nil, store)
	if !contains(needs.initProviders, "codex") {
		t.Error("openai grant with stored cred should add codex to initProviders")
	}
}

func TestResolveImageNeedsOpenAINoStore(t *testing.T) {
	needs := resolveImageNeedsWithStore([]string{"openai"}, nil, nil)
	if contains(needs.initProviders, "codex") {
		t.Error("openai grant without store should NOT add codex to initProviders")
	}
}

func TestResolveImageNeedsGeminiNoStore(t *testing.T) {
	needs := resolveImageNeedsWithStore([]string{"gemini"}, nil, nil)
	if contains(needs.initProviders, "gemini") {
		t.Error("gemini grant without store should NOT add gemini to initProviders")
	}
}

func TestResolveImageNeedsGemini(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderGemini,
		Token:     "gemini-api-key",
		CreatedAt: time.Now(),
	})

	needs := resolveImageNeedsWithStore([]string{"gemini"}, nil, store)
	if !contains(needs.initProviders, "gemini") {
		t.Error("gemini grant with stored cred should add gemini to initProviders")
	}
}

func TestResolveImageNeedsClaudeCodeDep(t *testing.T) {
	// claude-code dependency without a claude grant should still trigger claude init.
	depList := []deps.Dependency{{Name: "claude-code"}}
	needs := resolveImageNeedsWithStore(nil, depList, nil)
	if !contains(needs.initProviders, "claude") {
		t.Error("claude-code dep should add claude to initProviders via fallback")
	}
}

func TestResolveImageNeedsGeminiCLIDep(t *testing.T) {
	depList := []deps.Dependency{{Name: "gemini-cli"}}
	needs := resolveImageNeedsWithStore(nil, depList, nil)
	if !contains(needs.initProviders, "gemini") {
		t.Error("gemini-cli dep should add gemini to initProviders via fallback")
	}
}

func TestResolveImageNeedsGrantAndDep(t *testing.T) {
	// When a grant already covers claude, the dep fallback shouldn't duplicate.
	depList := []deps.Dependency{{Name: "claude-code"}}
	needs := resolveImageNeedsWithStore([]string{"claude"}, depList, nil)
	count := 0
	for _, p := range needs.initProviders {
		if p == "claude" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("claude should appear exactly once, got %d", count)
	}
}

func TestResolveImageNeedsMultipleGrants(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{Provider: credential.ProviderOpenAI, Token: "sk-test", CreatedAt: time.Now()})
	store.Save(credential.Credential{Provider: credential.ProviderGemini, Token: "gemini-test", CreatedAt: time.Now()})

	needs := resolveImageNeedsWithStore([]string{"claude", "openai", "gemini"}, nil, store)
	if !contains(needs.initProviders, "claude") {
		t.Error("should have claude")
	}
	if !contains(needs.initProviders, "codex") {
		t.Error("should have codex")
	}
	if !contains(needs.initProviders, "gemini") {
		t.Error("should have gemini")
	}
}

func TestResolveImageNeedsSorted(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{Provider: credential.ProviderOpenAI, Token: "sk-test", CreatedAt: time.Now()})
	store.Save(credential.Credential{Provider: credential.ProviderGemini, Token: "gemini-test", CreatedAt: time.Now()})

	needs := resolveImageNeedsWithStore([]string{"gemini", "openai", "claude"}, nil, store)
	for i := 1; i < len(needs.initProviders); i++ {
		if needs.initProviders[i-1] > needs.initProviders[i] {
			t.Errorf("initProviders not sorted: %v", needs.initProviders)
			break
		}
	}
}

func TestResolveImageNeedsEmpty(t *testing.T) {
	needs := resolveImageNeedsWithStore(nil, nil, nil)
	if len(needs.initProviders) != 0 {
		t.Errorf("empty input should yield no initProviders, got %v", needs.initProviders)
	}
	if needs.initFiles {
		t.Error("empty input should not need init files")
	}
}

func TestResolveImageNeedsGrantWithScope(t *testing.T) {
	// Grants like "claude:read" should still be recognized.
	needs := resolveImageNeedsWithStore([]string{"claude:read"}, nil, nil)
	if !contains(needs.initProviders, "claude") {
		t.Error("scoped claude grant should add claude to initProviders")
	}
}

func contains(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}
