# Pi Config Bridge Implementation Plan

> **For agentic workers:** implement task-by-task. Steps use checkbox (`- [ ]`) syntax. Run `make lint` + targeted `go test` after each task; commit per task.

**Goal:** Bake Moat-owned safe global Pi settings and declared `pi.packages` into the agent image at build time, and warn when `moat pi` runs under a permissive network policy.

**Architecture:** One build-time step (mirroring the claude-plugins Dockerfile snippet) runs `pi install` for each declared package and merges Moat's safe defaults into `~/.pi/agent/settings.json`. Wired through `ImageSpec` → `GenerateDockerfile` → `ImageTag`. A permissive-policy warning is emitted from the `moat pi` command.

**Tech Stack:** Go, the Moat deps/image build pipeline, Pi CLI (`pi install`), Node (for the settings merge).

## Global Constraints

- **Build-time bake, not runtime staging.** One owner of `~/.pi/agent/settings.json`; **no `moat-init.sh` change**.
- **Baked settings (constants):** `defaultProjectTrust: "never"`, `enableInstallTelemetry: false`, `enableAnalytics: false`, `quietStartup: true`. **No `httpProxy`** (redundant with moat-owned `HTTP_PROXY` env).
- **`pi.packages` remote sources only:** must start with `npm:` / `git:` / `https://` / `ssh://`. Local paths rejected (they record relative paths that break at runtime).
- **Package install runs as `moatuser`** in user context, `set -e` (a failed install fails the build).
- **`containerUser`** is inserted directly into the Dockerfile — always the hardcoded `containerUser` constant, never user input.
- Conventional Commits; no `Co-Authored-By`. `make lint` before each commit.

---

### Task 1: `pi.packages` config field + validation (TDD)

**Files:**
- Modify: `internal/config/config.go` (add `Packages` to `PiConfig`; add `validatePiPackages`; call it in `Load`)
- Test: `internal/config/config_test.go`

**Produces:** `config.PiConfig.Packages []string`; `validatePiPackages(pkgs []string) error`.

- [ ] **Step 1: Write the failing tests** in `internal/config/config_test.go`:

```go
func TestValidatePiPackages(t *testing.T) {
	tests := []struct {
		name    string
		pkgs    []string
		wantErr bool
	}{
		{name: "npm ok", pkgs: []string{"npm:@acme/pi-reviewer@1.2.0"}},
		{name: "git ok", pkgs: []string{"git:github.com/acme/pi-skills@v3"}},
		{name: "git scp-like ok", pkgs: []string{"git:git@github.com:acme/x@v1"}},
		{name: "https ok", pkgs: []string{"https://github.com/acme/pi-skills"}},
		{name: "ssh ok", pkgs: []string{"ssh://git@github.com/acme/x"}},
		{name: "empty allowed (no packages)", pkgs: nil},
		{name: "empty string rejected", pkgs: []string{""}, wantErr: true},
		{name: "relative path rejected", pkgs: []string{"./local/pkg"}, wantErr: true},
		{name: "absolute path rejected", pkgs: []string{"/abs/pkg"}, wantErr: true},
		{name: "parent path rejected", pkgs: []string{"../pkg"}, wantErr: true},
		{name: "bare name rejected", pkgs: []string{"chalk"}, wantErr: true},
		{name: "shell metachar rejected", pkgs: []string{"npm:x;rm -rf /"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePiPackages(tt.pkgs)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for %v", tt.pkgs)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestLoadConfigParsesPiPackages(t *testing.T) {
	dir := t.TempDir()
	content := `
agent: pi
pi:
  packages:
    - "npm:@acme/pi-reviewer@1.2.0"
    - "git:github.com/acme/pi-skills@v3"
