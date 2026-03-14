# Mount Excludes Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Allow users to exclude directories from shared mounts via tmpfs overlays, solving VirtioFS FD exhaustion on Apple Containers.

**Architecture:** Extend `moat.yaml` mounts to accept object-form entries with an `exclude` field. Excluded paths become tmpfs mounts in the container. Both Docker (`mount.TypeTmpfs`) and Apple (`--tmpfs`) runtimes handle tmpfs natively. The manager detects when an explicit mount targets `/workspace` and skips the implicit workspace mount.

**Tech Stack:** Go, gopkg.in/yaml.v3, Docker SDK (`mount` package), Apple container CLI

**Spec:** `docs/superpowers/specs/2026-03-14-mount-excludes-design.md`

---

## File Structure

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/config/mount.go` | Modify | `MountEntry` type, `UnmarshalYAML`, `ParseMount`, validation |
| `internal/config/mount_test.go` | Modify | Tests for YAML unmarshaling, parsing, validation |
| `internal/config/config.go` | Modify | Change `Mounts []string` → `Mounts []MountEntry`, add mount validation in `Load()` |
| `internal/config/config_test.go` | Modify | Fix existing tests for `MountEntry` type, add tests for object-form mounts |
| `internal/config/integration_test.go` | Modify | Fix `ParseMount(cfg.Mounts[0])` → direct field access on `MountEntry` |
| `internal/container/runtime.go` | Modify | Add `TmpfsMount` type, add `TmpfsMounts` to `Config` |
| `internal/container/apple.go` | Modify | Add `--tmpfs` flags in `buildCreateArgs` |
| `internal/container/apple_test.go` | Modify | Test `--tmpfs` flag generation |
| `internal/container/docker.go` | Modify | Add `mount.TypeTmpfs` entries in `CreateContainer` |
| `internal/run/manager.go` | Modify | Workspace replacement detection, exclude→tmpfs resolution |
| `docs/content/reference/05-mounts.md` | Modify | Document object form and exclude behavior |
| `docs/content/reference/02-moat-yaml.md` | Modify | Document `exclude` field on mounts |

---

## Chunk 1: Config Layer

### Task 1: MountEntry type and YAML unmarshaling

**Files:**
- Modify: `internal/config/mount.go`
- Modify: `internal/config/mount_test.go`

- [ ] **Step 1: Write failing tests for MountEntry YAML unmarshaling**

Add tests to `internal/config/mount_test.go`:

```go
func TestMountEntryUnmarshalYAML(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    []MountEntry
		wantErr bool
	}{
		{
			name: "string form",
			yaml: `
- ./data:/data:ro
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", ReadOnly: true},
			},
		},
		{
			name: "object form with exclude",
			yaml: `
- source: .
  target: /workspace
  exclude:
    - node_modules
    - .venv
`,
			want: []MountEntry{
				{Source: ".", Target: "/workspace", Exclude: []string{"node_modules", ".venv"}},
			},
		},
		{
			name: "object form read-only",
			yaml: `
- source: ./data
  target: /data
  mode: ro
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", ReadOnly: true},
			},
		},
		{
			name: "mixed array",
			yaml: `
- ./data:/data:ro
- source: .
  target: /workspace
  exclude:
    - node_modules
`,
			want: []MountEntry{
				{Source: "./data", Target: "/data", ReadOnly: true},
				{Source: ".", Target: "/workspace", Exclude: []string{"node_modules"}},
			},
		},
		{
			name: "object form no exclude",
			yaml: `
- source: ./cache
  target: /cache
  mode: rw
`,
			want: []MountEntry{
				{Source: "./cache", Target: "/cache"},
			},
		},
		{
			name: "invalid mode",
			yaml: `
- source: .
  target: /workspace
  mode: readonly
`,
			wantErr: true,
		},
		{
			name: "invalid yaml node type",
			yaml: `
- 42
`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []MountEntry
			err := yaml.Unmarshal([]byte(tt.yaml), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /workspace && go test ./internal/config/ -run TestMountEntryUnmarshalYAML -v`
Expected: Compilation errors — `MountEntry` type doesn't exist yet.

- [ ] **Step 3: Implement MountEntry type with UnmarshalYAML**

Replace the contents of `internal/config/mount.go` with:

```go
package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MountEntry represents a mount configuration. It supports two YAML forms:
// - String: "source:target[:mode]" (existing format)
// - Object: {source, target, mode, exclude} (new format with exclude support)
type MountEntry struct {
	Source   string   `yaml:"source"`
	Target   string   `yaml:"target"`
	ReadOnly bool     `yaml:"-"`
	Exclude  []string `yaml:"exclude,omitempty"`
}

// UnmarshalYAML handles both string and object forms in a mixed-type YAML array.
func (m *MountEntry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// String form: "source:target[:mode]"
		parsed, err := parseMount(value.Value)
		if err != nil {
			return err
		}
		*m = *parsed
		return nil

	case yaml.MappingNode:
		// Object form: {source, target, mode, exclude}
		// Use an alias type to avoid infinite recursion.
		type mountAlias struct {
			Source  string   `yaml:"source"`
			Target  string   `yaml:"target"`
			Mode    string   `yaml:"mode"`
			Exclude []string `yaml:"exclude"`
		}
		var raw mountAlias
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("parsing mount object: %w", err)
		}
		if raw.Source == "" {
			return fmt.Errorf("mount object: 'source' is required")
		}
		if raw.Target == "" {
			return fmt.Errorf("mount object: 'target' is required")
		}
		switch raw.Mode {
		case "", "rw":
			// default read-write
		case "ro":
			m.ReadOnly = true
		default:
			return fmt.Errorf("mount object: invalid mode %q (must be 'ro' or 'rw')", raw.Mode)
		}
		m.Source = raw.Source
		m.Target = raw.Target
		m.Exclude = raw.Exclude
		return nil

	default:
		return fmt.Errorf("mount entry must be a string or object, got %v", value.Kind)
	}
}

