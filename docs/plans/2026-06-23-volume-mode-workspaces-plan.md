# Volume-Mode Workspaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in `workspace.mode: volume` that copies the host working tree into an ephemeral Docker named volume (host protection + macOS I/O), with snapshot-based extraction back to the host.

**Architecture:** A new `WorkspaceConfig` selects bind (default) or volume mode. In volume mode the run manager mounts the host tree read-only at a staging path and a fresh per-run Docker named volume at `/workspace`; `moat-init.sh` (forced to run as root) copies staging → volume honoring excludes, then chowns. Extraction reuses the existing archive snapshot backend by first materializing the volume to a host temp dir via a short-lived sidecar, then archiving it with `.git` included. Docker-first; Apple and git-worktree workspaces are rejected with clear errors.

**Tech Stack:** Go, Cobra (CLI), Docker SDK (`github.com/docker/docker`), POSIX `sh` (`moat-init.sh`), `tar`.

**Source spec:** `docs/plans/2026-06-23-volume-mode-workspaces-design.md` (read it before starting). The one deferred decision — worktree handling — is **resolved: ship v1 with a detect-and-reject guard** (no reconstruction).

---

## File Structure

**New files:**
- `internal/config/workspace.go` — `WorkspaceConfig` type, `WorkspaceMode` constants, validation, effective-mode resolution. Keeps workspace-mode logic out of the already-large `config.go`.
- `internal/config/workspace_test.go` — table tests for parsing/validation/resolution.
- `internal/run/volume.go` — volume-mode helpers used by the manager: name derivation, mount construction, guards, env. Keeps `manager.go` (already ~3000 lines) from growing further.
- `internal/run/volume_test.go` — unit tests for the helpers.

**Modified files:**
- `internal/config/config.go:48` — add `Workspace WorkspaceConfig` field.
- `internal/config/mount.go:119` — harden `ValidateExcludes` (reject shell metacharacters); add `ValidateNoGitExclude`.
- `internal/container/runtime.go:365` — extend `MountConfig` with `Kind`/`Name`; add volume lifecycle methods to the `Runtime` interface; add `MountKind` constants.
- `internal/container/docker.go:167` — branch `buildContainerMounts` on `Kind`; implement `VolumeCreate`/`VolumeRemove`/`VolumeList`/`VolumeExport`.
- `internal/container/apple.go` — implement the new `Runtime` methods as clear "unsupported" errors.
- `internal/run/manager.go` — wire volume mode into mount setup (~660), guards, init env, force-root user (~2297), disable auto pre-run snapshot (~3065), register volume cleanup.
- `internal/snapshot/archive.go:18,44,156` — add `IncludeGit` to `ArchiveOptions`; gate the `.git` skip and the in-place `.git`-preserve on it; write archives `0600`.
- `internal/snapshot/engine.go:69` — thread `IncludeGit` through `EngineOptions`/`detectBackend`.
- `cmd/moat/run.go` (or wherever run flags live) — add `--workspace-mode`.
- `cmd/moat/cli/snapshot.go:74,400` — block in-place restore for volume-mode runs; materialize-from-volume on capture.
- `internal/daemon/*` — orphan `moat-ws-*` GC in idle cleanup (Task 11).
- `docs/content/reference/01-cli.md`, `docs/content/reference/02-moat-yaml.md`, `docs/content/guides/`, `CHANGELOG.md` (Task 12).

> **Note on `storage.Metadata`:** several tasks need to know after the run starts whether it was volume-mode. Task 6 adds a `WorkspaceMode string` (and `WorkspaceVolume string`) field to the run metadata struct in `internal/storage`. Grep for the metadata struct (`grep -rn "Workspace " internal/storage`) and add the field there; the exact file is `internal/storage/metadata.go` unless the layout has changed.

---

## Task 1: WorkspaceConfig type and mode resolution

**Files:**
- Create: `internal/config/workspace.go`
- Create: `internal/config/workspace_test.go`
- Modify: `internal/config/config.go:48`

- [ ] **Step 1: Write the failing test**

`internal/config/workspace_test.go`:

```go
package config

import "testing"

func TestWorkspaceModeValidate(t *testing.T) {
	tests := []struct {
		name    string
		mode    string
		wantErr bool
	}{
		{"empty defaults ok", "", false},
		{"bind", "bind", false},
		{"volume", "volume", false},
		{"invalid", "vol", true},
		{"garbage", "host", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wc := WorkspaceConfig{Mode: tt.mode}
			err := wc.Validate()
			if (err != nil) != tt.wantErr {
				t.Fatalf("Validate(%q) err=%v wantErr=%v", tt.mode, err, tt.wantErr)
			}
		})
	}
}

func TestResolveWorkspaceMode(t *testing.T) {
	// CLI override wins over yaml; yaml wins over default.
	cases := []struct {
		yaml     string
		override string
		want     WorkspaceMode
	}{
		{"", "", WorkspaceModeBind},          // default
		{"volume", "", WorkspaceModeVolume},  // yaml
		{"bind", "volume", WorkspaceModeVolume}, // override wins
		{"volume", "bind", WorkspaceModeBind},   // override wins
	}
	for _, c := range cases {
		got, err := ResolveWorkspaceMode(WorkspaceConfig{Mode: c.yaml}, c.override)
		if err != nil {
			t.Fatalf("ResolveWorkspaceMode(%q,%q) unexpected err: %v", c.yaml, c.override, err)
		}
		if got != c.want {
			t.Fatalf("ResolveWorkspaceMode(%q,%q)=%q want %q", c.yaml, c.override, got, c.want)
		}
	}
}

func TestResolveWorkspaceModeInvalidOverride(t *testing.T) {
	if _, err := ResolveWorkspaceMode(WorkspaceConfig{}, "vol"); err == nil {
		t.Fatal("expected error for invalid override")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestWorkspaceMode|TestResolveWorkspaceMode' -v`
Expected: FAIL — `undefined: WorkspaceConfig` / `WorkspaceModeBind` / `ResolveWorkspaceMode`.

- [ ] **Step 3: Write minimal implementation**

`internal/config/workspace.go`:

```go
package config

import "fmt"

// WorkspaceMode selects how the host working tree is presented to the container.
type WorkspaceMode string

const (
	// WorkspaceModeBind bind-mounts the host tree at /workspace (default, current behavior).
	WorkspaceModeBind WorkspaceMode = "bind"
	// WorkspaceModeVolume copies the host tree into an ephemeral Docker named volume.
	WorkspaceModeVolume WorkspaceMode = "volume"
)

// WorkspaceConfig is the moat.yaml `workspace:` block.
type WorkspaceConfig struct {
	// Mode is "bind" (default) or "volume". Empty means bind.
	Mode string `yaml:"mode,omitempty"`
}

// Validate rejects any mode other than "", "bind", or "volume".
func (w WorkspaceConfig) Validate() error {
	switch w.Mode {
	case "", string(WorkspaceModeBind), string(WorkspaceModeVolume):
		return nil
	default:
		return fmt.Errorf("workspace.mode %q is invalid (must be 'bind' or 'volume')", w.Mode)
	}
}

// ResolveWorkspaceMode applies precedence: CLI override > yaml > default(bind).
// override is the raw --workspace-mode flag value ("" when unset).
func ResolveWorkspaceMode(w WorkspaceConfig, override string) (WorkspaceMode, error) {
	pick := w.Mode
	if override != "" {
		pick = override
	}
	switch pick {
	case "", string(WorkspaceModeBind):
		return WorkspaceModeBind, nil
	case string(WorkspaceModeVolume):
		return WorkspaceModeVolume, nil
	default:
		return "", fmt.Errorf("workspace mode %q is invalid (must be 'bind' or 'volume')", pick)
	}
}
```

