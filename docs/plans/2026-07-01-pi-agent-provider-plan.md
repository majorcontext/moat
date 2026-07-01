# Pi Coding Agent Provider — Implementation Plan

> **For agentic workers:** implement task-by-task. Steps use checkbox (`- [ ]`) syntax. Run `make lint` and targeted `go test` after each task; commit per task.

**Goal:** Add `moat pi` — run the Pi coding agent in a Moat container with transparent credential injection, reusing the existing `anthropic`/`openai` grants, failing hard on every unsupported configuration.

**Architecture:** A new `internal/providers/pi` package mirroring `codex`. Pi has no credential of its own; a pure `resolvePiProvider` function picks `anthropic` or `openai` from config override + store presence and hard-errors otherwise. Runtime context is injected via Pi's `--append-system-prompt <file>` (no clobbering user files). Credential injection is delegated entirely to the `anthropic`/`openai` credential providers (verified: Pi honors `HTTP_PROXY`).

**Tech Stack:** Go, Cobra, the Moat provider registry. Pi CLI = npm `@earendil-works/pi-coding-agent`, binary `pi`.

## Global Constraints

- **Supported backends: `anthropic` and `openai` ONLY.** Every other Pi provider fails hard as future work.
- **v1 config surface: `pi.provider`, `pi.model` only.** `pi.mcp` and `pi.sync_logs` are deferred (Pi core has no MCP config; session-log mounting is claude-only).
- **Pi launches explicitly:** `pi --provider <resolved> [--model <m>] --append-system-prompt <ctx> [-p <prompt>]`, env `PI_OFFLINE=1`. Pi's default provider is `google`, so `--provider` is mandatory.
- **No credential ever hits container disk** — placeholder env from the grant provider; proxy injects the real key.
- **Match `validateGrants` error style:** two-space-indented bullets, `Run: moat grant <x>`.
- Conventional Commits; no `Co-Authored-By`. Run `make lint` before每 commit.

---

### Task 1: Provider skeleton + registration + dependency

**Files:**
- Create: `internal/providers/pi/provider.go`, `internal/providers/pi/constants.go`, `internal/providers/pi/doc.go`
- Modify: `internal/providers/register.go` (add blank import), `internal/deps/registry.yaml` (add `pi-cli`)

**Produces:** `pi.Provider` (implements `provider.AgentProvider`), registered as `"pi"`.

- [ ] **Step 1: Add the `pi-cli` dependency** to `internal/deps/registry.yaml` (next to `codex-cli`):

```yaml
pi-cli:
  description: Pi coding agent CLI
  type: npm
  package: "@earendil-works/pi-coding-agent"
  requires: [node]
```

- [ ] **Step 2: Write `internal/providers/pi/constants.go`:**

```go
package pi

// PiInitMountPath is where the Pi staging directory is mounted in containers.
const PiInitMountPath = "/moat/pi-init"

// ContextFileName is the staged runtime-context file, injected into Pi's
// system prompt via --append-system-prompt.
const ContextFileName = "moat-context.md"
```

- [ ] **Step 3: Write `internal/providers/pi/provider.go`** (Grant is a directing error — Pi has no credential of its own; ConfigureProxy/ContainerEnv/ContainerMounts are no-ops because injection is delegated to the anthropic/openai grant providers):

```go
package pi

import (
	"context"
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.AgentProvider for the Pi coding agent.
// Pi has no credential of its own: it runs against whichever backend the
// user's anthropic/openai grant provides, so credential injection is handled
// by those providers, not here.
type Provider struct{}

var (
	_ provider.CredentialProvider = (*Provider)(nil)
	_ provider.AgentProvider      = (*Provider)(nil)
)

func init() {
	provider.Register(&Provider{})
}

// Name returns the provider identifier.
func (p *Provider) Name() string { return "pi" }

// Grant always errors: Pi has no credential of its own. Users grant a backend
// with `moat grant anthropic` or `moat grant openai`.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return nil, errors.New(
		"pi has no credential of its own — grant a model backend instead:\n" +
			"  Run: moat grant anthropic\n" +
			"  or:  moat grant openai")
}

// ConfigureProxy is a no-op: credential injection is delegated to the
// anthropic/openai credential providers for the resolved backend.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {}

// ContainerEnv is a no-op for the same reason (the backend grant provider sets
// the placeholder API-key env var that Pi reads).
func (p *Provider) ContainerEnv(cred *provider.Credential) []string { return nil }

// ContainerMounts returns none — Pi uses the staging-directory approach.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op (staging dir cleanup handled by PrepareContainer's Cleanup).
func (p *Provider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns none.
func (p *Provider) ImpliedDependencies() []string { return nil }
```