`
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644)
	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Pi.Packages) != 2 {
		t.Fatalf("Pi.Packages = %v, want 2 entries", cfg.Pi.Packages)
	}
}

// Companion: an invalid pi.packages entry fails Load.
func TestLoadConfigRejectsBadPiPackage(t *testing.T) {
	dir := t.TempDir()
	content := "agent: pi\npi:\n  packages:\n    - \"./local\"\n"
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644)
	if _, err := Load(dir); err == nil {
		t.Fatal("expected Load to reject a local-path pi package")
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/config/ -run 'ValidatePiPackages|PiPackages|BadPiPackage'` → FAIL (`validatePiPackages` / `cfg.Pi.Packages` undefined).

- [ ] **Step 3: Add the `Packages` field** to `PiConfig` (`config.go:353-356`):

```go
type PiConfig struct {
	Provider string   `yaml:"provider,omitempty"`
	Model    string   `yaml:"model,omitempty"`
	Packages []string `yaml:"packages,omitempty"`
}
```

- [ ] **Step 4: Add `validatePiPackages`** (near the other `validate*` helpers in `config.go`; ensure `regexp` and `strings` are imported — `strings` already is):

```go
// piPackageSafe restricts pi.packages entries to characters valid in package
// specs, URLs, and git refs — rejecting shell metacharacters as defense-in-depth
// (the source is also single-quoted when written into the build script).
var piPackageSafe = regexp.MustCompile(`^[A-Za-z0-9@:/._~%+#-]+$`)

// validatePiPackages checks that each pi.packages entry is a remote source
// Moat can install at image build time. Local paths are rejected because
// `pi install <path>` records a relative path that does not resolve at runtime.
func validatePiPackages(pkgs []string) error {
	for _, p := range pkgs {
		if p == "" {
			return fmt.Errorf("pi.packages: empty package source")
		}
		if !(strings.HasPrefix(p, "npm:") || strings.HasPrefix(p, "git:") ||
			strings.HasPrefix(p, "https://") || strings.HasPrefix(p, "ssh://")) {
			return fmt.Errorf("pi.packages: %q is not a remote source — use npm:, git:, https://, or ssh:// "+
				"(local paths are not supported at build time; publish the package to npm or git)", p)
		}
		if !piPackageSafe.MatchString(p) {
			return fmt.Errorf("pi.packages: %q contains invalid characters "+
				"(allowed: letters, digits and @ : / . _ ~ %% + # -)", p)
		}
	}
	return nil
}
```

- [ ] **Step 5: Call it in `Load`** (in the per-agent validation region, after the Gemini MCP block ~`config.go:692`):

```go
	// Validate Pi packages
	if err := validatePiPackages(cfg.Pi.Packages); err != nil {
		return nil, err
	}
```

- [ ] **Step 6: Run to verify pass** — `go test ./internal/config/ -run 'ValidatePiPackages|PiPackages|BadPiPackage' -v` → PASS.

- [ ] **Step 7: Commit**
```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "feat(config): add pi.packages with remote-source validation"
```

---

### Task 2: `pi.GenerateDockerfileSnippet` (TDD)

**Files:**
- Create: `internal/providers/pi/dockerfile.go`, `internal/providers/pi/dockerfile_test.go`

**Interfaces:**
- Produces: `pi.SnippetResult{ DockerfileSnippet string; ScriptName string; ScriptContent []byte }` and `pi.GenerateDockerfileSnippet(packages []string, containerUser string) SnippetResult`. Consumed by Task 3 (`internal/deps/dockerfile.go`).

- [ ] **Step 1: Write the failing test** `internal/providers/pi/dockerfile_test.go`:

```go
package pi

import (
	"strings"
	"testing"
)

func TestGenerateDockerfileSnippet_bakesSettingsWithNoPackages(t *testing.T) {
	r := GenerateDockerfileSnippet(nil, "moatuser")
	if r.ScriptName == "" || len(r.ScriptContent) == 0 {
		t.Fatal("expected a generated script even with no packages")
	}
	script := string(r.ScriptContent)
	if strings.Contains(script, "pi install ") {
		t.Errorf("no packages: script should not run pi install, got:\n%s", script)
	}
	for _, want := range []string{`defaultProjectTrust:"never"`, "enableInstallTelemetry:false", "enableAnalytics:false", "quietStartup:true", ".pi/agent"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// Dockerfile snippet runs as the container user and copies+runs the script.
	for _, want := range []string{"USER moatuser", "WORKDIR /home/moatuser", "COPY " + r.ScriptName, "RUN bash /tmp/" + r.ScriptName} {
		if !strings.Contains(r.DockerfileSnippet, want) {
			t.Errorf("dockerfile snippet missing %q, got:\n%s", want, r.DockerfileSnippet)
		}
	}
}

func TestGenerateDockerfileSnippet_installsPackagesSortedAndQuoted(t *testing.T) {
	r := GenerateDockerfileSnippet([]string{"npm:b@2", "npm:a@1"}, "moatuser")
	script := string(r.ScriptContent)
	ai := strings.Index(script, "pi install 'npm:a@1'")
	bi := strings.Index(script, "pi install 'npm:b@2'")
	if ai < 0 || bi < 0 {
		t.Fatalf("expected both packages single-quoted, got:\n%s", script)
	}
	if ai > bi {
		t.Errorf("packages should install in sorted order (a before b), got:\n%s", script)
	}
	// settings merge still present after installs
	if !strings.Contains(script, `defaultProjectTrust:"never"`) {
		t.Errorf("settings merge missing after installs")
	}
}
```

- [ ] **Step 2: Run to verify failure** — `go test ./internal/providers/pi/ -run TestGenerateDockerfileSnippet` → FAIL (undefined).

- [ ] **Step 3: Write `internal/providers/pi/dockerfile.go`:**

```go
package pi

import (
	"fmt"
	"sort"
	"strings"
)

// SnippetResult holds a Dockerfile snippet and the generated script it runs.
type SnippetResult struct {
	// DockerfileSnippet is Dockerfile text to append (USER/WORKDIR/COPY/RUN).
	DockerfileSnippet string
	// ScriptName is the build-context filename for ScriptContent.
	ScriptName string
	// ScriptContent is the generated shell script.
	ScriptContent []byte
}

// piConfigScriptName is the build-context filename for the generated bake script.
const piConfigScriptName = "pi-config.sh"

// piSettingsMergeJS assigns Moat's safe global Pi settings. httpProxy is omitted
// (redundant with the moat-owned HTTP_PROXY env). Bump the "pi-settings" hash
// marker in deps.ImageTag whenever this changes so cached images rebuild.
const piSettingsMergeJS = `Object.assign(s,{defaultProjectTrust:"never",enableInstallTelemetry:false,enableAnalytics:false,quietStartup:true})`

// GenerateDockerfileSnippet builds the Dockerfile snippet + script that installs
// the declared Pi packages and bakes Moat's safe global settings into
// ~/.pi/agent/settings.json, as containerUser at image build time.
//
// Commands are written to a separate script (a build-context file) rather than
// inline RUN steps — mirroring claude.GenerateDockerfileSnippet — to stay under
// the Apple containers builder's ~16KB Dockerfile gRPC limit.
//
// containerUser is inserted directly into the Dockerfile; callers must pass a
// safe, validated value (the hardcoded containerUser constant). Package sources
// are validated by config.validatePiPackages and are additionally single-quoted.
func GenerateDockerfileSnippet(packages []string, containerUser string) SnippetResult {
	sorted := make([]string, len(packages))
	copy(sorted, packages)
	sort.Strings(sorted)

	var s strings.Builder
	s.WriteString("#!/bin/bash\n")
	s.WriteString("# Auto-generated Pi config bake: declared packages + Moat safe global settings.\n")
	s.WriteString("set -e\n")
	fmt.Fprintf(&s, "export HOME=/home/%s\n", containerUser)
	s.WriteString("export GIT_TERMINAL_PROMPT=0\n")
	s.WriteString("export GIT_SSH_COMMAND='ssh -o BatchMode=yes -o ConnectTimeout=10'\n")
	s.WriteString("mkdir -p \"$HOME/.pi/agent\"\n")
	for _, p := range sorted {
		fmt.Fprintf(&s, "echo 'Installing Pi package %s'\n", p)
		fmt.Fprintf(&s, "pi install %s\n", shellSingleQuote(p))
	}
	// Merge Moat's safe global settings, preserving any packages array pi wrote.
	s.WriteString("node -e '")
	s.WriteString(`const fs=require("fs"),p=process.env.HOME+"/.pi/agent/settings.json";let s={};try{s=JSON.parse(fs.readFileSync(p,"utf8"))}catch(e){}`)
	s.WriteString(piSettingsMergeJS)
	s.WriteString(`;fs.writeFileSync(p,JSON.stringify(s,null,2))`)
	s.WriteString("'\n")

	var d strings.Builder
	d.WriteString("# Pi config (packages + Moat safe global settings)\n")
	fmt.Fprintf(&d, "USER %s\n", containerUser)
	fmt.Fprintf(&d, "WORKDIR /home/%s\n", containerUser)
	fmt.Fprintf(&d, "COPY %s /tmp/%s\n", piConfigScriptName, piConfigScriptName)
	fmt.Fprintf(&d, "RUN bash /tmp/%s && rm -f /tmp/%s\n", piConfigScriptName, piConfigScriptName)

	return SnippetResult{
		DockerfileSnippet: d.String(),
		ScriptName:        piConfigScriptName,
		ScriptContent:     []byte(s.String()),
	}
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes,
// so a validated package source cannot break out of the install command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
```

- [ ] **Step 4: Run to verify pass** — `go test ./internal/providers/pi/ -run TestGenerateDockerfileSnippet -v` → PASS.

- [ ] **Step 5: Commit**
```bash
git add internal/providers/pi/dockerfile.go internal/providers/pi/dockerfile_test.go
git commit -m "feat(pi): generate build-time Dockerfile snippet for packages + safe settings"
```

---

### Task 3: Wire the snippet into the image build (ImageSpec + GenerateDockerfile + ImageTag)

**Files:**
- Modify: `internal/deps/imagespec.go` (add `PiBakeSettings`, `PiPackages`; `NeedsCustomImage`)
- Modify: `internal/deps/builder.go` (`ImageTag` hash inputs)
- Modify: `internal/deps/dockerfile.go` (call `pi.GenerateDockerfileSnippet`; `inUserContext`)
- Modify: `internal/run/manager_create.go` (populate the new `ImageSpec` fields)
- Test: `internal/deps/dockerfile_test.go`, `internal/deps/builder_test.go` (or the existing image-tag test file)

**Interfaces:**
- Consumes: `pi.GenerateDockerfileSnippet` (Task 2), `config.PiConfig.Packages` (Task 1).

- [ ] **Step 1: Add `ImageSpec` fields** in `internal/deps/imagespec.go` (after the `ClaudePlugins` field, ~line 58):

```go
	// PiBakeSettings indicates the image should bake Moat's safe global Pi
	// settings (and any PiPackages) into ~/.pi/agent/settings.json at build time.
	PiBakeSettings bool

	// PiPackages are remote Pi package sources (npm:/git:/https:/ssh:) installed
	// via `pi install` at build time. Format validated by config.validatePiPackages.
	PiPackages []string
```

- [ ] **Step 2: Include in `NeedsCustomImage`** (`imagespec.go:83-85`) — add the `PiBakeSettings` term:

```go
	return hasDeps || s.BaseImage != "" || s.NeedsSSH || len(s.InitProviders) > 0 ||
		s.NeedsFirewall || s.NeedsInitFiles || s.NeedsClipboard ||
		len(s.ClaudePlugins) > 0 || hasHooks || s.NeedsWorkspaceVolume || s.PiBakeSettings
```

- [ ] **Step 3: Write the failing hash test** in `internal/deps/builder_test.go` (create if absent; package `deps`):

```go
func TestImageTagIncludesPiPackagesAndSettings(t *testing.T) {
	base := ImageTag(nil, &ImageSpec{})
	bake := ImageTag(nil, &ImageSpec{PiBakeSettings: true})
	if base == bake {
		t.Error("PiBakeSettings should change the image tag")
	}
	pkgsA := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:a@1"}})
	pkgsB := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:b@1"}})
	if pkgsA == bake || pkgsA == pkgsB {
		t.Error("different PiPackages should produce different tags")
	}
	// Order-independent: same set → same tag.
	ab := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:a@1", "npm:b@1"}})
	ba := ImageTag(nil, &ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:b@1", "npm:a@1"}})
	if ab != ba {
		t.Error("PiPackages hash should be order-independent")
	}
}
```

- [ ] **Step 4: Run to verify failure** — `go test ./internal/deps/ -run TestImageTagIncludesPiPackagesAndSettings` → FAIL (tags equal).

- [ ] **Step 5: Add hash inputs** in `internal/deps/builder.go` `ImageTag`, right after the `ClaudePlugins` block (~line 84):

```go
	if opts.PiBakeSettings {
		hashInput += ",pi-settings:v1"
	}
	if len(opts.PiPackages) > 0 {
		sortedPkgs := make([]string, len(opts.PiPackages))
		copy(sortedPkgs, opts.PiPackages)
		sort.Strings(sortedPkgs)
		for _, p := range sortedPkgs {
			hashInput += ",pi-pkg:" + p
		}
	}
```

- [ ] **Step 6: Run to verify pass** — `go test ./internal/deps/ -run TestImageTagIncludesPiPackagesAndSettings -v` → PASS.

- [ ] **Step 7: Write the failing Dockerfile-integration test** in `internal/deps/dockerfile_test.go`:

```go
func TestGenerateDockerfilePiBake(t *testing.T) {
	// With PiBakeSettings, the generated Dockerfile references the pi-config
	// script and the context files include it.
	res, err := GenerateDockerfile(
		[]Dependency{{Name: "pi-cli", Type: TypeNpm, Package: "@earendil-works/pi-coding-agent"}},
		&ImageSpec{PiBakeSettings: true, PiPackages: []string{"npm:@acme/x@1"}, InitProviders: []string{"pi"}},
	)
	if err != nil {
		t.Fatalf("GenerateDockerfile: %v", err)
	}
	if !strings.Contains(res.Dockerfile, "pi-config.sh") {
		t.Errorf("Dockerfile should reference pi-config.sh:\n%s", res.Dockerfile)
	}
	if _, ok := res.ContextFiles["pi-config.sh"]; !ok {
		t.Errorf("context files should include pi-config.sh, got keys %v", keysOf(res.ContextFiles))
	}
	// Companion: without PiBakeSettings, neither appears.
	res2, _ := GenerateDockerfile(nil, &ImageSpec{})
	if strings.Contains(res2.Dockerfile, "pi-config.sh") {
		t.Errorf("no bake: Dockerfile should not reference pi-config.sh")
	}
	if _, ok := res2.ContextFiles["pi-config.sh"]; ok {
		t.Errorf("no bake: context files should not include pi-config.sh")
	}
}

func keysOf(m map[string][]byte) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
```

- [ ] **Step 8: Run to verify failure** — `go test ./internal/deps/ -run TestGenerateDockerfilePiBake` → FAIL.

- [ ] **Step 9: Call the pi snippet** in `internal/deps/dockerfile.go` `GenerateDockerfile`, right after the claude block (after line 236) and update `inUserContext` (line 241). Add the `pi` import (`"github.com/majorcontext/moat/internal/providers/pi"`):

```go
	pluginResult := claude.GenerateDockerfileSnippet(opts.ClaudeMarketplaces, opts.ClaudePlugins, containerUser)
	b.WriteString(pluginResult.DockerfileSnippet)
	if pluginResult.ScriptName != "" {
		contextFiles[pluginResult.ScriptName] = pluginResult.ScriptContent
	}
	for name, content := range pluginResult.ExtraContextFiles {
		contextFiles[name] = content
	}

	// Pi: bake safe global settings + declared packages at build time.
	var piResult pi.SnippetResult
	if opts.PiBakeSettings {
		piResult = pi.GenerateDockerfileSnippet(opts.PiPackages, containerUser)
		b.WriteString(piResult.DockerfileSnippet)
		if piResult.ScriptName != "" {
			contextFiles[piResult.ScriptName] = piResult.ScriptContent
		}
	}

	// Restore root context only if user-space sections switched to moatuser ...
	inUserContext := len(c.userCustomDeps) > 0 || pluginResult.DockerfileSnippet != "" || piResult.DockerfileSnippet != ""
```

Note: if this creates an import cycle (`deps` → `providers/pi` → …), the build fails immediately at Step 11; `deps` already imports `providers/claude` the same way, and `providers/pi` does not import `deps`, so no cycle is expected — but if one appears, extract `GenerateDockerfileSnippet` into a leaf package `internal/providers/pi/pidockerfile` with no internal imports.

- [ ] **Step 10: Populate the fields** in `internal/run/manager_create.go` where `imageSpec` is built (~line 1161). Just before the struct literal, add:

```go
	var piPackages []string
	if opts.Config != nil {
		piPackages = opts.Config.Pi.Packages
	}
```

and add these two fields inside the `imageSpec := &deps.ImageSpec{ ... }` literal:

```go
		PiBakeSettings:       hasDep(installableDeps, "pi-cli"),
		PiPackages:           piPackages,
```

- [ ] **Step 11: Build + run tests** — `go build ./...` (catches any import cycle), then `go test ./internal/deps/ ./internal/run/ -run 'PiBake|ImageTag|Pi'` → PASS.

- [ ] **Step 12: Commit**
```bash
git add internal/deps/ internal/run/manager_create.go
git commit -m "feat(pi): wire build-time settings/packages bake into the image build"
```

---

### Task 4: Permissive-network warning in `moat pi`

**Files:**
- Modify: `internal/providers/pi/cli.go` (add `internal/ui` import; warn in `ConfigureAgent`)

- [ ] **Step 1: Add the import** to `internal/providers/pi/cli.go` (import block, lines 3-9):

```go
	"github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/ui"
```

- [ ] **Step 2: Emit the warning** in the `ConfigureAgent` closure in `runPi` (currently `cli.go:108-113`). `cfg` is guaranteed non-nil here; `!= "strict"` treats an empty/permissive policy as warn-worthy:

```go
		ConfigureAgent: func(cfg *config.Config) {
			// Running `moat pi` means the pi agent, regardless of any `agent:`
			// field in moat.yaml (which only sets the `moat run` default). This
			// makes the isPiRun guard in Create reliable.
			cfg.Agent = "pi"

			// Pi config (baseUrl/streamSimple/extensions) can redirect model
			// traffic to arbitrary hosts, so only the network policy actually
			// contains egress. Warn when it isn't strict.
			if cfg.Network.Policy != "strict" {
				ui.Warnf("Pi runs under a permissive network policy: Pi extensions/config can redirect " +
					"model traffic to arbitrary hosts. Use `network.policy: strict` for untrusted work.")
			}
		},
```

- [ ] **Step 3: Verify the `ui.Warnf` signature** — `grep -n "func Warnf" internal/ui/*.go`. If it is `Warnf(format string, args ...any)`, the call above is correct (a literal format with no args is fine; `%` chars must be escaped as `%%` — there are none here). If the helper is named `Warn`, use that instead.

- [ ] **Step 4: Build + smoke test** — `go build ./... && make lint`. Then:
```bash
go build -o /tmp/moat ./cmd/moat
cd $(mktemp -d) && printf 'agent: pi\n' > moat.yaml
/tmp/moat pi --provider anthropic --dry-run -p hi 2>&1 | grep -i "permissive network policy"   # expect the warning
printf 'agent: pi\nnetwork:\n  policy: strict\n' > moat.yaml
/tmp/moat pi --provider anthropic --dry-run -p hi 2>&1 | grep -ci "permissive network policy"   # expect 0
```
(The `--dry-run` path still runs `ConfigureAgent`. A missing anthropic grant makes resolution fail before the warning; grant a placeholder as in the design spike, or run with a configured backend.)

- [ ] **Step 5: Commit**
```bash
git add internal/providers/pi/cli.go
git commit -m "feat(pi): warn when moat pi runs under a permissive network policy"
```

---

### Task 5: End-to-end sandbox verification (DIND)

**Files:** none (verification).

- [ ] **Step 1:** `go build -o /tmp/moat ./cmd/moat`.
- [ ] **Step 2:** Inject a placeholder `anthropic` credential (as in the prior spike), create a temp workspace:
```
agent: pi
grants: [anthropic]
pi:
  packages: ["npm:chalk"]
```
- [ ] **Step 3:** `moat pi --no-sandbox -p "print hello"` and confirm from the build/run output: the image build runs `pi install 'npm:chalk'`; inside the container `pi list` shows `npm:chalk`; and `~/.pi/agent/settings.json` contains `"defaultProjectTrust": "never"` and the `packages` array. (A 401 from the placeholder key is the expected inference outcome — the bake is what we're verifying.) If interactive-container access is awkward, add a temporary `--prompt "cat ~/.pi/agent/settings.json && pi list"` style check.
- [ ] **Step 4:** Record the result in the design doc's grounding section. No commit unless a tagged e2e test is added.

---

### Task 6: Docs + changelog

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md` (`pi.packages`), `docs/content/guides/16-pi.md` (packages + safe-defaults + network warning), `CHANGELOG.md`

- [ ] **Step 1: moat.yaml reference** — under the `## Pi` section, add `pi.packages` (remote sources only: npm/git/https/ssh; installed at build; version-pin since there's no lockfile; a package's runtime deps must be added to `dependencies:`). Note the baked safe defaults (`defaultProjectTrust: never`, telemetry off) and that a workspace's `.pi/` config does not auto-load as a result.
- [ ] **Step 2: Pi guide** — add a "Packages" subsection (declare + example) and a "Safety defaults" note (trust never, telemetry off, permissive-policy warning, prefer strict for untrusted work).
- [ ] **Step 3: CHANGELOG** — under `### Added`, a bold entry: **Pi packages & safe defaults** — `pi.packages` baked at build time + Moat-owned safe global settings; `#NNN` placeholder (filled at PR time).
- [ ] **Step 4:** `make lint`; commit.
```bash
git add docs/ CHANGELOG.md
git commit -m "docs(pi): document pi.packages and baked safe defaults"
```

---

## Self-Review (author checklist)

- **Spec coverage:** `pi.packages` + validation (Task 1); build-time snippet baking settings + packages (Task 2); ImageSpec/Dockerfile/ImageTag wiring incl. rebuild-on-change (Task 3); permissive-network warning (Task 4); e2e (Task 5); docs (Task 6). httpProxy omitted (redundant) — reflected. No moat-init change — reflected.
- **Placeholder scan:** none — all code is concrete. The `ui.Warnf` name is verified in Task 4 Step 3 (the one lookup), with the fallback stated.
- **Type consistency:** `SnippetResult{DockerfileSnippet,ScriptName,ScriptContent}`, `GenerateDockerfileSnippet(packages,containerUser)`, `ImageSpec.PiBakeSettings/PiPackages`, `validatePiPackages`, `PiConfig.Packages`, `piConfigScriptName` — used consistently across tasks.
- **Open risk:** import cycle (`deps`→`providers/pi`) — mitigated (Task 3 Step 9 note); caught at build.
