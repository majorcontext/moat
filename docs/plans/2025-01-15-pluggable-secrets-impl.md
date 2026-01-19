# Pluggable Secrets Backend - Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a `secrets:` block to `agent.yaml` that resolves secret references from 1Password at runtime and injects them as container env vars.

**Architecture:** Pluggable resolver interface with registry pattern. 1Password resolver shells out to `op read`. Secrets resolved on host before container start, merged into container env alongside `env:` block.

**Tech Stack:** Go, 1Password CLI (`op`), `os/exec`

---

## Task 1: Add Secrets field to Config

**Files:**
- Modify: `internal/config/config.go:13-25`
- Modify: `internal/config/config_test.go`

**Step 1: Add Secrets field to Config struct**

Edit `internal/config/config.go`, add `Secrets` field after `Env`:

```go
type Config struct {
	Name         string            `yaml:"name,omitempty"`
	Agent        string            `yaml:"agent"`
	Version      string            `yaml:"version,omitempty"`
	Dependencies []string          `yaml:"dependencies,omitempty"`
	Grants       []string          `yaml:"grants,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Secrets      map[string]string `yaml:"secrets,omitempty"`
	Mounts       []string          `yaml:"mounts,omitempty"`
	Ports        map[string]int    `yaml:"ports,omitempty"`

	// Deprecated: use Dependencies instead
	Runtime *deprecatedRuntime `yaml:"runtime,omitempty"`
}
```

**Step 2: Write test for secrets parsing**

Add to `internal/config/config_test.go`:

```go
func TestLoad_Secrets(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key
  DATABASE_URL: op://Prod/Database/url
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(cfg.Secrets) != 2 {
		t.Errorf("expected 2 secrets, got %d", len(cfg.Secrets))
	}
	if cfg.Secrets["OPENAI_API_KEY"] != "op://Dev/OpenAI/api-key" {
		t.Errorf("unexpected OPENAI_API_KEY: %s", cfg.Secrets["OPENAI_API_KEY"])
	}
	if cfg.Secrets["DATABASE_URL"] != "op://Prod/Database/url" {
		t.Errorf("unexpected DATABASE_URL: %s", cfg.Secrets["DATABASE_URL"])
	}
}
```

**Step 3: Run test to verify it passes**

Run: `go test -run TestLoad_Secrets ./internal/config/`
Expected: PASS

**Step 4: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add secrets field to agent.yaml schema"
```

---

## Task 2: Create secrets package with Resolver interface

**Files:**
- Create: `internal/secrets/resolver.go`
- Create: `internal/secrets/resolver_test.go`

**Step 1: Write test for resolver registry**

Create `internal/secrets/resolver_test.go`:

```go
package secrets

import (
	"context"
	"testing"
)

type mockResolver struct {
	scheme string
	values map[string]string
}

func (m *mockResolver) Scheme() string {
	return m.scheme
}

func (m *mockResolver) Resolve(ctx context.Context, ref string) (string, error) {
	if v, ok := m.values[ref]; ok {
		return v, nil
	}
	return "", &NotFoundError{Reference: ref}
}

func TestResolve_DispatchesToCorrectResolver(t *testing.T) {
	// Register mock resolver
	mock := &mockResolver{
		scheme: "mock",
		values: map[string]string{
			"mock://vault/item/field": "secret-value",
		},
	}
	Register(mock)
	defer func() { resolvers = make(map[string]Resolver) }()

	val, err := Resolve(context.Background(), "mock://vault/item/field")
	if err != nil {
		t.Fatal(err)
	}
	if val != "secret-value" {
		t.Errorf("expected 'secret-value', got %q", val)
	}
}

func TestResolve_UnsupportedScheme(t *testing.T) {
	resolvers = make(map[string]Resolver) // Clear registry

	_, err := Resolve(context.Background(), "unknown://vault/item")
	if err == nil {
		t.Fatal("expected error for unsupported scheme")
	}

	var unsupported *UnsupportedSchemeError
	if !errors.As(err, &unsupported) {
		t.Errorf("expected UnsupportedSchemeError, got %T", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/secrets/`
