# Pi Config Bridge (Global Settings + Build-Time Packages) — Design

**Date:** 2026-07-03
**Status:** Approved (approach); moving to implementation
**Scope:** Two coupled features of the Moat↔Pi configuration bridge:
- **#1 Moat-owned global settings** — bake a `~/.pi/agent/settings.json` with safe security/telemetry defaults, plus a permissive-network warning.
- **#2 Build-time packages** — `pi.packages:` in moat.yaml, `pi install`-ed into the image at build time (reproducible layer).

Both are unified at **build time** because `pi install` and the security settings write the *same* file (`~/.pi/agent/settings.json`); one build step owns it.

## Problem

Pi is highly configurable (settings, extensions, skills, packages, providers). Moat currently bridges only `pi.provider`/`pi.model` (via CLI flags). Two gaps:

1. **No Moat-owned safety posture.** Pi defaults to loading project-local `.pi/` config/extensions (arbitrary code) and to telemetry-on. In a sandbox that opens an arbitrary repo, Moat should set a safe baseline the repo cannot subvert.
2. **No reproducible way to ship Pi packages.** A project that needs Pi extensions/skills has no declarative, image-baked way to get them — the essence of Pi's configurability.

## Grounding (verified)

- **Pi honors `HTTP_PROXY`**, which Moat already injects into every container and marks moat-owned/unoverridable (`buildProxyEnv`, `isMoatOwnedProxyVar`). → a `httpProxy` setting is **redundant**; omit it.
- **`httpProxy` and `defaultProjectTrust` are global-only** settings — a repo's `.pi/settings.json` cannot override them. → Moat's baked global settings are unsubvertable by a workspace.
- **`baseUrl`/`streamSimple` can redirect Pi's LLM traffic to any host** — Pi config is *not* an egress boundary; only Moat's network policy is. → surface a warning under permissive policy.
- **`pi install <source>` (spike, 2026-07-03)** runs non-interactively as a non-root user, writes `~/.pi/agent/settings.json` `packages` array + on-disk `~/.pi/agent/npm|git/…`. Local-path sources record a *relative* path (breaks at runtime); remote sources (`npm:`/`git:`/`https:`/`ssh:`) are stable.
- Post-install `settings.json` contains only `packages`; a shallow merge adds security keys cleanly.

## Decisions (locked)

| Decision | Choice |
| --- | --- |
| Where config is owned | **Build-time bake** into the image (not runtime staging) — one owner of `~/.pi/agent/settings.json`, no `moat-init.sh` change |
| `defaultProjectTrust` default | **`never`** — a repo's `.pi/` config/extensions do not auto-load (safe against untrusted repos). `AGENTS.md`/`CLAUDE.md` still load (ungated by Pi design) |
| Telemetry | `enableInstallTelemetry: false`, `enableAnalytics: false` |
| Startup noise | `quietStartup: true` |
| `httpProxy` in settings | **Omitted** — already provided by moat-owned `HTTP_PROXY` env |
| Network coupling | **Warn** on `moat pi` under a permissive policy; no behavior change |
| `pi.packages` sources | **Remote only** (`npm:`/`git:`/`https:`/`ssh:`); local paths rejected with a clear error |

## Non-goals (this spec)