// parseMount parses a mount string like "./data:/data:ro".
func parseMount(s string) (*MountEntry, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 {
		return nil, fmt.Errorf("invalid mount: %s (expected source:target[:ro])", s)
	}

	m := &MountEntry{
		Source: parts[0],
		Target: parts[1],
	}

	if len(parts) >= 3 && parts[2] == "ro" {
		m.ReadOnly = true
	}

	return m, nil
}

// ParseMount parses a mount string like "./data:/data:ro".
// This is the public API used by the run manager for --mount CLI flags.
func ParseMount(s string) (*MountEntry, error) {
	return parseMount(s)
}

// ValidateExcludes validates exclude paths on a MountEntry.
// Paths are normalized with filepath.Clean before validation.
// Returns the cleaned exclude list or an error.
func ValidateExcludes(excludes []string, target string) ([]string, error) {
	if len(excludes) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool, len(excludes))
	cleaned := make([]string, 0, len(excludes))

	for _, exc := range excludes {
		if exc == "" {
			return nil, fmt.Errorf("mount %s: exclude path must not be empty", target)
		}

		c := filepath.Clean(exc)

		// After cleaning, reject "." (e.g., from "./")
		if c == "." {
			return nil, fmt.Errorf("mount %s: exclude path %q resolves to current directory", target, exc)
		}

		// Must be relative
		if filepath.IsAbs(c) {
			return nil, fmt.Errorf("mount %s: exclude path %q must be relative", target, exc)
		}

		// Must not contain ".."
		for _, part := range strings.Split(c, string(filepath.Separator)) {
			if part == ".." {
				return nil, fmt.Errorf("mount %s: exclude path %q must not contain '..'", target, exc)
			}
		}

		// No duplicates
		if seen[c] {
			return nil, fmt.Errorf("mount %s: duplicate exclude path %q", target, c)
		}
		seen[c] = true
		cleaned = append(cleaned, c)
	}

	return cleaned, nil
}
```

- [ ] **Step 4: Add yaml.v3 import to test file**

Update `internal/config/mount_test.go` imports to include `"gopkg.in/yaml.v3"` and `"reflect"`.

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /workspace && go test ./internal/config/ -run TestMountEntryUnmarshalYAML -v`
Expected: All tests PASS.

- [ ] **Step 6: Write failing tests for ValidateExcludes**

Add to `internal/config/mount_test.go`:

