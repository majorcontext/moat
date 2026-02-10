# Worktree Integration Design

Consolidate branch creation, worktree creation, and agent execution into a single command.

## CLI Surface

### Top-level command: `moat wt`

```
moat wt <branch> [-- command]     # create/reuse worktree, start agent
moat wt list                       # show worktree-based runs
moat wt clean [branch]             # remove worktree dirs for stopped runs
```

Flags for `moat wt <branch>`:

- `--detach / -d` — run in background
- `--name / -n` — override auto-generated run name
- `--grant / -g` — credential grants
- `--env / -e` — environment variables
- `--rebuild` — force image rebuild
- `--keep` — keep container after completion

Agent and provider determined from `agent.yaml`. Error with a clear message if no `agent.yaml` exists.

### `--wt` flag on provider commands

```
moat claude --wt=dark-mode [other flags]
moat codex --wt=dark-mode [other flags]
moat gemini --wt=dark-mode [other flags]
```

`--wt` is a workspace modifier. All existing provider flags (`--prompt`, `--detach`, `--noyolo`, etc.) compose normally. The worktree resolution runs first, then the provider command proceeds with the resolved workspace path.

### Run naming

Format: `{agent-name}-{branch}` when `agent.yaml` has a `name` field, otherwise just `{branch}`. The `--name` flag overrides.

## Worktree Lifecycle

### Creation flow

When you run `moat wt dark-mode`:

1. **Resolve repo** — find the git repo root from the current directory. Extract remote origin to build the repo identifier.

2. **Check branch** — if `dark-mode` exists, use it. If not, create it from current HEAD.

3. **Check worktree** — look for existing worktree at `~/.moat/worktrees/{repo-id}/dark-mode`. If it exists, reuse it. If not, run `git worktree add <path> <branch>`.

4. **Check for active run** — if there's already a running session in that worktree, attach to it instead of starting a new one.

5. **Start run** — call the normal run flow with the worktree path as workspace.

User feedback at each step:

```
Using existing branch dark-mode
Creating worktree at ~/.moat/worktrees/github.com/acme/myrepo/dark-mode
Starting run myapp-dark-mode...
```

### Cleanup

- `moat wt clean dark-mode` — removes the worktree directory and runs `git worktree prune`. Only works if no active run is using it. Never deletes branches.
- `moat wt clean` (no args) — removes all worktrees for the current repo whose runs are stopped.

## `moat wt list`

Shows worktree-based runs for the current repo:

```
BRANCH          RUN NAME            STATUS     WORKTREE
dark-mode       myapp-dark-mode     running    ~/.moat/worktrees/github.com/acme/myrepo/dark-mode
checkout-fix    myapp-checkout-fix  stopped    ~/.moat/worktrees/github.com/acme/myrepo/checkout-fix
```

Scoped to the current repo by default. Shows only worktree-managed runs.

Runs store a `worktree` field in metadata (`metadata.json`) with the branch name and worktree path. `moat wt list` filters runs that have this field and match the current repo.

## `--wt` on Provider Commands

When you run `moat claude --wt=dark-mode --prompt "implement dark mode" --detach`:

1. `--wt` triggers the same worktree resolution as `moat wt`.
2. The resolved worktree path replaces the workspace argument.
3. Run naming follows `{agent-name}-{branch}`.
4. Everything else proceeds as normal for the provider command.

The worktree logic lives in `internal/worktree/` and is called by both `moat wt` and the `--wt` flag.

```go
// internal/worktree
type Result struct {
    WorkspacePath string // absolute path to worktree
    Branch        string
    RunName       string // auto-generated, can be overridden
    Reused        bool   // whether worktree already existed
    ActiveRunID   string // non-empty if a run is already active
}

func Resolve(repoRoot, remotePath, branch, agentName string) (*Result, error)
```

## Repo Identifier Resolution

Worktree path: `~/.moat/worktrees/{repo-id}/{branch}`

Resolution:

1. Parse `git remote get-url origin`.
2. Normalize to `host/owner/repo` — strip protocol, `.git` suffix, `git@` SSH prefix. Both `git@github.com:acme/myrepo.git` and `https://github.com/acme/myrepo.git` become `github.com/acme/myrepo`.
3. If no remote origin exists, fall back to `_local/{repo-dir-name}`.

Base path `~/.moat/worktrees/` can be overridden via `MOAT_WORKTREE_BASE` environment variable. Global config file support can be added later.

## Example Workflows

### Quick feature branch with agent

```bash
moat wt dark-mode
# Creates branch, worktree, starts agent from agent.yaml
```

### Fire-and-forget with Claude

```bash
moat claude --wt=dark-mode --prompt "implement dark mode" --detach
# Creates branch+worktree, starts Claude with prompt, returns immediately
```

### Multiple parallel features

```bash
moat claude --wt=dark-mode --prompt "implement dark mode" --detach
moat claude --wt=auth-refactor --prompt "refactor auth to use JWT" --detach
moat wt list
# BRANCH          RUN NAME              STATUS    WORKTREE
# dark-mode       myapp-dark-mode       running   ~/.moat/worktrees/...
# auth-refactor   myapp-auth-refactor   running   ~/.moat/worktrees/...
```

### Cleanup

```bash
moat wt clean dark-mode   # remove one worktree
moat wt clean             # remove all stopped worktrees for this repo
```
