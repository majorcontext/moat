# Global Mounts Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `mounts:` section to `~/.moat/config.yaml` that injects read-only mounts into every run, enabling users to stage personal files (like statusline scripts) into containers without a new primitive.

**Architecture:** Extend `GlobalConfig` with a `Mounts []MountEntry` field. `LoadGlobal` parses and validates these entries. The run manager loads global config (already done at startup) and appends global mounts to the mount list during `Create()`. Global mounts use the same `MountEntry` schema as `moat.yaml` mounts but enforce read-only mode and require absolute source paths (since there's no workspace to resolve relative paths against). Excludes are not supported on global mounts (no practical use case for personal file mounts).

**Tech Stack:** Go, `gopkg.in/yaml.v3`

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `internal/config/global.go` | Modify | Add `Mounts` field to `GlobalConfig`, validation |
| `internal/config/global_test.go` | Modify | Tests for global mount parsing and validation |
| `internal/run/manager.go` | Modify | Inject global mounts into `Create()` mount list |
| `docs/content/reference/05-mounts.md` | Modify | Document global mounts |
| `docs/content/guides/13-recipes.md` | Modify | Add statusline recipe |

---

### Task 1: Add Mounts field to GlobalConfig with validation

**Files:**
- Modify: `internal/config/global.go`
- Test: `internal/config/global_test.go`

- [ ] **Step 1: Write failing test for global mount parsing**

```go
func TestLoadGlobal_Mounts(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: /home/user/.moat/claude/statusline.js
    target: /home/user/.claude/moat/statusline.js
    mode: ro
  - /home/user/.moat/scripts/helper.sh:/home/user/.local/bin/helper.sh:ro
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if len(cfg.Mounts) != 2 {
		t.Fatalf("Mounts = %d, want 2", len(cfg.Mounts))
	}

	// Object form
	if cfg.Mounts[0].Source != "/home/user/.moat/claude/statusline.js" {
		t.Errorf("mount[0].Source = %q", cfg.Mounts[0].Source)
	}
	if cfg.Mounts[0].Target != "/home/user/.claude/moat/statusline.js" {
		t.Errorf("mount[0].Target = %q", cfg.Mounts[0].Target)
	}
	if !cfg.Mounts[0].ReadOnly {
		t.Error("mount[0] should be read-only")
	}

	// String form
	if cfg.Mounts[1].Source != "/home/user/.moat/scripts/helper.sh" {
		t.Errorf("mount[1].Source = %q", cfg.Mounts[1].Source)
	}
	if !cfg.Mounts[1].ReadOnly {
		t.Error("mount[1] should be read-only")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -run TestLoadGlobal_Mounts ./internal/config/ -v`
Expected: FAIL — `cfg.Mounts` field doesn't exist

- [ ] **Step 3: Add Mounts field to GlobalConfig**

In `internal/config/global.go`:

```go
// GlobalConfig holds global Moat settings from ~/.moat/config.yaml.
type GlobalConfig struct {
	Proxy  ProxyConfig  `yaml:"proxy"`
	Debug  DebugConfig  `yaml:"debug"`
	Mounts []MountEntry `yaml:"mounts,omitempty"`
}
```

- [ ] **Step 4: Add validation in LoadGlobal**

The validation must go **inside** the existing `if err == nil` block (line 45) where `homeDir` is in scope, after the YAML unmarshal (line 48). Add `"strings"` to the imports.

Note: `LoadGlobal` currently silently ignores unmarshal errors (`_ = yaml.Unmarshal(...)`). The unmarshal itself can stay lenient, but mount validation errors should be returned so the user knows their config is invalid. Existing callers that discard the error (`globalCfg, _ := config.LoadGlobal()` at `manager.go:125` and `:1099`) are fine — they use `LoadGlobal` for proxy port only and don't need mounts.

Here is the updated `LoadGlobal` function body (replace lines 40-60 of `global.go`):

```go
// LoadGlobal reads ~/.moat/config.yaml and applies environment overrides.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	// Try to load from file
	homeDir, err := os.UserHomeDir()
	if err == nil {
		configPath := filepath.Join(homeDir, ".moat", "config.yaml")
		if data, err := os.ReadFile(configPath); err == nil {
			_ = yaml.Unmarshal(data, cfg) // Ignore unmarshal errors, use defaults
		}

		// Validate global mounts: require absolute source paths and read-only mode.
		var validMounts []MountEntry
		for i, m := range cfg.Mounts {
			// Expand ~ in source path
			if strings.HasPrefix(m.Source, "~/") {
				m.Source = filepath.Join(homeDir, m.Source[2:])
			}

			if !filepath.IsAbs(m.Source) {
				return nil, fmt.Errorf("global mount %d: source %q must be an absolute path (no workspace to resolve relative paths against)", i+1, m.Source)
			}

			// Enforce read-only
			m.ReadOnly = true
			m.Mode = "ro"

			// Excludes not supported on global mounts
			if len(m.Exclude) > 0 {
				return nil, fmt.Errorf("global mount %d: excludes are not supported on global mounts", i+1)
			}

			validMounts = append(validMounts, m)
		}
		cfg.Mounts = validMounts
	}

	// Apply environment overrides
	if portStr := os.Getenv("MOAT_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test -run TestLoadGlobal_Mounts ./internal/config/ -v`
Expected: PASS

- [ ] **Step 6: Run all global config tests**

Run: `go test ./internal/config/ -v -run TestLoadGlobal`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/config/global.go internal/config/global_test.go
git commit -m "feat(config): add mounts field to GlobalConfig

Global mounts from ~/.moat/config.yaml are validated to require
absolute source paths, enforce read-only mode, and disallow excludes.
Tilde expansion is supported for source paths."
```

---

### Task 2: Add validation tests for global mount edge cases

**Files:**
- Test: `internal/config/global_test.go`

Note: These tests use `strings.Contains` — add `"strings"` to the test file imports if not already present.

- [ ] **Step 1: Write validation tests**

```go
func TestLoadGlobal_MountsRelativeSourceRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: ./relative/path
    target: /container/path
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	_, err := LoadGlobal()
	if err == nil {
		t.Fatal("expected error for relative source path")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Errorf("error should mention absolute path, got: %v", err)
	}
}