```go
func TestValidateExcludes(t *testing.T) {
	tests := []struct {
		name     string
		excludes []string
		target   string
		want     []string
		wantErr  bool
	}{
		{
			name:     "valid single exclude",
			excludes: []string{"node_modules"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "valid nested exclude",
			excludes: []string{"foo/bar/baz"},
			target:   "/workspace",
			want:     []string{"foo/bar/baz"},
		},
		{
			name:     "normalizes leading dot-slash",
			excludes: []string{"./node_modules"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "normalizes trailing slash",
			excludes: []string{"node_modules/"},
			target:   "/workspace",
			want:     []string{"node_modules"},
		},
		{
			name:     "normalizes redundant separators",
			excludes: []string{"foo//bar"},
			target:   "/workspace",
			want:     []string{"foo/bar"},
		},
		{
			name:     "nil excludes",
			excludes: nil,
			target:   "/workspace",
			want:     nil,
		},
		{
			name:     "empty excludes",
			excludes: []string{},
			target:   "/workspace",
			want:     nil,
		},
		{
			name:     "rejects empty string",
			excludes: []string{""},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dot-only",
			excludes: []string{"./"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects absolute path",
			excludes: []string{"/tmp/foo"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dotdot",
			excludes: []string{"../foo"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects dotdot in middle",
			excludes: []string{"foo/../bar"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects duplicates",
			excludes: []string{"node_modules", "node_modules"},
			target:   "/workspace",
			wantErr:  true,
		},
		{
			name:     "rejects normalized duplicates",
			excludes: []string{"node_modules", "./node_modules"},
			target:   "/workspace",
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ValidateExcludes(tt.excludes, tt.target)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 7: Run ValidateExcludes tests**

Run: `cd /workspace && go test ./internal/config/ -run TestValidateExcludes -v`
Expected: All tests PASS (implementation was written in Step 3).

- [ ] **Step 8: Update ParseMount tests for new return type**

The existing `TestParseMount` in `mount_test.go` references the old `*Mount` type. Update it to use `*MountEntry`:

```go
func TestParseMount(t *testing.T) {
	tests := []struct {
		input    string
		source   string
		target   string
		readOnly bool
		wantErr  bool
	}{
		{"./data:/data", "./data", "/data", false, false},
		{"./data:/data:ro", "./data", "/data", true, false},
		{"/abs/path:/container", "/abs/path", "/container", false, false},
		{"./cache:/cache:rw", "./cache", "/cache", false, false},
		{"invalid", "", "", false, true},
	}

	for _, tt := range tests {
		m, err := ParseMount(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("ParseMount(%q) expected error", tt.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseMount(%q): %v", tt.input, err)
			continue
		}
		if m.Source != tt.source {
			t.Errorf("ParseMount(%q) Source = %q, want %q", tt.input, m.Source, tt.source)
		}
		if m.Target != tt.target {
			t.Errorf("ParseMount(%q) Target = %q, want %q", tt.input, m.Target, tt.target)
		}
		if m.ReadOnly != tt.readOnly {
			t.Errorf("ParseMount(%q) ReadOnly = %v, want %v", tt.input, m.ReadOnly, tt.readOnly)
		}
	}
}
```

The test bodies are identical — only the type changed from `*Mount` to `*MountEntry`. The old `Mount` type no longer exists.

- [ ] **Step 9: Run all config tests**

Run: `cd /workspace && go test ./internal/config/ -v`
Expected: All tests PASS. Compilation may fail if `config.go` still references old `Mounts []string` — that's fixed in Task 2.

- [ ] **Step 10: Commit**

```bash
git add internal/config/mount.go internal/config/mount_test.go
git commit -m "feat(config): add MountEntry type with YAML unmarshaling and exclude validation"
```

### Task 2: Update Config struct and Load validation

**Files:**
- Modify: `internal/config/config.go:30` (change `Mounts` field type)
- Modify: `internal/config/config_test.go:93-115` (fix `TestLoadConfigWithMounts` for `MountEntry` type)
- Modify: `internal/config/integration_test.go:69-74` (fix `ParseMount(cfg.Mounts[0])` → field access)

- [ ] **Step 1: Write failing test for Load with object-form mounts**

Add to `internal/config/config_test.go`:

```go
func TestLoadConfigWithMountExcludes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "moat.yaml")

	content := `
agent: claude-code

mounts:
  - ./data:/data:ro
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
`
	os.WriteFile(configPath, []byte(content), 0644)

	cfg, err := Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}

	// First mount: string form
	if cfg.Mounts[0].Source != "./data" {
		t.Errorf("Mounts[0].Source = %q, want %q", cfg.Mounts[0].Source, "./data")
	}
	if !cfg.Mounts[0].ReadOnly {
		t.Error("Mounts[0].ReadOnly = false, want true")
	}

	// Second mount: object form with excludes
	if cfg.Mounts[1].Source != "." {
		t.Errorf("Mounts[1].Source = %q, want %q", cfg.Mounts[1].Source, ".")
	}
	if cfg.Mounts[1].Target != "/workspace" {
		t.Errorf("Mounts[1].Target = %q, want %q", cfg.Mounts[1].Target, "/workspace")
	}
	if len(cfg.Mounts[1].Exclude) != 2 {
		t.Fatalf("Mounts[1].Exclude = %d, want 2", len(cfg.Mounts[1].Exclude))
	}
	if cfg.Mounts[1].Exclude[0] != "node_modules" {
		t.Errorf("Mounts[1].Exclude[0] = %q, want %q", cfg.Mounts[1].Exclude[0], "node_modules")
	}
}

func TestLoadConfigMountExcludeValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name: "rejects absolute exclude path",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - /tmp/foo
`,
			wantErr: "must be relative",
		},
		{
			name: "rejects dotdot exclude path",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - ../foo
`,
			wantErr: "must not contain '..'",
		},
		{
			name: "rejects duplicate exclude paths",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - node_modules
`,
			wantErr: "duplicate exclude",
		},
		{
			name: "rejects duplicate mount targets",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
  - source: ./other
    target: /workspace
`,
			wantErr: "duplicate mount target",
		},
		{
			name: "rejects invalid mode",
			yaml: `
agent: test
mounts:
  - source: .
    target: /workspace
    mode: readonly
`,
			wantErr: "invalid mode",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(tt.yaml), 0644)

			_, err := Load(dir)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /workspace && go test ./internal/config/ -run TestLoadConfigWithMountExcludes -v`
Expected: Compilation error — `Mounts` is still `[]string`.

- [ ] **Step 3: Update Config struct and add mount validation in Load**

In `internal/config/config.go`, change line 30:

```go
// Before:
Mounts       []string          `yaml:"mounts,omitempty"`

// After:
Mounts       []MountEntry      `yaml:"mounts,omitempty"`
```

Add mount validation in the `Load()` function, after the volumes validation block (after line 587) and before the snapshot defaults (line 589):

```go
	// Validate mounts
	if len(cfg.Mounts) > 0 {
		seenMountTargets := make(map[string]bool)
		for i, m := range cfg.Mounts {
			prefix := fmt.Sprintf("mounts[%d]", i)
			if m.Target != "" {
				if seenMountTargets[m.Target] {
					return nil, fmt.Errorf("%s: duplicate mount target %q", prefix, m.Target)
				}
				seenMountTargets[m.Target] = true
			}
			// Validate and normalize exclude paths
			cleaned, err := ValidateExcludes(m.Exclude, m.Target)
			if err != nil {
				return nil, err
			}
			cfg.Mounts[i].Exclude = cleaned

			// Check for volume/exclude conflicts
			for _, exc := range cleaned {
				excAbs := filepath.Join(m.Target, exc)
				for _, vol := range cfg.Volumes {
					if vol.Target == excAbs {
						return nil, fmt.Errorf("%s: exclude path %q conflicts with volume target %q", prefix, exc, vol.Target)
					}
				}
			}
		}
	}
```

- [ ] **Step 4: Fix existing TestLoadConfigWithMounts test**

In `internal/config/config_test.go`, replace lines 109-114 (`TestLoadConfigWithMounts`):

```go
	// Before:
	if cfg.Mounts[0] != "./data:/data:ro" {
		t.Errorf("Mounts[0] = %q, want %q", cfg.Mounts[0], "./data:/data:ro")
	}

	// After:
	if cfg.Mounts[0].Source != "./data" || cfg.Mounts[0].Target != "/data" || !cfg.Mounts[0].ReadOnly {
		t.Errorf("Mounts[0] = %+v, want source=./data target=/data ro=true", cfg.Mounts[0])
	}
```

- [ ] **Step 5: Fix integration_test.go ParseMount call**

In `internal/config/integration_test.go`, replace lines 69-74:

```go
	// Before:
	m, err := ParseMount(cfg.Mounts[0])
	if err != nil {
		t.Fatalf("ParseMount: %v", err)
	}
	if m.Source != "./data" || m.Target != "/data" || !m.ReadOnly {
		t.Errorf("Mount = %+v", m)
	}

	// After:
	m := cfg.Mounts[0]
	if m.Source != "./data" || m.Target != "/data" || !m.ReadOnly {
		t.Errorf("Mount = %+v", m)
	}
