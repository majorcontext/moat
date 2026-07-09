---
title: "Running GitHub Copilot CLI"
navTitle: "Copilot CLI"
description: "Run GitHub Copilot CLI in an isolated container with credential injection."
keywords: ["moat", "copilot", "github copilot", "ai agent", "coding assistant"]
---

# Running GitHub Copilot CLI

This guide covers running GitHub Copilot CLI in a Moat container.

## Prerequisites

- Moat installed
- An active GitHub Copilot subscription
- A Copilot-capable GitHub token, either from `gh auth login` or a fine-grained PAT with the **Copilot Requests** permission

## Granting GitHub credentials

Run `moat grant github` to configure authentication:

```bash
moat grant github
```

Moat checks `GITHUB_TOKEN` and `GH_TOKEN` first. If none are set, it can use `gh auth token` from GitHub CLI, or prompt for a PAT.

The token must be able to use Copilot. GitHub CLI OAuth tokens from `gh auth login` work when the account has an active Copilot subscription. Fine-grained PATs must be created for your personal account with the **Copilot Requests** account permission. Classic PATs are not supported by GitHub Copilot CLI.

Before starting a Copilot container, Moat checks the stored GitHub credential against `api.github.com/copilot_internal/user`. If the token cannot use Copilot, the run fails before building or starting the container.

## How credentials are injected

The raw GitHub credential is stored encrypted on the host. Inside the container, Moat sets format-valid placeholders in `COPILOT_GITHUB_TOKEN` and `GH_TOKEN`. The proxy intercepts GitHub and Copilot HTTPS requests and injects the real GitHub token for `api.github.com`, Copilot API hosts, and HTTPS git operations against `github.com`.

## Running Copilot

Interactive mode:

```bash
moat copilot
moat copilot ./my-project
```

Interactive mode with an initial prompt:

```bash
moat copilot -- "explain this codebase"
```

Non-interactive mode:

```bash
moat copilot -p "fix the failing tests"
```

Moat passes `--allow-all` to Copilot CLI by default so the agent can complete tasks without approval prompts. The container, workspace mount, and network policy still constrain the run.

## Configuration

```yaml
agent: copilot
grants:
  - github

copilot:
  model: gpt-5.4
  context: long_context
  reasoning_effort: high
  experimental: false
  autopilot: false
```

`moat copilot` adds the `github` grant automatically when a GitHub credential is configured, so you do not need to list it unless you use `moat run` directly.

## Network policy

`moat copilot` adds the GitHub and Copilot hosts it needs to the run's network rules. Under `network.policy: strict`, add any extra hosts your task needs with `--allow-host` or `network.rules`.

```bash
moat copilot --allow-host registry.npmjs.org -p "update dependencies"
```
