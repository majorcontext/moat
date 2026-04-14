# gcloud Provider Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `moat grant gcloud` credential provider that authenticates the `gcloud` CLI and every Google client library inside a Moat sandbox, without leaking refresh tokens or service-account keys into the container.

**Architecture:** Mirror the AWS provider. The host daemon reads Google credentials from `~/.config/gcloud/application_default_credentials.json` (or a specified SA key / impersonation target), mints short-lived access tokens using `golang.org/x/oauth2/google`, caches them, and serves them via an HTTP endpoint on the proxy that emulates the GCE metadata server. The container is pointed at that endpoint via `GCE_METADATA_HOST`. No long-lived material ever enters the container.

**Tech Stack:** Go, `golang.org/x/oauth2/google`, existing `internal/provider` + `internal/daemon` + `internal/providers/aws` patterns, existing `internal/deps` gcloud install recipe.

**Why the AWS pattern, not GitHub/Claude:** gcloud always refreshes its own token locally before making API calls by POSTing to `oauth2.googleapis.com/token`. Pure Authorization-header injection at the TLS proxy is insufficient — we'd have to synthesize OAuth token-response bodies. The GCE metadata server is the native, documented extension point: set `GCE_METADATA_HOST=<proxy>` and both gcloud and every ADC client library happily fetch bearer tokens from us. This is structurally identical to AWS's ECS credential endpoint.

---

## Background reading (before starting)

- `/workspace/internal/providers/aws/endpoint.go` — reference shape of a credential endpoint handler (auth token gating, cache with `credentialRefreshBuffer`).
- `/workspace/internal/providers/aws/credential_provider.go` — reference for the host-side credential-fetching component used by the daemon.
- `/workspace/internal/providers/aws/provider.go` — CredentialProvider + EndpointProvider interface implementation, `init()` registration pattern.
- `/workspace/internal/providers/aws/grant.go` — grant UX shape, `WithGrantOptions` context pattern, storing config in `Credential.Metadata`.
- `/workspace/internal/daemon/runcontext.go` lines 39–76, 146–151, 414–416 — how AWS is plumbed into `RunContext` via `AWSConfig` field, `awsHandler` (`http.Handler`) field, and `SetAWSHandler`. We will add `GCloudConfig` and `gcloudHandler` in the same pattern.
- `/workspace/internal/daemon/server.go` lines 230–251 — where the daemon constructs the AWS handler at run-register time. Add parallel block for gcloud.
- `/workspace/internal/daemon/api.go` line 76 — `AWSConfig` field on `RegisterRequest`. We will add `GCloudConfig`. **Must be additive-only: the daemon API is backwards-compatible across binary versions (see `internal/daemon/api.go` package doc).**
- `/workspace/internal/daemon/persist.go` lines 34, 79, 208, 231–248 — daemon persistence: add `GCloudConfig` alongside `AWSConfig`.
- `/workspace/internal/proxy/proxy.go` lines 296, 338, 409–411, 987–1051, 1077–1083, 1107–1111 — how AWS handler is routed (`/_aws/credentials` path and the `/_aws/` direct-request path). We will not use a new proxy path — instead we will add a `GCloudHandler` field on `RunContextData` and route `/computeMetadata/` requests there.
- `/workspace/internal/run/manager.go` lines 660–998 — how AWS is set up per run (provider creation, daemon registration, container env injection, file helper, mount). Mirror for gcloud but **no files mounted**: just env vars.
- `/workspace/internal/providers/gemini/token_refresh.go` — an existing Google OAuth refresher, but uses Gemini's OAuth client, not gcloud's. We will NOT reuse it directly; we will use `golang.org/x/oauth2/google.FindDefaultCredentials` which handles user OAuth, SA keys, impersonation, and federation uniformly.
- `/workspace/internal/deps/registry.yaml` line 241 — `gcloud` dependency already exists. `ImpliedDependencies()` should return `["gcloud"]`.
- Google docs on the metadata server endpoints (for emulation): `https://cloud.google.com/compute/docs/metadata/default-metadata-values` and `https://cloud.google.com/compute/docs/metadata/overriding-metadata`.

## Relevant skills

- @superpowers:test-driven-development — required for every step below that writes code.
- @superpowers:verification-before-completion — required before committing each task.

## Scope

**In scope:**
- New `internal/providers/gcloud/` package.
- Daemon plumbing (`RunContext.GCloudConfig`, `gcloudHandler`, API field, persist field).
- Proxy routing for `/computeMetadata/` paths.
- Run manager wiring (container env vars).
- Tests for grant parsing, credential minting (with mocked token source), endpoint handler, and proxy routing.
- Documentation: `docs/content/reference/02-moat-yaml.md` and a new `docs/content/guides/` page for `moat grant gcloud`.
- CHANGELOG entry.

**Out of scope:**
- Per-request credential scoping by Google API service.
- Workload Identity Federation from non-GCP external sources (supported automatically if the host's ADC file is an `external_account` config, but we won't build CLI UX for it).
- Emulating the full metadata server (only the subset gcloud + ADC libraries need).
- Changes to the `gcloud` install recipe.

## File Structure

**Create:**
- `internal/providers/gcloud/doc.go` — package docs.
- `internal/providers/gcloud/provider.go` — `Provider` implementing `CredentialProvider` + `EndpointProvider`, `init()` registration.
- `internal/providers/gcloud/grant.go` — grant flow, config parsing, `WithGrantOptions`, `Config` struct, `ConfigFromCredential`.
- `internal/providers/gcloud/credential_provider.go` — host-side `CredentialProvider` struct with `GetToken(ctx)` cached token source. Internally wraps `google.FindDefaultCredentials` or a `credentials.json` file path.
- `internal/providers/gcloud/endpoint.go` — `http.Handler` that serves the metadata emulation routes.
- `internal/providers/gcloud/endpoint_test.go`
- `internal/providers/gcloud/grant_test.go`
- `internal/providers/gcloud/credential_provider_test.go`
- `internal/providers/gcloud/provider_test.go`
- `docs/content/guides/XX-gcloud.md` — user-facing guide.