```

- [ ] **Step 6: Update manager to use MountEntry instead of old Mount type**

In `internal/run/manager.go`, update the mount parsing loop (lines 449-466). The old code calls `config.ParseMount(mountStr)` on `[]string`. The new `Mounts` is `[]MountEntry`, so iteration changes:

```go
	// Before (lines 449-466):
	for _, mountStr := range opts.Config.Mounts {
		mount, err := config.ParseMount(mountStr)
		if err != nil {
			return nil, fmt.Errorf("parsing mount %q: %w", mountStr, err)
		}
		source := mount.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(opts.Workspace, source)
		}
		mounts = append(mounts, container.MountConfig{
			Source:   source,
			Target:   mount.Target,
			ReadOnly: mount.ReadOnly,
		})
	}

	// After:
	for _, me := range opts.Config.Mounts {
		source := me.Source
		if !filepath.IsAbs(source) {
			source = filepath.Join(opts.Workspace, source)
		}
		mounts = append(mounts, container.MountConfig{
			Source:   source,
			Target:   me.Target,
			ReadOnly: me.ReadOnly,
		})
	}
```

(Tmpfs handling is added in Task 6.)

- [ ] **Step 7: Run all config tests**

Run: `cd /workspace && go test ./internal/config/ -v`
Expected: All tests PASS.

- [ ] **Step 8: Run full build to check compilation**

Run: `cd /workspace && go build ./...`
Expected: Compiles successfully. If any other files reference the old `Mount` type or `Mounts []string`, fix those references.

- [ ] **Step 9: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go internal/config/integration_test.go internal/run/manager.go
git commit -m "feat(config): change Mounts to []MountEntry with exclude validation"
```

---

## Chunk 2: Container Runtime Layer

### Task 3: Add TmpfsMount to container Config

**Files:**
- Modify: `internal/container/runtime.go:314-319`

- [ ] **Step 1: Add TmpfsMount type and field to Config**

In `internal/container/runtime.go`, add the `TmpfsMount` type after `MountConfig` (after line 319):

```go
// TmpfsMount describes a tmpfs mount inside the container.
// Used to overlay excluded directories with in-memory filesystems.
type TmpfsMount struct {
	Target string // absolute container path
}
```

Add the `TmpfsMounts` field to the `Config` struct (after `Mounts` on line 265):

```go
	Mounts       []MountConfig
	TmpfsMounts  []TmpfsMount   // tmpfs overlays (e.g., for mount excludes)
```

- [ ] **Step 2: Run build to verify**

Run: `cd /workspace && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/container/runtime.go
git commit -m "feat(container): add TmpfsMount type to container Config"
```

### Task 4: Apple container --tmpfs support

**Files:**
- Modify: `internal/container/apple.go:292-299`
- Modify: `internal/container/apple_test.go`

- [ ] **Step 1: Write failing test for tmpfs flags**

Add test cases to the `tests` slice in `TestBuildCreateArgs` in `internal/container/apple_test.go`:

```go
		{
			name: "with tmpfs mount",
			cfg: Config{
				Image: "ubuntu:22.04",
				TmpfsMounts: []TmpfsMount{
					{Target: "/workspace/node_modules"},
				},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--tmpfs", "/workspace/node_modules", "ubuntu:22.04"},
		},
		{
			name: "with volume and tmpfs mounts",
			cfg: Config{
				Image: "ubuntu:22.04",
				Mounts: []MountConfig{
					{Source: "/home/user/project", Target: "/workspace"},
				},
				TmpfsMounts: []TmpfsMount{
					{Target: "/workspace/node_modules"},
					{Target: "/workspace/.venv"},
				},
			},
			want: []string{"create", "--memory", "4096MB", "--dns", "8.8.8.8", "--dns", "8.8.4.4", "--volume", "/home/user/project:/workspace", "--tmpfs", "/workspace/node_modules", "--tmpfs", "/workspace/.venv", "ubuntu:22.04"},
		},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /workspace && go test ./internal/container/ -run TestBuildCreateArgs -v`
Expected: FAIL — tmpfs flags not generated yet.

- [ ] **Step 3: Add tmpfs flag generation in buildCreateArgs**

In `internal/container/apple.go`, after the volume mounts loop (after line 299), add:

