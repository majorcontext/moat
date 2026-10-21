---
title: "Installation"
description: "Install Moat on macOS or Linux with Docker or Apple containers."
keywords: ["moat", "installation", "docker", "apple containers", "setup", "homebrew"]
---

# Installation

## Requirements

- **Container runtime** -- Docker or Apple containers (macOS 26+ with Apple Silicon)

## Install Moat

### Homebrew (recommended)

```bash
brew tap majorcontext/tap
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

Requires Go 1.25.5 or later.

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
container system start
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

On Linux, Moat runs containers under gVisor by default and exits with installation instructions if it is missing. Install it on Debian/Ubuntu using the [gVisor package repository](https://gvisor.dev/docs/user_guide/install/):

```bash
sudo apt-get install -y ca-certificates curl gnupg
curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor.gpg && \
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor.gpg] https://storage.googleapis.com/gvisor/releases release main" | \
  sudo tee /etc/apt/sources.list.d/gvisor.list && \
  sudo apt-get update && sudo apt-get install -y runsc && \
  sudo runsc install && \
  sudo systemctl reload docker
```

`runsc install` registers gVisor as an additional Docker runtime; Docker's default runtime stays `runc`, and Moat selects `runsc` for its own containers. See [Runtimes](../concepts/07-runtimes.md#overriding-sandbox-mode) to run without gVisor.

**Windows (WSL 2):**

Install Docker Engine and gVisor inside your WSL distribution using the Debian/Ubuntu steps above, and run Moat from the same distribution. Moat in WSL is a Linux binary and uses the Linux defaults: gVisor, and containers that run as your WSL user's UID so workspace files keep their ownership. The `systemctl` commands require systemd. If it is not already enabled, add this to `/etc/wsl.conf`:

```ini
[boot]
systemd=true
```

Save your work, run `wsl.exe --shutdown` from PowerShell, then reopen the distribution before running the installation commands. This stops all running WSL distributions. See [Microsoft’s systemd instructions](https://learn.microsoft.com/en-us/windows/wsl/systemd) for prerequisites and verification.

**Other platforms:**

- **Linux (other distros)**: [Docker Engine](https://docs.docker.com/engine/install/)
- **Windows**: [Docker Desktop for Windows](https://docs.docker.com/desktop/install/windows-install/)

> **Note:** When using `docker:dind` mode in moat.yaml, Moat automatically deploys a BuildKit sidecar for image builds. See the [docker dependency documentation](../reference/02-moat-yaml.md#docker) for details.

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
  proxy/           # Proxy daemon state (CA, logs, lock files)
  debug/           # Debug logs (visible with --verbose)
  config.yaml      # Global configuration (optional)
```

## Next steps

- [Quick start](./03-quick-start.md) -- Run your first agent