- [ ] **Step 4: Write `internal/providers/pi/doc.go`** (package doc, mirror codex's structure — describe: reuses anthropic/openai grants, no own credential, context via --append-system-prompt, supported backends anthropic/openai only).

- [ ] **Step 5: Add the blank import** to `internal/providers/register.go` (alphabetical, after `oauth` or `npm`):

```go
	_ "github.com/majorcontext/moat/internal/providers/pi"       // registers Pi provider
```

- [ ] **Step 6: Build** — `go build ./...` (RegisterCLI/PrepareContainer are added in later tasks, so temporarily add stub methods to satisfy `AgentProvider`, OR order Task 4/5 before building). To keep this task compiling, add minimal stubs in provider.go:

```go
// (temporary stubs — real impls in agent.go / cli.go)
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	return nil, errors.New("not implemented")
}
func (p *Provider) RegisterCLI(root *cobra.Command) {}
```
(Import `github.com/spf13/cobra`. These are replaced in Tasks 4–5; the compile-time interface assertions force us to keep them until then.)

- [ ] **Step 7: Verify + commit**

Run: `go build ./... && go vet ./internal/providers/pi/`
Expected: builds clean.
```bash
git add internal/providers/pi/ internal/providers/register.go internal/deps/registry.yaml
git commit -m "feat(pi): scaffold Pi provider package and register pi-cli dependency"
```

---

### Task 2: `resolvePiProvider` — backend selection + fail-hard (TDD)

**Files:**
- Create: `internal/providers/pi/resolve.go`, `internal/providers/pi/resolve_test.go`

**Produces:** `resolvePiProvider(providerOverride, modelOverride string, hasAnthropic, hasOpenAI bool) (providerName, model string, err error)` — pure, the single source of truth for selection and every failure path.

- [ ] **Step 1: Write the failing test** `internal/providers/pi/resolve_test.go`:

```go
package pi

import "testing"

func TestResolvePiProvider(t *testing.T) {
	tests := []struct {
		name         string
		provOverride string
		modelOver    string
		hasAnthropic bool
		hasOpenAI    bool
		wantProvider string
		wantModel    string
		wantErr      bool
	}{
		{name: "infer anthropic", hasAnthropic: true, wantProvider: "anthropic"},
		{name: "infer openai", hasOpenAI: true, wantProvider: "openai"},
		{name: "model passthrough", hasAnthropic: true, modelOver: "claude-opus-4-8", wantProvider: "anthropic", wantModel: "claude-opus-4-8"},
		{name: "both without override is ambiguous", hasAnthropic: true, hasOpenAI: true, wantErr: true},
		{name: "neither is an error", wantErr: true},
		{name: "override anthropic ok", provOverride: "anthropic", hasAnthropic: true, wantProvider: "anthropic"},
		{name: "override openai ok", provOverride: "openai", hasOpenAI: true, wantProvider: "openai"},
		{name: "override anthropic but not granted", provOverride: "anthropic", hasOpenAI: true, wantErr: true},
		{name: "override openai but not granted", provOverride: "openai", hasAnthropic: true, wantErr: true},
		{name: "override both present picks override", provOverride: "openai", hasAnthropic: true, hasOpenAI: true, wantProvider: "openai"},
		{name: "unsupported backend fails hard", provOverride: "gemini", hasAnthropic: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov, model, err := resolvePiProvider(tt.provOverride, tt.modelOver, tt.hasAnthropic, tt.hasOpenAI)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got provider=%q", prov)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prov != tt.wantProvider {
				t.Errorf("provider = %q, want %q", prov, tt.wantProvider)
			}
			if model != tt.wantModel {
				t.Errorf("model = %q, want %q", model, tt.wantModel)
			}
		})
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/providers/pi/ -run TestResolvePiProvider` → FAIL (`resolvePiProvider` undefined).

- [ ] **Step 3: Write `internal/providers/pi/resolve.go`:**

```go
package pi

import (
	"errors"
	"fmt"
)

// resolvePiProvider decides which backend Pi uses and hard-errors on every
// known bad state. providerOverride is the effective --provider / pi.provider
// ("" if unset); modelOverride is --model / pi.model. hasAnthropic / hasOpenAI
// report whether that grant's credential is configured in the store.
func resolvePiProvider(providerOverride, modelOverride string, hasAnthropic, hasOpenAI bool) (providerName, model string, err error) {
	switch providerOverride {
	case "anthropic":
		if !hasAnthropic {
			return "", "", missingGrantErr("anthropic")
		}
		return "anthropic", modelOverride, nil
	case "openai":
		if !hasOpenAI {
			return "", "", missingGrantErr("openai")
		}
		return "openai", modelOverride, nil
	case "":
		// fall through to inference
	default:
		return "", "", fmt.Errorf(
			"pi provider %q is not supported yet (supported: anthropic, openai)\n"+
				"Other Pi backends are planned but not wired up — set pi.provider (or --provider) to a supported value.",
			providerOverride)
	}

	switch {
	case hasAnthropic && hasOpenAI:
		return "", "", errors.New(
			"pi: both the anthropic and openai grants are configured — Pi cannot pick one automatically\n" +
				"Set pi.provider in moat.yaml (or pass --provider anthropic|openai) to choose.")
	case hasAnthropic:
		return "anthropic", modelOverride, nil
	case hasOpenAI:
		return "openai", modelOverride, nil
	default:
		return "", "", errors.New(
			"pi requires a model backend, but no supported grant is configured:\n" +
				"  - anthropic\n" +
				"  - openai\n\n" +
				"Run 'moat grant anthropic' or 'moat grant openai', then run again.")
	}
}

func missingGrantErr(name string) error {
	return fmt.Errorf(
		"pi.provider is %q but that grant isn't configured\n"+
			"  - %s: not configured\n"+
			"    Run: moat grant %s",
		name, name, name)
}
```

- [ ] **Step 4: Run to verify it passes** — `go test ./internal/providers/pi/ -run TestResolvePiProvider -v` → PASS (all 11 cases).

- [ ] **Step 5: Commit**
```bash
git add internal/providers/pi/resolve.go internal/providers/pi/resolve_test.go
git commit -m "feat(pi): add resolvePiProvider backend selection with hard failures"
```

---

### Task 3: `PiConfig` in moat.yaml

**Files:**
- Modify: `internal/config/config.go` (add `Pi PiConfig` field + struct)
- Test: `internal/config/config_test.go`

**Produces:** `config.PiConfig{ Provider, Model string }`, `Config.Pi`.

- [ ] **Step 1: Write the failing test** in `internal/config/config_test.go`:

```go
func TestLoadConfigParsesPiBlock(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: pi
pi:
  provider: anthropic
  model: claude-opus-4-8
`
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Pi.Provider != "anthropic" {
		t.Errorf("Pi.Provider = %q, want anthropic", cfg.Pi.Provider)
	}
	if cfg.Pi.Model != "claude-opus-4-8" {
		t.Errorf("Pi.Model = %q, want claude-opus-4-8", cfg.Pi.Model)
	}
}

