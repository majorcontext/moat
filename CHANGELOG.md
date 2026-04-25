# Changelog

Moat runs AI coding agents in isolated containers with credential injection, network policy enforcement, operation-level policy on tool calls and API traffic, and full observability. The core loop — declare what your agent needs in `moat.yaml`, run it in an isolated container, and audit everything it did — has stayed the same since v0.1. The runtime layer has broadened from Docker-only to Apple containers and Rancher Desktop, the proxy runs as a shared daemon that scopes credentials per run, and `gatekeeper` ships the credential-injecting proxy as a standalone binary for use outside the moat runtime.

Moat is pre-1.0. The CLI interface and `moat.yaml` schema may change between minor versions. Breaking changes are listed under **Breaking** headings below.

## Unreleased

### Added

- **TUI debug shortcuts** — `Ctrl-/ d` dumps a snapshot of recent terminal I/O to `~/.moat/runs/<id>/tui-debug-<unix-ts>.json` for offline analysis or feeding into a Claude session; `Ctrl-/ r` issues a soft terminal reset and nudges the child to redraw, recovering wedged sessions. The dump uses the same JSON format as `--tty-trace` and works with `moat tty-trace analyze`. Ring buffer size is 8 MB by default, tunable via `MOAT_TTY_RING_BYTES`. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))

### Changed