**Modify:**
- `internal/providers/register.go` — add blank import of gcloud package.
- `internal/daemon/runcontext.go` — add `GCloudConfig *GCloudConfig` field (JSON tag `gcloud_config,omitempty`), `gcloudHandler http.Handler` field, `SetGCloudHandler` method, and wire into `ToProxyContextData()`.
- `internal/daemon/api.go` — add `GCloudConfig *GCloudConfig` field to `RegisterRequest` (additive only), copy to `RunContext` in `handleRegister`. **Document backwards-compat impact in the package doc comment.**
- `internal/daemon/server.go` — add block mirroring lines 230–251 that constructs `gcloudprov.NewCredentialProvider(...)` and calls `rc.SetGCloudHandler(...)`.
- `internal/daemon/persist.go` — add `GCloudConfig` to `persistedRun`, copy into/out of `RunContext`, reconstruct handler on daemon restart.
- `internal/daemon/persist_test.go` — round-trip test for `GCloudConfig`.
- `internal/daemon/api_test.go` — API request/response round-trip for `GCloudConfig`.
- `internal/proxy/proxy.go` — add `GCloudHandler http.Handler` to `RunContextData`, field on `Proxy`, `SetGCloudHandler`, `getGCloudHandlerForRequest`, and route `/computeMetadata/` paths to it in `ServeHTTP`. Mirror the `/_aws/` direct-request path for containers that reach the handler via `GCE_METADATA_HOST`.
- `internal/run/manager.go` — in the section that configures provider endpoints per run, add gcloud block that sets `GCE_METADATA_HOST=<proxyHost>`, `GCE_METADATA_IP=<proxyHost>`, `GOOGLE_CLOUD_PROJECT=<project>`, `CLOUDSDK_CORE_PROJECT=<project>`, `CLOUDSDK_AUTH_DISABLE_CREDENTIALS_FILE=true` (so gcloud itself uses metadata), and includes the per-run auth token if needed.
- `internal/run/run.go` — add `GCloudCredentialProvider *gcloudprov.CredentialProvider` field alongside `AWSCredentialProvider`.
- `docs/content/reference/02-moat-yaml.md` — document the new grant.
- `CHANGELOG.md` — add Added entry under next release, link to PR.
- `go.mod` / `go.sum` — `golang.org/x/oauth2` (likely already present transitively via AWS SDK; verify).

## Design decisions locked in

1. **Token source:** Use `golang.org/x/oauth2/google.FindDefaultCredentials(ctx, scopes...)` for the default path. This transparently handles user OAuth refresh, service account JWT-bearer, impersonation, and federation. For the MVP we accept whatever ADC the host has configured.
2. **Scopes:** Request `https://www.googleapis.com/auth/cloud-platform` by default. Allow override in grant metadata `scopes`.
3. **Project ID:** Required. Read from `gcloud config get-value project` at grant time, or accept `--project` flag. Store in credential metadata. The metadata server endpoint returns it to clients; some libraries require it.
4. **Endpoint auth:** The metadata server does not use bearer auth — but containers reach our handler via the proxy's per-run context resolution (same mechanism AWS uses for `/_aws/credentials` direct requests). We additionally require the `Metadata-Flavor: Google` header on every request, matching real metadata server behavior and blocking accidental DNS-rebinding hits.
5. **No files in container.** The container gets env vars only. This is strictly better than AWS's model (which writes a helper script and config file) and is possible because Google's stack natively supports the `GCE_METADATA_HOST` override.
6. **Token caching:** Same shape as AWS — cache in the `CredentialProvider`, refresh `credentialRefreshBuffer` (5 min) before expiry, under a mutex. Reuse the constant from `aws` package? No — duplicate it locally to avoid coupling.
7. **Metadata endpoints to implement (minimum viable set):**
   - `GET /computeMetadata/v1/instance/service-accounts/default/token` → `{"access_token":"…","expires_in":<int>,"token_type":"Bearer"}`
   - `GET /computeMetadata/v1/instance/service-accounts/default/email` → SA email (from credentials; synthesized as `user@moat.local` for user OAuth)
   - `GET /computeMetadata/v1/instance/service-accounts/default/scopes` → newline-separated scope list
   - `GET /computeMetadata/v1/instance/service-accounts/default/aliases` → `default`
   - `GET /computeMetadata/v1/instance/service-accounts/default/` → `aliases\nemail\nidentity\nscopes\ntoken\n`
   - `GET /computeMetadata/v1/instance/service-accounts/` → `default/\n<email>/\n`
   - `GET /computeMetadata/v1/project/project-id` → the configured project ID
   - `GET /computeMetadata/v1/project/numeric-project-id` → `0` (most libs accept this)
   - `GET /` and `GET /computeMetadata/` → 200 with `Metadata-Flavor: Google` header for liveness probes
   - All responses MUST include `Metadata-Flavor: Google` header. All requests MUST have `Metadata-Flavor: Google` header or we return 403.
   - ID token endpoint (`.../identity?audience=…`) is deferred; return 404.

---

## Tasks

### Task 1: Package skeleton + provider registration

**Files:**
- Create: `internal/providers/gcloud/doc.go`
- Create: `internal/providers/gcloud/provider.go`
- Create: `internal/providers/gcloud/provider_test.go`
- Modify: `internal/providers/register.go`

- [ ] **Step 1: Write failing test for provider name and registration**

```go
// provider_test.go
package gcloud

import (
	"testing"
	"github.com/majorcontext/moat/internal/provider"
)

func TestProviderName(t *testing.T) {
	p := New()
	if p.Name() != "gcloud" {
		t.Errorf("Name() = %q, want %q", p.Name(), "gcloud")
	}
}

func TestProviderRegistered(t *testing.T) {
	if _, ok := provider.Get("gcloud"); !ok {
		t.Error("gcloud provider not registered")
	}
}

func TestImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()
	if len(deps) != 1 || deps[0] != "gcloud" {
		t.Errorf("ImpliedDependencies() = %v, want [gcloud]", deps)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/gcloud/...`
Expected: build failure (package does not exist).

- [ ] **Step 3: Create doc.go**

```go
// Package gcloud implements a credential provider for Google Cloud.
//
// Unlike header-injection providers (GitHub, Claude), gcloud follows the
// AWS model: the host daemon mints short-lived access tokens using the
// host's Application Default Credentials and serves them to the container
// via a GCE metadata server emulator. The container is pointed at the
// emulator via GCE_METADATA_HOST. No long-lived credentials enter the
// container.
package gcloud
```

- [ ] **Step 4: Create minimal provider.go**

```go
package gcloud

import (
	"context"
	"net/http"

	"github.com/majorcontext/moat/internal/provider"
)

type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.EndpointProvider   = (*Provider)(nil)
)

func New() *Provider { return &Provider{} }

func init() { provider.Register(New()) }

func (p *Provider) Name() string { return "gcloud" }

func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return grant(ctx)
}

func (p *Provider) ConfigureProxy(pc provider.ProxyConfigurer, cred *provider.Credential) {
	// No-op: gcloud uses metadata endpoint, not header injection.
}

func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	// Env vars are injected by the run manager since they depend on the
	// per-run proxy host:port.
	return nil
}

func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

func (p *Provider) Cleanup(cleanupPath string) {}

func (p *Provider) ImpliedDependencies() []string { return []string{"gcloud"} }

func (p *Provider) RegisterEndpoints(mux *http.ServeMux, cred *provider.Credential) {
	// Metadata emulation is served by a per-run handler attached to the
	// RunContext at daemon-register time, not via this package-level mux.
}
```