Then add the field to `internal/config/config.go` after the `Hooks` line (line 48):

```go
	Hooks     HooksConfig    `yaml:"hooks,omitempty"`
	Workspace WorkspaceConfig `yaml:"workspace,omitempty"`
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestWorkspaceMode|TestResolveWorkspaceMode' -v`
Expected: PASS.

- [ ] **Step 5: Wire validation into config loading**

Find where the config is validated after parse (`grep -n "func .*Validate" internal/config/config.go` and the loader that calls it, e.g. `Load`/`Parse`). Add a call to `cfg.Workspace.Validate()` alongside the existing validations and return its error. If there is no central validate, add the call in the loader right after unmarshal.

- [ ] **Step 6: Commit**

```bash
git add internal/config/workspace.go internal/config/workspace_test.go internal/config/config.go
git commit -m "feat(config): add workspace.mode (bind|volume) config and resolution"
```

---

## Task 2: Harden excludes (shell metacharacters + .git)

**Files:**
- Modify: `internal/config/mount.go:119`
- Test: `internal/config/mount_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

Append to `internal/config/mount_test.go`:

```go
func TestValidateExcludesRejectsShellMetacharacters(t *testing.T) {
	bad := []string{"foo;rm -rf /", "$(whoami)", "a`b`", "x|y", "we ird", "a&b", "a>b"}
	for _, p := range bad {
		if _, err := ValidateExcludes([]string{p}, "/workspace"); err == nil {
			t.Errorf("expected rejection for exclude %q", p)
		}
	}
	// Companion: ordinary patterns still validate.
	good := []string{"node_modules", "dist", ".cache/build", "a-b_c.tmp"}
	if _, err := ValidateExcludes(good, "/workspace"); err != nil {
		t.Errorf("ordinary excludes rejected: %v", err)
	}
}