Expected: FAIL (package doesn't exist)

**Step 3: Implement resolver.go**

Create `internal/secrets/resolver.go`:

```go
// Package secrets provides pluggable secret resolution from external backends.
package secrets

import (
	"context"
	"strings"
	"sync"
)

// Resolver resolves a secret reference to its plaintext value.
type Resolver interface {
	// Scheme returns the URI scheme this resolver handles (e.g., "op", "ssm").
	Scheme() string

	// Resolve fetches the secret value for the given reference.
	// The reference is the full URI (e.g., "op://Dev/OpenAI/api-key").
	Resolve(ctx context.Context, reference string) (string, error)
}

var (
	resolvers = make(map[string]Resolver)
	mu        sync.RWMutex
)

// Register adds a resolver to the registry.
func Register(r Resolver) {
	mu.Lock()
	defer mu.Unlock()
	resolvers[r.Scheme()] = r
}

// Resolve dispatches to the appropriate resolver based on URI scheme.
func Resolve(ctx context.Context, reference string) (string, error) {
	scheme := parseScheme(reference)
	if scheme == "" {
		return "", &InvalidReferenceError{Reference: reference, Reason: "missing scheme"}
	}

	mu.RLock()
	r, ok := resolvers[scheme]
	mu.RUnlock()

	if !ok {
		return "", &UnsupportedSchemeError{Scheme: scheme}
	}

	return r.Resolve(ctx, reference)
}

// parseScheme extracts the scheme from a URI (e.g., "op" from "op://vault/item").
func parseScheme(ref string) string {
	idx := strings.Index(ref, "://")
	if idx < 1 {
		return ""
	}
	return ref[:idx]
}
```

**Step 4: Add errors.go**

Create `internal/secrets/errors.go`:

```go
package secrets

import "fmt"

// UnsupportedSchemeError indicates an unrecognized URI scheme.
type UnsupportedSchemeError struct {
	Scheme string
}

func (e *UnsupportedSchemeError) Error() string {
	return fmt.Sprintf("unsupported secret scheme: %s", e.Scheme)
}

// InvalidReferenceError indicates a malformed secret reference.
type InvalidReferenceError struct {
	Reference string
	Reason    string
}

func (e *InvalidReferenceError) Error() string {
	return fmt.Sprintf("invalid secret reference %q: %s", e.Reference, e.Reason)
}

// NotFoundError indicates the secret was not found in the backend.
type NotFoundError struct {
	Reference string
	Backend   string
}

func (e *NotFoundError) Error() string {
	if e.Backend != "" {
		return fmt.Sprintf("secret not found in %s: %s", e.Backend, e.Reference)
	}
	return fmt.Sprintf("secret not found: %s", e.Reference)
}

// BackendError wraps errors from secret backends with actionable context.
type BackendError struct {
	Backend   string
	Reference string
	Reason    string
	Fix       string
}

func (e *BackendError) Error() string {
	msg := fmt.Sprintf("%s: %s", e.Backend, e.Reason)
	if e.Fix != "" {
		msg += "\n\n  " + e.Fix
	}
	return msg
}
```

**Step 5: Add missing import to test**

Add `"errors"` import to `resolver_test.go`.

**Step 6: Run tests to verify they pass**

Run: `go test ./internal/secrets/`
Expected: PASS

**Step 7: Commit**

```bash
git add internal/secrets/
git commit -m "feat(secrets): add resolver interface and registry"
```

---

## Task 3: Implement 1Password resolver

**Files:**
- Create: `internal/secrets/onepassword.go`
- Create: `internal/secrets/onepassword_test.go`

**Step 1: Write test for op resolver (mocked)**

Create `internal/secrets/onepassword_test.go`:

```go
package secrets

import (
	"context"
	"testing"
)

func TestOnePasswordResolver_Scheme(t *testing.T) {
	r := &OnePasswordResolver{}
	if r.Scheme() != "op" {
		t.Errorf("expected scheme 'op', got %q", r.Scheme())
	}
}

func TestOnePasswordResolver_ParseError_NotSignedIn(t *testing.T) {
	r := &OnePasswordResolver{}

	// Simulate "not signed in" error from op CLI
	stderr := []byte("[ERROR] 2024/01/15 10:00:00 You are not currently signed in")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T", err)
	}
	if backendErr.Backend != "1Password" {
		t.Errorf("expected backend '1Password', got %q", backendErr.Backend)
	}
	if !strings.Contains(backendErr.Fix, "op signin") {
		t.Errorf("expected fix to mention 'op signin', got %q", backendErr.Fix)
	}
}

func TestOnePasswordResolver_ParseError_ItemNotFound(t *testing.T) {
	r := &OnePasswordResolver{}

	stderr := []byte("[ERROR] 2024/01/15 10:00:00 \"OpenAI\" isn't an item")
	err := r.parseOpError(stderr, "op://Dev/OpenAI/api-key")

	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected NotFoundError, got %T", err)
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestOnePasswordResolver ./internal/secrets/`
Expected: FAIL (OnePasswordResolver not defined)

**Step 3: Implement onepassword.go**

Create `internal/secrets/onepassword.go`:

```go
package secrets

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// OnePasswordResolver resolves secrets from 1Password using the op CLI.
type OnePasswordResolver struct{}

// Scheme returns "op".
func (r *OnePasswordResolver) Scheme() string {
	return "op"
}

// Resolve fetches a secret using `op read`.
func (r *OnePasswordResolver) Resolve(ctx context.Context, reference string) (string, error) {
	// Check op CLI is available
	if _, err := exec.LookPath("op"); err != nil {
		return "", &BackendError{
			Backend: "1Password",
			Reason:  "op CLI not found in PATH",
			Fix:     "Install from https://1password.com/downloads/command-line/\nThen run: op signin",
		}
	}

	cmd := exec.CommandContext(ctx, "op", "read", reference)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", r.parseOpError(stderr.Bytes(), reference)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// parseOpError converts op CLI errors to actionable error types.
func (r *OnePasswordResolver) parseOpError(stderr []byte, reference string) error {
	msg := string(stderr)

	// Not signed in
	if strings.Contains(msg, "not currently signed in") || strings.Contains(msg, "not signed in") {
		return &BackendError{
			Backend:   "1Password",
			Reference: reference,
			Reason:    "not signed in",
			Fix:       "Run: eval $(op signin)\n\nOr for CI/automation, set OP_SERVICE_ACCOUNT_TOKEN.",
		}
	}

	// Item not found
	if strings.Contains(msg, "isn't an item") || strings.Contains(msg, "could not be found") {
		return &NotFoundError{
			Reference: reference,
			Backend:   "1Password",
		}
	}

	// Vault not found / access denied
	if strings.Contains(msg, "isn't a vault") || strings.Contains(msg, "vault") && strings.Contains(msg, "not found") {
		// Extract vault name from reference: op://VaultName/Item/Field
		parts := strings.Split(strings.TrimPrefix(reference, "op://"), "/")
		vaultName := "unknown"
		if len(parts) > 0 {
			vaultName = parts[0]
		}
		return &BackendError{
			Backend:   "1Password",
			Reference: reference,
			Reason:    "vault not found or not accessible",
			Fix:       "Vault \"" + vaultName + "\" not found.\n\nList available vaults with: op vault list",
		}
	}

	// Generic error
	return &BackendError{
		Backend:   "1Password",
		Reference: reference,
		Reason:    strings.TrimSpace(msg),
	}
}

func init() {
	Register(&OnePasswordResolver{})
}
```

**Step 4: Add missing imports to test**

Add `"errors"` and `"strings"` imports to `onepassword_test.go`.

**Step 5: Run tests to verify they pass**

Run: `go test -run TestOnePasswordResolver ./internal/secrets/`
Expected: PASS

**Step 6: Commit**

```bash
git add internal/secrets/onepassword.go internal/secrets/onepassword_test.go
git commit -m "feat(secrets): add 1Password resolver using op CLI"
```

---

## Task 4: Add ResolveAll helper function

**Files:**
- Modify: `internal/secrets/resolver.go`
- Modify: `internal/secrets/resolver_test.go`

**Step 1: Write test for ResolveAll**

Add to `internal/secrets/resolver_test.go`:

```go
func TestResolveAll(t *testing.T) {
	mock := &mockResolver{
		scheme: "mock",
		values: map[string]string{
			"mock://vault/key1": "value1",
			"mock://vault/key2": "value2",
		},
	}
	Register(mock)
	defer func() { resolvers = make(map[string]Resolver) }()

	secrets := map[string]string{
		"SECRET_1": "mock://vault/key1",
		"SECRET_2": "mock://vault/key2",
	}

	resolved, err := ResolveAll(context.Background(), secrets)
	if err != nil {
		t.Fatal(err)
	}

	if resolved["SECRET_1"] != "value1" {
		t.Errorf("SECRET_1: expected 'value1', got %q", resolved["SECRET_1"])
	}
	if resolved["SECRET_2"] != "value2" {
		t.Errorf("SECRET_2: expected 'value2', got %q", resolved["SECRET_2"])
	}
}

func TestResolveAll_FailsOnError(t *testing.T) {
	mock := &mockResolver{
		scheme: "mock",
		values: map[string]string{}, // Empty - all lookups fail
	}
	Register(mock)
	defer func() { resolvers = make(map[string]Resolver) }()

	secrets := map[string]string{
		"MISSING": "mock://vault/nonexistent",
	}

	_, err := ResolveAll(context.Background(), secrets)
	if err == nil {
		t.Fatal("expected error for missing secret")
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test -run TestResolveAll ./internal/secrets/`
Expected: FAIL (ResolveAll not defined)

**Step 3: Implement ResolveAll**

Add to `internal/secrets/resolver.go`:

```go
// ResolveAll resolves all secrets in the map, returning resolved values.
// Keys are environment variable names, values are secret references.
// Fails fast on first error.
func ResolveAll(ctx context.Context, secrets map[string]string) (map[string]string, error) {
	if len(secrets) == 0 {
		return nil, nil
	}

	resolved := make(map[string]string, len(secrets))
	for name, ref := range secrets {
		val, err := Resolve(ctx, ref)
		if err != nil {
			return nil, fmt.Errorf("resolving secret %s: %w", name, err)
		}
		resolved[name] = val
	}
	return resolved, nil
}
```

Add `"fmt"` to imports.

**Step 4: Run tests to verify they pass**

Run: `go test -run TestResolveAll ./internal/secrets/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/secrets/resolver.go internal/secrets/resolver_test.go
git commit -m "feat(secrets): add ResolveAll helper for batch resolution"
```

---

## Task 5: Add audit entry type for secret resolution

**Files:**
- Modify: `internal/audit/entry.go`

**Step 1: Add EntrySecret type and SecretData struct**

Edit `internal/audit/entry.go`, add after line 21:

```go
const (
	EntryConsole    EntryType = "console"
	EntryNetwork    EntryType = "network"
	EntryCredential EntryType = "credential"
	EntrySecret     EntryType = "secret"
)
```

Add SecretData struct after CredentialData:

```go
// SecretData holds secret resolution entry data.
type SecretData struct {
	Name    string `json:"name"`    // env var name, e.g., "OPENAI_API_KEY"
	Backend string `json:"backend"` // e.g., "1password", "ssm"
	// Note: value is never logged
}
```

**Step 2: Run existing tests to ensure no regression**

Run: `go test ./internal/audit/`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/audit/entry.go
git commit -m "feat(audit): add secret resolution entry type"
```

---

## Task 6: Integrate secrets resolution into run manager

**Files:**
- Modify: `internal/run/manager.go`

**Step 1: Add secrets import and resolve secrets in Create**

Add import at top of `internal/run/manager.go`:

```go
import (
	// ... existing imports ...
	"github.com/andybons/moat/internal/secrets"
)
```

In the `Create` function, after the env vars are collected from config (around line 274-278), add secret resolution:

Find this block:
```go
	// Add config env vars
	if opts.Config != nil {
		for k, v := range opts.Config.Env {
			proxyEnv = append(proxyEnv, k+"="+v)
		}
	}
```

Add after it:

```go
	// Resolve and add secrets
	if opts.Config != nil && len(opts.Config.Secrets) > 0 {
		resolved, err := secrets.ResolveAll(ctx, opts.Config.Secrets)
		if err != nil {
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, err
		}
		for k, v := range resolved {
			proxyEnv = append(proxyEnv, k+"="+v)
		}
	}
```

**Step 2: Run existing tests to ensure no regression**

Run: `go test ./internal/run/`
Expected: PASS

**Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): resolve secrets from agent.yaml before container start"
```

---

## Task 7: Add config validation for secret/env key overlap

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`

**Step 1: Write test for overlap validation**

Add to `internal/config/config_test.go`:

```go
func TestLoad_SecretsEnvOverlap(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: claude
env:
  API_KEY: literal-value
secrets:
  API_KEY: op://Dev/Key/value
`
	if err := os.WriteFile(filepath.Join(dir, "agent.yaml"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error for overlapping env/secrets keys")
	}
	if !strings.Contains(err.Error(), "API_KEY") {
		t.Errorf("error should mention the overlapping key: %v", err)
	}
}
```

Add `"strings"` to imports if not present.

**Step 2: Run test to verify it fails**

Run: `go test -run TestLoad_SecretsEnvOverlap ./internal/config/`
Expected: FAIL (validation not implemented)

**Step 3: Add validation to Load function**

In `internal/config/config.go`, add validation after yaml.Unmarshal in the `Load` function:

```go
	// Check for overlapping env and secrets keys
	for key := range cfg.Secrets {
		if _, exists := cfg.Env[key]; exists {
			return nil, fmt.Errorf("key %q defined in both 'env' and 'secrets' - use one or the other", key)
		}
	}
```

**Step 4: Run test to verify it passes**

Run: `go test -run TestLoad_SecretsEnvOverlap ./internal/config/`
Expected: PASS

**Step 5: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): validate no overlap between env and secrets keys"
```

---

## Task 8: Add integration test

**Files:**
- Create: `internal/secrets/integration_test.go`

**Step 1: Write integration test (skipped without op)**

Create `internal/secrets/integration_test.go`:

```go
//go:build integration

package secrets

import (
	"context"
	"os/exec"
	"testing"
	"time"
)

func TestOnePasswordResolver_Integration(t *testing.T) {
	// Skip if op CLI not available
	if _, err := exec.LookPath("op"); err != nil {
		t.Skip("op CLI not installed, skipping integration test")
	}

	// Skip if not signed in (check with op whoami)
	cmd := exec.Command("op", "whoami")
	if err := cmd.Run(); err != nil {
		t.Skip("not signed in to 1Password, skipping integration test")
	}

	// This test requires a real 1Password item to exist.
	// Create a test item: op item create --category=login --title="Moat Test" --vault="Private" password=test-secret
	// Then set this reference:
	testRef := "op://Private/Moat Test/password"

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolver := &OnePasswordResolver{}
	val, err := resolver.Resolve(ctx, testRef)
	if err != nil {
		t.Fatalf("failed to resolve: %v", err)
	}

	if val == "" {
		t.Error("resolved value is empty")
	}

	t.Logf("Successfully resolved secret (length: %d)", len(val))
}
```

**Step 2: Verify test compiles**

Run: `go build ./internal/secrets/`
Expected: Success

**Step 3: Commit**

```bash
git add internal/secrets/integration_test.go
git commit -m "test(secrets): add 1Password integration test"
```

---

## Task 9: Run full test suite and verify

**Step 1: Run all tests**

Run: `go test ./...`
Expected: PASS

**Step 2: Run linter**

Run: `golangci-lint run` (if available)
Expected: No errors

**Step 3: Final commit for any fixes**

If any fixes needed, commit them.

---

## Summary

After completing all tasks, you will have:

1. `secrets:` field in `agent.yaml` config
2. `internal/secrets/` package with:
   - Pluggable `Resolver` interface
   - Registry pattern for scheme dispatch
   - `OnePasswordResolver` using `op read`
   - Typed errors with actionable messages
3. Validation preventing env/secrets key overlap
4. Integration into run lifecycle (resolve before container start)
5. Audit entry type for secret resolution (ready for logging)

To test manually:

```yaml
# agent.yaml
agent: claude
secrets:
  MY_SECRET: op://Private/TestItem/password
```

```bash
agent run my-agent .
# Should resolve the secret and inject as MY_SECRET env var
```