```go
	// Tmpfs mounts (overlays for excluded directories)
	for _, tm := range cfg.TmpfsMounts {
		args = append(args, "--tmpfs", tm.Target)
	}
```

This must come after the `--volume` loop and before the image argument (line 302).

- [ ] **Step 4: Run test to verify it passes**

Run: `cd /workspace && go test ./internal/container/ -run TestBuildCreateArgs -v`
Expected: All tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/container/apple.go internal/container/apple_test.go
git commit -m "feat(container): add --tmpfs flag support for Apple containers"
```

### Task 5: Docker tmpfs support

**Files:**
- Modify: `internal/container/docker.go:176-185`

- [ ] **Step 1: Add tmpfs mount conversion in CreateContainer**

In `internal/container/docker.go` (lines 176-185), replace the mount conversion block. Change the pre-allocated slice to append-based and add tmpfs mounts after bind mounts (close with `})` instead of `}`):

```go
// Before:
mounts := make([]mount.Mount, len(cfg.Mounts))
for i, m := range cfg.Mounts {
	mounts[i] = mount.Mount{
		Type:     mount.TypeBind,
		Source:   m.Source,
		Target:   m.Target,
		ReadOnly: m.ReadOnly,
	}
}

// After:
mounts := make([]mount.Mount, 0, len(cfg.Mounts)+len(cfg.TmpfsMounts))
for _, m := range cfg.Mounts {
	mounts = append(mounts, mount.Mount{
		Type:     mount.TypeBind,
		Source:   m.Source,
		Target:   m.Target,
		ReadOnly: m.ReadOnly,
	})
}

// Tmpfs mounts — overlays for excluded directories.
// Appended after bind mounts so tmpfs overlays subdirectories of bind-mounted paths.
for _, tm := range cfg.TmpfsMounts {
	mounts = append(mounts, mount.Mount{
		Type:   mount.TypeTmpfs,
		Target: tm.Target,
	})
}
```

- [ ] **Step 2: Run build to verify compilation**

Run: `cd /workspace && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/container/docker.go
git commit -m "feat(container): add tmpfs mount support for Docker containers"
```

---

## Chunk 3: Manager Integration

### Task 6: Workspace replacement and tmpfs resolution in manager

**Files:**
- Modify: `internal/run/manager.go:424-466`

- [ ] **Step 1: Implement workspace replacement detection and tmpfs resolution**

Replace the mount assembly section in `internal/run/manager.go` (lines 424-466) with:

```go
	var mounts []container.MountConfig
	var tmpfsMounts []container.TmpfsMount

	// Check if any config mount explicitly targets /workspace.
	// If so, skip the implicit workspace mount (the explicit one replaces it).
	hasExplicitWorkspace := false
	if opts.Config != nil {
		for _, me := range opts.Config.Mounts {
			if me.Target == "/workspace" {
				hasExplicitWorkspace = true
				break
			}
		}
	}

	// Mount workspace (unless replaced by an explicit mount)
	if !hasExplicitWorkspace {
		mounts = append(mounts, container.MountConfig{
			Source:   opts.Workspace,
			Target:   "/workspace",
			ReadOnly: false,
		})
	}

	// If workspace is a git worktree, mount the main .git directory so git
	// operations work inside the container. The .git file in worktrees contains
	// an absolute host path; mounting the main .git at that same path makes
	// the reference resolve as-is.
	if info, err := worktree.ResolveGitDir(opts.Workspace); err != nil {
		log.Debug("failed to resolve worktree git dir", "error", err)
	} else if info != nil {
		mounts = append(mounts, container.MountConfig{
			Source:   info.MainGitDir,
			Target:   info.MainGitDir,
			ReadOnly: false,
		})
		log.Debug("mounted main git dir for worktree", "path", info.MainGitDir)
	}

	// Add mounts from config
	if opts.Config != nil {
		for _, me := range opts.Config.Mounts {
			source := me.Source
			if !filepath.IsAbs(source) {
				source = filepath.Join(opts.Workspace, source)
			}
			mounts = append(mounts, container.MountConfig{
				Source:   source,
				Target:   me.Target,
				ReadOnly: me.ReadOnly,
			})
			// Resolve excludes to tmpfs mounts
			for _, exc := range me.Exclude {
				tmpfsMounts = append(tmpfsMounts, container.TmpfsMount{
					Target: filepath.Join(me.Target, exc),
				})
			}
		}
	}