// Companion: an empty pi block leaves defaults empty (provider inferred later).
func TestLoadConfigPiBlockDefaultsEmpty(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte("agent: pi\n"), 0o644)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Pi.Provider != "" || cfg.Pi.Model != "" {
		t.Errorf("expected empty Pi config, got %+v", cfg.Pi)
	}
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/config/ -run TestLoadConfigParsesPiBlock` → FAIL (`cfg.Pi` undefined).

- [ ] **Step 3: Add the field** to the `Config` struct (after `Gemini GeminiConfig`):

```go
	Pi           PiConfig          `yaml:"pi,omitempty"`
```

- [ ] **Step 4: Add the struct** near `GeminiConfig`:

```go
// PiConfig configures the Pi coding agent integration.
//
// Pi has no credential of its own; it runs against the anthropic or openai
// grant. Provider selects the backend (must be "anthropic" or "openai" in v1);
// when unset it is inferred from the single configured grant. Model optionally
// pins a model pattern (Pi's per-provider default is used when empty).
type PiConfig struct {
	Provider string `yaml:"provider,omitempty"`
	Model    string `yaml:"model,omitempty"`
}
```

- [ ] **Step 5: Run to verify it passes** — `go test ./internal/config/ -run 'TestLoadConfigParsesPiBlock|TestLoadConfigPiBlockDefaultsEmpty' -v` → PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add pi.provider and pi.model config block"
```