- **Runtime/per-run settings** beyond provider/model/thinking (those stay CLI flags). No `pi.settings:` raw passthrough yet (future).
- **Workspace `.pi/` trust opt-in** (`pi.trust_workspace_config`) — future (#4). The default is simply `never`.
- **Forcing strict network policy** — only a warning this pass.
- **Auto-resolving a package's runtime deps** — if a Pi package needs a runtime tool, the user declares it in `dependencies:` (documented).
- **Host `~/.pi` mount** — declined (leaks auth per Pi's own docs).

## Architecture

### moat.yaml surface

```yaml
agent: pi
pi:
  provider: anthropic       # existing
  model: claude-opus-4-8    # existing
  packages:                 # NEW — remote pi install sources, baked at build
    - "npm:@acme/pi-reviewer@1.2.0"
    - "git:github.com/acme/pi-skills@v3"
```

`config.PiConfig` gains `Packages []string`. Validation (`validatePiPackages`, called from `Load`): each entry non-empty and prefixed `npm:`/`git:`/`https:`/`ssh:`; a local-looking path (`./`, `/`, `../`) is rejected with guidance to publish it to npm/git.

### Build-time bake (mirrors the claude-plugins precedent)

New `internal/providers/pi/dockerfile.go`:

```go
// GenerateDockerfileSnippet returns a Dockerfile snippet + a generated script
// (context file) that, as the container user, installs the declared Pi packages
// and bakes Moat's safe global settings into ~/.pi/agent/settings.json.
func GenerateDockerfileSnippet(packages []string, containerUser string) SnippetResult
```

The generated script (`pi-config.sh`, run as `moatuser`, `WORKDIR /home/moatuser`):

```sh
set -e
export HOME=/home/moatuser
export GIT_TERMINAL_PROMPT=0
export GIT_SSH_COMMAND='ssh -o BatchMode=yes -o ConnectTimeout=10'
mkdir -p "$HOME/.pi/agent"
# (one line per package, only when packages are declared)
pi install "npm:@acme/pi-reviewer@1.2.0"
# ... merge Moat's safe defaults, preserving any packages array pi install wrote
node -e 'const fs=require("fs"),p=process.env.HOME+"/.pi/agent/settings.json";let s={};try{s=JSON.parse(fs.readFileSync(p,"utf8"))}catch(e){}Object.assign(s,{defaultProjectTrust:"never",enableInstallTelemetry:false,enableAnalytics:false,quietStartup:true});fs.writeFileSync(p,JSON.stringify(s,null,2))'
```

Generated as a separate script + `COPY`/`RUN` (not inlined) to stay under Apple's ~16 KB Dockerfile limit — exactly how `claude.GenerateDockerfileSnippet` works. Runs after the npm section (so `pi` is installed) and enters user context (writes to `$HOME`); the existing `inUserContext` bookkeeping restores `USER root` afterward.

### Wiring

- `internal/deps/imagespec.go`: `ImageSpec` gains `PiBakeSettings bool` and `PiPackages []string`. `NeedsCustomImage` already true (pi-cli is an npm dep); include `PiPackages` (and a `pi-settings-v1` marker) in the tag-hash inputs so changing packages — or the baked-settings version — forces a rebuild.
- `internal/deps/dockerfile.go` `GenerateDockerfile`: after the claude-plugins call, if `spec.PiBakeSettings`, call `pi.GenerateDockerfileSnippet(spec.PiPackages, containerUser)`, append its snippet + context script.
- `internal/run/manager_create.go`: when building `ImageSpec`, set `PiBakeSettings = hasDep(installableDeps, "pi-cli")` and `PiPackages = opts.Config.Pi.Packages`.
- `internal/providers/pi/cli.go`: in `moat pi`, when `cfg.Network.Policy != "strict"`, `ui.Warn` a one-line notice (Pi config can redirect model traffic; prefer strict for untrusted work).

The Pi provider's runtime staging (context file mount + `--append-system-prompt`) is **unchanged**; no `moat-init.sh` change.

## Data flow

`moat.yaml pi.packages` → `cfg.Pi.Packages` → `ImageSpec.PiPackages` → `GenerateDockerfile` → `pi.GenerateDockerfileSnippet` → generated `pi-config.sh` in build context → `RUN` as moatuser → `~/.pi/agent/{npm,git,settings.json}` baked into the image layer → present at runtime (home is image content, not shadowed by the `/workspace` mount).

## Error handling

- `pi.packages` validation fails `Load` with an actionable message (which entry, why, how to fix).
- A failing `pi install` (bad source, network) fails the **image build** (`set -e`) — surfaced as a build error naming the package, not a silent runtime gap.
- Empty `pi.packages`: the bake step still runs (writes the security settings); the install loop is simply empty.

## Testing

- `validatePiPackages` table test: accepts `npm:`/`git:`/`https:`/`ssh:`; rejects empty, local paths (`./x`, `/abs`, `../x`), and bare names — each rejection paired with an accepted mirror (invariant #1).
- `config_test`: `pi.packages` round-trips; empty/absent block → empty slice (companion).
- `pi.GenerateDockerfileSnippet` test: (a) zero packages → snippet still bakes settings (contains the `defaultProjectTrust`/telemetry merge, no `pi install` lines); (b) N packages → one `pi install "<src>"` line each, in order, plus the settings merge; script is a context file; runs as the container user.
- `GenerateDockerfile` integration: with `PiBakeSettings` + packages, the Dockerfile references the pi-config script and the context files include it; without it, neither appears (companion).
- Sandbox e2e (DIND, `--no-sandbox`): `moat pi` with a `pi.packages` entry builds, the package appears in `pi list` inside the container, and `~/.pi/agent/settings.json` has `defaultProjectTrust: "never"`.
- Warning: unit-assert the permissive-policy branch emits the notice and strict does not.

## Open risks

- **No checksum/lockfile** for Pi packages (Pi limitation) — pin exact `@version`/`@ref`; documented.
- **Build-time network** for `pi install` — builds run with the builder's network (fine); documented that private registries/git auth are a container/npm/git concern, not Pi's.
- **Baked-settings changes across Moat versions** — the image tag content-hashes the generated bake script (`pi.GenerateDockerfileSnippet(...).ScriptContent`), so any change to the baked defaults or the package set invalidates cached images automatically (no manual version bump).
