---
title: "Lifecycle hooks"
navTitle: "Hooks"
description: "Run commands at build time, before the agent starts, and after it completes using agent.yaml lifecycle hooks."
keywords: ["moat", "hooks", "lifecycle", "post_build", "pre_run", "post_run", "agent.yaml"]
---

# Lifecycle hooks

Hooks let you run commands at specific points in the container lifecycle: after the image is built, before the main command runs, and after it completes. This guide covers each hook type, what it has access to, and practical patterns for common setups.

## Prerequisites

- A working Moat installation with Docker or Apple container runtime
- An `agent.yaml` file in your project directory

## Hook types

Moat supports three lifecycle hooks, each running at a different stage:

| Hook | When it runs | User | Cached | Workspace available |
|------|-------------|------|--------|---------------------|
| `post_build_root` | During image build, after dependencies | root | Yes | No |
| `post_build` | During image build, after `post_build_root` | moatuser | Yes | No |
| `pre_run` | Every container start, before `command` | moatuser | No | Yes |

### post_build_root

Runs as `root` during image build. Use for system-level setup that requires elevated privileges: installing system packages, modifying `/etc` files, or kernel tuning.

```yaml
hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq figlet
```

### post_build

Runs as `moatuser` during image build. Use for user-level image setup: configuring tools, setting git defaults, or compiling assets.

```yaml
hooks:
  post_build: git config --global core.autocrlf input
```

### pre_run

Runs as `moatuser` in `/workspace` on every container start, before the main command. Use for workspace-level setup that needs your project files: installing dependencies, running codegen, or building assets.

```yaml
hooks:
  pre_run: npm install
```

`pre_run` runs before any command, including when `moat claude` or `moat codex` overrides `command`.

## Execution order

The full container lifecycle proceeds in this order:

```text
Image build (cached):
  1. Dependencies installed (from dependencies:)
  2. post_build_root runs as root in /workspace
  3. post_build runs as moatuser in /workspace

Container start (every run):
  4. Workspace mounted
  5. pre_run runs as moatuser in /workspace
  6. command (or agent command) executes
```

Build hooks (steps 1-3) are baked into the image layer and cached. Runtime hooks (steps 5-6) run on every container start.

## Build time vs runtime

Build hooks (`post_build`, `post_build_root`) run during image build. They cannot access workspace files because the workspace is not mounted yet. They have access to the container filesystem and network.

`pre_run` runs at container start when the workspace is mounted. It has access to your project files, environment variables, and secrets.

Choose the right hook based on what you need:

| Task | Hook | Why |
|------|------|-----|
| Install system packages | `post_build_root` | Needs root, cached in image |
| Configure git defaults | `post_build` | User-level, cached in image |
| Install project dependencies | `pre_run` | Needs workspace files |
| Run database migrations | `pre_run` | Needs workspace and network |
| Compile a binary | `post_build` or `pre_run` | `post_build` if source is in image; `pre_run` if source is in workspace |

## Configuration examples

### Node.js project

Install project dependencies on every start. Package managers like `npm install` are fast no-ops when `node_modules` is current.

```yaml
name: my-node-app

dependencies:
  - node@20
  - git

hooks:
  pre_run: npm install

command: ["npm", "start"]
```

### Python project with system dependencies

Install a system library at build time (cached), then install project dependencies at runtime.

```yaml
name: my-python-app

dependencies:
  - python@3.11

hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq libpq-dev
  pre_run: pip install -r requirements.txt

command: ["python", "app.py"]
```

### Go project with tools

Configure git and install linting tools at build time. Build the project from the workspace at runtime.

```yaml
name: my-go-service

dependencies:
  - go@1.22
  - golangci-lint
  - git

hooks:
  post_build: git config --global core.autocrlf input && git config --global pull.rebase true
  pre_run: go build ./...

command: ["./my-go-service"]
```

### Multi-step build hooks

Chain commands with `&&` for multi-step setup within a single hook:

```yaml
hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq curl gnupg
  post_build: git config --global core.autocrlf input && git config --global pull.rebase true
  pre_run: npm install && npm run build
```

> **Note:** Git identity (`user.name` and `user.email`) is imported automatically from the host when `git` is listed as a dependency. Use a `post_build` hook only if you need to override the host identity or set other git configuration.

### Claude Code agent with setup

```yaml
name: my-agent

dependencies:
  - node@20
  - claude-code
  - git
  - gh

grants:
  - anthropic
  - github

hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq jq
  pre_run: npm install
```

## Build caching

Build hooks (`post_build`, `post_build_root`) are Dockerfile `RUN` commands. The build system caches each layer, so they only re-run when:

- The command string changes in `agent.yaml`
- You use `--rebuild` to force a fresh build
- A preceding layer changes (new dependency, base image update, etc.)

`pre_run` is not cached. It runs on every container start.

To force re-running build hooks:

```bash
$ moat run --rebuild ./my-project
```

### Moving work to build hooks for speed

If `pre_run` takes a long time on every start, consider whether part of the work can move to a build hook. For example, installing a Python package that compiles C extensions is slow in `pre_run` but fast on subsequent runs when cached in `post_build`.

However, build hooks cannot access your workspace files. Only packages or tools that don't depend on your project code can be installed at build time. For project-specific dependencies (those listed in `package.json`, `requirements.txt`, etc.), use `pre_run`. Workspace-aware package managers like `npm install` and `pip install` skip already-installed packages, keeping repeated `pre_run` execution fast.

## Troubleshooting

### Hook fails during image build

Build hooks run without access to workspace files. If a command references a file from your project, move it to `pre_run` instead.

```text
Error: post_build command failed: npm install
```

`npm install` needs `package.json` from the workspace. Use `pre_run` instead:

```yaml
hooks:
  # Wrong: workspace not available during build
  # post_build: npm install

  # Correct: workspace is mounted at runtime
  pre_run: npm install
```

### Command not found in hook

The command must be available in the image at the time the hook runs. If a build hook references a tool, ensure it is either:

- Part of the base image
- Installed by a preceding step (dependency or earlier hook)

`post_build_root` runs before `post_build`, so system packages installed in `post_build_root` are available in `post_build`. Both build hooks run after dependencies are installed.

```yaml
dependencies:
  - node@20       # Installed before hooks run

hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq imagemagick
  post_build: node --version   # node is available from dependencies
  pre_run: npx prisma generate  # npx is available from node dependency
```

### pre_run slows down every start

If `pre_run` performs expensive work, consider:

1. **Moving static setup to build hooks** -- System packages and tool configuration belong in `post_build_root` or `post_build` where they are cached.
2. **Using workspace-aware commands** -- `npm install`, `pip install`, and `go build` skip work when outputs are current. These are safe and fast in `pre_run`.
3. **Adding guards** -- For custom scripts, check whether the work is already done:

```yaml
hooks:
  pre_run: "test -f .setup-done || (npm install && npm run codegen && touch .setup-done)"
```

### Hook runs but changes are lost

Build hooks modify the image layer. If you install something in `post_build` but it is missing at runtime, check that:

- The installation path is not inside `/workspace` (which gets replaced by the mounted workspace at runtime)
- The command ran successfully during build (check build output with `--verbose`)

## Related guides

- [agent.yaml reference](../reference/02-agent-yaml.md#hooks) -- Complete hook field definitions
- [Dependencies](../reference/06-dependencies.md) -- Image selection and dependency installation
- [Runtimes](../concepts/07-runtimes.md) -- Container runtime details