---

### Task 4: `PrepareContainer` — stage runtime context (TDD)

**Files:**
- Create: `internal/providers/pi/agent.go`, `internal/providers/pi/agent_test.go`
- Modify: `internal/providers/pi/provider.go` (remove the temporary `PrepareContainer` stub)

**Interfaces:**
- Produces: `(*Provider).PrepareContainer` — stages `moat-context.md`, mounts `PiInitMountPath` read-only, returns env `PI_OFFLINE=1` + `MOAT_PI_INIT=<path>`.

- [ ] **Step 1: Write the failing test** `internal/providers/pi/agent_test.go`:

```go
package pi

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestPrepareContainerStagesContext(t *testing.T) {
	p := &Provider{}
	cfg, err := p.PrepareContainer(context.Background(), provider.PrepareOpts{
		RuntimeContext: "# Moat Environment\n\nhello",
	})
	if err != nil {
		t.Fatalf("PrepareContainer: %v", err)
	}
	t.Cleanup(func() {
		if cfg.Cleanup != nil {
			cfg.Cleanup()
		}
	})

	// Context file written into the staging dir.
	ctxPath := filepath.Join(cfg.StagingDir, ContextFileName)
	data, readErr := os.ReadFile(ctxPath)
	if readErr != nil {
		t.Fatalf("reading staged context: %v", readErr)
	}
	if !strings.Contains(string(data), "hello") {
		t.Errorf("context file missing content: %q", data)
	}

	// Mount + env wired.
	foundMount := false
	for _, m := range cfg.Mounts {
		if m.Target == PiInitMountPath && m.Source == cfg.StagingDir && m.ReadOnly {
			foundMount = true
		}
	}
	if !foundMount {
		t.Errorf("expected read-only mount of staging dir at %s, got %+v", PiInitMountPath, cfg.Mounts)
	}
	assertEnv(t, cfg.Env, "PI_OFFLINE=1")
	assertEnv(t, cfg.Env, "MOAT_PI_INIT="+PiInitMountPath)
}

func assertEnv(t *testing.T, env []string, want string) {
	t.Helper()
	for _, e := range env {
		if e == want {
			return
		}
	}
	t.Errorf("env missing %q, got %v", want, env)
}
```

- [ ] **Step 2: Run to verify it fails** — `go test ./internal/providers/pi/ -run TestPrepareContainerStagesContext` → FAIL (stub returns "not implemented").

- [ ] **Step 3: Write `internal/providers/pi/agent.go`:**

```go
package pi

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer stages the Pi runtime-context file and returns the mount +
// env needed to inject it. The real API credential is injected by the proxy
// (via the anthropic/openai grant provider); nothing secret is staged here.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-pi-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}
	cleanupFn := func() { os.RemoveAll(tmpDir) }

	if opts.RuntimeContext != "" {
		if writeErr := os.WriteFile(filepath.Join(tmpDir, ContextFileName), []byte(opts.RuntimeContext), 0o644); writeErr != nil {
			cleanupFn()
			return nil, fmt.Errorf("writing context file: %w", writeErr)
		}
	}

	env := []string{
		"PI_OFFLINE=1", // suppress startup catalog fetch; does NOT block inference
		"MOAT_PI_INIT=" + PiInitMountPath,
	}
	mounts := []provider.MountConfig{
		{Source: tmpDir, Target: PiInitMountPath, ReadOnly: true},
	}
	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup:    cleanupFn,
	}, nil
}
```

- [ ] **Step 4: Remove the temporary `PrepareContainer` stub** from `provider.go` (keep the `RegisterCLI` stub until Task 5).

- [ ] **Step 5: Run to verify it passes** — `go test ./internal/providers/pi/ -run TestPrepareContainerStagesContext -v` → PASS.

- [ ] **Step 6: Commit**
```bash
git add internal/providers/pi/agent.go internal/providers/pi/agent_test.go internal/providers/pi/provider.go
git commit -m "feat(pi): stage runtime context for --append-system-prompt injection"
```

---

### Task 5: `moat pi` CLI command + credential resolution wiring

**Files:**
- Create: `internal/providers/pi/cli.go`
- Modify: `internal/providers/pi/provider.go` (remove `RegisterCLI` stub)
- Test: `internal/providers/pi/cli_test.go`

