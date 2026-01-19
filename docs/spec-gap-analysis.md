# Moat: Spec vs. Implementation Gap Analysis

This document compares the original vision for Moat against the current implementation, identifying gaps and opportunities.

## Original Vision

> Run an agent locally with one command, zero Docker knowledge, zero secret copying, and full visibility.
> Think: local-first + ephemeral sandboxes + opinionated defaults.

**Mental model:** A "run" = a sealed workspace (code + deps + tools, temporary credentials, network + ports, logs + traces + artifacts). Don't manage containers. Manage runs.

**Ideal flow:**
```
> agent run claude-code ./my-repo
```

What happens implicitly:
- Workspace is created
- Dependencies resolved automatically
- Credentials injected just-in-time
- Ports auto-mapped + OAuth aware
- Full trace/log/debug capture
- Workspace is disposable or promotable

## What We Built Well

| Feature | Status | Notes |
|---------|--------|-------|
| Sealed workspace concept | ✅ Complete | Runs bundle code, deps, creds, logs as one unit |
| Credential injection | ✅ Complete | Network-layer injection via TLS proxy—agent never sees tokens |
| Full observability | ✅ Complete | Logs, network traces, structured JSONL |
| Tamper-proof audit | ✅ Exceeded spec | Hash chains, Merkle trees, Ed25519 attestations, exportable proofs |
| Network policy | ✅ Complete | Allow-list strict mode, clear error messages |
| Multi-runtime support | ✅ Complete | Docker + Apple containers auto-detected |

The credential injection model is genuinely differentiated—neither Pack'n'Play nor Leash offer network-layer token injection where agents never see raw credentials.

## Major Gaps

### 1. Agent-Aware CLI

**Spec:** `agent run claude-code ./my-repo`

**Current:** `moat run --grant github -- npx claude-code`

**Gap:** The spec envisions Moat *knowing about agents*. The current implementation is agent-agnostic—users pass arbitrary commands. This is flexible but breaks the "zero Docker knowledge" promise because users must know:

- What command runs each agent (`npx claude-code`? `claude`? `codex`?)
- What base image includes the agent
- What credentials the agent typically needs
- What ports the agent might use

**Impact:** Users need agent-specific knowledge that the tool should encapsulate.

**Solution direction:** Agent registry with presets:
```bash
moat run claude ./my-repo          # Knows: image, command, typical grants
moat run codex ./my-repo           # Different preset
moat agents list                   # Show available agents
```

---

### 2. Dependencies Resolved Automatically

**Spec:** "Dependencies resolved automatically"

**Current:** Must declare in `agent.yaml` or get `ubuntu:22.04`

**Gap:** The spec implies analyzing the repo and auto-detecting runtime requirements. A Node project should get `node:20` without configuration. Currently users must explicitly set:

```yaml
dependencies:
  - node@20
```

**Impact:** Extra configuration step, requires understanding of the dependency system.

**Solution direction:** Project detection from standard files:

| File | Detected Runtime |
|------|------------------|
| `package.json` | Node (version from `engines` or LTS) |
| `requirements.txt` / `pyproject.toml` | Python |
| `go.mod` | Go (version from file) |
| `Cargo.toml` | Rust |
| `Gemfile` | Ruby |

```bash
moat run ./my-repo    # Detects package.json → uses node:20
```

---

### 3. Ports Auto-Mapped + OAuth Aware

**Spec:** "Ports auto-mapped + OAuth aware"

**Current:** Manual port declaration in `agent.yaml`, hostname routing requires explicit config

**Gap:** Two missing capabilities:

**Auto-mapping:** The system should detect common ports (3000, 8080, 5173) or read from project config (`package.json` scripts, `vite.config.js`, etc.).

**OAuth aware:** The spec suggests automatic callback URL configuration. Currently users must:
1. Create their own OAuth apps
2. Configure callback URLs manually
3. Set environment variables for client IDs