- [ ] **Step 5: Add blank import to `internal/providers/register.go`**

Add line in the import block:

```go
_ "github.com/majorcontext/moat/internal/providers/gcloud" // registers gcloud provider
```

Maintain alphabetical order.

- [ ] **Step 6: Add temporary stub grant function**

Create `internal/providers/gcloud/grant.go`:

```go
package gcloud

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

func grant(ctx context.Context) (*provider.Credential, error) {
	return nil, errors.New("gcloud grant: not yet implemented")
}
```

- [ ] **Step 7: Run tests**

Run: `go test ./internal/providers/gcloud/...`
Expected: PASS (3 tests).

- [ ] **Step 8: Run full build**

Run: `go build ./...`
Expected: no errors.

- [ ] **Step 9: Commit**

```bash
git add internal/providers/gcloud/ internal/providers/register.go
git commit -m "feat(gcloud): add provider skeleton and registration"
```

### Task 2: Config + grant parsing

**Files:**
- Modify: `internal/providers/gcloud/grant.go`
- Create/extend: `internal/providers/gcloud/grant_test.go`

- [ ] **Step 1: Write failing tests**

```go
// grant_test.go
package gcloud

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestConfigFromCredential(t *testing.T) {
	cred := &provider.Credential{
		Provider: "gcloud",
		Token:    "", // gcloud has no primary token; project is in metadata
		Metadata: map[string]string{
			MetaKeyProject:     "my-proj",
			MetaKeyScopes:      "https://www.googleapis.com/auth/cloud-platform",
			MetaKeyImpersonate: "sa@my-proj.iam.gserviceaccount.com",
			MetaKeyKeyFile:     "",
		},
	}
	cfg, err := ConfigFromCredential(cred)
	if err != nil {
		t.Fatalf("ConfigFromCredential: %v", err)
	}
	if cfg.ProjectID != "my-proj" {
		t.Errorf("ProjectID = %q", cfg.ProjectID)
	}
	if cfg.ImpersonateSA != "sa@my-proj.iam.gserviceaccount.com" {
		t.Errorf("ImpersonateSA = %q", cfg.ImpersonateSA)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("Scopes = %v", cfg.Scopes)
	}
}

func TestConfigFromCredentialDefaultScope(t *testing.T) {
	cred := &provider.Credential{
		Provider: "gcloud",
		Metadata: map[string]string{MetaKeyProject: "p"},
	}
	cfg, _ := ConfigFromCredential(cred)
	if len(cfg.Scopes) == 0 {
		t.Error("expected default scope when none specified")
	}
}

func TestConfigFromCredentialMissingProject(t *testing.T) {
	cred := &provider.Credential{Provider: "gcloud", Metadata: map[string]string{}}
	_, err := ConfigFromCredential(cred)
	if err == nil {
		t.Error("expected error when project is missing")
	}
}
```

