---
title: "Installation"
description: "Install Moat on macOS, Linux, or Windows with Docker or Apple containers."
keywords: ["moat", "installation", "docker", "apple containers", "setup"]
---

# Installation

## Requirements

- **Go 1.21 or later** — For building from source
- **Container runtime** — Docker or Apple containers (macOS 15+ with Apple Silicon)

## Install Moat

### Using `go install`

```bash
go install github.com/andybons/moat/cmd/moat@latest
```

Ensure `$GOPATH/bin` (typically `~/go/bin`) is in your `PATH`.

### From source

```bash
git clone https://github.com/andybons/moat.git
cd moat
go build -o moat ./cmd/moat
```

Move the binary to a directory in your `PATH`:

```bash
mv moat /usr/local/bin/
```

### Verify installation

```bash
$ moat --help

Usage:
  moat [command]

Available Commands:
  run         Run an agent
  claude      Run Claude Code
  grant       Store credentials
  ...
```

## Container runtime setup

Moat requires a container runtime. It detects the available runtime automatically.

### macOS 15+ with Apple Silicon

Apple containers are built into macOS 15 (Sequoia) on Apple Silicon Macs. No additional installation required.

Verify Apple containers are available:

```bash
$ moat status

Runtime: apple
...
```

### macOS (Intel), Linux, Windows

Install Docker:

- **macOS**: [Docker Desktop for Mac](https://docs.docker.com/desktop/install/mac-install/)
- **Linux**: [Docker Engine](https://docs.docker.com/engine/install/)
- **Windows**: [Docker Desktop for Windows](https://docs.docker.com/desktop/install/windows-install/)

> **Note:** When using `docker:dind` mode in agent.yaml, Moat automatically deploys a BuildKit sidecar for optimized image builds. No additional installation is required. See the [docker dependency documentation](../reference/02-agent-yaml.md#docker) for details.

Verify Docker is running:

```bash
$ docker info
...

$ moat status

Runtime: docker
...
```

## GitHub OAuth setup

To use `moat grant github`, you need a GitHub OAuth App with device flow enabled.

1. Go to [GitHub Developer Settings](https://github.com/settings/developers)
2. Click **New OAuth App**
3. Fill in the application details:
   - **Application name**: Moat (or any name)
   - **Homepage URL**: `http://localhost`
   - **Authorization callback URL**: `http://localhost`
4. Click **Register application**
5. On the app settings page, check **Enable Device Flow**
6. Copy the **Client ID**

Set the environment variable in your shell profile (`~/.bashrc`, `~/.zshrc`, etc.):

```bash
export MOAT_GITHUB_CLIENT_ID="your-client-id"
```

Reload your shell or run `source ~/.zshrc`.

## Optional dependencies

These CLI tools enable additional features:

| Tool | Purpose | Installation |
|------|---------|--------------|
| 1Password CLI (`op`) | Resolve `op://` secrets | `brew install 1password-cli` |
| AWS CLI (`aws`) | Resolve `ssm://` secrets | `brew install awscli` |

## Directory structure

Moat stores data in `~/.moat/`:

```
~/.moat/
  credentials/     # Encrypted credentials
  runs/            # Per-run artifacts (logs, traces, audit)
  proxy/           # Routing proxy CA certificate
  config.yaml      # Global configuration (optional)
```

## Next steps

- [Quick start](./03-quick-start.md) — Run your first agent