**Interfaces:**
- Consumes: `resolvePiProvider` (Task 2), `PiInitMountPath`/`ContextFileName` (Task 1), `config.PiConfig` (Task 3).
- Produces: `moat pi` command; `GetCredentialGrant`/`BuildCommand` using package vars `piResolvedProvider`/`piResolvedModel`; `DefaultDependencies()`, `NetworkHosts()`.

- [ ] **Step 1: Write `internal/providers/pi/cli.go`.** `runPi` resolves the backend up front (fail hard before `Create`) and stashes it for the closures. Verify `config.Load`'s no-file behavior and the workspace-arg helper against `internal/cli` during implementation; the load is best-effort (nil cfg ⇒ infer from grants).

```go
package pi

import (
	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

var (
	piFlags        cli.ExecFlags
	piPromptFlag   string
	piAllowedHosts []string
	piWtFlag       string
	piProviderFlag string
	piModelFlag    string
)

// Resolved by runPi before RunProvider's closures fire.
var (
	piResolvedProvider string
	piResolvedModel    string
)

// NetworkHosts lists the LLM API hosts Pi needs under a strict network policy.
// Pi may talk to either backend; both are allowed so a run works regardless of
// the resolved provider.
func NetworkHosts() []string {
	return []string{
		"api.anthropic.com",
		"api.openai.com",
	}
}

// DefaultDependencies returns the default dependencies for running Pi.
func DefaultDependencies() []string {
	return []string{"node@22", "git", "pi-cli"}
}

// RegisterCLI registers the `moat pi` command.
func (p *Provider) RegisterCLI(root *cobra.Command) {
	piCmd := &cobra.Command{
		Use:   "pi [workspace] [flags]",
		Short: "Run the Pi coding agent in an isolated container",
		Long: `Run the Pi coding agent in an isolated container with automatic credential injection.

Pi has no credential of its own — it runs against your anthropic or openai grant.
If exactly one of those grants is configured it is used automatically; if both
are configured, set pi.provider (or --provider) to choose. Only the anthropic and
openai backends are supported today.

Examples:
  moat pi
  moat pi ./my-project
  moat pi -p "explain this codebase"
  moat pi --provider openai
  moat pi --grant github`,
		Args: cobra.ArbitraryArgs,
		RunE: runPi,
	}
	cli.AddExecFlags(piCmd, &piFlags)
	piCmd.Flags().StringVarP(&piPromptFlag, "prompt", "p", "", "run with prompt (non-interactive mode)")
	piCmd.Flags().StringSliceVar(&piAllowedHosts, "allow-host", nil, "additional hosts to allow network access to")
	piCmd.Flags().StringVar(&piProviderFlag, "provider", "", "model backend: anthropic or openai (overrides pi.provider)")
	piCmd.Flags().StringVar(&piModelFlag, "model", "", "model pattern to use (overrides pi.model)")
	piCmd.Flags().StringVar(&piWtFlag, "worktree", "", "run in a git worktree for this branch")
	piCmd.Flags().StringVar(&piWtFlag, "wt", "", "alias for --worktree")
	_ = piCmd.Flags().MarkHidden("wt")
	root.AddCommand(piCmd)
}

func runPi(cmd *cobra.Command, args []string) error {
	providerOverride := piProviderFlag
	modelOverride := piModelFlag
	if cfg := loadWorkspaceConfig(args); cfg != nil {
		if providerOverride == "" {
			providerOverride = cfg.Pi.Provider
		}
		if modelOverride == "" {
			modelOverride = cfg.Pi.Model
		}
	}

	prov, model, err := resolvePiProvider(
		providerOverride, modelOverride,
		credentialConfigured(credential.ProviderAnthropic),
		credentialConfigured(credential.ProviderOpenAI),
	)
	if err != nil {
		return err
	}
	piResolvedProvider = prov
	piResolvedModel = model

	return cli.RunProvider(cmd, args, cli.ProviderRunConfig{
		Name:                  "pi",
		Flags:                 &piFlags,
		PromptFlag:            piPromptFlag,
		AllowedHosts:          piAllowedHosts,
		WtFlag:                piWtFlag,
		GetCredentialGrant:    func() string { return piResolvedProvider },
		Dependencies:          DefaultDependencies(),
		NetworkHosts:          NetworkHosts(),
		SupportsInitialPrompt: true,
		DryRunNote:            "Running Pi with backend: " + piResolvedProvider,
		BuildCommand: func(promptFlag, initialPrompt string) ([]string, error) {
			c := []string{"pi", "--provider", piResolvedProvider}
			if piResolvedModel != "" {
				c = append(c, "--model", piResolvedModel)
			}
			c = append(c, "--append-system-prompt", PiInitMountPath+"/"+ContextFileName)
			if promptFlag != "" {
				c = append(c, "-p", promptFlag)
			} else if initialPrompt != "" {
				c = append(c, initialPrompt)
			}
			return c, nil
		},
	})
}

// credentialConfigured reports whether a credential for prov exists in the store.
func credentialConfigured(prov credential.Provider) bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	_, err = store.Get(prov)
	return err == nil
}

// loadWorkspaceConfig best-effort loads moat.yaml from the run's workspace so
// pi.provider/pi.model overrides are honored. Returns nil if none is found.
func loadWorkspaceConfig(args []string) *config.Config {
	ws := workspaceFromArgs(args)
	cfg, err := config.Load(ws)
	if err != nil {
		return nil
	}
	return cfg
}
```

