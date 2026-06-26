# Changelog

Moat runs AI coding agents in isolated containers with credential injection, network policy enforcement, operation-level policy on tool calls and API traffic, and full observability. The core loop — declare what your agent needs in `moat.yaml`, run it in an isolated container, and audit everything it did — has stayed the same since v0.1. The runtime layer has broadened from Docker-only to Apple containers and Rancher Desktop, the proxy runs as a shared daemon that scopes credentials per run, and `gatekeeper` ships the credential-injecting proxy as a standalone binary for use outside the moat runtime.

Moat is pre-1.0. The CLI interface and `moat.yaml` schema may change between minor versions. Breaking changes are listed under **Breaking** headings below.

## Unreleased

Adds HTTP request-body inspection to Keep policies. File- and pack-based `network.keep_policy` rules can now match on the parsed JSON request body, so policies can enforce content-based rules (e.g. block requests whose body carries a secret) instead of host/method/path alone. Also adds native volume backing for `volumes:` (`type: volume`) — a Docker named volume on the engine's native filesystem, for container-only working directories that should bypass the host↔VM filesystem-sharing layer a bind mount crosses. The routing proxy now serves a discovery index at its bare hosts so you can browse an agent's endpoints instead of memorizing hostnames.

### Added

- **Routing discovery index** — the routing proxy now serves a browsable index at its bare hosts: `http://localhost:<port>` lists every running agent and its endpoints, and `http://<agent>.localhost:<port>` lists that agent's endpoints when it exposes more than one. Browsers get an HTML page; clients sending `Accept: application/json` get JSON. Previously the bare proxy root returned a `not a .localhost host` 400, and a bare multi-endpoint agent host routed to a nondeterministic first endpoint. Single-endpoint agents and fully-qualified endpoint hosts (`web.demo.localhost`) are unaffected. See `examples/multi-endpoint`. ([#407](https://github.com/majorcontext/moat/pull/407))
- **`moat open`** — open a running agent's endpoint in your browser: `moat open` for the discovery index, `moat open <agent>` for an agent, `moat open <agent> <endpoint>` for a specific endpoint. The agent defaults from the current directory's `moat.yaml` (or the sole running agent), and the URL is always printed so `--print` / headless / SSH use still works. `moat run`, `moat list`, and `moat proxy status` now advertise the endpoint index URL, and `moat run` prints the proxy's *actual* bound port (previously it always printed the configured default, which was wrong whenever the proxy fell back to another port). ([#407](https://github.com/majorcontext/moat/pull/407))
- **Native volume backing for `volumes:`** — a volume entry can set `type: volume` to use a Docker named volume (`moat_<agent>_<name>`) on the container engine's native filesystem instead of a host bind mount. This bypasses the host↔VM filesystem-sharing layer that bind mounts cross on VM-based runtimes (Docker Desktop, Rancher Desktop), which is faster for I/O- and metadata-heavy directories and avoids that layer's differing file-locking and memory-mapping semantics. The default is unchanged (`bind`), so existing configs are unaffected. Docker runtime only; the Apple container runtime rejects `type: volume`. See [Volumes](https://majorcontext.com/moat/reference/moat-yaml). ([#402](https://github.com/majorcontext/moat/pull/402))
- **Volume-mode workspaces** — opt into an isolated copy of the workspace in an ephemeral Docker named volume (`workspace.mode: volume` or `--workspace-mode volume`) for host protection and faster macOS I/O. Extract changes with `moat snapshot` / `moat snapshot restore --to`. Docker-only; git worktree and submodule checkouts are rejected with a clear error (use `workspace.mode: bind` for those). ([#404](https://github.com/majorcontext/moat/pull/404))
- **HTTP request-body policies** — file/pack `network.keep_policy` rules can match on the parsed JSON request body via `params.body` (e.g. `hasSecrets(params.body)` for a whole-body secret scan, or `params.body.field == 'x'` for an exact match). The proxy buffers and inspects the body only when a rule references it. Inspection applies to `application/json` over HTTPS; once any body rule exists, the `http` scope fail-closes on non-JSON, compressed, malformed, duplicate-key, or oversized bodies. A new `keep-body-policy` daemon capability gates the feature — an older proxy daemon fails fast with a `moat proxy restart` upgrade message rather than silently under-enforcing. Requires `keep` ≥ v0.6.0 and `gatekeeper` ≥ v0.13.0. See [HTTP request-body rules](https://majorcontext.com/moat/reference/moat-yaml) and `examples/policy-body`. ([#395](https://github.com/majorcontext/moat/pull/395))
- **`MOAT_KEYRING_BACKEND` environment variable** — set to `file` to skip the system keychain entirely and use file-based credential sources instead. This covers both the credential-store encryption key and the macOS Keychain lookup for Claude Code OAuth credentials. Useful on headless or locked-down macOS where touching the keychain pops a blocking GUI authorization prompt (including the "allow this app to modify the keychain item" dialog an unsigned binary triggers). The default (keychain-first, file fallback) is unchanged. See [Environment variables](https://majorcontext.com/moat/reference/environment). ([#406](https://github.com/majorcontext/moat/pull/406))

### Fixed

- Fix `moat logs -f` silently doing nothing — previously, follow mode printed a debug-log line ("not yet implemented", only visible with `--verbose`) and exited 0 as if it had streamed, so `-f` looked like it worked. moat now prints a visible notice that follow mode isn't supported yet and shows the current logs. ([#413](https://github.com/majorcontext/moat/pull/413))
- Fix non-deterministic Dockerfile generation causing spurious image rebuilds — previously, dependency `ENV` lines were emitted in random map order, so the generated Dockerfile changed between runs and missed Docker's layer cache. `ENV` keys are now sorted. ([#413](https://github.com/majorcontext/moat/pull/413))
- Fix a malformed `~/.moat/config.yaml` being silently ignored — previously, a YAML typo in the global config was swallowed and moat ran on built-in defaults with no signal, so settings like `proxy.port` appeared not to take effect for no visible reason. moat now warns and falls back to defaults instead of failing silently. ([#412](https://github.com/majorcontext/moat/pull/412))
- Fix potential `routes.json` corruption on a crash — previously, the hostname-routing table was written non-atomically (truncate-then-write), so a crash mid-write could leave a truncated file that every routing proxy then failed to parse. It is now written atomically via a temp file and rename. ([#412](https://github.com/majorcontext/moat/pull/412))
- Fix a data race on a run's provider metadata — previously, provider stopped-hooks wrote `ProviderMeta` while metadata-save read it concurrently (from the container-exit monitor and `moat stop`) without synchronization, which could corrupt the saved metadata or crash under the race detector. Both paths now hold the run's state lock. ([#412](https://github.com/majorcontext/moat/pull/412))
- Fix an unhelpful error when the routing proxy's port is in use — previously, starting an agent that exposes `ports:` while something already held the routing port (default 8080) failed with a raw `listen tcp 127.0.0.1:8080: bind: address already in use`. The routing port is deterministic by design (it is not auto-reassigned, so advertised endpoint URLs stay stable), so moat now reports which port is taken and how to change it: `MOAT_PROXY_PORT=<port>` or `proxy.port` in `~/.moat/config.yaml`. ([#409](https://github.com/majorcontext/moat/pull/409))
- Fix Apple container runs failing under the `container` CLI 1.0.0 — previously, moat parsed `container inspect` / `container list` output expecting `status` to be a string ("running") and network details in a top-level `networks` array. The 1.0.0 CLI nests these in a `status` object (`status.state`, `status.networks`), so every Apple container run failed with `parsing container info: json: cannot unmarshal object into Go struct field .status of type string`, and container IP lookup (host-traffic routing) found no address. moat now parses both the legacy and 1.0.0 schemas. Affects anyone on the Apple container runtime with `container` ≥ 1.0.0. ([#406](https://github.com/majorcontext/moat/pull/406))
- Fix `moat system containers`, `moat clean`, and `moat status` listing no Moat containers — the run-container filter expected an 8-character hex name, but run IDs have been `run_<12 hex>` (e.g. `run_d2d975055e71`) for some time, so the filter matched nothing and the commands reported "No moat containers found" even with containers present. The filter now matches the real run-ID format on both Docker and Apple runtimes. ([#406](https://github.com/majorcontext/moat/pull/406))
- Fix intermittent broken mouse-wheel scrolling in interactive sessions — previously, when a child agent (e.g. Claude Code) opened a fullscreen view (the alternate screen) and then closed it, moat's compositor swallowed the agent's mouse-disable sequence into its VT emulator instead of forwarding it to the terminal (the disable arrives just before the alt-screen-exit, while moat is still in compositor mode). The terminal was left reporting mouse events after the agent returned to its inline UI, so the scroll wheel sent mouse reports to the agent instead of scrolling the terminal's scrollback. moat now forwards mouse-tracking mode set/reset to the host while in compositor mode, and disables any it enabled on compositor exit, reset, or cleanup. The leak only triggered once a session opened a fullscreen view, which is why it was intermittent. ([#394](https://github.com/majorcontext/moat/pull/394))
- Fix bypass-permissions mode silently turning off in `moat claude` sessions — previously, moat enabled bypass mode only via the `--dangerously-skip-permissions` CLI flag. When Claude Code's "Try the new fullscreen renderer?" upsell appeared and was accepted (or when Claude silently auto-graduated a session to the fullscreen renderer), Claude re-exec'd its own process **without** the original CLI flags, so bypass mode reverted to default and tool calls started prompting again (e.g. a plain `cat`) mid-session. moat now (1) pins the renderer in the generated `settings.json` via `tui` — mirroring the host's choice and defaulting to the classic renderer — so the upsell and the flag-dropping re-exec never fire, and (2) persists `permissions.defaultMode: "bypassPermissions"` in `settings.json` when bypass is requested, so bypass survives any re-exec that does occur (e.g. a manual `/tui` switch). Use `--noyolo` to opt out of bypass as before. ([#400](https://github.com/majorcontext/moat/pull/400))

### Security

- Upgrade dependencies to clear 15 vulnerabilities that `govulncheck` reports moat actually calls — `containerd/v2` (→2.2.5), `golang.org/x/crypto` (→0.53.0), `in-toto-golang` (→0.11.0), `go-git/v5` (→5.17.1), `moby/buildkit` (→0.28.1), and `ulikunitz/xz` (→0.5.15). No user action required. Remaining `govulncheck` findings are in `docker/docker` (no upstream fix available yet) and the Go standard library (cleared by building with a newer Go toolchain). ([#416](https://github.com/majorcontext/moat/pull/416))
- Build with the Go 1.25.11 toolchain to clear ~16 Go standard-library vulnerabilities `govulncheck` reports moat calls (`crypto/x509`, `crypto/tls`, `html/template`, `net/*`, `archive/tar`, …), pinned via a `toolchain` directive. No user action required. With this and the dependency upgrades above, `govulncheck`'s reachable count drops from 36 to 5 — the rest being `docker/docker`, which has no upstream fix yet. ([#417](https://github.com/majorcontext/moat/pull/417))

## v0.6.1 — 2026-06-19

Security and ergonomics patch. Fixes a cross-profile credential leak in the shared proxy daemon — token refresh and the daemon-restart restore path now scope to the run's own credential profile instead of the daemon process's. Also adds inline grant prompting so `moat run`/`moat claude`/`moat codex` can grant missing credentials in place rather than failing and requiring a separate `moat grant` plus a re-run.

### Added

- **Inline grant prompting** — on an interactive terminal, `moat run`/`moat claude`/`moat codex` now detect missing credential grants before starting the container and offer to grant each one inline, instead of failing and requiring a separate `moat grant` plus a re-run. Non-interactive runs are unchanged; use `--no-prompt` (or `MOAT_NO_PROMPT=1`) to force the fail-fast behavior. ([#389](https://github.com/majorcontext/moat/pull/389))

### Security

- Fix cross-profile credential leak in the shared proxy daemon — previously, background OAuth token refresh opened the credential store via the daemon process's global active profile, which the daemon freezes at spawn (usually the default) and which does not reflect the profile a served run was created under. A run started with `--profile <name>` could therefore pick up the **default** profile's credential for the same grant on the first refresh tick — the refresh replaced the run's live OAuth token and overwrote the stored one — e.g. a `--profile vibrant` run suddenly using the default profile's Linear auth. The CLI now sends the run's profile to the daemon, and token refresh (plus the daemon-restart restore path, which had the same flaw) scopes the credential store to it. Affects anyone using `--profile`/`MOAT_PROFILE` with refreshable OAuth grants. **User action:** upgrade the CLI, then run `moat proxy restart` so the running daemon picks up the fix (an older daemon ignores the new profile field and keeps leaking). ([#392](https://github.com/majorcontext/moat/pull/392))

## v0.6.0 — 2026-06-17

Feature release centered on MCP ergonomics: well-known servers can be listed in `moat.yaml` by name alone, resolving URL, auth, and grant from a built-in catalog (with Langfuse regional and PostHog OAuth shortcuts), and MCP API-key grants adopt the `mcp:<name>` naming convention. Also adds `moat join` to launch a second agent in a running container, `moat proxy restart` for version-aware daemon replacement, and the `ministack` local-cloud service, plus fixes for GitHub HTTPS git auth, Apple async container teardown, and a remote-MCP relay 404 regression.

### Added

- **`moat proxy restart`** — stop the running proxy daemon and start a fresh one from the current binary, holding the daemon spawn lock across the entire stop and start so an active run's health monitor can't resurrect the old daemon in the gap. `EnsureRunning` now also adopts the caller's version automatically: when a healthy daemon's recorded commit and the caller's build commit are both known and differ, it restarts to the caller's version (dev builds reporting `none`/empty are left alone to avoid thrashing). This lets a newer CLI replace a stale daemon — e.g. one with an outdated MCP relay — without waiting for the idle timeout. ([#385](https://github.com/majorcontext/moat/pull/385))
- **Langfuse MCP shortcuts** — `langfuse-eu`, `langfuse-us`, `langfuse-jp`, and `langfuse-hipaa` are now recognized shorthand names in `mcp:`. Each resolves to the regional Langfuse MCP endpoint (`/api/public/mcp`) with Basic auth via a shared `mcp:langfuse` grant. Grant once with `moat grant mcp langfuse` (credential: `Basic <base64(pk:sk)>`), then list the regional name in `moat.yaml`. Self-hosted instances still use the full `url` + `auth` form. ([#384](https://github.com/majorcontext/moat/pull/384))
- **Declarative MCP shorthand** — list a well-known MCP server in `moat.yaml` by name alone (a bare `- linear` under `mcp:`), and Moat resolves the URL, auth header, and required grant from its built-in catalog. The map form (`- name: linear`) still works for attaching a policy or overriding fields, and unknown servers still take an explicit `url` + `auth`. `moat grant oauth` now prints this shorthand. ([#383](https://github.com/majorcontext/moat/pull/383))
- **`moat join`** — launch a second agent inside an already-running container,
  reusing its workspace, grants, and credentials without a new container. v1
  supports same-agent joins (e.g. joining claude into a `moat claude` run). The
  status footer shows the session role and joined-agent count.
  ([#379](https://github.com/majorcontext/moat/pull/379))
- **PostHog OAuth shortcut** — `moat grant oauth posthog` now auto-discovers OAuth endpoints from PostHog's MCP server (`https://mcp.posthog.com/mcp`) without needing `--url` or a config file, matching the other well-known services (asana, cloudflare, hubspot, linear, notion, stripe). ([#382](https://github.com/majorcontext/moat/pull/382))
- **Ministack service** — `ministack` is now available as a `service` dependency, running the LocalStack-compatible Ministack local cloud emulator as a sidecar container. Declare `ministack` under `dependencies` and configure it under `services.ministack` (e.g. `env`, `wait`). Readiness is probed against the container's `/_ministack/health` endpoint. ([#366](https://github.com/majorcontext/moat/pull/366))

### Changed

- MCP API-key grants now use the `mcp:<name>` (colon) naming convention, mirroring `oauth:<name>`. `moat grant mcp <name>` stores the credential as `mcp:<name>` and prints `grant: mcp:<name>` in its moat.yaml snippet, and the well-known `context7` catalog entry resolves to `mcp:context7`. The previous `mcp-<name>` (hyphen) form is still accepted everywhere — existing stored credentials and `moat.yaml` files keep working — but it is deprecated; prefer `mcp:<name>`. No migration is required. ([#386](https://github.com/majorcontext/moat/pull/386))

### Fixed

- Fix remote MCP servers failing to connect with `MOAT: MCP server '<token>' not configured` (HTTP 404) — previously, the relay URL written into the container's `.claude.json` addressed the proxy by the raw host-gateway IP (`GetHostAddress`), which is not in `NO_PROXY`. The MCP client's request was therefore routed *through* the proxy's CONNECT tunnel, so the proxy saw a proxied request and dispatched it to the relay handler that expects `/mcp/{name}` rather than the one that strips the per-run token from `/mcp/{token}/{name}` — parsing the auth token as the server name and returning 404. The relay URL now uses the synthetic `moat-proxy` host (the only host in `NO_PROXY`), so the client connects directly and the token is stripped correctly. This regressed in [#321](https://github.com/majorcontext/moat/pull/321) (v0.5.1), which moved the proxy to synthetic hostnames but left the MCP relay URL on `GetHostAddress`; the `ANTHROPIC_BASE_URL` relay was updated at the time but the MCP relay was missed. Affects all remote `mcp:` servers. ([#387](https://github.com/majorcontext/moat/pull/387))
- Fix the `moat.yaml` snippet printed by `moat grant oauth` being invalid — previously, the suggested `mcp:` block emitted `auth.grant` but omitted `auth.header`, so copying it verbatim into `moat.yaml` failed config validation with `'auth.header' is required when auth is specified`. The snippet now includes `header: Authorization`. ([#382](https://github.com/majorcontext/moat/pull/382))
- Surface an actionable hint when an injected GitHub credential is rejected — previously, a stale/expired stored token failed HTTPS git with only git's opaque `could not read Username for 'https://github.com'` (the proxy's `401` was hidden in the network log). After a **failed** run, moat now scans the network log and, when a `github`-granted request to `github.com`/`api.github.com` was rejected (401/403) and did not recover, prints `Run 'moat grant github' to refresh it.` ([#380](https://github.com/majorcontext/moat/pull/380))
- Fix `moat grant github` not taking effect on a running proxy daemon for env-sourced tokens — previously, the daemon's background token-refresh re-derived the GitHub token from its own process environment (`GITHUB_TOKEN`/`GH_TOKEN`), which is frozen when the daemon starts. After re-granting a fresh token, the daemon kept injecting (and wrote back to the credential store) the stale env value until `moat proxy stop`. Env-sourced GitHub tokens are now treated as static and no longer proactively refreshed, so a re-grant takes effect on the next run; a stale token surfaces as a 401 with a re-grant hint instead. `gh`-CLI-sourced tokens (`gh auth token`) still refresh, since that keyring is shared with `moat grant` and never diverges from it. ([#381](https://github.com/majorcontext/moat/pull/381))
- Fix HTTPS git fetch/push to `github.com` failing with only the `github` grant — previously, the proxy injected `Authorization: Bearer <token>` for `github.com`, but GitHub's git smart-HTTP endpoints reject Bearer with a 401 and require Basic auth, and git also aborted on the proxy's 407 CONNECT challenge because it doesn't send proxy credentials preemptively. The provider now injects `Basic x-access-token:<token>` for `github.com` (Bearer is still used for `api.github.com`), and containers set `git http.proxyAuthMethod=basic`, so `git clone`/`fetch`/`push` over HTTPS work with just `--grant github` — no SSH grant required. When both `github` and `ssh:github.com` are granted, git still routes over SSH as before. ([#376](https://github.com/majorcontext/moat/pull/376))
- Fix Claude Code reporting `Failed with non-blocking status code: /bin/sh: 1: python3: not found` — previously, the generated image for the Claude agent had no Python interpreter, so Claude Code's security-guidance feature (which shells out to `python3`) failed. Running the Claude agent now implicitly adds `python` to the container dependencies. Specify an explicit `python@<version>` in `dependencies` to override the version. ([#369](https://github.com/majorcontext/moat/issues/369))
- Fix service dependencies declared without an explicit version failing to start with `invalid reference format` — previously, a service listed as `name` rather than `name@version` (e.g. `ministack` instead of `ministack@latest`) left the image tag empty, so the reference was built as `repo:` and the container runtime rejected it. The service version now falls back to the registry default, matching the runtime and Dockerfile resolution paths. ([#366](https://github.com/majorcontext/moat/pull/366))
- Fix orphaned Apple container networks exhausting the IP pool — previously, because Apple's container CLI removes containers asynchronously, the `container network delete` issued during run teardown often ran before the run's containers had detached and failed with "active containers" / "network has a pending operation". `RemoveNetwork` made a single attempt and `ForceRemoveNetwork` re-issued the same command, so the network leaked; accumulated `moat-run_*` networks eventually exhausted Apple's `/24` IP pool and blocked new runs. Network deletion now retries with exponential backoff until the async detach completes. ([#367](https://github.com/majorcontext/moat/pull/367))
- Fix Apple service runs failing to start with "no network address found for container" on `container` CLI 0.12.x — previously, moat read the service container's ID from the **combined** stdout+stderr of `container run --detach`, but newer `container` versions write startup progress (`[1/6] Fetching image`, …) to stderr, so the captured "ID" was polluted with progress text and the follow-up `inspect` matched no container. moat now reads the ID from stdout only, and additionally polls for the address in case it is assigned shortly after start. ([#367](https://github.com/majorcontext/moat/pull/367))
- Fix a failing `pre_run` hook looking like the container failed to start — previously, a non-zero exit from the `hooks.pre_run` command aborted the entrypoint (under `set -e`) with a bare exit code and no indication the hook was the cause, so the real error was easy to miss in the container output. moat now reports `pre_run hook failed (exit code N)` with the offending command and how to fix it, and exits with the hook's status. ([#377](https://github.com/majorcontext/moat/pull/377))

## v0.5.4 — 2026-06-01

Patch release with three Claude Code fixes: an Ink-based TUI freeze on first paint, host/container session-directory divergence on workspaces with `.` or `_` in the path, and marketplace plugin hook scripts losing the executable bit.

### Fixed

- Fix Claude Code (and other Ink-based TUIs) freezing on the first paint inside the moat container — previously, when the child emitted any terminal-capability query that required a reply (CSI c Primary Device Attributes, CSI 6n cursor position, in-band resize, color queries), moat's VT compositor blocked forever on the emulator's internal reply pipe with no drain, holding `tui.Writer.mu` and starving the render goroutine. Input still reached the child, but no further output reached the host terminal, so the session appeared frozen and Ctrl+C wasn't visibly acknowledged even though it terminated the child. Surfaced 100% of the time with Claude Code 2.1.150+, which queries Device Attributes at startup. Moat now drains the emulator's reply pipe and routes replies back into the child's input stream via the existing injectable-reader chain. ([#362](https://github.com/majorcontext/moat/pull/362))
- Fix Claude Code sessions inside the container writing to a different `~/.claude/projects/` directory than host sessions — previously, moat slugified the workspace path by replacing only `/` with `-`, while Claude Code replaces every non-alphanumeric character. For any workspace path containing a `.`, `_`, or space (e.g. a macOS username like `user.name`), moat and the host CLI computed different project-directory names, so container and host sessions silently forked the project's session history and memory store. The slug now matches Claude Code's rule exactly (verified against the claude binary). Note: already-forked directories are not migrated automatically. ([#364](https://github.com/majorcontext/moat/pull/364))
- Fix marketplace plugin hook scripts failing with `Permission denied` inside the container — previously, `CollectMarketplaceTar` wrote every tar header with a hardcoded mode of `0644`, so executable scripts shipped by marketplace plugins (e.g. `bin/aw-hook`, `scripts/on-prompt-submit.sh`) extracted as non-executable and Claude Code's `UserPromptSubmit` hook failed at runtime. The tar header now derives `Mode` from the source file's permissions, preserving the executable bit. ([#363](https://github.com/majorcontext/moat/pull/363))

## v0.5.3 — 2026-05-25

Patch release centered on Claude Code authentication inside containers — subscription detection, `setup-token` capture, and version pinning — plus a non-root tmpfs permissions fix.

### Fixed

- Fix `claude-code@<version>` pinning being ignored — previously, specifying a version such as `claude-code@2.1.139` in `dependencies` still installed the latest release, because the install command dropped the version argument. The version is now passed to the official installer. ([#357](https://github.com/majorcontext/moat/pull/357))
- Fix Claude Code showing "not logged in" / "API Usage Billing" inside containers — previously, the generated `~/.claude/.credentials.json` had `null` scopes and no `subscriptionType`, which Claude Code treats as an unauthenticated session. Moat now writes the standard OAuth scopes and a `subscriptionType` (default `max`, overridable via `claude.subscription_type`; the new `claude.rate_limit_tier` is also supported). Grants created by importing existing credentials use the real plan. The real plan is still enforced server-side via the proxy-injected token. Surfaced by v0.5.2 dropping the `CLAUDE_CODE_OAUTH_TOKEN` placeholder env var that had masked the incomplete file. ([#358](https://github.com/majorcontext/moat/pull/358))
- Fix `moat grant claude` failing to capture a `setup-token` — recent Claude CLI versions render `setup-token` as a TUI, so moat's output scraping always failed ("could not find OAuth token") and fell back to a manual paste anyway. Moat now runs `setup-token` attached to the terminal (no scraping) and reads the pasted token, reassembling it when a narrow terminal soft-wrapped it across lines. ([#353](https://github.com/majorcontext/moat/pull/353))
- Fix `EACCES` writing to tmpfs mounts as the non-root container user — tmpfs (used by `mounts.exclude` paths) was created mode `755` owned by root, so `moatuser` could not write to it, and `noexec` blocked native binaries in excluded `node_modules`. tmpfs is now mounted mode `1777` with `exec` on Docker, and with an explicit `mode=1777` on Apple containers. ([#355](https://github.com/majorcontext/moat/pull/355))

## v0.5.2 — 2026-05-18

Patch release with Claude Code credential fixes (OAuth placeholder shape and credential expiry) plus marketplace and TUI rendering fixes.

### Fixed

- Fix Claude Code skipping OAuth-only code paths in containers — the container credentials placeholder did not look like an OAuth token and was also set in `CLAUDE_CODE_OAUTH_TOKEN`, so Claude Code could skip paths that determine account capabilities (e.g. 1M-context access). Moat now writes an `sk-ant-oat01-*`-shaped placeholder to `.credentials.json` and no longer sets the env var; the real token is still injected by the proxy. ([#351](https://github.com/majorcontext/moat/pull/351))
- Fix Claude Code treating injected container credentials as expired — `setup-token` grants carry no expiry, so the zero-value timestamp serialized to year 0001 and showed the session as logged out. A far-future expiry is now written when the grant has none. ([#352](https://github.com/majorcontext/moat/pull/352))
- Fix `claude.marketplaces` entries written as `{source: github, repo: ...}` being normalized to a `git`/`url` shape that broke plugin allowlist matching in the container. The original source shape is now preserved end to end. ([#345](https://github.com/majorcontext/moat/pull/345))
- Fix the moat footer scrolling into scrollback with Ink-based TUIs (Claude Code) — the child emits `ESC[r` on startup, resetting moat's scroll region, so newlines on the bottom row pushed the footer into scrollback and left a trail of copies. Moat is now the sole authority over the scroll region and reserves the footer row at the TTY level. ([#349](https://github.com/majorcontext/moat/pull/349))

## v0.5.1 — 2026-04-28

Patch release with one security fix (IPv6 egress firewall) and a batch of run-lifecycle and proxy fixes. Adds `MOAT_HOME` for relocating moat state, a multi-runtime manager so Docker and Apple containers can coexist in one install, TUI debug shortcuts, and Python 3.13/3.14 support. Gatekeeper is extracted to its own repository.

### Added

- **TUI debug shortcuts** — `Ctrl-/ d` dumps a snapshot of recent terminal I/O to `~/.moat/runs/<id>/tui-debug-<unix-ts>.json` for offline analysis or feeding into a Claude session; `Ctrl-/ r` issues a soft terminal reset and nudges the child to redraw, recovering wedged sessions. The dump uses the same JSON format as `--tty-trace` and works with `moat tty-trace analyze`. Ring buffer size is 8 MB by default, tunable via `MOAT_TTY_RING_BYTES`. ([#343](https://github.com/majorcontext/moat/pull/343))
- **`MOAT_HOME`** — single env var to relocate the moat configuration directory (default `~/.moat`); when set, it replaces `~/.moat` as the root for runs, credentials, daemon socket/lock, keyring key file, per-run SSH sockets, and routing proxy state. Third-party state (`~/.claude`, `~/.config/gh`, etc.) still resolves against `$HOME`. ([#323](https://github.com/majorcontext/moat/pull/323))
- **Multi-runtime manager** — Docker and Apple containers can now coexist in a single install. New runs use the default runtime; operations on existing runs (`status`, `clean`, `list`, `system images`, `system containers`) resolve the correct runtime automatically. Adds a `RUNTIME` column to the relevant tables and a `runtime` field to `--json` output. ([#311](https://github.com/majorcontext/moat/pull/311))
- Python 3.13 and 3.14 added to supported versions; bundled `uv` updated from 0.5.14 to 0.11.6 to support modern `pyproject.toml` features ([#316](https://github.com/majorcontext/moat/pull/316))

### Changed

- **Gatekeeper extracted to standalone module** — `internal/proxy/` and `cmd/gatekeeper/` have moved to [github.com/majorcontext/gatekeeper](https://github.com/majorcontext/gatekeeper). The `ghcr.io/majorcontext/moat-gatekeeper` image is no longer published from this repository. ([#333](https://github.com/majorcontext/moat/pull/333))

### Security

- **IPv6 egress firewall** — containers on dual-stack hosts could bypass `network.policy: strict` by using IPv6 addresses (e.g. AAAA DNS records). The firewall now installs ip6tables rules mirroring the existing iptables rules. No user action required; the fix applies automatically on upgrade. If ip6tables is unavailable in the container image, a diagnostic warning is logged. ([#324](https://github.com/majorcontext/moat/pull/324))

### Fixed

- Fix `network.host` bypass via raw loopback addresses — previously, containers running under Docker host-network mode could bypass `network.host` enforcement by connecting to `localhost` or `127.0.0.1` directly, since those addresses were in `NO_PROXY` and skipped the proxy entirely. Loopback addresses are no longer excluded from proxy routing. ([#327](https://github.com/majorcontext/moat/pull/327))
- Fix ip6tables hanging indefinitely on hosts without the `ip6_tables` kernel module — previously, `ip6tables -w` (wait forever) blocked the firewall setup, hanging the container start and E2E tests on CI. Now uses a 5-second timeout and treats ip6tables failure as non-fatal with partial-rule cleanup. ([#325](https://github.com/majorcontext/moat/pull/325))
- Fix `moat status` and `moat list` corrupting persisted run state — previously, reconciliation could overwrite metadata for active runs (e.g. mark them stopped) when a status check ran with the wrong runtime, causing active runs to disappear from `moat status`. Read-only commands no longer write metadata, and reconciliation skips cross-runtime container checks. ([#309](https://github.com/majorcontext/moat/pull/309))
- Fix host unreachable from custom networks on Docker Desktop — previously, runs on user-defined Docker networks could not reach the host, breaking proxy access and producing `Unable to connect to API (ConnectionRefused)` errors. ([#337](https://github.com/majorcontext/moat/pull/337))
- Fix MCP servers with `auth.grant` failing to load credentials — previously, grant names listed under `mcp[].auth.grant` were validated but never appended to the credential-loading list, so auth headers were never injected. The grant list is now merged before credential processing, and `FileStore.Get()` is hardened against path traversal via crafted provider names. ([#338](https://github.com/majorcontext/moat/pull/338))
- Fix multi-line YAML block scalars in `post_build` and `post_build_root` hooks producing invalid Dockerfiles — previously, raw newlines from `|` block scalars were interpolated directly into `RUN` commands. Lines are now joined with `&&`, with trailing shell operators (`&&`, `;`, `\`) stripped before joining. ([#339](https://github.com/majorcontext/moat/pull/339))
- Fix E2E service tests intermittently hanging due to orphan `moat-*` Docker networks accumulating without cleanup. `Close()` is now bounded so a stuck monitor goroutine can't deadlock teardown, and orphan networks are reaped on startup. ([#342](https://github.com/majorcontext/moat/pull/342))
- Fix capability-mismatch error message pointing at a nonexistent command — error paths suggested `moat proxy restart`, but the proxy command only registers `start`, `stop`, and `status`. Messages now point at `moat proxy stop` followed by re-running `moat run`. ([#336](https://github.com/majorcontext/moat/pull/336))

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