- **Gatekeeper extracted to standalone module** — `internal/proxy/` and `cmd/gatekeeper/` have moved to [github.com/majorcontext/gatekeeper](https://github.com/majorcontext/gatekeeper). The `ghcr.io/majorcontext/moat-gatekeeper` image is no longer published from this repository. ([#333](https://github.com/majorcontext/moat/pull/333))

### Security

- **IPv6 egress firewall** — containers on dual-stack hosts could bypass `network.policy: strict` by using IPv6 addresses (e.g. AAAA DNS records). The firewall now installs ip6tables rules mirroring the existing iptables rules. No user action required; the fix applies automatically on upgrade. If ip6tables is unavailable in the container image, a diagnostic warning is logged. ([#324](https://github.com/majorcontext/moat/pull/324))

### Fixed

- Fix `network.host` bypass via raw loopback addresses — previously, containers running under Docker host-network mode could bypass `network.host` enforcement by connecting to `localhost` or `127.0.0.1` directly, since those addresses were in `NO_PROXY` and skipped the proxy entirely. Loopback addresses are no longer excluded from proxy routing. ([#327](https://github.com/majorcontext/moat/pull/327))
- Fix ip6tables hanging indefinitely on hosts without the `ip6_tables` kernel module — previously, `ip6tables -w` (wait forever) blocked the firewall setup, hanging the container start and E2E tests on CI. Now uses a 5-second timeout and treats ip6tables failure as non-fatal with partial-rule cleanup. ([#325](https://github.com/majorcontext/moat/pull/325))

## v0.5.0 — 2026-04-07

v0.5 hardens network isolation and introduces operation-level policy enforcement on MCP tool calls and HTTP traffic. Host traffic is now blocked by default in every network policy mode — including `permissive` — and must be opted into per-port with `network.host`. Keep policy integration adds allow/deny/redact rules for MCP tool calls and REST API requests, with starter packs for common services and an LLM response policy that evaluates `tool_use` blocks before forwarding to the container. The credential-injecting proxy is now also available as a standalone `gatekeeper` binary that runs without the moat runtime. Other additions include multi-credential per host, custom base images, OAuth grants for MCP servers, sandbox-local MCP servers, and global mounts in `~/.moat/config.yaml`.

### Breaking

- **Host traffic blocked by default** — containers can no longer reach services on the host machine without explicit configuration. This affects all network policy modes, including `permissive` ([#303](https://github.com/majorcontext/moat/pull/303)). Add a `network.host` list to restore access:

  ```yaml
  network:
    host:
      - 11434   # Ollama
      - 5432    # local Postgres
  ```

### Added

- **Keep policy integration** — enforce operation-level allow/deny/redact on MCP tool calls and REST API requests via `mcp[].policy` and `network.keep_policy`; includes an LLM response policy that evaluates `tool_use` blocks in Anthropic API responses through `claude.llm-gateway` before forwarding to the container, plus starter packs like `linear-readonly` for quick MCP server lockdown ([#288](https://github.com/majorcontext/moat/pull/288))
- **`gatekeeper` standalone proxy** — the credential-injecting proxy is now packaged as a standalone binary that runs without the moat runtime ([#299](https://github.com/majorcontext/moat/pull/299))
- **`network.host`** — list TCP ports on the host machine that the container may access; all host traffic is blocked by default even in permissive mode ([#303](https://github.com/majorcontext/moat/pull/303))
- **`MOAT_HOST_GATEWAY`** env var — set automatically in every container; resolves to the host gateway address across all runtimes (Docker, Apple containers, Rancher Desktop). Always use `$MOAT_HOST_GATEWAY` rather than hardcoding addresses ([#303](https://github.com/majorcontext/moat/pull/303))
- **Multi-credential per host** — multiple grants (e.g., `claude` and `anthropic`) can now target the same host with different headers; clients that send placeholder headers choose which credential to use, otherwise the proxy auto-injects with `anthropic` preferred over `claude` ([#295](https://github.com/majorcontext/moat/pull/295))
- **`moat grant show`** — inspect stored grants and the credentials they hold ([#297](https://github.com/majorcontext/moat/pull/297))
- **Custom base image** — declare a prebuilt image as the base for the generated Dockerfile instead of inferring one from `dependencies` ([#292](https://github.com/majorcontext/moat/pull/292))
- **OAuth grant provider for MCP servers** — authenticate to remote MCP servers via OAuth flows handled on the host ([#278](https://github.com/majorcontext/moat/pull/278))
- **Sandbox-local MCP servers** — run MCP servers as processes inside the container under `claude.mcp` / `codex.mcp` ([#184](https://github.com/majorcontext/moat/pull/184))
- **Settings passthrough** — `~/.moat/claude/settings.json` on the host is forwarded into the container's Claude configuration ([#281](https://github.com/majorcontext/moat/pull/281))
- **Global mounts** in `~/.moat/config.yaml` — declare mounts that apply to every run without editing per-project `moat.yaml` ([#282](https://github.com/majorcontext/moat/pull/282))
- **Host-side marketplace cloning** — clone private Claude plugin repos on the host (with SSH fallback) before injecting them into the container ([#240](https://github.com/majorcontext/moat/pull/240))
- **Protobuf compiler and plugin dependencies** — `protoc` and language plugins are declarable in `dependencies` ([#276](https://github.com/majorcontext/moat/pull/276))
- **Managed settings cache for Claude** — copy `~/.claude/remote-settings.json` into the container so Claude Code does not prompt for managed settings approval on every startup ([#306](https://github.com/majorcontext/moat/pull/306))
- **AWS_PROFILE propagation** — the AWS profile selected at grant time is forwarded to the proxy daemon and used for credential resolution ([#296](https://github.com/majorcontext/moat/pull/296))

### Changed

- Default Node.js version updated from 20 to 22 (current LTS) for all providers (Claude, Codex, Gemini) and the TypeScript language server ([#304](https://github.com/majorcontext/moat/pull/304))
- Homebrew tap moved to its new location ([#279](https://github.com/majorcontext/moat/pull/279))

### Fixed

- Fix `network.host` bypass — previously, `MOAT_HOST_GATEWAY` was included in `NO_PROXY`, causing host traffic to bypass the proxy entirely and skip network policy enforcement. Host traffic now flows through the proxy using synthetic hostnames (`moat-host` for host services, `moat-proxy` for proxy access). `MOAT_HOST_GATEWAY` now contains the synthetic hostname `moat-host` instead of a runtime-specific IP; update any container scripts that passed it to `ping`, `nslookup`, or `iptables` expecting an address literal. HTTP(S) clients continue to work without changes. Run `moat proxy restart` after upgrading to pick up this fix. User-supplied `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` / `ALL_PROXY` / `CURL_ALL_PROXY` / `MOAT_HOST_GATEWAY` / `MOAT_EXTRA_HOSTS` values in `moat.yaml env:` or `-e` flags are now filtered with a warning so they cannot re-open the bypass. On Apple containers with strict policy, moat-init.sh fails closed if it cannot write synthetic hostnames to `/etc/hosts` — rebuild non-root custom base images so moat-init runs as root, or grant `CAP_DAC_OVERRIDE`.
- Install the Rust toolchain to a shared location (`/usr/local/cargo`) so non-root container users can use `cargo` and `rustc` — previously, rustup installed under `/root` and was unreadable to the default container user ([#305](https://github.com/majorcontext/moat/pull/305))
- Add Anthropic and Gemini token placeholders so clients sending placeholder `Authorization` headers select the correct credential ([#293](https://github.com/majorcontext/moat/pull/293))
- Skip plugin install when the `claude` CLI is absent — previously, image builds failed in containers that didn't include Claude Code ([#291](https://github.com/majorcontext/moat/pull/291))
- Surface actionable errors from the AWS provider and rename legacy `agentops` references ([#290](https://github.com/majorcontext/moat/pull/290))
- Use `ConfigureProxy` for token refresh propagation in the daemon — previously, refreshed tokens weren't always picked up by in-flight requests ([#289](https://github.com/majorcontext/moat/pull/289))
- SSH fallback for Claude marketplace clones with clearer error output — previously, HTTPS-only clones failed silently for private repos ([#285](https://github.com/majorcontext/moat/pull/285))
- Correct SSH `known_hosts` ordering and surface clone fallback visibility — previously, marketplace clones fell back to alternate transports without logging it, making failures hard to diagnose, and `known_hosts` entries were written in an order that could cause SSH host key verification to fail ([#283](https://github.com/majorcontext/moat/pull/283))
- Disable interactive git credential prompts during host-side marketplace clones — previously, missing credentials caused the host clone step to hang ([#280](https://github.com/majorcontext/moat/pull/280))

## v0.4.0 — 2026-03-19

v0.4 introduces HTTP-level request rules for the network firewall, an `env://` resolver for forwarding host environment variables into containers, and `moat exec` for running commands in existing containers. New credential providers cover Meta Graph API and Graphite CLI, and Ollama is now a declarable service dependency. Rancher Desktop is now a supported runtime alongside Docker and Apple containers. The proxy daemon fixes a credential scoping race and now separates credential traffic from routing traffic on distinct ports.

### Added

- **HTTP request rules for the network firewall** — enforce path-level policies on outbound HTTP traffic ([#230](https://github.com/majorcontext/moat/pull/230))
- **`moat exec` command** — run commands in an existing container ([#232](https://github.com/majorcontext/moat/pull/232))
- **`env://` secret resolver** — forward host environment variables into containers as secrets ([#236](https://github.com/majorcontext/moat/pull/236))
- **Host clipboard bridging** — copy/paste between host and container during interactive sessions ([#219](https://github.com/majorcontext/moat/pull/219))
- **Rancher Desktop** container runtime support ([#239](https://github.com/majorcontext/moat/pull/239))
- **Ollama service dependency** — declare Ollama as a service in `moat.yaml` with provisioning and cache support ([#238](https://github.com/majorcontext/moat/pull/238))
- **Meta Graph API** credential provider ([#226](https://github.com/majorcontext/moat/pull/226))
- **Graphite CLI** credential provider ([#218](https://github.com/majorcontext/moat/pull/218))
- **Host-local MCP servers** — run MCP servers as processes on the host and relay them into the container ([#183](https://github.com/majorcontext/moat/pull/183))
- **`moat init`** command — scaffold a `moat.yaml` and inject runtime context into the container ([#207](https://github.com/majorcontext/moat/pull/207))
- Expose per-path network rules to agents via the `MOAT_CONTEXT` runtime context ([#266](https://github.com/majorcontext/moat/pull/266))
- Include documentation URLs in the `MOAT_CONTEXT` runtime context ([#228](https://github.com/majorcontext/moat/pull/228))
- `ulimits` field in `moat.yaml` for container resource limits ([#211](https://github.com/majorcontext/moat/pull/211))
- `tmpfs`-backed excludable directories for filesystem mounts ([#233](https://github.com/majorcontext/moat/pull/233))
- Node 24 runtime support ([#269](https://github.com/majorcontext/moat/pull/269))

### Security

- Separate credential proxy port from routing proxy — previously, credential injection and general traffic routing shared a port, which widened the proxy's attack surface. No action required; upgrading resolves this ([#213](https://github.com/majorcontext/moat/pull/213))
- Complete run setup before registry insertion — previously, a race window during run startup could cause the proxy to serve requests before credentials were fully configured, resulting in failed or unauthenticated requests. No action required; upgrading resolves this ([#250](https://github.com/majorcontext/moat/pull/250))

### Fixed

- Auto-clean stale routes on agent name collision — previously, reusing a run name while a stale route existed caused traffic to route to the wrong container ([#237](https://github.com/majorcontext/moat/pull/237))
- Configure git to use SSH for GitHub when the SSH grant is active — previously, git defaulted to HTTPS even with an SSH grant ([#252](https://github.com/majorcontext/moat/pull/252))
- Generate legacy-compatible Dockerfiles when BuildKit is unavailable — previously, image builds failed on Docker installations without BuildKit ([#235](https://github.com/majorcontext/moat/pull/235))
- Rewrite proxy host for custom network gateways — previously, non-default Docker network configurations caused the container to fail to reach the proxy ([#217](https://github.com/majorcontext/moat/pull/217))
- Resolve default runtime versions to prevent broken tarball URLs — previously, omitting a version in `dependencies` could produce a 404 during image build ([#227](https://github.com/majorcontext/moat/pull/227))
- Suppress interactive prompts for corepack and Playwright — previously, these tools blocked image builds waiting for TTY input ([#223](https://github.com/majorcontext/moat/pull/223))
- Fail the Docker build on plugin and marketplace install errors — previously, install failures were silently ignored ([#260](https://github.com/majorcontext/moat/pull/260))
- Add Claude CLI path so plugins can be pre-installed into container images — previously, the missing path caused plugin installs to fail silently ([#261](https://github.com/majorcontext/moat/pull/261))

## v0.3.0 — 2026-03-04

v0.3 replaces the per-run proxy with a shared daemon process that outlives the CLI and scopes credentials by run. The configuration file is renamed from `agent.yaml` to `moat.yaml`, and the attach/detach execution model is removed in favor of a single run lifecycle.

### Breaking

- **Rename `agent.yaml` to `moat.yaml`** — rename the file in your project root; the old filename is no longer recognized ([#204](https://github.com/majorcontext/moat/pull/204))
- **Remove attach/detach execution model** — runs now have a single lifecycle; `moat attach` and `moat detach` no longer exist ([#196](https://github.com/majorcontext/moat/pull/196))

### Added

- **Shared proxy daemon** with per-run credential scoping — a single daemon serves all active runs ([#193](https://github.com/majorcontext/moat/pull/193))
- **Credential profiles** via `--profile` and `MOAT_PROFILE` ([#181](https://github.com/majorcontext/moat/pull/181))
- **Config-driven credential providers** via YAML ([#168](https://github.com/majorcontext/moat/pull/168))
- **`claude --resume`** — resume a previous Claude Code run by ID ([#187](https://github.com/majorcontext/moat/pull/187))
- **Prepackaged language servers** in `moat.yaml` ([#185](https://github.com/majorcontext/moat/pull/185))
- **Initial prompt passthrough** via `--` args ([#156](https://github.com/majorcontext/moat/pull/156))
- **Manual snapshots** during interactive sessions via Ctrl+/ then s ([#198](https://github.com/majorcontext/moat/pull/198))
- Import host git identity (name and email) into containers ([#173](https://github.com/majorcontext/moat/pull/173))
- Resolve runs by name or ID prefix ([#162](https://github.com/majorcontext/moat/pull/162))
- `claude.base_url` field for host-side LLM proxies ([#191](https://github.com/majorcontext/moat/pull/191))
- `clean` and `list` commands are now worktree-aware ([#182](https://github.com/majorcontext/moat/pull/182))

### Changed

- Increase Apple container memory default to 8 GB ([#203](https://github.com/majorcontext/moat/pull/203))
- Decouple Anthropic OAuth tokens from API keys per updated Anthropic ToS — run `moat grant claude` again to re-authenticate ([#190](https://github.com/majorcontext/moat/pull/190))

### Fixed

- Make proxy liveness checks resilient to transient failures — previously, a brief network hiccup during startup caused the CLI to report the proxy as down and abort the run ([#199](https://github.com/majorcontext/moat/pull/199))
- Add timeouts to container operations and parallelize run loading — previously, a hung container blocked all CLI commands ([#192](https://github.com/majorcontext/moat/pull/192))
- Improve Apple container runtime detection — previously, detection failed silently on some macOS configurations ([#176](https://github.com/majorcontext/moat/pull/176))
- Clean up stale routes for stopped containers on startup ([#172](https://github.com/majorcontext/moat/pull/172))
- Fail early when declared grants are unavailable — previously, runs started and failed mid-execution ([#160](https://github.com/majorcontext/moat/pull/160))
- Mount main `.git` directory for worktree workspaces — previously, git operations inside the container failed for worktree-based projects ([#157](https://github.com/majorcontext/moat/pull/157))

## v0.2.0 — 2026-02-10

v0.2 removes the sessions abstraction in favor of runs as the single organizational unit, and adds worktree and npm registry support.

### Breaking

- **Remove sessions feature** — runs are the single source of truth; use `moat list` to see runs ([#151](https://github.com/majorcontext/moat/pull/151))

### Added

- **Git worktree utilities** for managing runs inside worktrees ([#153](https://github.com/majorcontext/moat/pull/153))
- **npm registry** credential provider ([#152](https://github.com/majorcontext/moat/pull/152))

### Fixed

- Connect to Docker Desktop's embedded BuildKit — previously, builds failed on Docker Desktop when BuildKit was only available via DialHijack ([#150](https://github.com/majorcontext/moat/pull/150))
- Prevent Docker network leaks during cleanup — previously, orphaned Docker networks accumulated after failed runs ([#149](https://github.com/majorcontext/moat/pull/149))

## v0.1.0 — 2026-02-08

First public release. Supports Claude Code and Gemini agents on Docker (Linux, macOS) and Apple containers (macOS 26+ with Apple Silicon).

### Added

- **Container isolation** — each run executes in its own container with workspace mounting
- **Credential injection** via TLS-intercepting proxy — tokens are injected at the network layer, never exposed in the container environment ([#128](https://github.com/majorcontext/moat/pull/128))
- **GitHub device flow authentication** and encrypted credential store ([#125](https://github.com/majorcontext/moat/pull/125))
- **Observability** — stdout/stderr logging with timestamps, network request tracing, and trace spans ([#107](https://github.com/majorcontext/moat/pull/107))
- **`agent.yaml` configuration** — declarative runtime, dependency, and grant definitions (renamed to `moat.yaml` in v0.3)
- **Automatic image selection** based on declared runtime (Node, Python, Go) ([#102](https://github.com/majorcontext/moat/pull/102))
- **Service dependencies** — sidecar containers (e.g., Postgres) with readiness checks ([#102](https://github.com/majorcontext/moat/pull/102))
- **Apple containers runtime** on macOS 26+ with Apple Silicon ([#102](https://github.com/majorcontext/moat/pull/102))
- **Gemini agent** support ([#118](https://github.com/majorcontext/moat/pull/118))
- **`moat doctor`** diagnostic command ([#124](https://github.com/majorcontext/moat/pull/124))
- **Lifecycle hooks** in `agent.yaml` ([#145](https://github.com/majorcontext/moat/pull/145))
- **TUI** — interactive terminal with footer controls and trace overlay ([#108](https://github.com/majorcontext/moat/pull/108))
- **Audit logging** with cryptographic hash chaining ([#114](https://github.com/majorcontext/moat/pull/114))
- Homebrew tap and GoReleaser for automated releases ([#142](https://github.com/majorcontext/moat/pull/142), [#146](https://github.com/majorcontext/moat/pull/146))