func TestValidateNoGitExclude(t *testing.T) {
	for _, p := range []string{".git", ".git/objects", ".git/config"} {
		if err := ValidateNoGitExclude([]string{p}); err == nil {
			t.Errorf("expected .git exclude %q to be rejected in volume mode", p)
		}
	}
	if err := ValidateNoGitExclude([]string{"node_modules", "dist"}); err != nil {
		t.Errorf("non-.git excludes should pass: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run 'TestValidateExcludesRejectsShell|TestValidateNoGitExclude' -v`
Expected: FAIL — `undefined: ValidateNoGitExclude`, and the metacharacter case fails (current `ValidateExcludes` allows them).

- [ ] **Step 3: Write minimal implementation**

In `internal/config/mount.go`, add a character-class check inside the `ValidateExcludes` loop, right after the empty-string check (line ~130):

```go
		// Reject shell metacharacters. Exclude patterns are interpolated into a
		// root-run tar command during volume-mode copy-in (moat-init.sh); only a
		// path-safe character class is allowed. Patterns are fed via
		// --exclude-from a temp file, but defense-in-depth validation stops bad
		// input at config load.
		if i := strings.IndexFunc(exc, func(r rune) bool {
			switch {
			case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
				return false
			case r == '/' || r == '.' || r == '-' || r == '_' || r == '*':
				return false
			default:
				return true
			}
		}); i >= 0 {
			return nil, fmt.Errorf("mount %s: exclude path %q contains unsupported character %q (allowed: letters, digits, / . - _ *)", target, exc, string(exc[i]))
		}
```

Then add a new function at the end of the file:

```go
// ValidateNoGitExclude rejects excluding .git (or any subpath) in volume mode.
// A partial .git is a broken repository, so volume-mode copy-in must always
// include the full .git directory.
func ValidateNoGitExclude(excludes []string) error {
	for _, exc := range excludes {
		c := filepath.Clean(exc)
		if c == ".git" || strings.HasPrefix(c, ".git/") {
			return fmt.Errorf("exclude %q is not allowed in volume mode: .git must be copied whole (a partial .git is a broken repository)", exc)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run 'TestValidateExcludesRejectsShell|TestValidateNoGitExclude' -v`
Expected: PASS.

- [ ] **Step 5: Run the whole config package to catch regressions**

Run: `go test ./internal/config/ -v`
Expected: PASS (existing exclude tests still pass — letters/digits/`/.-_*` cover the documented `node_modules`, `dist`, `.git/objects` examples).

- [ ] **Step 6: Commit**

```bash
git add internal/config/mount.go internal/config/mount_test.go
git commit -m "feat(config): reject shell metacharacters and .git in volume-mode excludes"
```

---

## Task 3: MountConfig named-volume kind

**Files:**
- Modify: `internal/container/runtime.go:365`
- Modify: `internal/container/docker.go:167`
- Test: `internal/container/docker_test.go` (create if absent)

- [ ] **Step 1: Write the failing test**

`internal/container/docker_test.go`:

```go
package container

import (
	"testing"

	"github.com/docker/docker/api/types/mount"
)

func TestBuildContainerMountsBranchesOnKind(t *testing.T) {
	binds := []MountConfig{
		{Source: "/host/ws", Target: "/mnt/host-workspace", ReadOnly: true},        // zero-value Kind == bind
		{Kind: MountKindVolume, Name: "moat-ws-abc", Target: "/workspace"},          // volume
	}
	got := buildContainerMounts(binds, nil)
	if len(got) != 2 {
		t.Fatalf("want 2 mounts, got %d", len(got))
	}
	// Companion: zero-value Kind still produces a bind mount.
	if got[0].Type != mount.TypeBind || got[0].Source != "/host/ws" || !got[0].ReadOnly {
		t.Errorf("bind mount wrong: %+v", got[0])
	}
	if got[1].Type != mount.TypeVolume || got[1].Source != "moat-ws-abc" || got[1].Target != "/workspace" {
		t.Errorf("volume mount wrong: %+v", got[1])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/container/ -run TestBuildContainerMountsBranchesOnKind -v`
Expected: FAIL — `undefined: MountKindVolume`, `unknown field Kind`.

- [ ] **Step 3: Write minimal implementation**

In `internal/container/runtime.go`, replace the `MountConfig` struct (line 365) with:

```go
// MountKind distinguishes a host bind mount from a named-volume mount.
type MountKind string

const (
	// MountKindBind is a host-path bind mount (default, zero value).
	MountKindBind MountKind = ""
	// MountKindVolume is a named-volume mount (Docker named volume).
	MountKindVolume MountKind = "volume"
)

// MountConfig describes a mount into the container. The zero value is a bind
// mount, so existing call sites are unaffected.
type MountConfig struct {
	Source   string    // host path for bind mounts; ignored for volume mounts
	Target   string    // container path
	ReadOnly bool      // read-only mount
	Kind     MountKind // "" (bind) or "volume"
	Name     string    // volume name when Kind == MountKindVolume
}
```

In `internal/container/docker.go`, replace the bind loop in `buildContainerMounts` (lines 169-176) with:

```go
	for _, m := range binds {
		if m.Kind == MountKindVolume {
			mounts = append(mounts, mount.Mount{
				Type:     mount.TypeVolume,
				Source:   m.Name,
				Target:   m.Target,
				ReadOnly: m.ReadOnly,
			})
			continue
		}
		mounts = append(mounts, mount.Mount{
			Type:     mount.TypeBind,
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/container/ -run TestBuildContainerMountsBranchesOnKind -v`
Expected: PASS.

- [ ] **Step 5: Build the whole package to catch call-site breaks**

Run: `go build ./internal/container/...`
Expected: success — existing `MountConfig{Source,Target,ReadOnly}` literals are unaffected (new fields default to zero).

- [ ] **Step 6: Commit**

```bash
git add internal/container/runtime.go internal/container/docker.go internal/container/docker_test.go
git commit -m "feat(container): support named-volume mounts in MountConfig"
```

---

## Task 4: Volume lifecycle on the Runtime interface

**Files:**
- Modify: `internal/container/runtime.go:48` (interface)
- Modify: `internal/container/docker.go` (implementation)
- Modify: `internal/container/apple.go` (unsupported stubs)
- Test: `internal/container/apple_test.go` (create if absent)

- [ ] **Step 1: Write the failing test (Apple returns clear errors)**

`internal/container/apple_test.go`:

```go
package container

import (
	"context"
	"strings"
	"testing"
)

func TestAppleVolumeMethodsUnsupported(t *testing.T) {
	r := &AppleRuntime{} // zero value is fine; methods must not touch the binary
	ctx := context.Background()
	if err := r.VolumeCreate(ctx, "moat-ws-x"); err == nil || !strings.Contains(err.Error(), "volume mode") {
		t.Errorf("VolumeCreate: want 'volume mode' error, got %v", err)
	}
	if err := r.VolumeRemove(ctx, "moat-ws-x", true); err == nil {
		t.Errorf("VolumeRemove: want error, got nil")
	}
	if _, err := r.VolumeList(ctx, "moat-ws-"); err == nil {
		t.Errorf("VolumeList: want error, got nil")
	}
	if err := r.VolumeExport(ctx, "moat-ws-x", t.TempDir()); err == nil {
		t.Errorf("VolumeExport: want error, got nil")
	}
}
```

> Confirm the concrete type name with `grep -n "Runtime struct" internal/container/apple.go` (it is `AppleRuntime` unless renamed). Adjust the literal if needed.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/container/ -run TestAppleVolumeMethodsUnsupported -v`
Expected: FAIL — methods undefined.

- [ ] **Step 3: Add the methods to the interface and both implementations**

In `internal/container/runtime.go`, inside the `Runtime interface` (after `StartContainer`, ~line 60):

```go
	// VolumeCreate creates a named volume. No-op-safe if it already exists.
	VolumeCreate(ctx context.Context, name string) error
	// VolumeRemove removes a named volume. force removes even if it appears in use.
	VolumeRemove(ctx context.Context, name string, force bool) error
	// VolumeList returns names of volumes whose name starts with prefix.
	VolumeList(ctx context.Context, prefix string) ([]string, error)
	// VolumeExport copies the volume's contents to hostDir on the host
	// filesystem (used for snapshot capture). hostDir must already exist.
	VolumeExport(ctx context.Context, name, hostDir string) error
```

In `internal/container/docker.go` (Docker SDK is already imported as `volume "github.com/docker/docker/api/types/volume"` — add the import if missing):

```go
func (r *DockerRuntime) VolumeCreate(ctx context.Context, name string) error {
	_, err := r.client.VolumeCreate(ctx, volume.CreateOptions{
		Name:   name,
		Labels: map[string]string{"moat": "workspace"},
	})
	if err != nil {
		return fmt.Errorf("create volume %s: %w", name, err)
	}
	return nil
}

func (r *DockerRuntime) VolumeRemove(ctx context.Context, name string, force bool) error {
	if err := r.client.VolumeRemove(ctx, name, force); err != nil {
		return fmt.Errorf("remove volume %s: %w", name, err)
	}
	return nil
}

func (r *DockerRuntime) VolumeList(ctx context.Context, prefix string) ([]string, error) {
	resp, err := r.client.VolumeList(ctx, volume.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list volumes: %w", err)
	}
	var names []string
	for _, v := range resp.Volumes {
		if strings.HasPrefix(v.Name, prefix) {
			names = append(names, v.Name)
		}
	}
	return names, nil
}

// VolumeExport runs a short-lived container that mounts the volume read-only and
// copies its contents to hostDir (bind-mounted rw). Uses the same base image as
// the runtime's default to guarantee `cp`/`tar` availability.
func (r *DockerRuntime) VolumeExport(ctx context.Context, name, hostDir string) error {
	cfg := Config{
		Name:       "moat-export-" + name,
		Image:      exportHelperImage, // e.g. "debian:bookworm-slim"; see note below
		Cmd:        []string{"sh", "-c", "cp -a /vol/. /out/"},
		Mounts: []MountConfig{
			{Kind: MountKindVolume, Name: name, Target: "/vol", ReadOnly: true},
			{Source: hostDir, Target: "/out", ReadOnly: false},
		},
	}
	id, err := r.CreateContainer(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create export container: %w", err)
	}
	defer func() { _ = r.RemoveContainer(ctx, id) }()
	if err := r.StartContainer(ctx, id); err != nil {
		return fmt.Errorf("start export container: %w", err)
	}
	if _, err := r.WaitContainer(ctx, id); err != nil {
		return fmt.Errorf("export container wait: %w", err)
	}
	return nil
}
```

> **`exportHelperImage`:** define a package const set to the project's standard slim base (grep `image.Resolve`/`debian:bookworm-slim` in `internal/image` for the canonical value). `RemoveContainer`/`WaitContainer` already exist on the runtime — confirm exact names with `grep -n "func (r \*DockerRuntime)" internal/container/docker.go` and adjust.

In `internal/container/apple.go`:

```go
func (r *AppleRuntime) VolumeCreate(ctx context.Context, name string) error {
	return fmt.Errorf("volume mode is not supported with the Apple container runtime; use the Docker runtime (--runtime docker) or workspace.mode: bind")
}
func (r *AppleRuntime) VolumeRemove(ctx context.Context, name string, force bool) error {
	return fmt.Errorf("volume mode is not supported with the Apple container runtime")
}
func (r *AppleRuntime) VolumeList(ctx context.Context, prefix string) ([]string, error) {
	return nil, fmt.Errorf("volume mode is not supported with the Apple container runtime")
}
func (r *AppleRuntime) VolumeExport(ctx context.Context, name, hostDir string) error {
	return fmt.Errorf("volume mode is not supported with the Apple container runtime")
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/container/ -run 'TestAppleVolumeMethodsUnsupported|TestBuildContainerMounts' -v`
Expected: PASS.

- [ ] **Step 5: Build to confirm the interface is satisfied by both runtimes**

Run: `go build ./internal/container/...`
Expected: success (if either runtime is missing a method, the build fails here — that is the interface-conformance check).

- [ ] **Step 6: Commit**

```bash
git add internal/container/runtime.go internal/container/docker.go internal/container/apple.go internal/container/apple_test.go
git commit -m "feat(container): add volume lifecycle methods to Runtime (Docker impl, Apple errors)"
```

---

## Task 5: CLI `--workspace-mode` flag

**Files:**
- Modify: the run command file (find with `grep -rn "Flags().StringVar\|Flags().BoolVar" cmd/moat | grep -i run`; typically `cmd/moat/run.go`)
- Modify: run-options plumbing where flags become `run.CreateOptions`
- Test: covered by Task 6's manager tests + a CLI smoke check

- [ ] **Step 1: Add the flag**

In the run command's flag registration:

```go
runCmd.Flags().String("workspace-mode", "", "workspace mode: 'bind' (default) or 'volume' (isolated copy in a named volume)")
```

- [ ] **Step 2: Resolve and thread the mode**

Where the run command builds its options from flags and config (search for where `opts.Config` / `run.CreateOptions` is assembled), resolve the effective mode and store it on the options:

```go
overrideMode, _ := cmd.Flags().GetString("workspace-mode")
mode, err := config.ResolveWorkspaceMode(cfg.Workspace, overrideMode)
if err != nil {
	return err
}
opts.WorkspaceMode = mode // add this field to run.CreateOptions (Task 6, Step 3)
```

- [ ] **Step 3: Verify the flag parses**

Run: `go build ./... && ./moat run --help | grep -A1 workspace-mode`
Expected: the flag and its help text appear.

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/
git commit -m "feat(cli): add --workspace-mode flag"
```

> No `--volume-workspace` alias is added (design decision: single flag, no conflict-detection path).

---

## Task 6: Run manager — realize volume mode

**Files:**
- Create: `internal/run/volume.go`
- Create: `internal/run/volume_test.go`
- Modify: `internal/run/manager.go` (mount setup ~660; container user ~2297; pre-run snapshot ~3065; cleanup path)
- Modify: `internal/run/` options struct (`CreateOptions`) to carry `WorkspaceMode`
- Modify: `internal/storage/metadata.go` — persist `WorkspaceMode` + `WorkspaceVolume`

- [ ] **Step 1: Write the failing test for the helpers**

`internal/run/volume_test.go`:

```go
package run

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

func TestWorkspaceVolumeName(t *testing.T) {
	got := WorkspaceVolumeName("run_a1b2c3")
	if got != "moat-ws-run_a1b2c3" {
		t.Fatalf("got %q", got)
	}
}

func TestVolumeMounts(t *testing.T) {
	mounts := VolumeWorkspaceMounts("/host/ws", "moat-ws-x")
	if len(mounts) != 2 {
		t.Fatalf("want 2 mounts, got %d", len(mounts))
	}
	// staging bind: host tree, read-only, at the staging path
	if mounts[0].Source != "/host/ws" || mounts[0].Target != stagingPath || !mounts[0].ReadOnly {
		t.Errorf("staging mount wrong: %+v", mounts[0])
	}
	// volume mount at /workspace
	if mounts[1].Kind != container.MountKindVolume || mounts[1].Name != "moat-ws-x" || mounts[1].Target != "/workspace" {
		t.Errorf("volume mount wrong: %+v", mounts[1])
	}
}

func TestVolumeGuardRejectsWorktree(t *testing.T) {
	// A workspace whose .git is a FILE must be rejected.
	dir := t.TempDir()
	if err := writeFile(dir+"/.git", "gitdir: /elsewhere/.git/worktrees/x"); err != nil {
		t.Fatal(err)
	}
	err := GuardVolumeWorkspace(dir, container.RuntimeDocker)
	if err == nil || !strings.Contains(err.Error(), "worktree") {
		t.Fatalf("want worktree rejection, got %v", err)
	}
}

func TestVolumeGuardRejectsApple(t *testing.T) {
	dir := t.TempDir() // no .git → not a worktree
	err := GuardVolumeWorkspace(dir, container.RuntimeApple)
	if err == nil || !strings.Contains(err.Error(), "Docker") {
		t.Fatalf("want Apple rejection, got %v", err)
	}
}

func TestVolumeGuardAllowsNormalRepoOnDocker(t *testing.T) {
	dir := t.TempDir()
	if err := mkdirAll(dir + "/.git"); err != nil { // .git is a DIRECTORY → normal repo
		t.Fatal(err)
	}
	if err := GuardVolumeWorkspace(dir, container.RuntimeDocker); err != nil {
		t.Fatalf("normal repo on docker should pass: %v", err)
	}
}
```

> `writeFile`/`mkdirAll` are tiny test helpers — define them in the test file with `os.WriteFile`/`os.MkdirAll`. Confirm the runtime-type constants (`container.RuntimeDocker`, `container.RuntimeApple`) with `grep -n "RuntimeDocker\|RuntimeApple" internal/container/`.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/run/ -run 'TestWorkspaceVolumeName|TestVolume' -v`
Expected: FAIL — helpers undefined.

- [ ] **Step 3: Write the helpers**

`internal/run/volume.go`:

```go
package run

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/worktree"
)

// stagingPath is where the host tree is bind-mounted read-only during copy-in.
const stagingPath = "/mnt/host-workspace"

// WorkspaceVolumeName derives the per-run Docker volume name.
func WorkspaceVolumeName(runID string) string {
	return "moat-ws-" + runID
}

// VolumeWorkspaceMounts returns the staging bind (host tree, read-only) and the
// named-volume mount at /workspace for volume mode.
func VolumeWorkspaceMounts(hostWorkspace, volumeName string) []container.MountConfig {
	return []container.MountConfig{
		{Source: hostWorkspace, Target: stagingPath, ReadOnly: true},
		{Kind: container.MountKindVolume, Name: volumeName, Target: "/workspace"},
	}
}

// GuardVolumeWorkspace rejects volume mode when it cannot work: non-Docker
// runtime, or a git worktree/submodule (.git is a file, not a directory).
func GuardVolumeWorkspace(hostWorkspace string, rt container.RuntimeType) error {
	if rt != container.RuntimeDocker {
		return fmt.Errorf("volume mode requires the Docker runtime; set workspace.mode: bind or run with --runtime docker")
	}
	gitPath := filepath.Join(hostWorkspace, ".git")
	if info, err := os.Lstat(gitPath); err == nil && !info.IsDir() {
		return fmt.Errorf("volume mode does not support git worktrees or submodules (.git is a file at %s); use the main checkout or workspace.mode: bind", gitPath)
	}
	return nil
}
```

> If `worktree.ResolveGitDir` is the preferred detector, use it instead of the `Lstat` check for consistency with bind mode — `info, _ := worktree.ResolveGitDir(hostWorkspace); if info != nil { return ...worktree error... }`. Keep the import either way only if used.

- [ ] **Step 4: Run helper tests to verify they pass**

Run: `go test ./internal/run/ -run 'TestWorkspaceVolumeName|TestVolume' -v`
Expected: PASS.

- [ ] **Step 5: Add `WorkspaceMode` to options and metadata**

In the `CreateOptions` struct (`grep -n "type CreateOptions" internal/run/manager.go`), add:

```go
	WorkspaceMode config.WorkspaceMode // resolved bind|volume; empty == bind
```

In `internal/storage/metadata.go`, add to the run metadata struct:

```go
	WorkspaceMode   string `json:"workspace_mode,omitempty"`   // "bind" or "volume"
	WorkspaceVolume string `json:"workspace_volume,omitempty"` // moat-ws-<run-id> in volume mode
```

- [ ] **Step 6: Wire volume mode into manager mount setup**

In `internal/run/manager.go`, replace the implicit workspace-mount block (lines 660-682, the `if !hasExplicitWorkspace` block plus the worktree `.git` mount) with a mode branch:

```go
	volumeMode := opts.WorkspaceMode == config.WorkspaceModeVolume
	var workspaceVolumeName string

	if volumeMode {
		if err := GuardVolumeWorkspace(opts.Workspace, m.defaultRuntime().Type()); err != nil {
			return nil, err
		}
		// Reject .git excludes on the /workspace mount before we copy in.
		if opts.Config != nil {
			for _, me := range opts.Config.Mounts {
				if me.Target == "/workspace" {
					if err := config.ValidateNoGitExclude(me.Exclude); err != nil {
						return nil, err
					}
				}
			}
		}
		workspaceVolumeName = WorkspaceVolumeName(opts.RunID)
		mounts = append(mounts, VolumeWorkspaceMounts(opts.Workspace, workspaceVolumeName)...)
	} else if !hasExplicitWorkspace {
		mounts = append(mounts, container.MountConfig{
			Source:   opts.Workspace,
			Target:   "/workspace",
			ReadOnly: false,
		})
	}

	// Worktree main-.git bind only applies to bind mode (volume mode rejects worktrees).
	if !volumeMode {
		if info, err := worktree.ResolveGitDir(opts.Workspace); err != nil {
			log.Debug("failed to resolve worktree git dir", "error", err)
		} else if info != nil {
			mounts = append(mounts, container.MountConfig{Source: info.MainGitDir, Target: info.MainGitDir, ReadOnly: false})
		}
	}
```

> Confirm `opts.RunID` is the field carrying the run id at this point (`grep -n "RunID" internal/run/manager.go`). If the id is generated later, derive the volume name where the id is known and thread it down.

- [ ] **Step 7: Create the volume and set init env**

Immediately after computing `workspaceVolumeName`, create the volume and add env (find where the container `Env` slice and the volume creation can run before `CreateContainer`). Add:

```go
	if volumeMode {
		if err := m.defaultRuntime().VolumeCreate(ctx, workspaceVolumeName); err != nil {
			return nil, fmt.Errorf("create workspace volume: %w", err)
		}
		// Pass copy-in instructions to moat-init.sh.
		extraEnv = append(extraEnv,
			"MOAT_WORKSPACE_VOLUME=1",
			"MOAT_WORKSPACE_STAGING="+stagingPath,
		)
		// Build the NUL-delimited exclude list for the /workspace mount.
		if excl := workspaceExcludes(opts.Config); excl != "" {
			extraEnv = append(extraEnv, "MOAT_WORKSPACE_EXCLUDES="+excl)
		}
	}
```

Add a small helper to `internal/run/volume.go` (and a test in `volume_test.go`):

```go
// workspaceExcludes joins the /workspace mount's excludes into a NUL-delimited
// string for MOAT_WORKSPACE_EXCLUDES (consumed by moat-init.sh --exclude-from).
func workspaceExcludes(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	for _, me := range cfg.Mounts {
		if me.Target == "/workspace" {
			return strings.Join(me.Exclude, "\x00")
		}
	}
	return ""
}
```

> `extraEnv`/`ctx`/`m.defaultRuntime()` names: confirm the env accumulator variable used for the container (`grep -n "Env:" internal/run/manager.go` near line 2746) and append to that. If env is assembled later, stash the strings on a local and merge at assembly.

- [ ] **Step 8: Force root container user in volume mode**

At the container-user selection (lines 2297-2309), make volume mode force root so `moat-init.sh` can populate + chown, then drop via its existing gosu path:

```go
	var containerUser string
	if volumeMode {
		// Volume mode must start as root: the fresh volume mountpoint is root-owned
		// and populate_workspace_volume()/chown run as root before privilege drop.
		containerUser = "0:0"
	} else if goruntime.GOOS == "linux" {
		// ... existing workspace-UID logic unchanged ...
	}
```

> `volumeMode` must be in scope here. It is computed in the mount-setup block (Step 6); if that is a different function than the container-config assembly, recompute it from `opts.WorkspaceMode` or thread it on the run struct.

- [ ] **Step 9: Disable the automatic pre-run snapshot in volume mode**

At the pre-run snapshot site (line ~3065, `if r.SnapEngine != nil && !r.DisablePreRunSnapshot`), ensure volume-mode runs set `r.DisablePreRunSnapshot = true` (the host staging tree is not the live tree, so an auto snapshot of it is misleading). Set it where `r` is constructed for volume-mode runs, or guard inline:

```go
	if r.SnapEngine != nil && !r.DisablePreRunSnapshot && opts.WorkspaceMode != config.WorkspaceModeVolume {
		if _, err := r.SnapEngine.Create(snapshot.TypePreRun, ""); err != nil {
			// ... existing handling ...
		}
	}
```

- [ ] **Step 10: Persist metadata + register cleanup**

Where run metadata is written (`grep -n "Workspace:" internal/run/manager.go`, ~line 607), set:

```go
		WorkspaceMode:   string(opts.WorkspaceMode),
		WorkspaceVolume: workspaceVolumeName, // "" in bind mode
```

In the run cleanup/destroy path (`grep -n "func .*cleanup\|RemoveContainer" internal/run/manager.go`), after the container is stopped/removed, remove the volume:

```go
	if name := r.meta.WorkspaceVolume; name != "" {
		if err := m.defaultRuntime().VolumeRemove(ctx, name, true); err != nil {
			log.Warn("failed to remove workspace volume", "name", name, "error", err)
		}
	}
```

> Volume removal must happen AFTER the container is removed (Docker refuses removing an in-use volume). Place it after the existing container-removal call.

- [ ] **Step 11: Run the run package tests**

Run: `make test-unit ARGS='-run TestVolume ./internal/run/'` (or `go test ./internal/run/ -run 'Volume' -v`)
Expected: PASS. Then `go build ./...` to confirm manager wiring compiles.

- [ ] **Step 12: Commit**

```bash
git add internal/run/volume.go internal/run/volume_test.go internal/run/manager.go internal/storage/metadata.go cmd/moat/
git commit -m "feat(run): realize volume mode — staging bind, named volume, guards, root user, cleanup"
```

---

## Task 7: Init script — populate the volume

**Files:**
- Modify: `internal/deps/scripts/moat-init.sh`
- Test: `internal/deps/scripts/moat-init_volume_test.go` (a Go test that runs the script fragment under `sh`), or a shell test if the repo has one — check `ls internal/deps/scripts/*_test*` first.

- [ ] **Step 1: Write the failing test**

Create `internal/deps/scripts/moat-init_volume_test.go`. It writes a tiny staging tree + a fake `/workspace`, runs `populate_workspace_volume` extracted from the script, and asserts excludes/symlinks are honored. If isolating one function is awkward, test the exact `tar` pipeline the function will use:

```go
package scripts

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVolumeCopyInHonorsExcludesAndSymlinks(t *testing.T) {
	staging := t.TempDir()
	workspace := t.TempDir()
	must(t, os.MkdirAll(filepath.Join(staging, "node_modules"), 0755))
	must(t, os.WriteFile(filepath.Join(staging, "node_modules", "big"), []byte("x"), 0644))
	must(t, os.WriteFile(filepath.Join(staging, "main.go"), []byte("package main"), 0644))
	must(t, os.Symlink("/etc/hostname", filepath.Join(staging, "danglink"))) // points outside tree

	excludeFile := filepath.Join(t.TempDir(), "excludes")
	must(t, os.WriteFile(excludeFile, []byte("./node_modules\x00"), 0644))

	// This is the exact pipeline populate_workspace_volume() runs.
	cmd := exec.Command("sh", "-c",
		`set -e; cd "$1"; tar --no-dereference --null --exclude-from="$3" -cf - . | (cd "$2" && tar -xf -)`,
		"sh", staging, workspace, excludeFile)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("copy-in failed: %v\n%s", err, out)
	}

	// Companion: non-excluded file present.
	if _, err := os.Stat(filepath.Join(workspace, "main.go")); err != nil {
		t.Errorf("main.go should be copied: %v", err)
	}
	// Excluded dir absent.
	if _, err := os.Stat(filepath.Join(workspace, "node_modules")); !os.IsNotExist(err) {
		t.Errorf("node_modules should be excluded, stat err=%v", err)
	}
	// Symlink copied as a symlink (not its target contents).
	fi, err := os.Lstat(filepath.Join(workspace, "danglink"))
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("danglink should be a symlink, mode=%v err=%v", fi.Mode(), err)
	}
}

func must(t *testing.T, err error) { t.Helper(); if err != nil { t.Fatal(err) } }
```

> Confirm the package name to use (`head -1` any existing `*_test.go` in that dir, or use `package scripts`). If GNU `tar` `--null`/`--exclude-from=-` flag support differs in CI, the test still exercises the same flags the script ships with.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/deps/scripts/ -run TestVolumeCopyIn -v`
Expected: FAIL only if the pipeline is wrong; if it passes immediately it has validated the exact command the script must run — proceed to wire it into the script and keep the test as a regression guard.

- [ ] **Step 3: Add `populate_workspace_volume` to `moat-init.sh`**

In `internal/deps/scripts/moat-init.sh`, add the function and call it from the root branch **before** the `pre_run` hook and the privilege drop. Find the existing `run_pre_run_hook` call and the `id -u` root branch; insert the call ahead of them.

```sh
populate_workspace_volume() {
  [ "${MOAT_WORKSPACE_VOLUME:-}" = "1" ] || return 0

  staging="${MOAT_WORKSPACE_STAGING:-/mnt/host-workspace}"

  # Build a NUL-delimited exclude file from MOAT_WORKSPACE_EXCLUDES. Never expand
  # the variable into the tar command line — it originates in user moat.yaml and
  # this runs as root.
  exclude_file="$(mktemp)"
  if [ -n "${MOAT_WORKSPACE_EXCLUDES:-}" ]; then
    printf '%s' "$MOAT_WORKSPACE_EXCLUDES" > "$exclude_file"
  fi

  # Copy staging -> /workspace. --no-dereference keeps symlinks as symlinks
  # (no out-of-tree target leakage); --null + --exclude-from reads NUL records.
  ( cd "$staging" && tar --no-dereference --null --exclude-from="$exclude_file" -cf - . ) \
    | ( cd /workspace && tar -xf - )
  rc=$?
  rm -f "$exclude_file"
  if [ "$rc" -ne 0 ]; then
    echo "moat: failed to populate workspace volume (tar exit $rc)" >&2
    exit "$rc"
  fi

  # The fresh volume mountpoint is root-owned; hand it to the agent user.
  chown -R moatuser:moatuser /workspace
}

# ... in the root branch, before run_pre_run_hook and the gosu privilege drop:
populate_workspace_volume
```

> Match the script's existing style: it uses `MOAT_*` env guards and a root branch that drops privileges via gosu/su. Place `populate_workspace_volume` so it runs as root and before the workspace is handed to `moatuser`. If `mktemp` is not guaranteed in the base image, use a fixed path like `/tmp/moat-excludes.$$`.

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/deps/scripts/ -run TestVolumeCopyIn -v`
Expected: PASS.

- [ ] **Step 5: Lint the script**

Run: `shellcheck internal/deps/scripts/moat-init.sh` (if available; otherwise `sh -n internal/deps/scripts/moat-init.sh` for a syntax check)
Expected: no errors.

- [ ] **Step 6: Commit**

```bash
git add internal/deps/scripts/moat-init.sh internal/deps/scripts/moat-init_volume_test.go
git commit -m "feat(init): populate workspace volume from staging (excludes, no-deref, chown)"
```

---

## Task 8: Snapshot capture from the volume

**Files:**
- Modify: `internal/snapshot/archive.go:18,44,156`
- Modify: `internal/snapshot/engine.go:69`
- Modify: `cmd/moat/cli/snapshot.go` (capture path branches on volume mode)
- Test: `internal/snapshot/archive_test.go`

- [ ] **Step 1: Write the failing test for `IncludeGit`**

Append to `internal/snapshot/archive_test.go`:

```go
func TestArchiveIncludeGit(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, ws, ".git/config", "[remote]\n  url = https://x@github.com/y")
	mustWrite(t, ws, "main.go", "package main")
	mustWrite(t, ws, "secret.env", "TOKEN=abc") // gitignored
	mustWrite(t, ws, ".gitignore", "secret.env")

	snapDir := t.TempDir()
	b := NewArchiveBackend(snapDir, ArchiveOptions{UseGitignore: true, IncludeGit: true})
	ref, err := b.Create(ws, "snap_1")
	if err != nil { t.Fatal(err) }

	out := t.TempDir()
	if err := b.RestoreTo(ref, out); err != nil { t.Fatal(err) }

	// .git is INCLUDED when IncludeGit is set.
	if _, err := os.Stat(filepath.Join(out, ".git", "config")); err != nil {
		t.Errorf(".git/config should be in the archive: %v", err)
	}
	// Companion: gitignored file still excluded (only the .git rule is lifted).
	if _, err := os.Stat(filepath.Join(out, "secret.env")); !os.IsNotExist(err) {
		t.Errorf("gitignored secret.env should still be excluded")
	}
}

func TestArchiveExcludesGitByDefault(t *testing.T) {
	ws := t.TempDir()
	mustWrite(t, ws, ".git/config", "x")
	mustWrite(t, ws, "main.go", "package main")
	snapDir := t.TempDir()
	b := NewArchiveBackend(snapDir, ArchiveOptions{}) // IncludeGit false
	ref, _ := b.Create(ws, "snap_2")
	out := t.TempDir()
	_ = b.RestoreTo(ref, out)
	if _, err := os.Stat(filepath.Join(out, ".git")); !os.IsNotExist(err) {
		t.Errorf("bind-mode default must still exclude .git")
	}
}
```

> `mustWrite` is a helper that `MkdirAll`s the parent and writes the file — add it if not present.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/snapshot/ -run 'TestArchiveIncludeGit|TestArchiveExcludesGitByDefault' -v`
Expected: FAIL — `unknown field IncludeGit`.

- [ ] **Step 3: Add `IncludeGit` and gate the `.git` skip**

In `internal/snapshot/archive.go`, add to `ArchiveOptions` (near line 18):

```go
	// IncludeGit, when true, archives the .git directory (volume mode). Default
	// false preserves bind-mode behavior (the host already holds the repo).
	IncludeGit bool
```

Store it on the backend struct and in `NewArchiveBackend`. Then gate the skip at line 88-89:

```go
		// Skip .git unless IncludeGit is set.
		if !b.includeGit && (relPath == ".git" || strings.HasPrefix(relPath, ".git/") || strings.HasPrefix(relPath, ".git"+string(filepath.Separator))) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
```

Gate the in-place `.git`-preserve dance (lines 158-208) on `!b.includeGit` — when `.git` is in the archive, do not back it up and rename it back (that would clobber the restored `.git`). Wrap the preserve/restore branches:

```go
	var gitBackup string
	if !b.includeGit {
		// ... existing backup-and-restore-.git logic ...
	}
```

Write archives with `0600` (line 53) since they may contain `.git` secrets:

```go
	file, err := os.OpenFile(archivePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
```

- [ ] **Step 4: Thread `IncludeGit` through the engine**

In `internal/snapshot/engine.go`, add `IncludeGit bool` to `EngineOptions`, and pass it in both `detectBackend` `NewArchiveBackend` calls (lines 76 and 92):

```go
		return NewArchiveBackend(snapshotDir, ArchiveOptions{
			UseGitignore: opts.UseGitignore,
			Additional:   opts.Additional,
			IncludeGit:   opts.IncludeGit,
		})
```

Also force the archive backend in volume mode (APFS CoW does not apply to a temp materialization dir — but it will be a normal dir, so detection is fine; setting `ForceBackend: "archive"` from the caller is the explicit, safe choice).

- [ ] **Step 5: Run snapshot tests**

Run: `go test ./internal/snapshot/ -run 'Archive' -v`
Expected: PASS (including existing `.git`-exclusion tests — default path unchanged).

- [ ] **Step 6: Branch the `moat snapshot` capture on volume mode**

In `cmd/moat/cli/snapshot.go`, where the capture command builds the engine from `meta.Workspace` (line ~147), detect volume mode from metadata and materialize the volume first:

```go
	workspaceForSnapshot := meta.Workspace
	if meta.WorkspaceMode == "volume" {
		tmp, err := os.MkdirTemp("", "moat-vol-snap-")
		if err != nil { return err }
		defer os.RemoveAll(tmp)
		rt, err := container.NewRuntime() // or the project's runtime accessor
		if err != nil { return err }
		if err := rt.VolumeExport(cmd.Context(), meta.WorkspaceVolume, tmp); err != nil {
			return fmt.Errorf("export workspace volume for snapshot: %w", err)
		}
		workspaceForSnapshot = tmp
	}
	engine, err := snapshot.NewEngine(workspaceForSnapshot, snapshotDir, snapshot.EngineOptions{
		ForceBackend: backendForMode(meta.WorkspaceMode), // "archive" for volume, "" otherwise
		IncludeGit:   meta.WorkspaceMode == "volume",
		// ...existing UseGitignore/Additional...
	})
```

Add a small `backendForMode(mode string) string` helper returning `"archive"` for `"volume"`, else `""`.

> Confirm the runtime accessor used elsewhere in `cmd/moat/cli` (`grep -rn "NewRuntime\|defaultRuntime" cmd/moat`). Reuse the same construction the rest of the CLI uses.

- [ ] **Step 7: Build and run**

Run: `go build ./... && go test ./internal/snapshot/ ./cmd/... -v`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/snapshot/archive.go internal/snapshot/engine.go cmd/moat/cli/snapshot.go
git commit -m "feat(snapshot): capture from volume — IncludeGit, 0600 archives, materialize-then-archive"
```

---

## Task 9: Block in-place restore in volume mode

**Files:**
- Modify: `cmd/moat/cli/snapshot.go:400-440` (restore command)
- Test: `cmd/moat/cli/snapshot_test.go` (create if absent) — or a unit test on the guard helper

- [ ] **Step 1: Write the failing test on a guard helper**

To keep the test off real containers, extract the decision into a pure helper. `cmd/moat/cli/snapshot_test.go`:

```go
package cli

import "testing"

func TestRestoreRequiresToInVolumeMode(t *testing.T) {
	// volume mode, no --to → error
	if err := checkRestoreAllowed("volume", ""); err == nil {
		t.Error("volume mode in-place restore should be blocked")
	}
	// volume mode with --to → ok
	if err := checkRestoreAllowed("volume", "/tmp/out"); err != nil {
		t.Errorf("volume mode restore --to should be allowed: %v", err)
	}
	// bind mode in-place → ok (unchanged)
	if err := checkRestoreAllowed("bind", ""); err != nil {
		t.Errorf("bind mode in-place restore should be allowed: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/moat/cli/ -run TestRestoreRequiresToInVolumeMode -v`
Expected: FAIL — `undefined: checkRestoreAllowed`.

- [ ] **Step 3: Implement and call the guard**

Add to `cmd/moat/cli/snapshot.go`:

```go
// checkRestoreAllowed blocks in-place restore for volume-mode runs. The host
// directory was never the live tree, so an in-place restore would write the
// agent's changes straight into the developer's source tree — the host
// write-back volume mode exists to prevent. Require --to.
func checkRestoreAllowed(workspaceMode, restoreTo string) error {
	if workspaceMode == "volume" && restoreTo == "" {
		return fmt.Errorf("in-place restore is not allowed for volume-mode runs; use --to <dir> to extract (e.g. moat snapshot restore %s --to ~/out)", "<run-id>")
	}
	return nil
}
```

In the restore command's `RunE`, after loading metadata and before performing the restore (line ~404), call it:

```go
	if err := checkRestoreAllowed(meta.WorkspaceMode, snapshotRestoreTo); err != nil {
		return err
	}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/moat/cli/ -run TestRestoreRequiresToInVolumeMode -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/moat/cli/snapshot.go cmd/moat/cli/snapshot_test.go
git commit -m "feat(snapshot): block in-place restore for volume-mode runs (require --to)"
```

---

## Task 10: Destroy guard against silent data loss

**Files:**
- Modify: the destroy command (`grep -rn "destroy" cmd/moat | grep -i cmd`) and/or `internal/run` destroy path
- Test: a unit test on a pure guard helper

- [ ] **Step 1: Write the failing test**

In `internal/run/volume_test.go`:

```go
func TestDestroyNeedsForceWhenNoExtractionSnapshot(t *testing.T) {
	// volume mode, no non-pre-run snapshot, not forced → blocked
	if err := CheckDestroyAllowed("volume", false /*hasExtractSnap*/, false /*force*/); err == nil {
		t.Error("destroy should be blocked to prevent data loss")
	}
	// forced → allowed
	if err := CheckDestroyAllowed("volume", false, true); err != nil {
		t.Errorf("forced destroy should be allowed: %v", err)
	}
	// has an extraction snapshot → allowed
	if err := CheckDestroyAllowed("volume", true, false); err != nil {
		t.Errorf("destroy with snapshot should be allowed: %v", err)
	}
	// bind mode → always allowed
	if err := CheckDestroyAllowed("bind", false, false); err != nil {
		t.Errorf("bind destroy unaffected: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/run/ -run TestDestroyNeedsForce -v`
Expected: FAIL — `undefined: CheckDestroyAllowed`.

- [ ] **Step 3: Implement the guard**

In `internal/run/volume.go`:

```go
// CheckDestroyAllowed blocks destroying a volume-mode run that has no extraction
// snapshot, unless --force is passed. The volume is the only copy of the agent's
// work; removing it without an extraction snapshot loses everything.
func CheckDestroyAllowed(workspaceMode string, hasExtractionSnapshot, force bool) error {
	if workspaceMode == "volume" && !hasExtractionSnapshot && !force {
		return fmt.Errorf("this volume-mode run has no extraction snapshot; its workspace volume will be deleted and agent changes lost.\n" +
			"Run `moat snapshot <run>` then `moat snapshot restore <run> --to <dir>` first, or pass --force to destroy anyway")
	}
	return nil
}
```

- [ ] **Step 4: Wire it into the destroy command**

In the destroy command: load metadata, count non-pre-run snapshots for the run (reuse the snapshot engine's `List`, filtering out `TypePreRun`/`TypeSafety`), add a `--force` flag if absent, and call `CheckDestroyAllowed(meta.WorkspaceMode, hasExtract, force)` before tearing down. Pseudocode:

```go
	hasExtract := false
	for _, s := range snaps {
		if s.Type != snapshot.TypePreRun && s.Type != snapshot.TypeSafety {
			hasExtract = true
			break
		}
	}
	if err := run.CheckDestroyAllowed(meta.WorkspaceMode, hasExtract, forceFlag); err != nil {
		return err
	}
```

- [ ] **Step 5: Run test + build**

Run: `go test ./internal/run/ -run TestDestroyNeedsForce -v && go build ./...`
Expected: PASS / success.

- [ ] **Step 6: Commit**

```bash
git add internal/run/volume.go internal/run/volume_test.go cmd/moat/
git commit -m "feat(run): warn/require --force destroying a volume run with no extraction snapshot"
```

---

## Task 11: Orphan volume GC in daemon idle cleanup

**Files:**
- Modify: `internal/daemon/` idle-cleanup path (`grep -rn "idle\|cleanup\|shutdown" internal/daemon/*.go`)
- Test: unit test on a pure helper that computes orphans

- [ ] **Step 1: Write the failing test**

`internal/daemon/volume_gc_test.go`:

```go
package daemon

import (
	"reflect"
	"sort"
	"testing"
)

func TestOrphanVolumes(t *testing.T) {
	all := []string{"moat-ws-run_a", "moat-ws-run_b", "moat-ws-run_c"}
	live := map[string]bool{"run_b": true} // only run_b is still registered
	got := orphanVolumes(all, live)
	sort.Strings(got)
	want := []string{"moat-ws-run_a", "moat-ws-run_c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/daemon/ -run TestOrphanVolumes -v`
Expected: FAIL — `undefined: orphanVolumes`.

- [ ] **Step 3: Implement the helper + wire into idle cleanup**

`internal/daemon/volume_gc.go`:

```go
package daemon

import "strings"

// orphanVolumes returns moat-ws-* volume names whose run id is not in liveRunIDs.
func orphanVolumes(all []string, liveRunIDs map[string]bool) []string {
	var orphans []string
	for _, name := range all {
		runID := strings.TrimPrefix(name, "moat-ws-")
		if runID == name { // not a workspace volume
			continue
		}
		if !liveRunIDs[runID] {
			orphans = append(orphans, name)
		}
	}
	return orphans
}
```

In the daemon's idle-cleanup routine, after determining there are no active runs (or on each idle tick), enumerate Docker volumes and remove orphans:

```go
	all, err := rt.VolumeList(ctx, "moat-ws-")
	if err == nil {
		for _, name := range orphanVolumes(all, d.liveRunIDs()) {
			if err := rt.VolumeRemove(ctx, name, true); err != nil {
				log.Warn("orphan volume cleanup failed", "name", name, "error", err)
			} else {
				log.Info("removed orphaned workspace volume", "name", name)
			}
		}
	}
```

> `d.liveRunIDs()` — derive from the daemon's registered-runs map (`grep -n "runs\b" internal/daemon/*.go`). The daemon only has a Docker runtime handle when Docker is in use; guard the call so non-Docker daemons skip it.

- [ ] **Step 4: Run test + build**

Run: `go test ./internal/daemon/ -run TestOrphanVolumes -v && go build ./...`
Expected: PASS / success.

- [ ] **Step 5: Commit**

```bash
git add internal/daemon/volume_gc.go internal/daemon/volume_gc_test.go internal/daemon/
git commit -m "feat(daemon): GC orphaned moat-ws-* volumes during idle cleanup"
```

---

## Task 12: Documentation and changelog

**Files:**
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/reference/01-cli.md`
- Create: `docs/content/guides/NN-volume-workspaces.md` (use the next available number)
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Document `workspace.mode` in the moat.yaml reference**

Add a `workspace:` section to `docs/content/reference/02-moat-yaml.md` documenting `mode: bind|volume` (default bind), what gets copied, the `exclude:` "do not copy in" meaning, the `.git`-can't-be-excluded rule, and Docker-only/worktree-rejected constraints.

- [ ] **Step 2: Document the CLI flag and snapshot behavior**

In `docs/content/reference/01-cli.md`: add `--workspace-mode <bind|volume>` to `moat run`. Under `moat snapshot`, document that volume-mode snapshots capture from the volume, **include `.git`**, and that `restore` requires `--to` in volume mode (in-place blocked); bind-mode snapshots are unchanged (exclude `.git`, in-place allowed).

- [ ] **Step 3: Write the guide**

Create `docs/content/guides/NN-volume-workspaces.md` covering: when to use volume mode (host protection, macOS I/O), how to enable it, the copy-in semantics, the **safe extraction flow** (`snapshot → restore --to → git fetch`), the destroy-needs-snapshot warning, the `.git`-includes-secrets caution, and the worktree/Apple limitations with bind-mode fallback.

- [ ] **Step 4: Add the changelog entry**

In `CHANGELOG.md` under the Unreleased `### Added` section:

```markdown
- **Volume-mode workspaces** — opt into an isolated copy of the workspace in an
  ephemeral Docker named volume (`workspace.mode: volume` or `--workspace-mode
  volume`) for host protection and faster macOS I/O. Extract changes with
  `moat snapshot` / `moat snapshot restore --to`. Docker-only; git worktrees use
  bind mode. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

- [ ] **Step 5: Verify links/build**

Run: `make lint` (and the docs build if one exists). Replace the `#NNN` placeholder with the real PR number before pushing (CI fails on an unfilled placeholder).

- [ ] **Step 6: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs: document volume-mode workspaces"
```

---

## Task 13: Full verification pass

- [ ] **Step 1: Unit tests with race detector**

Run: `make test-unit`
Expected: all packages PASS.

- [ ] **Step 2: Lint**

Run: `make lint` (falls back to `go vet ./...` if golangci-lint is absent)
Expected: clean. Fix misspellings ("canceled" not "cancelled").

- [ ] **Step 3: Manual smoke test (requires Docker)**

```bash
go build -o moat ./cmd/moat
./moat run --workspace-mode volume -- sh -c 'echo hi > newfile.txt && git status'
# In another shell: confirm the volume exists and the host is untouched
docker volume ls --filter name=moat-ws-
ls newfile.txt 2>&1   # expected: not found on host (host protected)
./moat snapshot <run-id>
./moat snapshot restore <run-id> --to /tmp/vol-out
ls /tmp/vol-out/newfile.txt   # expected: present (extracted)
./moat snapshot restore <run-id>   # expected: error — in-place blocked in volume mode
./moat destroy <run-id>            # expected: proceeds (snapshot exists); volume removed
docker volume ls --filter name=moat-ws-  # expected: gone
```

- [ ] **Step 4: Negative smoke tests**

```bash
# Worktree rejected:
git worktree add /tmp/wt && cd /tmp/wt
moat run --workspace-mode volume -- true   # expected: clear worktree-rejection error
# Apple runtime rejected (on macOS): force apple runtime
moat run --runtime apple --workspace-mode volume -- true  # expected: clear Docker-required error
```

- [ ] **Step 5: Final commit if any fixes were needed**

```bash
git add -A && git commit -m "test: volume-mode verification fixes"
```

---

## Self-Review Notes (for the implementer)

- **Spec coverage:** Task 1 (config/precedence) · Task 2 (excludes hardening + .git) · Tasks 3-4 (runtime volume primitive + interface methods, the P2 review finding) · Task 5 (CLI flag, alias dropped) · Task 6 (manager: guards, staging+volume mounts, root user on Linux, auto-snapshot suppression, metadata, cleanup) · Task 7 (init copy-in: excludes, `--no-dereference`, chown) · Task 8 (volume capture, `.git` included, 0600) · Task 9 (in-place restore blocked) · Task 10 (destroy guard) · Task 11 (orphan GC) · Task 12 (docs/changelog). Every "Applied" review finding maps to a task.
- **Type consistency:** `WorkspaceMode`/`WorkspaceModeVolume` (config), `MountKindVolume`/`MountConfig.Kind`/`.Name` (container), `WorkspaceVolumeName`/`stagingPath`/`GuardVolumeWorkspace`/`CheckDestroyAllowed` (run), `IncludeGit` (snapshot), `WorkspaceMode`/`WorkspaceVolume` (storage metadata) are used identically across tasks.
- **Grep-confirm before editing:** several steps depend on exact identifiers in `manager.go` (the env accumulator, `opts.RunID`, `m.defaultRuntime()`, the cleanup function) and in the runtime (`RemoveContainer`/`WaitContainer`, runtime-type constants, the slim base image const). Each such step calls out the grep to run first — do not assume.
- **Open follow-ons (not in this plan, by design):** git-worktree reconstruction, Apple named-volume support, the IDE-attach helper, shared cache volumes.