```

Then, where the container `Config` is assembled (search for where `Mounts: mounts` is set), add `TmpfsMounts: tmpfsMounts` alongside it.

- [ ] **Step 2: Add TmpfsMounts to the container Config assembly**

At `manager.go:2010`, where `Mounts: mounts,` is set in the `container.Config{}` literal, add:

```go
		Mounts:       mounts,
		TmpfsMounts:  tmpfsMounts,
```

Note: The Docker sidecar method (`StartSidecar` at docker.go:938) uses `SidecarConfig.Mounts`, not `Config.TmpfsMounts`. Sidecars don't use tmpfs mounts — no changes needed there.

- [ ] **Step 3: Run build**

Run: `cd /workspace && go build ./...`
Expected: Compiles successfully.

- [ ] **Step 4: Run unit tests**

Run: `cd /workspace && go test ./internal/run/ -v -count=1`
Expected: All tests PASS.

- [ ] **Step 5: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: All tests PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): add workspace replacement detection and tmpfs resolution for mount excludes"
```

---

## Chunk 4: Lint, Docs, and Final Verification

### Task 7: Lint check

- [ ] **Step 1: Run linter**

Run: `cd /workspace && make lint`
Expected: No lint errors. Fix any issues that arise.

- [ ] **Step 2: Commit lint fixes if any**

```bash
git add -u
git commit -m "style: fix lint issues from mount excludes implementation"
```

### Task 8: Update documentation

**Files:**
- Modify: `docs/content/reference/05-mounts.md`
- Modify: `docs/content/reference/02-moat-yaml.md`

- [ ] **Step 1: Update mount syntax reference**

In `docs/content/reference/05-mounts.md`, add an "Object form" section after the "Mount string format" section (after line 37), and update the "moat.yaml usage" section. Also add an "Excluding directories" section before "Runtime differences".

Add after the examples table (after line 37):

```markdown
## Object form

For advanced configuration like directory exclusion, use the object form:

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `source` | `string` | yes | | Host path. Absolute or relative to the workspace directory. |
| `target` | `string` | yes | | Container path. Must be absolute. |
| `mode` | `string` | no | `rw` | `ro` (read-only) or `rw` (read-write). |
| `exclude` | `[]string` | no | `[]` | Paths relative to `target` to overlay with tmpfs. |

String and object forms can be mixed in the same `mounts` array.

## Excluding directories

Excluded directories are overlaid with tmpfs (in-memory) mounts inside the container. The host files at those paths are hidden, and the container sees an empty directory. Files written to excluded paths live in memory and do not touch the host filesystem.

This is useful for large dependency trees (`node_modules`, `.venv`, `vendor/`) that cause performance problems with shared filesystem mounts — particularly VirtioFS on Apple Containers, where each file descriptor accumulates over time.

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
      - .venv
```

Since excluded directories start empty, install dependencies inside the container. Use a `pre_run` hook:

```yaml
hooks:
  pre_run: npm install

mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```

Tmpfs overlays are always writable, even when the parent mount is read-only. This allows installing dependencies on tmpfs while keeping source files read-only.

Excludes are only available in `moat.yaml` (object form). The `--mount` CLI flag uses the string format and does not support excludes.
```

Update the "Default workspace mount" section (around line 67) to add:

```markdown
To add excludes to the workspace mount, declare it explicitly with the object form. This replaces the automatic mount:

```yaml
mounts:
  - source: .
    target: /workspace
    exclude:
      - node_modules
```
```

- [ ] **Step 2: Update moat.yaml reference**

In `docs/content/reference/02-moat-yaml.md`, find the `mounts` field documentation and update it to include the object form and `exclude` field. The exact location depends on the file structure — search for the mounts section and add the object form documentation.

- [ ] **Step 3: Commit docs**

```bash
git add docs/content/reference/05-mounts.md docs/content/reference/02-moat-yaml.md
git commit -m "docs: add mount excludes and tmpfs overlay documentation"
```

### Task 9: Final verification

- [ ] **Step 1: Run full test suite**

Run: `cd /workspace && make test-unit`
Expected: All 38+ packages pass.

- [ ] **Step 2: Run lint**

Run: `cd /workspace && make lint`
Expected: Clean.

- [ ] **Step 3: Run build**

Run: `cd /workspace && go build ./...`
Expected: Clean build.
