---
title: "Recipes"
navTitle: "Recipes"
description: "Complete moat.yaml examples for common project types and workflows."
keywords: ["moat", "recipes", "examples", "node", "python", "go", "volumes", "caching", "multi-repo"]
---

# Recipes

Complete `moat.yaml` configurations for common project types. Each recipe is a working starting point — copy it and adjust to your project.

## Prerequisites

- A working Moat installation with Docker or Apple container runtime
- A `moat.yaml` file in your project directory

## Node.js

```yaml
name: my-node-app

dependencies:
  - node@20
  - claude-code
  - git
  - gh

grants:
  - claude
  - github

env:
  ANTHROPIC_MODEL: opus
  CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS: 1

volumes:
  - name: node-modules
    target: /workspace/node_modules

hooks:
  pre_run: npm install
```

What this demonstrates:

- **Volume-cached `node_modules`** — persists across runs so `npm install` is a fast no-op when `package-lock.json` hasn't changed. The volume is a bind mount from `~/.moat/volumes/`, so it bypasses the workspace's shared filesystem (no VirtioFS performance issues).
- **`env` for Claude Code configuration** — sets the model and enables experimental features via environment variables injected into the container
- **`pre_run` hook** — installs dependencies on every start, but skips work when the volume is warm

> **Tip:** Run `moat volumes rm my-node-app` after major lockfile changes or Node version bumps to start fresh.

## Python

```yaml
name: my-python-app

dependencies:
  - python@3.12
  - uv
  - claude-code
  - git

grants:
  - claude
  - github

volumes:
  - name: venv
    target: /workspace/.venv

hooks:
  post_build_root: apt-get update -qq && apt-get install -y -qq libpq-dev
  pre_run: uv sync
```

What this demonstrates:

- **Volume-cached `.venv`** — persists the virtual environment across runs, bypassing the workspace's shared filesystem
- **`uv` for fast installs** — uv resolves and installs packages significantly faster than pip
- **System dependencies at build time** — `libpq-dev` is installed in `post_build_root` and cached in the image layer, avoiding reinstallation on every run

## Go

```yaml
name: my-go-service

dependencies:
  - go@1.22
  - golangci-lint
  - claude-code
  - git
  - gh

grants:
  - anthropic
  - github

volumes:
  - name: gomodcache
    target: /home/moatuser/go/pkg/mod
  - name: gobuildcache
    target: /home/moatuser/.cache/go-build

hooks:
  pre_run: go mod download
```

What this demonstrates:

- **`anthropic` grant for API key auth** — uses an Anthropic API key instead of the `claude` grant (which uses Claude subscription OAuth). Use `anthropic` for CI or when billing through the API.
- **Volume-cached module and build caches** — `go mod download` and `go build` reuse cached artifacts across runs
- **Two separate volumes** — the module cache (`go/pkg/mod`) and build cache (`go-build`) have different invalidation patterns; splitting them allows independent cleanup
- **`golangci-lint` as a dependency** — installed at image build time, cached in the Docker layer

## Full-stack with services

```yaml
name: fullstack-app

dependencies:
  - node@20
  - python@3.12
  - postgres@17
  - redis@7
  - claude-code
  - git
  - gh

grants:
  - claude
  - github

services:
  postgres:
    env:
      POSTGRES_DB: appdb

volumes:
  - name: node-modules
    target: /workspace/frontend/node_modules
  - name: venv
    target: /workspace/backend/.venv

hooks:
  pre_run: |
    set -e
    cd /workspace/frontend && npm install
    cd /workspace/backend && uv sync
```

What this demonstrates:

- **Multiple runtimes** — Node and Python in one container (uses `debian:bookworm-slim` base)
- **Service dependencies** — PostgreSQL and Redis start automatically with readiness checks; connection info injected as `MOAT_POSTGRES_URL` and `MOAT_REDIS_URL`
- **Per-directory volumes** — separate dependency caches for frontend and backend, each bypassing the workspace's shared filesystem

> **Tip:** Add a `network:` block to restrict which hosts the agent can reach. See [moat.yaml reference](../reference/02-moat-yaml.md#network).

## Multi-repo with clones

```yaml
name: my-platform

dependencies:
  - node@20
  - claude-code
  - git

grants:
  - claude
  - github
  - ssh:github.com

volumes:
  - name: repos
    target: /home/moatuser/repos

hooks:
  pre_run: |
    set -e
    cd /home/moatuser/repos
    test -d api/.git    || git clone git@github.com:my-org/api.git
    test -d shared/.git || git clone git@github.com:my-org/shared.git
    cd api && git pull --ff-only
    cd ../shared && git pull --ff-only
```

What this demonstrates:

- **SSH grant for private repos** — `ssh:github.com` proxies SSH agent requests without exposing private keys
- **Volume-persisted clones** — repos are cloned once and updated on subsequent runs with `git pull`
- **Conditional clone** — `test -d .git ||` skips the clone if the repo already exists in the volume
- **Workspace separation** — cloned repos live in a volume outside `/workspace`, keeping the primary workspace clean

> **Tip:** Run `moat volumes rm my-platform` to reclone from scratch if the repos get into a bad state.

## Cache invalidation

Volumes persist until explicitly removed. Rebuild or clear caches when:

- **Lockfile changes significantly** — a volume-cached `node_modules` may have stale or conflicting packages after major dependency changes. Run `moat volumes rm <agent-name>` and let the next run reinstall.
- **Runtime version changes** — cached native modules compiled for Node 20 won't work after switching to Node 22. Clear the volume.
- **Image rebuild** — `--rebuild` rebuilds the image but does not clear volumes. If a rebuild changes something that affects cached dependencies (e.g., a new system library), clear volumes manually.

```bash
# Clear all volumes for an agent
$ moat volumes rm my-node-app

# Clear all managed volumes
$ moat volumes prune

# List volumes to see what exists
$ moat volumes ls
```

## Related pages

- [moat.yaml reference](../reference/02-moat-yaml.md) — Full configuration options
- [Dependencies](../reference/06-dependencies.md) — Available dependencies and layer caching
- [Lifecycle hooks](10-hooks.md) — Build-time vs runtime hooks
- [SSH access](04-ssh.md) — SSH agent proxying for private repos
- [Service dependencies](08-services.md) — Running databases and caches alongside agents
- [Mount syntax](../reference/05-mounts.md) — Mounts, excludes, and volumes