- [ ] **Step 2: Run — expected to fail (constants/types don't exist)**

Run: `go test ./internal/providers/gcloud/ -run TestConfig`
Expected: FAIL to compile.

- [ ] **Step 3: Implement grant.go**

```go
package gcloud

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

const (
	MetaKeyProject     = "project"
	MetaKeyScopes      = "scopes"
	MetaKeyImpersonate = "impersonate_service_account"
	MetaKeyKeyFile     = "key_file"
	MetaKeyEmail       = "email"
)

const DefaultScope = "https://www.googleapis.com/auth/cloud-platform"

type ctxKey string

const (
	ctxKeyProject     ctxKey = "gcloud_project"
	ctxKeyScopes      ctxKey = "gcloud_scopes"
	ctxKeyImpersonate ctxKey = "gcloud_impersonate"
	ctxKeyKeyFile     ctxKey = "gcloud_key_file"
)

func WithGrantOptions(ctx context.Context, project, scopes, impersonate, keyFile string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyProject, project)
	ctx = context.WithValue(ctx, ctxKeyScopes, scopes)
	ctx = context.WithValue(ctx, ctxKeyImpersonate, impersonate)
	ctx = context.WithValue(ctx, ctxKeyKeyFile, keyFile)
	return ctx
}

type Config struct {
	ProjectID     string
	Scopes        []string
	ImpersonateSA string
	KeyFile       string // Path to a service account JSON key file on the host
	Email         string // Best-effort service account / principal email for metadata endpoint
}

func grant(ctx context.Context) (*provider.Credential, error) {
	cfg := &Config{Scopes: []string{DefaultScope}}

	if v, _ := ctx.Value(ctxKeyProject).(string); v != "" {
		cfg.ProjectID = v
	}
	if v, _ := ctx.Value(ctxKeyScopes).(string); v != "" {
		cfg.Scopes = splitScopes(v)
	}
	if v, _ := ctx.Value(ctxKeyImpersonate).(string); v != "" {
		cfg.ImpersonateSA = v
	}
	if v, _ := ctx.Value(ctxKeyKeyFile).(string); v != "" {
		cfg.KeyFile = v
	}

	if cfg.ProjectID == "" {
		// Try `gcloud config get-value project` on the host.
		cfg.ProjectID = detectProject()
	}
	if cfg.ProjectID == "" {
		p, err := util.PromptForToken("Enter GCP project ID")
		if err != nil {
			return nil, &provider.GrantError{
				Provider: "gcloud",
				Cause:    err,
				Hint:     "Set with --project or run `gcloud config set project <id>` on the host.",
			}
		}
		cfg.ProjectID = p
	}

	// Validate: the daemon will also do this at runtime, but catch misconfig early.
	if err := testCredentials(ctx, cfg); err != nil {
		return nil, &provider.GrantError{
			Provider: "gcloud",
			Cause:    err,
			Hint: "Ensure Application Default Credentials are configured on the host:\n" +
				"  gcloud auth application-default login\n" +
				"Or pass --key-file with a service account JSON key.\n" +
				"See: https://majorcontext.com/moat/guides/gcloud",
		}
	}

	cred := &provider.Credential{
		Provider:  "gcloud",
		Token:     "",
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			MetaKeyProject: cfg.ProjectID,
			MetaKeyScopes:  strings.Join(cfg.Scopes, " "),
		},
	}
	if cfg.ImpersonateSA != "" {
		cred.Metadata[MetaKeyImpersonate] = cfg.ImpersonateSA
	}
	if cfg.KeyFile != "" {
		cred.Metadata[MetaKeyKeyFile] = cfg.KeyFile
	}
	if cfg.Email != "" {
		cred.Metadata[MetaKeyEmail] = cfg.Email
	}
	return cred, nil
}

func splitScopes(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == ' ' || r == ',' })
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return []string{DefaultScope}
	}
	return out
}

func detectProject() string {
	if v := os.Getenv("GOOGLE_CLOUD_PROJECT"); v != "" {
		return v
	}
	if v := os.Getenv("CLOUDSDK_CORE_PROJECT"); v != "" {
		return v
	}
	out, err := exec.Command("gcloud", "config", "get-value", "project").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func ConfigFromCredential(cred *provider.Credential) (*Config, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}
	cfg := &Config{}
	if cred.Metadata != nil {
		cfg.ProjectID = cred.Metadata[MetaKeyProject]
		if s := cred.Metadata[MetaKeyScopes]; s != "" {
			cfg.Scopes = splitScopes(s)
		}
		cfg.ImpersonateSA = cred.Metadata[MetaKeyImpersonate]
		cfg.KeyFile = cred.Metadata[MetaKeyKeyFile]
		cfg.Email = cred.Metadata[MetaKeyEmail]
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{DefaultScope}
	}
	if cfg.ProjectID == "" {
		return nil, fmt.Errorf("gcloud credential is missing project ID")
	}
	return cfg, nil
}

// testCredentials is filled in Task 3 once CredentialProvider exists.
// For now, just return nil so the grant flow is testable.
func testCredentials(ctx context.Context, cfg *Config) error { return nil }
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/providers/gcloud/...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/gcloud/grant.go internal/providers/gcloud/grant_test.go
git commit -m "feat(gcloud): parse grant config and credential metadata"
```

### Task 3: Host-side CredentialProvider using oauth2/google

**Files:**
- Create: `internal/providers/gcloud/credential_provider.go`
- Create: `internal/providers/gcloud/credential_provider_test.go`
- Modify: `go.mod`, `go.sum` (likely adds `golang.org/x/oauth2/google`)

- [ ] **Step 1: Write failing test using a fake token source**

```go
// credential_provider_test.go
package gcloud

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type fakeTokenSource struct {
	tok  *oauth2.Token
	err  error
	hits int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.hits++
	return f.tok, f.err
}

func TestCredentialProviderReturnsToken(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour)
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "ya29.fake", Expiry: exp}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	tok, err := p.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok.AccessToken != "ya29.fake" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestCredentialProviderCaches(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour)
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: exp}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	for i := 0; i < 5; i++ {
		_, _ = p.GetToken(context.Background())
	}
	if fts.hits > 1 {
		t.Errorf("expected caching, token source hit %d times", fts.hits)
	}
}

func TestCredentialProviderRefreshesOnExpiry(t *testing.T) {
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(1 * time.Minute)}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	_, _ = p.GetToken(context.Background())
	_, _ = p.GetToken(context.Background())
	if fts.hits < 2 {
		t.Errorf("expected refresh within buffer window, hits=%d", fts.hits)
	}
}
```

- [ ] **Step 2: Run — should fail to compile**

Run: `go test ./internal/providers/gcloud/ -run TestCredentialProvider`
Expected: build failure.

- [ ] **Step 3: Implement credential_provider.go**

```go
package gcloud

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/impersonate"
	"google.golang.org/api/option"
)

// credentialRefreshBuffer is the time before expiration when the cached
// access token is considered stale and must be refreshed.
const credentialRefreshBuffer = 5 * time.Minute

// CredentialProvider wraps an oauth2.TokenSource with caching and metadata
// for serving via the metadata emulation endpoint.
type CredentialProvider struct {
	cfg    *Config
	source oauth2.TokenSource

	mu     sync.Mutex
	cached *oauth2.Token
}

// NewCredentialProvider builds a CredentialProvider from host-side config.
// It honors (in order): explicit service-account key file, impersonation
// target over ADC source, and plain Application Default Credentials.
func NewCredentialProvider(ctx context.Context, cfg *Config) (*CredentialProvider, error) {
	source, err := buildTokenSource(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &CredentialProvider{cfg: cfg, source: source}, nil
}

// NewCredentialProviderFromTokenSource is used by tests to inject a fake.
func NewCredentialProviderFromTokenSource(ts oauth2.TokenSource, cfg *Config) *CredentialProvider {
	return &CredentialProvider{cfg: cfg, source: ts}
}

func buildTokenSource(ctx context.Context, cfg *Config) (oauth2.TokenSource, error) {
	if cfg.KeyFile != "" {
		data, err := os.ReadFile(cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("reading key file: %w", err)
		}
		creds, err := google.CredentialsFromJSON(ctx, data, cfg.Scopes...)
		if err != nil {
			return nil, fmt.Errorf("parsing key file: %w", err)
		}
		if cfg.ImpersonateSA != "" {
			return impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
				TargetPrincipal: cfg.ImpersonateSA,
				Scopes:          cfg.Scopes,
			}, option.WithCredentials(creds))
		}
		return creds.TokenSource, nil
	}

	if cfg.ImpersonateSA != "" {
		return impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: cfg.ImpersonateSA,
			Scopes:          cfg.Scopes,
		})
	}

	creds, err := google.FindDefaultCredentials(ctx, cfg.Scopes...)
	if err != nil {
		return nil, fmt.Errorf("finding application default credentials: %w", err)
	}
	return creds.TokenSource, nil
}

// GetToken returns a cached access token, refreshing if near expiry.
func (p *CredentialProvider) GetToken(ctx context.Context) (*oauth2.Token, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Until(p.cached.Expiry) > credentialRefreshBuffer {
		return p.cached, nil
	}
	tok, err := p.source.Token()
	if err != nil {
		return nil, fmt.Errorf("fetching gcloud token: %w", err)
	}
	p.cached = tok
	return tok, nil
}

// ProjectID returns the configured GCP project ID.
func (p *CredentialProvider) ProjectID() string { return p.cfg.ProjectID }

// Scopes returns the configured scopes.
func (p *CredentialProvider) Scopes() []string { return p.cfg.Scopes }

// Email returns a best-effort principal email for the metadata endpoint.
// Returns "default@moat.local" if unknown.
func (p *CredentialProvider) Email() string {
	if p.cfg.Email != "" {
		return p.cfg.Email
	}
	return "default@moat.local"
}
```

- [ ] **Step 4: Wire testCredentials() in grant.go**

Replace the stub with:

```go
func testCredentials(ctx context.Context, cfg *Config) error {
	p, err := NewCredentialProvider(ctx, cfg)
	if err != nil {
		return err
	}
	_, err = p.GetToken(ctx)
	return err
}
```

- [ ] **Step 5: Tidy modules**

Run: `go mod tidy`
Expected: adds `golang.org/x/oauth2` (may already be present), `google.golang.org/api/impersonate`, `google.golang.org/api/option`.

- [ ] **Step 6: Run tests**

Run: `go test ./internal/providers/gcloud/...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/providers/gcloud/ go.mod go.sum
git commit -m "feat(gcloud): host-side credential provider with token caching"
```

### Task 4: Metadata emulation HTTP handler

**Files:**
- Create: `internal/providers/gcloud/endpoint.go`
- Create: `internal/providers/gcloud/endpoint_test.go`

- [ ] **Step 1: Write failing tests for every endpoint**

```go
// endpoint_test.go
package gcloud

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func newTestHandler(t *testing.T) *EndpointHandler {
	t.Helper()
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "ya29.x", Expiry: time.Now().Add(time.Hour)}}
	cp := NewCredentialProviderFromTokenSource(fts, &Config{
		ProjectID: "test-proj",
		Scopes:    []string{DefaultScope},
	})
	return NewEndpointHandler(cp)
}

func doReq(h http.Handler, path string, withFlavor bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if withFlavor {
		r.Header.Set("Metadata-Flavor", "Google")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestMetadataRequiresFlavorHeader(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/token", false)
	if w.Code != http.StatusForbidden {
		t.Errorf("missing Metadata-Flavor: got %d, want 403", w.Code)
	}
}

func TestMetadataToken(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/token", true)
	if w.Code != 200 {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
	if w.Header().Get("Metadata-Flavor") != "Google" {
		t.Error("missing response Metadata-Flavor header")
	}
	var body struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		TokenType   string `json:"token_type"`
	}
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.AccessToken != "ya29.x" || body.TokenType != "Bearer" || body.ExpiresIn <= 0 {
		t.Errorf("unexpected body: %+v", body)
	}
}

func TestMetadataProjectID(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/project/project-id", true)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "test-proj" {
		t.Errorf("project-id: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestMetadataNumericProjectID(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/project/numeric-project-id", true)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "0" {
		t.Errorf("numeric-project-id: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestMetadataScopes(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/scopes", true)
	if w.Code != 200 || !strings.Contains(w.Body.String(), DefaultScope) {
		t.Errorf("scopes: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestMetadataEmail(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/email", true)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != "default@moat.local" {
		t.Errorf("email: code=%d body=%q", w.Code, w.Body.String())
	}
}

func TestMetadataServiceAccountsDirListing(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/", true)
	body, _ := io.ReadAll(w.Body)
	if w.Code != 200 || !strings.Contains(string(body), "token") {
		t.Errorf("dir listing: code=%d body=%q", w.Code, body)
	}
}

func TestMetadataLiveness(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/", true)
	if w.Code != 200 {
		t.Errorf("liveness: code=%d", w.Code)
	}
	if w.Header().Get("Metadata-Flavor") != "Google" {
		t.Error("missing Metadata-Flavor on liveness")
	}
}

func TestMetadataIdentityNotImplemented(t *testing.T) {
	h := newTestHandler(t)
	w := doReq(h, "/computeMetadata/v1/instance/service-accounts/default/identity?audience=x", true)
	if w.Code != http.StatusNotFound {
		t.Errorf("identity: code=%d, want 404", w.Code)
	}
}

func TestMetadataTokenContextCancel(t *testing.T) {
	h := newTestHandler(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/token", nil).WithContext(ctx)
	r.Header.Set("Metadata-Flavor", "Google")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code == 200 {
		t.Error("expected error on canceled context")
	}
}
```

- [ ] **Step 2: Run tests — should fail to compile**

Run: `go test ./internal/providers/gcloud/ -run TestMetadata`
Expected: FAIL to compile (no EndpointHandler).

- [ ] **Step 3: Implement endpoint.go**

```go
package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// EndpointHandler serves the subset of the GCE metadata server required by
// gcloud and the Google client libraries. All responses include
// `Metadata-Flavor: Google` and all requests must include it.
type EndpointHandler struct {
	cp *CredentialProvider
}

func NewEndpointHandler(cp *CredentialProvider) *EndpointHandler {
	return &EndpointHandler{cp: cp}
}

func (h *EndpointHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Metadata-Flavor") != "Google" {
		http.Error(w, "Metadata-Flavor header required", http.StatusForbidden)
		return
	}
	w.Header().Set("Metadata-Flavor", "Google")

	path := r.URL.Path

	switch {
	case path == "/" || path == "/computeMetadata/" || path == "/computeMetadata/v1/":
		w.WriteHeader(http.StatusOK)
		return

	case path == "/computeMetadata/v1/instance/service-accounts/default/token":
		h.serveToken(w, r)

	case path == "/computeMetadata/v1/instance/service-accounts/default/email":
		fmt.Fprintln(w, h.cp.Email())

	case path == "/computeMetadata/v1/instance/service-accounts/default/scopes":
		for _, s := range h.cp.Scopes() {
			fmt.Fprintln(w, s)
		}

	case path == "/computeMetadata/v1/instance/service-accounts/default/aliases":
		fmt.Fprintln(w, "default")

	case path == "/computeMetadata/v1/instance/service-accounts/default/":
		fmt.Fprint(w, "aliases\nemail\nidentity\nscopes\ntoken\n")

	case path == "/computeMetadata/v1/instance/service-accounts/":
		fmt.Fprintf(w, "default/\n%s/\n", h.cp.Email())

	case path == "/computeMetadata/v1/project/project-id":
		fmt.Fprint(w, h.cp.ProjectID())

	case path == "/computeMetadata/v1/project/numeric-project-id":
		fmt.Fprint(w, "0")

	case strings.HasPrefix(path, "/computeMetadata/v1/instance/service-accounts/default/identity"):
		// ID tokens not yet supported.
		http.Error(w, "identity tokens not supported by moat metadata emulator", http.StatusNotFound)

	default:
		http.Error(w, "not found", http.StatusNotFound)
	}
}

func (h *EndpointHandler) serveToken(w http.ResponseWriter, r *http.Request) {
	tok, err := h.cp.GetToken(r.Context())
	if err != nil {
		log.Error("gcloud token fetch error", "error", err)
		msg := classifyError(err)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}

	expiresIn := int(time.Until(tok.Expiry).Seconds())
	if expiresIn < 0 {
		expiresIn = 0
	}
	resp := map[string]any{
		"access_token": tok.AccessToken,
		"expires_in":   expiresIn,
		"token_type":   "Bearer",
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Warn("failed to encode gcloud token response", "error", err)
	}
}

func classifyError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "could not find default credentials"):
		return `gcloud credential error: no host credentials found

The moat daemon cannot find Google Cloud credentials on the host.
Ensure one of these is configured:
  - gcloud auth application-default login
  - GOOGLE_APPLICATION_CREDENTIALS=<path to service account JSON>
  - A service account key passed via 'moat grant gcloud --key-file'

Run 'gcloud auth application-default print-access-token' on your host to verify.`
	case strings.Contains(msg, "invalid_grant"):
		return `gcloud credential error: refresh token revoked or expired

Re-authenticate on the host:
  gcloud auth application-default login`
	case strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "context canceled"):
		return "gcloud credential error: request canceled or timed out."
	default:
		return "gcloud credential error: see daemon log at ~/.moat/debug/daemon.log"
	}
}

// Compile-time check that the handler implements http.Handler.
var _ http.Handler = (*EndpointHandler)(nil)

// Silence unused import if context is not directly referenced in some builds.
var _ = context.Background
```

- [ ] **Step 4: Run the full test file**

Run: `go test ./internal/providers/gcloud/...`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/gcloud/endpoint.go internal/providers/gcloud/endpoint_test.go
git commit -m "feat(gcloud): metadata server emulation handler"
```

### Task 5: Daemon plumbing

**Files:**
- Modify: `internal/daemon/runcontext.go`
- Modify: `internal/daemon/api.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/persist.go`
- Modify: `internal/daemon/persist_test.go`
- Modify: `internal/daemon/api_test.go`

- [ ] **Step 1: Read the current `api.go` package doc about backwards compatibility**

Run: `Read internal/daemon/api.go` lines 1–40.
Confirm: additive-only rule for API fields.

- [ ] **Step 2: Write failing test for RunContext round-trip**

In `internal/daemon/persist_test.go`, after the AWSConfig test block, add:

```go
func TestGCloudConfigPersist(t *testing.T) {
	rc := NewRunContext("run-gcloud")
	rc.GCloudConfig = &GCloudConfig{
		ProjectID: "p",
		Scopes:    []string{"https://www.googleapis.com/auth/cloud-platform"},
	}
	// ... round-trip via the persister, mirroring existing AWSConfig test
}
```

Fill in the body by copying the existing `AWSConfig` test shape from lines 20-90.

- [ ] **Step 3: Write failing test for API round-trip**

In `internal/daemon/api_test.go`, add a gcloud parallel to the AWSConfig test around line 120.

- [ ] **Step 4: Run — should fail to compile**

Run: `go test ./internal/daemon/... -run GCloud`
Expected: FAIL.

- [ ] **Step 5: Add `GCloudConfig` type and plumbing to `runcontext.go`**

Add after `AWSConfig` struct (~line 46):

```go
// GCloudConfig holds Google Cloud credential provider configuration.
// All long-lived credential material stays on the host; the container
// only sees short-lived access tokens served via metadata emulation.
type GCloudConfig struct {
	ProjectID     string   `json:"project_id"`
	Scopes        []string `json:"scopes,omitempty"`
	ImpersonateSA string   `json:"impersonate_service_account,omitempty"`
	KeyFile       string   `json:"key_file,omitempty"`
	Email         string   `json:"email,omitempty"`
}
```

Add to `RunContext` struct near `AWSConfig`:

```go
GCloudConfig *GCloudConfig `json:"gcloud_config,omitempty"`
```

Add private field next to `awsHandler`:

```go
gcloudHandler http.Handler `json:"-"`
```

Add method mirroring `SetAWSHandler`:

```go
// SetGCloudHandler stores the gcloud metadata endpoint handler for this run.
func (rc *RunContext) SetGCloudHandler(h http.Handler) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.gcloudHandler = h
}
```

In `ToProxyContextData()`, near line 415 where `d.AWSHandler = rc.awsHandler`, add:

```go
d.GCloudHandler = rc.gcloudHandler
```

- [ ] **Step 6: Add API field**

In `internal/daemon/api.go` near `AWSConfig` (~line 76), add:

```go
GCloudConfig *GCloudConfig `json:"gcloud_config,omitempty"`
```

In the handler (`handleRegister`), next to `rc.AWSConfig = req.AWSConfig` (~line 146), add:

```go
rc.GCloudConfig = req.GCloudConfig
```

Update the package doc to note that `GCloudConfig` is the new additive field.

- [ ] **Step 7: Add daemon server construction**

In `internal/daemon/server.go`, after the AWS block (after line 251), add:

```go
if req.GCloudConfig != nil {
	gcloudCfg := &gcloudprov.Config{
		ProjectID:     req.GCloudConfig.ProjectID,
		Scopes:        req.GCloudConfig.Scopes,
		ImpersonateSA: req.GCloudConfig.ImpersonateSA,
		KeyFile:       req.GCloudConfig.KeyFile,
		Email:         req.GCloudConfig.Email,
	}
	cp, err := gcloudprov.NewCredentialProvider(runCtx, gcloudCfg)
	if err != nil {
		log.Warn("failed to create gcloud credential provider for run",
			"run_id", rc.RunID, "error", err)
	} else {
		rc.SetGCloudHandler(gcloudprov.NewEndpointHandler(cp))
	}
}
```

Add import at the top of the file:

```go
gcloudprov "github.com/majorcontext/moat/internal/providers/gcloud"
```

- [ ] **Step 8: Add persistence**

In `internal/daemon/persist.go`:

- Line 34: add `GCloudConfig *GCloudConfig \`json:"gcloud_config,omitempty"\`` to `persistedRun`.
- Line 79: in the save loop, add `GCloudConfig: rc.GCloudConfig,`.
- Line 208: in restore, add `rc.GCloudConfig = pr.GCloudConfig`.
- After line 248 (the AWS handler reconstruction block), add a parallel block that rebuilds the gcloud handler via `gcloudprov.NewCredentialProvider(...)` + `NewEndpointHandler(...)`.