- [ ] **Step 2: Implement `workspaceFromArgs`** to match how `cli.RunProvider` resolves the workspace (path before `--`, else `.`). Inspect `internal/cli/provider.go` for a reusable helper; if one exists, call it instead of reimplementing. Concrete minimal version:

```go
func workspaceFromArgs(args []string) string {
	if len(args) > 0 && args[0] != "" {
		return args[0]
	}
	return "."
}
```
(If `cli` exposes a dedicated resolver, replace the body with a call to it — DRY.)

- [ ] **Step 3: Remove the `RegisterCLI` stub** from `provider.go`.

- [ ] **Step 4: Write `internal/providers/pi/cli_test.go`** — cover `DefaultDependencies`/`NetworkHosts` contents and the `BuildCommand` shape for both prompt and interactive, by setting the package vars directly:

```go
package pi

import (
	"slices"
	"testing"

	"github.com/majorcontext/moat/internal/cli"
)

func TestDefaultDependenciesAndHosts(t *testing.T) {
	if !slices.Contains(DefaultDependencies(), "pi-cli") {
		t.Errorf("DefaultDependencies missing pi-cli: %v", DefaultDependencies())
	}
	if !slices.Contains(NetworkHosts(), "api.anthropic.com") || !slices.Contains(NetworkHosts(), "api.openai.com") {
		t.Errorf("NetworkHosts missing a backend host: %v", NetworkHosts())
	}
}

func TestBuildCommandShape(t *testing.T) {
	piResolvedProvider = "openai"
	piResolvedModel = "gpt-5"
	var rc cli.ProviderRunConfig
	// Re-derive BuildCommand by invoking runPi's config indirectly is heavy;
	// instead assert the command builder logic via a small local copy is NOT
	// done — call the exported builder if extracted. For v1, extract the
	// command builder into a helper buildPiCommand(promptFlag, initialPrompt)
	// and test it here.
	_ = rc
	got := buildPiCommand("do it", "")
	want := []string{"pi", "--provider", "openai", "--model", "gpt-5", "--append-system-prompt", PiInitMountPath + "/" + ContextFileName, "-p", "do it"}
	if !slices.Equal(got, want) {
		t.Errorf("buildPiCommand = %v, want %v", got, want)
	}
}
```
Refactor note: extract the command assembly from the `BuildCommand` closure into a package function `buildPiCommand(promptFlag, initialPrompt string) []string` so it is unit-testable; the closure calls it. Update `cli.go` accordingly.

- [ ] **Step 5: Run** — `go test ./internal/providers/pi/ -v` → PASS. Then `go build ./...`.

- [ ] **Step 6: Commit**
```bash
git add internal/providers/pi/cli.go internal/providers/pi/cli_test.go internal/providers/pi/provider.go
git commit -m "feat(pi): add moat pi command with backend resolution and context injection"
```

---

### Task 6: Run wiring — staging dispatch, init detection, AI-agent + anti-cross-staging guards

**Files:**
- Modify: `internal/run/manager_agentinit.go` (add `setupPiStaging`)
- Modify: `internal/run/imageneeds.go` (dep-fallback `pi-cli` → `initSet["pi"]`)
- Modify: `internal/run/manager_create.go` (needsPiInit, pi dispatch block, `isAIAgent` += pi, `isPiAgent` guard on claude/codex/gemini dispatch)

