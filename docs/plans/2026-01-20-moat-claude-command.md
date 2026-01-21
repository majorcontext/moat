# Moat Claude Command Design

**Date:** 2026-01-20
**Status:** Draft

## Problem

Running Claude Code with Moat currently requires:

```bash
moat run --grant anthropic -i -- npx @anthropic-ai/claude-code
```

This is verbose and requires remembering multiple flags. Users want:

```bash
moat claude
```

## Goals

1. **Simple command**: `moat claude` runs Claude Code in the current directory
2. **Session management**: List, resume, and manage Claude sessions
3. **Auto-configuration**: Sensible defaults without requiring agent.yaml
4. **Integration**: Seamless handling of grants, plugins, and workspace mounting

## Command Design

### Primary Command: `moat claude`

```bash
# Start Claude Code in current directory (interactive)
moat claude

# Start Claude Code in a specific directory
moat claude ./my-project

# Start with a prompt (one-shot mode)
moat claude -p "fix the bug in main.py"

# Start with additional grants
moat claude --grant github

# Resume a previous session
moat claude --resume
moat claude --resume <session-id>

# List Claude sessions
moat claude sessions

# Name the session for easy reference
moat claude --name my-feature
```

### Subcommands

```bash
moat claude sessions              # List all Claude sessions
moat claude sessions --active     # List running sessions
moat claude attach <session>      # Attach to running session
moat claude logs <session>        # View session logs
moat claude stop <session>        # Stop a running session
```

### Flags

| Flag | Short | Description | Default |
|------|-------|-------------|---------|
| `--prompt` | `-p` | Run with prompt (non-interactive) | - |
| `--grant` | `-g` | Additional grants (can repeat) | `anthropic` |
| `--name` | `-n` | Name for this session | auto-generated |
| `--resume` | `-r` | Resume previous session | - |
| `--detach` | `-d` | Run in background | false |
| `--model` | `-m` | Claude model to use | - |
| `--no-plugins` | - | Disable plugin loading | false |

## Behavior

### Default Grants

When running `moat claude`, these grants are automatically included:
- `anthropic` (required for API access)

Additional grants from:
- `.claude/settings.json` `grants` field (if present)
- `agent.yaml` `grants` field (if present)
- `--grant` flags

### Session Persistence

Claude sessions are persisted to enable resume:

```
~/.moat/claude/sessions/
├── index.json                    # Session index
└── <session-id>/
    ├── metadata.json             # Session metadata
    ├── workspace-path            # Original workspace path
    └── run-id                    # Associated moat run ID
```

**Session metadata:**
```json
{
  "id": "abc123",
  "name": "my-feature",
  "workspace": "/home/user/my-project",
  "runId": "run-xyz789",
  "createdAt": "2026-01-20T10:00:00Z",
  "lastAccessedAt": "2026-01-20T11:30:00Z",
  "state": "running|stopped|completed"
}
```

### Resume Behavior

```bash
# Resume most recent session for current workspace
moat claude --resume

# Resume specific session by name or ID
moat claude --resume my-feature
moat claude --resume abc123
```

Resume logic:
1. Find session matching workspace (or name/ID if specified)
2. If session's run is still active, attach to it
3. If session's run stopped, start new run with same configuration
4. Mount same workspace, apply same grants

### Workspace Detection

`moat claude` automatically detects workspace configuration:

1. Check for `agent.yaml` in current/specified directory
2. Check for `.claude/settings.json` for plugin configuration
3. Check for `.claude/` directory (indicates Claude Code project)
4. Fall back to sensible defaults

### Default Container Image

Without `agent.yaml`, use a pre-built Claude Code image:
- Base: `node:20-slim` (Claude Code requires Node.js)
- Pre-installed: `@anthropic-ai/claude-code`

Or build on-demand with dependencies detected from:
- `package.json` → Node.js
- `requirements.txt` / `pyproject.toml` → Python
- `go.mod` → Go

## Implementation

### Phase 1: Core Command

**Files:**
- `cmd/moat/cli/claude_run.go` (new) - Main `moat claude` command

**Implementation:**

```go
var claudeRunCmd = &cobra.Command{
    Use:   "claude [workspace]",
    Short: "Run Claude Code in an isolated container",
    Long: `Start an interactive Claude Code session in an isolated container.

Your workspace is mounted at /workspace. API credentials are injected
transparently via the Moat proxy - Claude Code never sees raw tokens.

Examples:
  # Start Claude Code in current directory
  moat claude

  # Start in a specific project
  moat claude ./my-project

  # Ask Claude to do something specific
  moat claude -p "explain this codebase"

  # Resume your last session
  moat claude --resume`,
    Args: cobra.MaximumNArgs(1),
    RunE: runClaude,
}
```

**Core logic:**