- [ ] **Step 9: Run tests**

Run: `go test ./internal/daemon/...`
Expected: PASS including the new GCloud tests.

- [ ] **Step 10: Commit**

```bash
git add internal/daemon/
git commit -m "feat(gcloud): daemon run context and persistence plumbing"
```

### Task 6: Proxy routing for /computeMetadata/

**Files:**
- Modify: `internal/proxy/proxy.go`
- Create test: inline in existing `internal/proxy/mcp_regression_test.go` or new `proxy_gcloud_test.go`.

- [ ] **Step 1: Write failing test**

Create `internal/proxy/proxy_gcloud_test.go`:

```go
package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGCloudMetadataRouting(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})

	p, err := New(Config{
		Port:         0,
		GCloudHandler: mock,
	})
	if err != nil {
		t.Fatal(err)
	}

	// Simulate a direct request to /computeMetadata/... from inside the container.
	req := httptest.NewRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	req.URL.Host = "" // direct, not proxied
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Error("expected gcloud handler to be called, got %d", w.Code)
	}
}
```

Note: the test uses the `Proxy` config shape that exists today (`AWSHandler` on the Config struct at line 296). Follow the exact same pattern.

- [ ] **Step 2: Run the test — expected to fail**

Run: `go test ./internal/proxy/ -run TestGCloudMetadata`
Expected: FAIL to compile (no `GCloudHandler` field).