**Interfaces:**
- Consumes: `provider.GetAgent("pi")`, `(*Provider).PrepareContainer`.

- [ ] **Step 1: Add `setupPiStaging`** to `internal/run/manager_agentinit.go` (mirror `setupCodexStaging`; Pi needs no credential, so pass a nil credential — PrepareContainer ignores it):

```go
// setupPiStaging builds the Pi container config (runtime context) via the
// provider interface. Pi has no credential of its own; the backend credential
// is injected by the anthropic/openai grant provider.
func (m *Manager) setupPiStaging(ctx context.Context, piProvider provider.AgentProvider, opts Options, containerHome, renderedContext string) (*provider.ContainerConfig, error) {
	piConfig, prepErr := piProvider.PrepareContainer(ctx, provider.PrepareOpts{
		ContainerHome:  containerHome,
		RuntimeContext: renderedContext,
	})
	if prepErr != nil {
		return nil, fmt.Errorf("preparing Pi container config: %w", prepErr)
	}
	return piConfig, nil
}
```

- [ ] **Step 2: Add the dependency fallback** in `internal/run/imageneeds.go` `resolveImageNeedsWithStore`, alongside the existing claude-code / gemini-cli fallbacks:

```go
	if !initSet["pi"] && hasDep(depList, "pi-cli") {
		initSet["pi"] = true
	}
```

- [ ] **Step 3: Add `needsPiInit`** in `manager_create.go` (next to `needsGeminiInit`):

```go
	needsPiInit := slices.Contains(imgNeeds.initProviders, "pi")
```

- [ ] **Step 4: Add the Pi staging dispatch block** in `manager_create.go` after the gemini block (~line 1403+). Pi has no local-MCP / sync-logs in v1, so the condition is just `needsPiInit`:

```go
	if needsPiInit {
		piProvider := provider.GetAgent("pi")
		if piProvider == nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, fmt.Errorf("pi provider not registered")
		}
		cfg, stageErr := m.setupPiStaging(ctx, piProvider, opts, containerHome, renderedContext)
		if stageErr != nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, stageErr
		}
		piConfig = cfg
		mounts = append(mounts, piConfig.Mounts...)
		proxyEnv = append(proxyEnv, piConfig.Env...)
	}
```
(Declare `var piConfig *provider.ContainerConfig` next to `codexConfig`/`geminiConfig`, and add `cleanupAgentConfig(piConfig)` to any later rollback chains that already clean the other agent configs.)

- [ ] **Step 5: Add `pi` to `isAIAgent`** (manager_create.go:2124):

```go
	return strings.HasPrefix(cfg.Agent, "claude") ||
		strings.HasPrefix(cfg.Agent, "codex") ||
		strings.HasPrefix(cfg.Agent, "gemini") ||
		strings.HasPrefix(cfg.Agent, "pi")
```

- [ ] **Step 6: Guard the other agents' staging against a Pi run.** Because a Pi run carries an `anthropic`/`openai` grant, the existing claude/codex/gemini dispatch conditions (and `ShouldSync*Logs`) would otherwise fire for a Pi run. Compute once before the claude block:

```go
	isPiRun := opts.Config != nil && strings.HasPrefix(opts.Config.Agent, "pi")
```
Then AND `&& !isPiRun` into each of the claude, codex, and gemini staging-dispatch conditions (the `if needs*Init || ... {` lines at ~1296/1348-area, 1379, 1403). This keeps a Pi run from staging other agents' config; credential injection is unaffected (it happens in the grant loop, not staging).

- [ ] **Step 7: Build + targeted vet** — `go build ./... && go vet ./internal/run/`. (Create is not unit-testable without a container runtime; verify the guard/dispatch by code review + the e2e task below.)

- [ ] **Step 8: Commit**
```bash
git add internal/run/manager_agentinit.go internal/run/imageneeds.go internal/run/manager_create.go
git commit -m "feat(pi): wire Pi container staging, init detection, and agent guards"
```

---

### Task 7: `moat init` scaffolding entry

**Files:**
- Modify: `cmd/moat/cli/init.go` (add pi to `agentConfigs()`)