**Impact:** Significant setup friction, especially for OAuth flows.

**Solution direction:**
- Port detection from project files and conventions
- Built-in OAuth app for common providers (GitHub, Google) with automatic callback routing
- Or: clear documentation that this is out of scope

---

### 4. Workspace is Disposable or Promotable

**Spec:** "Workspace is disposable or promotable"

**Current:** Runs stored in `~/.moat/runs/<id>/` indefinitely, no promotion workflow

**Gap:** "Promotable" implies you could take a successful run and:
- Save it as a template for future runs
- Extract the environment as a reproducible config
- Push workspace changes back to the repo
- Convert to a persistent development environment

Currently runs just accumulate with no lifecycle management beyond `moat destroy`.

**Impact:** No clear path from "experimental run" to "this worked, let's keep it."

**Solution direction:**
```bash
moat promote <run-id> --as template    # Save as reusable template
moat promote <run-id> --export         # Extract agent.yaml from run
moat gc --older-than 7d                # Clean up old runs
```

---

### 5. Zero Docker Knowledge

**Spec:** "zero Docker knowledge"

**Current:** Docker/container concepts leak through the abstraction

**Gap:** Users encounter container concepts in:

- **Configuration:** `dependencies` (image selection), `mounts` (volume mapping)
- **Error messages:** Often reference Docker errors directly
- **Mental model:** Documentation explains "containers" and "images"
- **Troubleshooting:** Requires understanding of container networking, volumes

**Impact:** The "sealed workspace" abstraction breaks down; users need container knowledge to debug issues.

**Solution direction:**
- Rename/abstract configuration terms (`mounts` → `files`? `dependencies` → `runtime`?)
- Wrap all Docker errors in user-friendly messages
- Documentation should avoid container terminology where possible
- Focus on "runs" and "workspaces" not "containers"

---

## Competitive Context

Comparing against Pack'n'Play and Leash reveals where these gaps hurt most:

| Capability | Moat | Pack'n'Play | Leash |
|------------|------|-------------|-------|
| Agent presets | ❌ None | ✅ 7 agents built-in | ✅ 5 agents built-in |
| Auto-detection | ❌ Manual config | ⚠️ Devcontainer-based | ❌ Manual config |
| First-run friction | ⚠️ OAuth app setup | ✅ Interactive wizard | ✅ Simple prompts |
| Zero Docker knowledge | ⚠️ Concepts leak | ✅ Well abstracted | ⚠️ Some exposure |

Pack'n'Play delivers better on "zero Docker knowledge" despite having weaker security properties. Users can run `packnplay run claude` immediately—no config, no OAuth app setup, no image selection.

## Prioritized Recommendations

### High Impact, Moderate Effort

1. **Agent presets** - `moat run claude` with built-in knowledge of image, command, typical grants
2. **Project auto-detection** - Read `package.json`/`go.mod`/etc. for runtime selection
3. **Simplify first-run** - Ship default GitHub OAuth client ID or support credential mounting as fallback

### Medium Impact, Lower Effort

4. **Abstract container terminology** - Rename config fields, improve error messages
5. **Workspace garbage collection** - `moat gc` for cleanup
6. **Port auto-detection** - Read from `package.json` scripts, common conventions

### Lower Priority (Nice to Have)

7. **Workspace promotion** - Templates and export workflows
8. **OAuth callback automation** - Complex, may be out of scope
9. **Devcontainer compatibility** - Read basic fields from `devcontainer.json`

## Summary

Moat's core security model (network-layer credential injection, tamper-proof audit) is genuinely differentiated and well-implemented. However, the developer experience gaps—particularly agent awareness and auto-detection—undermine the "zero Docker knowledge" promise.

The risk is that users choose Pack'n'Play for convenience despite weaker security, or Leash for governance despite weaker credential isolation. Closing the DX gaps while maintaining the security model would make Moat the clear choice for security-conscious teams who also value ease of use.