func TestLoadGlobal_MountsExcludeRejected(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: /home/user/data
    target: /data
    exclude:
      - node_modules
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	_, err := LoadGlobal()
	if err == nil {
		t.Fatal("expected error for excludes on global mount")
	}
	if !strings.Contains(err.Error(), "excludes") {
		t.Errorf("error should mention excludes, got: %v", err)
	}
}

func TestLoadGlobal_MountsTildeExpansion(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	content := `
mounts:
  - source: ~/.moat/scripts/statusline.js
    target: /home/user/.claude/moat/statusline.js
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if len(cfg.Mounts) != 1 {
		t.Fatalf("Mounts = %d, want 1", len(cfg.Mounts))
	}

	expected := filepath.Join(tmpHome, ".moat/scripts/statusline.js")
	if cfg.Mounts[0].Source != expected {
		t.Errorf("Source = %q, want %q", cfg.Mounts[0].Source, expected)
	}
}

func TestLoadGlobal_MountsEnforcesReadOnly(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	moatDir := filepath.Join(tmpHome, ".moat")
	os.MkdirAll(moatDir, 0755)

	// Mount specified as rw — should be forced to ro
	content := `
mounts:
  - source: /home/user/data
    target: /data
    mode: rw
`
	os.WriteFile(filepath.Join(moatDir, "config.yaml"), []byte(content), 0644)

	cfg, err := LoadGlobal()
	if err != nil {
		t.Fatalf("LoadGlobal: %v", err)
	}

	if !cfg.Mounts[0].ReadOnly {
		t.Error("global mount should be forced to read-only")
	}
}
```

- [ ] **Step 2: Run tests**

Run: `go test -run 'TestLoadGlobal_Mounts' ./internal/config/ -v`
Expected: All PASS

- [ ] **Step 3: Commit**

```bash
git add internal/config/global_test.go
git commit -m "test(config): add validation tests for global mount edge cases"
```

---

### Task 3: Inject global mounts in run manager

**Files:**
- Modify: `internal/run/manager.go:430-507`

The global config is already loaded in `NewManagerWithOptions` at line 125. We need to either store it on the Manager or load it again in `Create()`. Since `LoadGlobal` is cheap (reads a small YAML file) and is already called in multiple places, the simplest approach is to call it in `Create()`.

- [ ] **Step 1: Add global mounts after config mounts, before volumes**

In `internal/run/manager.go`, after the config mounts block (after line 489) and before the volumes block (line 494), add:

```go
	// Add global mounts from ~/.moat/config.yaml.
	// These are personal read-only mounts that apply to every run.
	globalCfg, globalErr := config.LoadGlobal()
	if globalErr != nil {
		log.Warn("failed to load global config for mounts", "error", globalErr)
	} else if len(globalCfg.Mounts) > 0 {
		for _, gm := range globalCfg.Mounts {
			mounts = append(mounts, container.MountConfig{
				Source:   gm.Source,
				Target:   gm.Target,
				ReadOnly: gm.ReadOnly,
			})
			log.Debug("added global mount", "source", gm.Source, "target", gm.Target)
		}
	}
```

Note: The `globalCfg` at line 125 is in `NewManagerWithOptions()` (different function). The one at line 1099 is inside `if len(ports) > 0 { ... }` — a separate block scope within `Create()`. Using `globalCfg` here (around line 489, outside that `if` block) does not shadow or collide. No rename needed.

- [ ] **Step 2: Run existing tests**

Run: `make test-unit`
Expected: All PASS (or pre-existing failures only)

- [ ] **Step 3: Commit**

```bash
git add internal/run/manager.go
git commit -m "feat(run): inject global mounts from ~/.moat/config.yaml into every run

Global mounts are appended after config mounts and before volumes.
They are always read-only."
```

---

### Task 4: Update mount documentation

**Files:**
- Modify: `docs/content/reference/05-mounts.md`

- [ ] **Step 1: Read the full mounts doc**

Run: Read `docs/content/reference/05-mounts.md`

- [ ] **Step 2: Add a "Global mounts" section**

Add after the existing mount documentation:

```markdown
## Global mounts

Global mounts are personal mounts that apply to every run. Configure them in `~/.moat/config.yaml`:

```yaml
mounts:
  - source: ~/.moat/scripts/statusline.js
    target: /home/user/.claude/moat/statusline.js
```

Global mounts use the same syntax as `moat.yaml` mounts (both string and object forms) with these constraints:

- **Source paths must be absolute** (or use `~` for home directory). There is no workspace to resolve relative paths against.
- **Always read-only.** Moat enforces read-only mode on global mounts regardless of the `mode` field.
- **Excludes are not supported.**

Global mounts are appended after project mounts and before volumes.
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/reference/05-mounts.md
git commit -m "docs(mounts): document global mounts in ~/.moat/config.yaml"
```

---

### Task 5: Add statusline recipe

**Files:**
- Modify: `docs/content/guides/13-recipes.md`

- [ ] **Step 1: Read the recipes guide**

Run: Read `docs/content/guides/13-recipes.md`

- [ ] **Step 2: Add statusline recipe**

Add a "Claude Code status line" recipe:

```markdown
## Claude Code status line

Use a global mount and `~/.moat/claude/settings.json` to display a custom status line inside moat containers.

**1. Create a status line script:**

```bash
mkdir -p ~/.moat/scripts
cat > ~/.moat/scripts/statusline.sh << 'EOF'
#!/bin/bash
echo "moat | $(hostname) | $(date +%H:%M)"
EOF
chmod +x ~/.moat/scripts/statusline.sh
```

**2. Mount the script into containers:**

```yaml
# ~/.moat/config.yaml
mounts:
  - source: ~/.moat/scripts/statusline.sh
    target: /home/user/.claude/moat/statusline.sh
```

**3. Configure Claude Code to use it:**

```json
// ~/.moat/claude/settings.json
{
  "statusLine": {
    "command": "/home/user/.claude/moat/statusline.sh"
  }
}
```

The global mount makes the script available in every container, and the settings passthrough forwards the `statusLine` config to Claude Code.
```

- [ ] **Step 3: Commit**

```bash
git add docs/content/guides/13-recipes.md
git commit -m "docs(recipes): add Claude Code status line recipe"
```

---

### Task 6: Lint and final verification

- [ ] **Step 1: Run linter**

Run: `make lint`
Expected: No errors

- [ ] **Step 2: Run full test suite for affected packages**

Run: `go test ./internal/config/ ./internal/run/ -v -race`
Expected: All PASS (or pre-existing failures only in container-dependent tests)

- [ ] **Step 3: Fix any issues and commit**
