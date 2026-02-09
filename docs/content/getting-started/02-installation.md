---
title: "Installation"
description: "Install Moat on macOS, Linux, or Windows with Docker or Apple containers."
keywords: ["moat", "installation", "docker", "apple containers", "setup", "homebrew"]
---

# Installation

## Requirements

- **Container runtime** -- Docker or Apple containers (macOS 26+ with Apple Silicon)

## Install Moat

### Homebrew (recommended)

```bash
brew tap majorcontext/moat
brew install moat
```

### Download binary

Download a prebuilt binary from the [GitHub releases](https://github.com/majorcontext/moat/releases) page. Archives are available for macOS (arm64, amd64) and Linux (arm64, amd64).

Extract the archive and move the binary to a directory in your `PATH`:

```bash
tar xzf moat_*.tar.gz
mv moat /usr/local/bin/
```

### Using `go install`

Requires Go 1.21 or later.

```bash
go install github.com/majorcontext/moat/cmd/moat@latest
```

Ensure `$GOPATH/bin` (typically `~/go/bin`) is in your `PATH`.

### From source

```bash
git clone https://github.com/majorcontext/moat.git
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

### Apple containers (macOS 26+ with Apple Silicon)

Apple containers require macOS 26 (Tahoe) on Apple Silicon Macs. Install the `container` CLI from the [Apple container releases](https://github.com/apple/container/releases) page.

Download the latest `.pkg` installer and run it:

```bash
sudo installer -pkg container-*.pkg -target /
```

Start the container system:

```bash
sudo container system start
```

Verify Apple containers are available:

```bash
$ container --version
container 1.x.x

$ moat status

Runtime: apple
...
```

### Docker (macOS, Linux, Windows)

**macOS (Homebrew):**

```bash
brew install --cask docker
open -a Docker
```

**Linux (Debian/Ubuntu):**

```bash
sudo apt-get update
sudo apt-get install docker.io
sudo systemctl enable --now docker
sudo usermod -aG docker $USER
```

Log out and back in for the group change to take effect.

**Other platforms:**

- **Linux (other distros)**: [Docker Engine](https://docs.docker.com/engine/install/)
- **Windows**: [Docker Desktop for Windows](https://docs.docker.com/desktop/install/windows-install/)

> **Note:** When using `docker:dind` mode in agent.yaml, Moat automatically deploys a BuildKit sidecar for image builds. See the [docker dependency documentation](../reference/02-agent-yaml.md#docker) for details.

Verify Docker is running:

```bash
$ docker info
...

$ moat status

Runtime: docker
...
```

## GitHub authentication setup (optional)

`moat grant github` automatically uses credentials from these sources (in order):

1. **Environment variable**: `GITHUB_TOKEN` or `GH_TOKEN`
2. **GitHub CLI**: `gh auth token` (if `gh` is installed and authenticated)
3. **Interactive prompt**: Enter a Personal Access Token

Most users don't need additional setup. If you already use `gh` CLI or have `GITHUB_TOKEN` set, you're ready to go.

If you need to create a Personal Access Token:

1. Visit [GitHub Personal Access Tokens](https://github.com/settings/tokens)
2. Click **Generate new token** → **Fine-grained token** (recommended)
3. Set expiration and select repositories
4. Under **Repository permissions**, grant **Contents** read/write access
5. Copy the token and either:
   - Set `GITHUB_TOKEN` in your shell profile
   - Use `moat grant github` which will prompt for it

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

- [Quick start](./03-quick-start.md) -- Run your first agent