- [ ] **Step 1: Add the import** for the pi provider package (alias `piprov`) and append to `agentConfigs()` (after gemini). Pi's `getCredGrant` returns the resolved backend only after resolution, which `moat init` scaffolding doesn't run — so return `""` (scaffolding only needs deps/hosts/command). Command mirrors non-interactive form:

```go
		{
			name:         "pi",
			dependencies: piprov.DefaultDependencies(),
			networkHosts: piprov.NetworkHosts(),
			getCredGrant: func() string { return "" },
			buildCommand: func(prompt, _ string) ([]string, error) {
				return []string{"pi", "-p", prompt}, nil
			},
		},
```

- [ ] **Step 2: Build** — `go build ./...`.

- [ ] **Step 3: Commit**
```bash
git add cmd/moat/cli/init.go
git commit -m "feat(pi): add pi to moat init agent scaffolding"
```

---

### Task 8: End-to-end sandbox verification (DIND)

**Files:** none (verification task); optionally add `internal/e2e/pi_test.go` guarded by the `e2e` build tag if the suite has a natural place.

- [ ] **Step 1: Build the moat binary** — `go build -o /tmp/moat ./cmd/moat`.
- [ ] **Step 2: With a throwaway/placeholder anthropic key granted**, run `moat pi -p "print hello"` against a DIND container and confirm: the container installs `pi`, the run reaches Pi, and traffic to `api.anthropic.com` traverses the moat proxy (network log shows it). A 401 from a placeholder key is an acceptable success signal (proves the path); a real key would complete.
- [ ] **Step 3: Verify the fail-hard paths** by running `moat pi` with (a) no grant → "requires a model backend" error; (b) both grants, no `pi.provider` → ambiguous error; (c) `pi.provider: gemini` → unsupported error. Each must exit non-zero **before** creating a container.
- [ ] **Step 4:** Record the results in the design doc's spike section. No commit unless an e2e test file is added.

---

### Task 9: Documentation + changelog + example

**Files:**
- Modify: `docs/content/reference/01-cli.md` (`moat pi`), `docs/content/reference/02-moat-yaml.md` (`pi:` block), `CHANGELOG.md`
- Create: `docs/content/guides/NN-pi.md` (short guide), `examples/pi/moat.yaml` (+ README)

- [ ] **Step 1: CLI reference** — document `moat pi`, its flags (`--provider`, `--model`, `-p`, `--grant`, `--worktree`), the "one of anthropic/openai" requirement, and the fail-hard behaviors.
- [ ] **Step 2: moat.yaml reference** — document the `pi:` block (`provider`, `model`), note anthropic/openai only, and that provider is inferred from the grant when unset. Explicitly note `pi.mcp`/`pi.sync_logs` are not yet supported.
- [ ] **Step 3: Guide + example** — a minimal `examples/pi/moat.yaml` (`agent: pi`, `grants: [anthropic]`) with a README showing `moat grant anthropic && moat pi`.
- [ ] **Step 4: CHANGELOG** — under `### Added`, bold entry, real PR link placeholder pattern (fill after PR): **Pi coding agent** — run `moat pi` backed by your anthropic or openai grant.
- [ ] **Step 5: `make lint`** and fix issues.
- [ ] **Step 6: Commit**
```bash
git add docs/ examples/ CHANGELOG.md
git commit -m "docs(pi): document moat pi command, config, and example"
```

---

## Self-Review (author checklist)

- **Spec coverage:** provider (§3), resolvePiProvider + all 4 failure paths (§4→Task 2), config surface (Task 3), context injection via --append-system-prompt (Task 4), reuse anthropic/openai grants (§Architecture→provider no-ops + grant loop), no auth.json (Task 4), isAIAgent/guard (Task 6), docs (Task 9). Deferred (documented): pi.mcp, pi.sync_logs, other backends, OAuth grant.
- **Placeholder scan:** none — every code step is concrete. Two "verify against `internal/cli`" notes (workspace helper, config.Load no-file behavior) are implementation confirmations, not TBDs; the concrete fallback is provided.
- **Type consistency:** `resolvePiProvider(string,string,bool,bool)→(string,string,error)`, `PiInitMountPath`/`ContextFileName`, `PiConfig{Provider,Model}`, `piResolvedProvider/Model`, `buildPiCommand` — used consistently across tasks.
- **Open risk:** `Create` isn't unit-testable without Docker; Task 6 logic is verified by the Task 8 DIND e2e run + code review.
