# Status Line Support for Moat

## Problem

Claude Code supports a `statusLine` setting that runs a command and displays its output. Users configure this in `~/.claude/settings.json`. However, moat's `Settings` struct only captures `enabledPlugins`, `extraKnownMarketplaces`, and `skipDangerousModePermissionPrompt` — all other fields are silently dropped during the load/merge/write cycle.

Additionally, status line commands often reference script files on the host that don't exist inside the container.

## Goals

- Users can configure their own status line without affecting other users or the project
- If a status line script is referenced, it gets copied into the container automatically
- The `statusLine` setting passes through to the container's `~/.claude/settings.json`

## Design

### User Configuration

Users add two fields to `~/.moat/claude/settings.json`:

```json
{
  "statusLine": {
    "type": "command",
    "command": "node {{statusLineScript}}"
  },
  "statusLineScript": "~/scripts/my-statusline.js"
}
```

- `statusLine` — standard Claude Code config, with optional `{{statusLineScript}}` template placeholder
- `statusLineScript` — host path to the script file (moat-specific field, not part of Claude's schema)

### Data Flow

```
~/.moat/claude/settings.json
  ↓ LoadAllSettings() — statusLine + statusLineScript parsed
  ↓ manager.go:
  │   1. Expand ~ in statusLineScript path
  │   2. Validate file exists, read it
  │   3. Copy script to staging dir under moat-statusline/
  │   4. Replace {{statusLineScript}} in statusLine.command
  │      with /home/moatuser/.claude/moat/<basename>
  │   5. Strip statusLineScript from settings (not a Claude field)
  │   6. Write final settings.json to staging
  ↓ moat-init.sh:
      1. Copy moat-statusline/* → ~/.claude/moat/
      2. Copy settings.json → ~/.claude/settings.json (existing)
  → Claude Code runs the resolved command
```

### Container Path Convention

Status line scripts are placed at `~/.claude/moat/<basename>` inside the container. The `moat/` subdirectory avoids conflicts with Claude Code's own files.

### Changes

#### 1. `internal/providers/claude/settings.go`

Add two fields to the `Settings` struct:

```go
// StatusLine is the Claude Code statusLine configuration.
// Preserved as raw JSON through merge since moat doesn't need to interpret its structure.
StatusLine json.RawMessage `json:"statusLine,omitempty"`

// StatusLineScript is a moat-specific field: a host path to a script file
// that should be copied into the container. Not written to container settings.
StatusLineScript string `json:"-"`
```

`StatusLineScript` uses `json:"-"` so it's never written to the container. Custom `UnmarshalJSON` is needed to parse it from the input.

Update `MergeSettings`: if the override has a `StatusLine`, it replaces the base's.

#### 2. `internal/run/manager.go`

After loading settings and before writing to staging:

1. Check `claudeSettings.StatusLineScript`
2. Expand `~` → `$HOME`
3. Read the file, warn and skip if missing/unreadable
4. Write to `<staging>/moat-statusline/<basename>`
5. Replace `{{statusLineScript}}` in `StatusLine` command with `/home/moatuser/.claude/moat/<basename>`
6. Ensure `statusLineScript` is not in the marshaled settings

#### 3. `internal/deps/scripts/moat-init.sh`

Add after existing Claude setup:

```bash
# Copy status line script if present
if [ -d "$MOAT_CLAUDE_INIT/moat-statusline" ]; then
  mkdir -p "$TARGET_HOME/.claude/moat"
  cp -p "$MOAT_CLAUDE_INIT/moat-statusline/"* "$TARGET_HOME/.claude/moat/" 2>/dev/null || true
fi
```

### Edge Cases

| Scenario | Behavior |
|----------|----------|
| `statusLineScript` file missing | `ui.Warn`, skip script copy, still pass `statusLine` through |
| `{{statusLineScript}}` not in command | Copy file anyway, pass `statusLine` as-is |
| `statusLine` without `statusLineScript` | Pass through (e.g., `date` command needs no script) |
| `~` in `statusLineScript` path | Expanded to `$HOME` |
| Script has no execute permission | Works if command uses `node`/`bash`/`python` explicitly |

### What This Does NOT Do

- No general passthrough of unknown settings fields (scoped to statusLine only)
- No templating in other settings fields
- No project-level or moat.yaml status line config (per-user only)
- No handling of script dependencies (single file only)