- [ ] **Step 3: Add `GCloudHandler` to `Config`**

In `internal/proxy/proxy.go` near line 296:

```go
GCloudHandler http.Handler
```

- [ ] **Step 4: Add field to `Proxy` struct (~line 338)**

```go
gcloudHandler http.Handler // Optional handler for gcloud metadata emulation
```

Initialize in the `New()` constructor next to `awsHandler: cfg.AWSHandler,`.

- [ ] **Step 5: Add `SetGCloudHandler` method (~line 411)**

```go
func (p *Proxy) SetGCloudHandler(h http.Handler) {
	p.gcloudHandler = h
}
```

- [ ] **Step 6: Add `GCloudHandler` to `RunContextData`**

Locate the struct (it's the type used in `getRunContext(r)`). Add:

```go
GCloudHandler http.Handler
```

- [ ] **Step 7: Add getter helper (near `getAWSHandlerForRequest` ~line 989)**

```go
func (p *Proxy) getGCloudHandlerForRequest(r *http.Request) http.Handler {
	if rc := getRunContext(r); rc != nil && rc.GCloudHandler != nil {
		return rc.GCloudHandler
	}
	return p.gcloudHandler
}
```

- [ ] **Step 8: Add direct-request handler (mirror `handleDirectAWSCredentials` ~line 1029)**

The metadata server does not use bearer auth, so we resolve run context by a different mechanism. Two options:

- **Option A (recommended, matches how containers reach the proxy anyway):** Route `/computeMetadata/` only from proxied requests (r.URL.Host empty + per-run context already resolved via proxy token). Since the container points at `GCE_METADATA_HOST=<proxyHost>:<proxyPort>` with the normal `HTTP_PROXY` auth token as basic-auth on the `GCE_METADATA_HOST` env var (gcloud supports `http://user:token@host:port` form? — **verify**, if not fall back to option B).

- **Option B (safer, always works):** Include the run's auth token in the URL path: `GCE_METADATA_HOST=<proxyHost>:<proxyPort>` and set `GOOGLE_AUTH_TOKEN_URL=http://<proxyHost>/computeMetadata/... `? That doesn't work — gcloud hard-codes the metadata path.
  Instead: register a dedicated port per run in the daemon for metadata, OR resolve the run by the source container IP (the proxy already knows container IP → run mapping via registry). **Use container IP resolution.**

  Check `internal/daemon/registry.go` / `internal/proxy/proxy.go` for existing container-IP-to-run lookup. If one exists, use it here. If not, add a `contextResolverByIP(ip)` helper.

Before writing code: **Step 8a** is a 10-minute spike to determine which option works. Read `internal/daemon/registry.go` and search for any container-IP-based resolution.

Run: `Grep ContainerID|containerIP|resolveByIP internal/daemon/ internal/proxy/`

If IP-based resolution exists, use it. If not, implement the simpler approach: embed the auth token in a path prefix, e.g. `/_gcloud/<token>/computeMetadata/v1/...`, and set `GCE_METADATA_HOST=<proxyHost>:<port>/_gcloud/<token>`. **Gotcha:** `GCE_METADATA_HOST` is hostname:port only, it does not accept a path. So this path-prefix approach does NOT work.

**Resolution:** The proxy is already running in front of the container with `HTTP_PROXY`. When a container makes a GET to `http://metadata.google.internal/computeMetadata/v1/...`, the Google client libraries honor `HTTP_PROXY`. The request reaches our proxy with `Proxy-Authorization: Basic <token>` already set. So the existing per-run context resolution via proxy token works — we just need to NOT set `GCE_METADATA_HOST` (leave it at `metadata.google.internal`) and instead ensure `metadata.google.internal` is routed to the proxy and responses are handled by `gcloudHandler`.

BUT: the normal moat TLS-intercepting proxy would try to CONNECT to the real metadata server. We need an early bypass that says "if the request host is metadata.google.internal or 169.254.169.254, serve locally from `gcloudHandler` instead of forwarding." This is cleaner than inventing a URL scheme.

**Final decision:** In `ServeHTTP`, before the CONNECT handling, add:

```go
if r.Host == "metadata.google.internal" || strings.HasPrefix(r.Host, "169.254.169.254") {
	if h := p.getGCloudHandlerForRequest(r); h != nil {
		h.ServeHTTP(w, r)
		return
	}
}
```

Metadata is plain HTTP (no HTTPS), so there is no CONNECT — these are regular proxied GETs the proxy receives in `ServeHTTP`. The per-run `RunContextData` has already been set by the contextResolver above (line 1089–1101) at this point in the flow, so `getRunContext(r)` works.

Put this block **after** the contextResolver block (~line 1105) but **before** the existing AWS handler check (~line 1107), so run context is populated.

- [ ] **Step 9: Implement the routing**

Make the code change described in step 8 final decision. Drop the `/_gcloud/` path ideas.

Also do NOT set `GCE_METADATA_HOST` in the container. Instead: (a) do nothing, letting the normal metadata hostname resolve into the proxy via `HTTP_PROXY`; (b) ensure the proxy does not reject `metadata.google.internal` in any allow-list.

- [ ] **Step 10: Update the test**

Rewrite the test to set `req.Host = "metadata.google.internal"` instead of an empty host and an absolute `/computeMetadata/...` path. Actually the full proxied-GET form: method `GET`, `URL = http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token`, headers `Metadata-Flavor: Google`.

Also test that without `Metadata-Flavor` the handler returns 403 (delegated to the gcloud endpoint).

- [ ] **Step 11: Run tests**

Run: `go test ./internal/proxy/ -run TestGCloudMetadata`
Expected: PASS.

Run full proxy tests to ensure no regressions: `go test ./internal/proxy/...`
Expected: PASS.

- [ ] **Step 12: Commit**

```bash
git add internal/proxy/
git commit -m "feat(gcloud): route metadata.google.internal to per-run handler"
```

### Task 7: Run manager wiring

**Files:**
- Modify: `internal/run/run.go`
- Modify: `internal/run/manager.go`

- [ ] **Step 1: Read `manager.go` lines 640–1000**

Understand the AWS setup block so the gcloud block can slot alongside it.

- [ ] **Step 2: Add `GCloudCredentialProvider` field to `run.go`**

In `internal/run/run.go` near line 99:

```go
GCloudCredentialProvider *gcloudprov.CredentialProvider
```

Add import:

```go
gcloudprov "github.com/majorcontext/moat/internal/providers/gcloud"
```

- [ ] **Step 3: In `manager.go`, add gcloud-config detection near the AWS block (~line 660)**

When a credential with provider `"gcloud"` is encountered, parse config via `gcloudprov.ConfigFromCredential(provCred)`, build `CredentialProvider`, set `r.GCloudCredentialProvider`, and populate `runCtx.GCloudConfig = &daemon.GCloudConfig{...}`.

- [ ] **Step 4: In `manager.go`, add container env block near the AWS env block (~line 984)**

```go
if r.GCloudCredentialProvider != nil {
	proxyEnv = append(proxyEnv,
		"GOOGLE_CLOUD_PROJECT="+r.GCloudCredentialProvider.ProjectID(),
		"CLOUDSDK_CORE_PROJECT="+r.GCloudCredentialProvider.ProjectID(),
		// metadata.google.internal and 169.254.169.254 are routed by the proxy
		// to the gcloud metadata emulation handler. Client libraries find this
		// automatically via ADC when HTTP_PROXY is set.
		"GOOGLE_APPLICATION_CREDENTIALS=", // clear any inherited value
		"CLOUDSDK_AUTH_DISABLE_CREDENTIALS_FILE=true",
	)
}
```

No files are written, no mounts needed — strictly better than AWS.

- [ ] **Step 5: Update the daemon register request**

Near the line that already sets `runCtx.AWSConfig`, also populate `runCtx.GCloudConfig` and ensure the `RegisterRequest` in `internal/daemon/api.go` is passed through. Follow exactly how AWSConfig flows today.

- [ ] **Step 6: Build and run unit tests**

Run: `go build ./... && make test-unit`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/run/
git commit -m "feat(gcloud): wire provider into run manager"
```

### Task 8: End-to-end sanity test (manual, documented)

**Files:** none.

- [ ] **Step 1: Build binary**

Run: `go build -o /tmp/moat ./cmd/moat`

- [ ] **Step 2: Verify host has ADC**

Run: `gcloud auth application-default print-access-token`
Expected: prints a token.

- [ ] **Step 3: Grant gcloud**

Run: `/tmp/moat grant gcloud --project=$(gcloud config get-value project)`
Expected: success message.

- [ ] **Step 4: Launch an interactive run with the grant**

Run: `/tmp/moat run --grant gcloud -- bash`

- [ ] **Step 5: Inside the container, verify**

Run:
```bash
curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/project/project-id
curl -s -H "Metadata-Flavor: Google" http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token | jq
gcloud auth print-access-token
gcloud projects list --limit=1
```
Expected: project id, access token JSON, access token string, project list.

- [ ] **Step 6: Verify no secrets leaked**

Run inside container:
```bash
ls ~/.config/gcloud/ 2>/dev/null || echo "no gcloud dir"
env | grep -i google
env | grep -i gcloud
```
Expected: no `~/.config/gcloud/` unless gcloud itself created one; only `GOOGLE_CLOUD_PROJECT` / `CLOUDSDK_*` in env; no tokens.

- [ ] **Step 7: Record results**

Paste the output as a comment in the PR, no commit needed.

### Task 9: Documentation and changelog

**Files:**
- Create: `docs/content/guides/XX-gcloud.md` (pick next free number)
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Write the guide**

Follow `docs/STYLE-GUIDE.md`. Keep it short:

1. Prerequisites: `gcloud auth application-default login` on the host.
2. `moat grant gcloud [--project <id>] [--key-file <path>] [--impersonate-service-account <email>]`.
3. `moat run --grant gcloud -- <cmd>`.
4. Notes: credentials never enter the container; metadata server is emulated; impersonation and key files stay on the host.
5. Troubleshooting: "no host credentials found" → run ADC login; "invalid_grant" → refresh-token revoked.

- [ ] **Step 2: Update moat.yaml reference**

Add `gcloud` to the list of supported grants with its metadata fields.

- [ ] **Step 3: Update CHANGELOG.md**

Under the next unreleased version, add:

```markdown
### Added

- **gcloud credential provider** — authenticate the Google Cloud CLI and every Google client library inside a moat sandbox without leaking refresh tokens or service account keys. The host daemon mints short-lived access tokens via Application Default Credentials and serves them to the container through an emulated GCE metadata server. Use `moat grant gcloud` then `moat run --grant gcloud`. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 4: Run lint**

Run: `make lint`
Expected: clean.

- [ ] **Step 5: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs(gcloud): guide, reference, and changelog entry"
```

---

## Final verification checklist (before PR)

Use @superpowers:verification-before-completion.

- [ ] `go build ./...` clean
- [ ] `make test-unit` PASS (no new skipped tests)
- [ ] `make lint` clean
- [ ] Manual E2E from Task 8 runs successfully and output is recorded
- [ ] No `~/.config/gcloud/` mounted into the container
- [ ] No refresh tokens or SA keys in container env or files
- [ ] Daemon persistence round-trips `GCloudConfig`
- [ ] Daemon API remains backwards-compatible (only additive fields)
- [ ] Guide and changelog updated

## Known risks and open questions

1. **Hostname resolution inside the container.** `metadata.google.internal` is a real DNS name that resolves to `169.254.169.254` on GCE. Inside a moat container, DNS may not resolve it. Mitigations:
   - (a) Add an `/etc/hosts` entry in the container mapping both names to a routable sentinel (e.g., `127.0.0.1` would not work because the proxy is not local). Better: map to the proxy host.
   - (b) Rely on `HTTP_PROXY` — most Google client libraries honor it even for metadata (they use the standard `http.Transport`). Verify: **spike test this in Task 8 before committing to this approach.**
   - (c) Fall back to `GCE_METADATA_HOST=<proxyHost>:<port>` — this is the documented override and does not require DNS. If (a) and (b) don't work, use this. It requires reopening the Task 6/7 decision about how the proxy identifies the run.
   If (c) is required: use container IP to run lookup in the daemon registry. Search confirmed this capability exists if `ContainerID` → run lookup is present.

2. **`metadata.google.internal` plain HTTP vs TLS.** Real metadata is HTTP only. Our proxy currently intercepts HTTPS via MITM. Plain HTTP goes through transparently. Verify that the proxy's direct HTTP path is reached for this host.

3. **Proxy health checks that probe `169.254.169.254` with a 250ms timeout.** Google client libraries probe the metadata server at startup. The emulator must respond within that window or libraries silently fall back to "no credentials." Our handler is in-process so latency is sub-millisecond — should be fine, but note it if tests flake.

4. **`CLOUDSDK_AUTH_ACCESS_TOKEN` alternative.** If metadata emulation turns out to be too fiddly, we can fall back to injecting a raw access token via env var. It works for gcloud CLI only (not client libraries) and needs refresh every hour. Document this as the fallback.

5. **ID tokens (`audience=…`).** Deferred. Document as a known gap in the guide. Adding later is purely additive to `EndpointHandler`.

---

## Review loop

After writing this plan, dispatch a single plan-document-reviewer subagent pointing at this file and the research summary above. Address any issues, re-review, and proceed to execution via @superpowers:subagent-driven-development.