```go
func runClaude(cmd *cobra.Command, args []string) error {
    workspace := "."
    if len(args) > 0 {
        workspace = args[0]
    }

    // Resolve workspace
    absPath, err := filepath.Abs(workspace)
    if err != nil {
        return err
    }

    // Check for anthropic grant
    if !hasAnthropicGrant() {
        return fmt.Errorf(`Anthropic API key not configured.

Run 'moat grant anthropic' to set up your API key.`)
    }

    // Load config if present, use defaults otherwise
    cfg := loadConfigOrDefaults(absPath)

    // Ensure anthropic grant is included
    grants := ensureGrant(cfg.Grants, "anthropic")
    grants = append(grants, additionalGrants...)

    // Build command
    containerCmd := []string{"npx", "@anthropic-ai/claude-code"}
    if promptFlag != "" {
        containerCmd = append(containerCmd, "-p", promptFlag)
    }

    // Create and run
    opts := run.Options{
        Name:        nameFlag,
        Workspace:   absPath,
        Grants:      grants,
        Cmd:         containerCmd,
        Config:      cfg,
        Interactive: promptFlag == "",
        TTY:         promptFlag == "",
    }

    // ... create and start run
}
```

### Phase 2: Session Management

**Files:**
- `internal/claude/session.go` (new) - Session persistence
- `cmd/moat/cli/claude_sessions.go` (new) - Session subcommands

**Session tracking:**

```go
type Session struct {
    ID             string    `json:"id"`
    Name           string    `json:"name"`
    Workspace      string    `json:"workspace"`
    RunID          string    `json:"runId"`
    Grants         []string  `json:"grants"`
    CreatedAt      time.Time `json:"createdAt"`
    LastAccessedAt time.Time `json:"lastAccessedAt"`
    State          string    `json:"state"`
}

type SessionManager struct {
    dir string
}

func (m *SessionManager) Create(workspace string, runID string, grants []string) (*Session, error)
func (m *SessionManager) Get(idOrName string) (*Session, error)
func (m *SessionManager) GetByWorkspace(workspace string) (*Session, error)
func (m *SessionManager) List() ([]*Session, error)
func (m *SessionManager) UpdateState(id string, state string) error
```

### Phase 3: Resume Support

**Resume logic:**

```go
func runClaudeResume(workspace string) error {
    sessions := claude.NewSessionManager()

    var session *claude.Session
    if resumeFlag == "" {
        // Find most recent session for this workspace
        session, _ = sessions.GetByWorkspace(workspace)
    } else {
        // Find by name or ID
        session, _ = sessions.Get(resumeFlag)
    }

    if session == nil {
        return fmt.Errorf("no session found to resume")
    }

    // Check if run is still active
    manager, _ := run.NewManager()
    r, err := manager.Get(session.RunID)

    if err == nil && r.State == run.StateRunning {
        // Attach to existing run
        return manager.Attach(ctx, r.ID, os.Stdin, os.Stdout, os.Stderr)
    }

    // Start new run with same configuration
    return startNewSession(session.Workspace, session.Grants, session.Name)
}
```

## CLI Examples

### Basic Usage

```bash
# First time setup
$ moat grant anthropic
Enter your Anthropic API key: sk-ant-...
API key validated and saved.

# Run Claude Code
$ moat claude
Starting Claude Code in /home/user/my-project...
Session: serene-willow (run-abc123)

╭─────────────────────────────────────────────────────────────────╮
│ Claude Code                                                      │
│ Workspace: /workspace                                            │
│ Escape: Ctrl-/ d (detach), Ctrl-/ k (stop)                      │
╰─────────────────────────────────────────────────────────────────╯

>
```

### One-Shot Mode

```bash
$ moat claude -p "what does this code do?"
Starting Claude Code in /home/user/my-project...

This codebase is a CLI tool for running AI agents in isolated containers...
[Claude's response]

Session completed.
```

### Session Management

```bash
$ moat claude sessions
SESSION     WORKSPACE                    STATE      LAST ACCESSED
serene-willow  /home/user/my-project    running    2 minutes ago
bold-river     /home/user/other-project stopped    1 hour ago

$ moat claude --resume serene-willow
Attaching to session serene-willow...
```

## Migration Path

The existing `moat claude plugins` and `moat claude marketplace` commands remain unchanged. The new `moat claude` (without subcommand) becomes the primary entry point for running Claude Code.

Command structure:
```
moat claude                       # Run Claude Code (new)
moat claude sessions              # List sessions (new)
moat claude plugins list          # List plugins (existing)
moat claude marketplace list      # List marketplaces (existing)
moat claude marketplace update    # Update marketplaces (existing)
```

## Future Enhancements

1. **Multi-model support**: `moat claude --model opus` to select Claude model
2. **Session sharing**: Export/import sessions for collaboration
3. **Auto-resume**: Detect interrupted sessions and offer to resume
4. **Context management**: `moat claude context add <file>` to add files to Claude's context
5. **Cost tracking**: Track API usage per session
