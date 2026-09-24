# Codex Subscription Authentication Implementation Plan

> **For agentic workers:** Implement this plan task-by-task and keep the checkboxes current. Task 0 is a release gate. Do not build or ship the production path until its observed-contract section is complete.

**Goal:** Let `moat codex` use a ChatGPT-backed Codex subscription without mounting, copying, or exposing any real OAuth credential inside the container, while preserving OpenAI API-key support and failing closed across CLI, daemon, and Codex version skew.

**Architecture:** `moat grant codex` performs a separate Codex login on the host in a private, temporary `CODEX_HOME`. This gives Moat its own refresh credential instead of copying the refresh token used by the user's normal Codex installation. Moat immediately imports that login into its encrypted credential store and removes the temporary plaintext cache. At run time, the container receives a synthetic `auth.json` using Codex's external-host-auth mode. The daemon resolves the latest encrypted credential itself and installs a TLS-only, canonical-path credential bundle in the existing Gatekeeper proxy. The bundle atomically replaces the synthetic `Authorization` and `ChatGPT-Account-ID` headers only on verified Codex backend routes. Refresh is centralized in the daemon, persisted before publication, and never performed inside the container.

**Tech Stack:** Go 1.25, Codex CLI (`@openai/codex`, currently pinned to 0.146.0), existing encrypted credential store, existing proxy daemon, `github.com/majorcontext/gatekeeper`, `net/http`.

## Draft Implementation Status (2026-09-14)

The implementation described here is present on the feature branch and is
intentionally being proposed as a draft. Synthetic, unit, race, and static
verification pass. The checklist remains the release checklist rather than a
claim that the live contract has been proven.

Still release-blocking:

- Run the Task 0 matrix against an authorized ChatGPT account, including two
  independent logins, rotation/revocation, SSE, WebSocket, and managed-account
  behavior.
- Review and merge/release the scoped credential-bundle change in Gatekeeper;
  the draft implementation temporarily pins its reviewed feature branch.
- Confirm the supported Codex CLI range from the live matrix and narrow it if
  any version differs.

## Baseline at Planning Time

- `moat grant codex` and `moat grant openai` currently resolve to the same provider. Both acquire an `OPENAI_API_KEY`; neither provisions a ChatGPT subscription login.
- Codex staging currently writes only a fake `OPENAI_API_KEY` to `auth.json`, and proxy injection only targets `api.openai.com`.
- Moat previously imported Codex OAuth credentials and staged fake JWTs (PR #57). That implementation was removed in PR #91 because the traffic contract had not been validated and the integration did not work reliably.
- Inspected Codex 0.154 source has an internal external-host auth mode serialized as `chatgptAuthTokens`. It treats supplied tokens as ChatGPT authentication for backend selection while leaving refresh to the host application. This is an implementation detail, not a documented compatibility contract, so the supported CLI version must be pinned and verified.
- OpenAI documents that Codex can store credentials in `auth.json` or an OS credential store and automatically refreshes ChatGPT tokens during use. Copying an existing refresh token and refreshing it independently would create competing token owners, so this plan no longer imports the user's normal Codex cache.
- Gatekeeper already handles intercepted HTTP, SSE, and WebSocket upgrades. It currently injects credentials at hostname scope and can auto-inject when the client sends no placeholder. Subscription auth therefore needs a stricter TLS-only, canonical-path, exact-placeholder bundle primitive.
- The daemon currently refreshes per run, starts its initial refresh asynchronously, ignores `RefreshInterval()`, persists only when the access-token string changes, and lets the CLI replay a stale credential-bearing registration request after daemon restart. These lifecycle paths must be corrected before subscription auth is enabled.

## Security and Capability Model

This design protects credential confidentiality, not process-level use of the grant:

- The container never receives the real access token, refresh token, ID token, account ID, or a reversible derivative.
- A process inside a run can use the run's proxy capability to exercise the narrowly granted Codex backend routes. Exact placeholders prevent accidental injection into unrelated traffic, but they do not distinguish Codex from another process that can read the same synthetic auth file and proxy environment.
- Preventing another process in the same container from exercising the grant would require a separate process-identity boundary and is out of scope.
- Subscription credentials are valid only for an exact approved HTTPS origin and canonical backend paths. Network host allowlisting alone is not treated as a credential boundary.

## Credential Identities and Selection

The user-facing grants remain simple, but the subscription uses a new internal store identity that old binaries do not recognize:

| User-facing grant | Stored provider | Intended consumer | Upstream injection |
|---|---|---|---|
| `codex` | `codex-subscription-v1` | Codex CLI only | Atomic scoped bundle on an approved ChatGPT Codex origin |
| `openai` | `openai` | Codex fallback and general OpenAI tooling | `Authorization` on `api.openai.com` |

Rules:

- `moat codex` prefers a granted `codex` subscription and falls back to a granted `openai` API key.
- A declarative `agents: [codex]` derives the logical `codex` grant, whose resolver accepts `codex-subscription-v1` first and `openai` second for backward compatibility.
- An explicitly requested `openai` grant is API-key-only and never resolves to a subscription.
- Staging is selected from the run's effective grants, not merely from whichever credentials happen to exist in the store.
- If both grants are present, Codex uses subscription auth. The OpenAI API-key placeholder must not be exported process-wide where Codex can treat it as the active login; it is scoped to the explicitly configured consumer that needs it.
- `agent: openai` and `agents: [openai]` remain aliases for the Codex agent through a dedicated agent-alias mechanism. They are not credential-provider aliases.

```text
moat grant codex
        |
        | launches host Codex login with private temporary CODEX_HOME
        v
new Moat-owned OAuth session ---- immediate encrypted import ---- remove temp cache
        |
        | daemon resolves latest credential by reference
        v
central refresh coordinator -----------------------------+
        |                                                 |
        | atomic encrypted persistence                    |
        v                                                 v
TLS-only Gatekeeper credential bundle                 active runs
        ^
        | exact synthetic bearer + synthetic account ID
        |
container synthetic auth.json --> Codex CLI --> https://chatgpt.com/backend-api/codex/**
```

## Global Constraints

- **Never clone an active refresh credential.** The user's normal Codex cache and Moat must not independently refresh the same token. `moat grant codex` obtains a separate login session owned by Moat.
- **No real auth material in the container.** Never mount or copy a real `auth.json`. Never put a real access token, refresh token, ID token, account ID, email, subject, or reversible derivative into container files, environment variables, command arguments, image layers, logs, audit fields, or run metadata.
- **Minimize host-side secrets.** Retain only fields required for proxying and refresh. Do not store the real ID token after validated claim extraction unless Task 0 proves it is required for a later refresh.
- **Synthetic means non-secret.** Staged JWTs and identity fields are generated. Copy plan/FedRAMP or routing claims only when Task 0 proves they are required and non-identifying.
- **Fail closed on transport and path.** Subscription bundles require verified TLS, exact origin matching, canonical unambiguous paths, approved methods, and all expected placeholders. A mismatch injects nothing.
- **Fail closed on daemon capabilities.** A new CLI must stop before registration when the daemon lacks credential-reference or scoped-bundle support. It must never fall back to host-wide injection.
- **Fail closed on Codex version.** Subscription auth is enabled only for explicitly verified Codex CLI versions. Both declared dependency overrides and the executable actually launched in the container are checked.
- **Default endpoint only in v1.** Support `https://chatgpt.com` only. Detect and reject custom or managed `chatgpt_base_url` configurations until their origins and routing behavior have a separate reviewed design.
- **API keys and subscriptions remain distinct.** Subscription tokens are never made available to generic OpenAI SDKs, MCP servers, custom model providers, or custom base URLs.
- **Daemon resolves secrets.** Credential-bearing data is not cached in the CLI's restart registration snapshot. The daemon resolves the latest encrypted value from a reference during registration and restore.
- **Refresh is centralized and transactional.** One coordinator per profile/store identity serializes refresh, reloads after locking, validates the complete response, persists all rotations atomically, and then publishes to active runs.
- **Daemon API is additive-only.** New fields use `omitempty`; existing fields retain their meaning. New behavior requires advertised capabilities.
- **Detector/validator/resolver parity.** Missing-grant detection, validation, run loading, staging, daemon restore, re-registration, and refresh use one credential-selection contract. Add a drift-guard matrix.
- **Companion-case tests.** Every positive auth/injection test has negative cases for wrong grant, origin, scheme, method, path, placeholder, version, and daemon capability.
- **No live credentials in automated tests.** Use synthetic fixtures and `httptest`. Live login/refresh checks are explicit, opt-in release gates with redacted capture.
- **User output vs diagnostics:** actionable auth failures go through `internal/ui`; redacted diagnostics go through `internal/log`.
- **Commits:** Conventional Commits, no `Co-Authored-By` lines. Run `make lint` and `make test-unit` before each commit.

## Expected File Structure

| Path | Responsibility |
|---|---|
| `internal/providers/codex/provider.go` | Codex agent/subscription provider and API-key fallback behavior |
| `internal/providers/codex/openai.go` | Separate OpenAI API-key provider |
| `internal/providers/codex/login.go` | Moat-owned host Codex login and temporary-cache lifecycle |
| `internal/providers/codex/token.go` | Auth parsing, claim validation, expiry, and refresh merging |
| `internal/providers/codex/refresh.go` | Pure host-side refresh exchange; no proxy mutation |
| `internal/providers/codex/agent.go` | Grant-aware synthetic auth staging and version marker |
| `internal/providers/codex/constants.go` | Store identity, supported versions, origin/path scope, metadata keys |
| `internal/providers/codex/*_test.go` | Login, staging, scope, refresh, redaction, and compatibility tests |
| `internal/credential/types.go` | `ProviderCodexSubscriptionV1` internal store identity |
| `internal/credential/provider.go` | Optional credential-bundle proxy interface |
| `internal/provider/interfaces.go` | Pure refresh and compatible-grant interfaces |
| `internal/cli/agents.go` | Agent-only `openai -> codex` compatibility alias |
| `internal/run/credentialresolve.go` | Shared effective-grant and stored-credential resolution contract |
| `internal/run/grants.go`, `internal/run/run.go`, `internal/run/imageneeds.go` | Detection/validation using shared resolution |
| `internal/run/manager_create.go`, `internal/run/manager_agentinit.go` | Grant-aware staging, capability gates, credential references |
| `internal/run/manager_monitor.go`, `internal/run/run.go` | Secret-free daemon re-registration snapshots |
| `internal/daemon/api.go` | Additive credential references/bundles and capability constants |
| `internal/daemon/runcontext.go` | Scoped bundle state and hot-swap support |
| `internal/daemon/persist.go` | Restore references and resolve current credentials without persisting secrets |
| `internal/daemon/refresh.go` | Central, serialized, persistence-first refresh coordinator |
| `docs/content/guides/02-codex.md` | Subscription/API-key setup and security guarantees |
| `docs/content/concepts/02-credentials.md` | Credential confidentiality versus grant-use capability |
| `docs/content/reference/04-grants.md` | Grant behavior and compatibility |

Gatekeeper changes are developed and tested upstream first. Moat then consumes a released version and carries the bundle contract through its additive daemon API.

---

### Task 0: Release-gate spike — prove auth, login ownership, and traffic contracts

The repository is pinned to Codex 0.146.0, while the source behavior inspected during planning was 0.154.0. The private `chatgptAuthTokens` format, independent-login assumption, exact network routes, and transport metadata must all be proven before implementation.

**Files:**
- Create: `internal/providers/codex/testdata/external-auth/` (synthetic fixtures only)
- Create: `internal/providers/codex/testdata/compat/README.md` (observed results, no tokens)
- Modify if required: `internal/deps/registry.yaml`
- Modify if required: `internal/deps/dockerfile_test.go`

- [ ] Build isolated test images for pinned 0.146.0 and candidate 0.154.0. Record executable version and package digest.
- [ ] Preserve the production destination while capturing traffic: use a local TLS-intercept test proxy that observes the original CONNECT authority and request target, returns fixture responses, and never forwards to ChatGPT. Do not use `openai_base_url` for this measurement because it changes routing.
- [x] Stage a fully synthetic `auth.json` with `auth_mode: "chatgptAuthTokens"`, parseable fake ID/access JWTs, a fake refresh value, a synthetic account ID, and non-null `last_refresh`.
- [ ] Run `codex login status`, `codex exec`, interactive startup, normal SSE, and WebSocket v2 against the capture proxy. Confirm backend selection, header names, request methods/paths, and absence of in-container refresh attempts.
- [ ] Exercise startup, model discovery, configuration, plugin, and session flows. Record every credential-bearing route separately from merely allowed network routes.
- [ ] Test malformed signatures, expired synthetic access claims, missing claims, wrong auth mode, and a synthetic account ID that differs from ID-token claims.
- [ ] On an authorized test account, perform two separate Codex logins in isolated `CODEX_HOME` directories. Refresh one, then prove the other can still refresh. Record only equality/difference and success/failure—never token values. Stop if logins do not receive independent refresh authority.
- [ ] Verify whether a Moat-owned login can force file storage in a private `CODEX_HOME` on macOS and Linux. Record behavior when local requirements force keyring storage.
- [ ] Verify the default ChatGPT origin and detect behavior for `chatgpt_base_url`, managed workspaces, and data-residency accounts where test access exists. V1 remains default-origin-only unless each alternative is explicitly approved.
- [ ] Verify the exact Codex version can be determined from resolved dependencies and again inside the container immediately before launch.
- [x] If 0.146.0 lacks the required behavior, bump to the lowest fully verified version and run `codex --strict-config doctor` against Moat's generated configuration.
- [ ] Commit a repeatable synthetic compatibility harness. Keep the independent-login refresh test opt-in because it requires a real account.

**Observed contract (partially completed — 2026-09-17):**

Everything below marked CONFIRMED was measured, not inferred. Everything
marked UNVERIFIED still gates release. Nothing here was established by reading
Codex's source or grepping its binary alone; where a symbol's presence is all
we have, it is recorded as UNVERIFIED.

```text
Supported Codex version(s) and package digest(s):
  CONFIRMED both ends of the range:
    @openai/codex@0.146.0  sha512-yG3sPWNda/2YAIQIDq9MrrjoCTIQ7rxYM5IasrG3VBcuhCLTkgeg/JzqmJq1V98RE4MJ5jCxDXXQlOjrditFRw==
    @openai/codex@0.154.0  sha512-FV/x1OHXYv/ifjf3mXj9ThTTAWcUZN6cGIRQRhRxkKNOPuImu1WW0c8ev1vUkE9XGH90dEnYG1tBjIkxRikg0w==
  Registry default moved 0.146.0 -> 0.154.0 so the host CLI that performs the
  grant and the container CLI that reads its result match by default.

Accepted auth_mode spelling:
  CONFIRMED (write side, 0.154.0): a real `codex login --device-auth` writes
    auth_mode: "chatgpt". Evidenced by `moat grant codex` succeeding against an
    authorized account — grant.go rejects any other spelling, so completion is
    proof of this value.
  CONFIRMED (read side, 0.146.0 and 0.154.0): the synthetic container file
    using auth_mode: "chatgptAuthTokens" is accepted by both. `codex login
    status` reports "Logged in using ChatGPT" against a CODEX_HOME containing
    only Moat's generated auth.json and config.toml.
  The two spellings are distinct AuthMode variants, not a contradiction.

Required auth.json fields and claims:
  CONFIRMED sufficient (both versions) — the staged file Codex accepts is:
    auth_mode, OPENAI_API_KEY (null), tokens{id_token, access_token,
    refresh_token, account_id}, last_refresh (RFC3339).
  NOT established: which of these are individually *required*. No field was
  removed and re-tested, so this records a sufficient set, not a minimal one.

Default HTTPS origin:
  PARTIAL. chatgpt_base_url = "https://chatgpt.com" is accepted schema —
  `codex --strict-config doctor` loads Moat's generated config.toml on 0.154.0
  (and the API-key variant, which omits the key entirely, also loads).
  UNVERIFIED that Codex actually routes requests there; that needs live traffic.

Credential-bearing methods and canonical path prefixes:
  UNVERIFIED. The bundle currently scopes GET+POST on /backend-api/codex.
  Nothing has observed which routes a running Codex actually sends credentials
  on. This is the largest remaining gap: too narrow and Codex breaks in ways
  that look like auth failure, too broad and the scope stops being a boundary.

Required companion routes and rationale:
  UNVERIFIED. Whether startup, model discovery, or account/plan lookups need
  credentials on routes outside /backend-api/codex is unknown.

Authorization placeholder shape:
  CONFIRMED deterministic and byte-identical between staging and the proxy
  bundle (credential.GenerateAccessTokenPlaceholder with a fixed iat; pinned by
  TestGenerateAccessTokenPlaceholderDeterministic and
  TestStagedAccessTokenMatchesTheBundlePlaceholder).
  UNVERIFIED against live traffic — that the value Codex puts on the wire is
  the staged access_token verbatim has not been observed.

Account header name/shape:
  UNVERIFIED. "ChatGPT-Account-ID" appears in the 0.146.0 binary, which is
  necessary but not sufficient. That Codex sends it, on which routes, and
  whether its value is the staged account_id verbatim are all unobserved.
  RequireAll makes this load-bearing: if the header is absent on a credential
  bearing route, the bundle injects nothing and the request fails closed.

SSE result:
  CONFIRMED on an authorized account. Streaming completions run normally
  through the injected bundle over the TLS-interception path. This is Codex's
  default transport, so it is the common path rather than an edge case.

WebSocket result:      UNVERIFIED — needs a live session on the v2 transport.

In-container refresh attempted:
  CONFIRMED no, by inference from a multi-hour session. The injected access
  token lives about an hour. Had Codex been refreshing its own synthetic
  credential — whose refresh_token is the literal "moat-proxy-injected" — it
  would have replaced a working token with a broken one and failed. It did not.

Daemon-side refresh over a long session:
  CONFIRMED working, by the same session. The daemon exchanges ~10 minutes
  before expiry and republishes the bundle; had that failed, requests would
  have started returning 401 after the first hour. Hours of clean operation
  means rotations are reaching a running container without a restart.
  NOT independently confirmed from the logs: a successful exchange logged
  nothing until the fix in this branch, so the positive signal
  ("Codex subscription refreshed" in ~/.moat/debug) is only available from
  that build onward.

Independent login refresh authority verified: UNVERIFIED.
  Requires two logins in separate CODEX_HOMEs on one authorized account,
  refreshing one and then proving the other still refreshes. This is the
  assumption the "never clone an active refresh credential" constraint rests
  on; if it is false the whole ownership model needs rework. Record only
  equality/difference and success/failure — never token values.

Temporary CODEX_HOME file storage verified on:
  CONFIRMED macOS: an operator's `moat grant codex` completed end to end, which
  requires the login to have written auth.json into the private temp CODEX_HOME
  as a file (the grant reads it back from there; a keyring-only store would
  have failed at the os.Stat).
  UNVERIFIED on Linux — no real login has been performed there.

Managed/keyring behavior: UNVERIFIED.
  No test against a managed workspace, a data-residency account, or a host
  whose policy forces keyring storage.

Login flow:
  CONFIRMED. The grant uses `codex login --device-auth` unconditionally. The
  browser flow completes via a localhost callback (redirect_uri/callback paths
  in the binary) and so depends on where the grant runs; device code prints a
  link and a one-time code and works from any machine. Observed output on a
  real account: verification URL, one-time code, "Successfully logged in".
```

**Still blocking release**, after live testing on 2026-09-23/24:

1. Independent login refresh authority. Untested, and the one that is not a
   bug if it fails — it is a design error. "Never clone an active refresh
   credential" assumes two logins on one account hold separate authority; if
   they share it, `moat grant codex` can invalidate the user's own Codex
   session and the ownership model needs rework rather than a patch. Cheapest
   item on this list to settle: two logins in separate CODEX_HOMEs, refresh
   one, confirm the other still refreshes.
2. WebSocket transport. Unexercised.
3. Managed workspace / keyring-forced storage, and Linux host login.

Credential-bearing routes, the account header, SSE, in-container refresh, and
daemon-side rotation are all now measured rather than assumed. The route scope
needed one correction found this way: Codex's codex_apps connector uses
/backend-api/MCP, outside the bundle's scope. That is intentional — the
connector reaches services linked to the user's ChatGPT account, which is a
broader capability than running completions — and gatekeeper v0.23.2 makes an
out-of-scope request degrade to an ordinary auth failure instead of a 403.

**Release gate:** stop if synthetic auth fails, the two host logins share refresh authority, credential-bearing routes cannot be narrowly scoped, or the actual CLI version cannot be enforced.

---

### Task 1: Provision a separately owned Codex login on the host

**Files:**
- Create: `internal/providers/codex/login.go`
- Create: `internal/providers/codex/token.go`
- Modify: `internal/providers/codex/grant.go`
- Test: `internal/providers/codex/login_test.go`
- Test: `internal/providers/codex/token_test.go`

- [ ] Change `moat grant codex` from API-key prompting/import to a host-side Codex login. Never read or clone the user's normal `CODEX_HOME` refresh credential.
- [ ] Create a private temporary directory with mode `0700`, write a minimal user-level Codex configuration selecting file credential storage, and launch a supported host `codex login` with `CODEX_HOME` set only for that child process.
- [ ] Support browser login and pass through the official device-code option when requested. The login happens on the host, never in the container.
- [ ] Require a supported host Codex version for the login helper or invoke the same verified package version used by the container. Do not silently use an incompatible executable.
- [ ] After login succeeds, open the temporary `auth.json` without printing it; validate ChatGPT mode, issuer/audience where available, required claims, top-level versus token account consistency, expiry, and the exact default ChatGPT origin contract.
- [ ] Store the access token in `Credential.Token`; store the refresh token, real account ID, expiry/plan/routing metadata, and a stable random synthetic identity in encrypted metadata under `codex-subscription-v1`.
- [ ] Do not retain the real ID token unless the release-gate spike proves it is required after claim extraction.
- [ ] Atomically save the encrypted credential before reporting success. Always remove the temporary `CODEX_HOME`, including login failure, cancellation, and parse/save errors.
- [ ] If managed requirements force keyring or a non-default `chatgpt_base_url`, fail with a specific unsupported-configuration message. Do not weaken storage or origin validation.
- [ ] Add cancellation, missing executable, unsupported version, login failure, keyring override, malformed auth, account mismatch, expired-access-with-valid-refresh, encrypted-save failure, cleanup, and sentinel-secret redaction tests.

The temporary plaintext cache exists only on the host for the duration of the login. Document that boundary explicitly. File deletion is not secure erasure on copy-on-write filesystems; users requiring no transient host file must wait for a direct device/browser OAuth implementation.

---

### Task 2: Separate credential providers and make selection grant-aware

**Files:**
- Modify: `internal/credential/types.go`
- Modify: `internal/providers/codex/provider.go`
- Create: `internal/providers/codex/openai.go`
- Modify: `internal/providers/codex/runtime.go`
- Modify: `internal/providers/codex/cli.go`
- Modify: `internal/cli/agents.go`
- Create: `internal/run/credentialresolve.go`
- Modify: `internal/run/grants.go`
- Modify: `internal/run/run.go`
- Modify: `internal/run/imageneeds.go`
- Modify: `internal/run/manager_create.go`
- Modify: `internal/run/manager_agentinit.go`
- Test: related provider, CLI, grant, image-needs, and agent-init tests

- [ ] Add `credential.ProviderCodexSubscriptionV1 = "codex-subscription-v1"`. It is an internal store key, not a user-entered grant or agent name.
- [ ] Keep the agent-capable registry provider named `codex`; add an API-key-only provider named `openai`; remove the credential-provider alias between them.
- [ ] Preserve `agent: openai` and `agents: [openai]` through the agent-variant map, canonicalizing them to `codex` without making the `openai` credential provider an agent.
- [ ] Implement one resolver that takes the effective grant set and returns `{AuthKind, LogicalGrant, StoreProvider, Credential}`. It may use the API-key fallback only when resolving the logical Codex agent grant.
- [ ] Use the resolver in missing detection, validation, run loading, image needs, staging, daemon credential references, restore, re-registration, and refresh. Add a detector/validator/resolver drift guard.
- [ ] Ensure an `openai`-only run never loads or stages a stored subscription. Ensure a `codex` run prefers the subscription and falls back to `openai` only when the subscription is absent.
- [ ] Define the both-grants behavior explicitly: stage subscription auth for Codex and prevent process-wide `OPENAI_API_KEY` from overriding it. Reuse the shell/subprocess-scoping pattern already used when Claude OAuth and an Anthropic API key coexist.
- [ ] Keep `openai` available to local MCP configuration only when explicitly declared. Never treat `codex` as a valid generic MCP credential.
- [ ] Do not migrate a copied legacy refresh token automatically. If an old OAuth-shaped value is found under `openai` or `codex`, leave it untouched and require a new `moat grant codex` login. API keys remain under `openai`.
- [ ] Test subscription only, API key only, both, neither, explicit replacement grants, `agents: [codex]`, `agents: [openai]`, decrypt failure, malformed legacy OAuth, and API-key nonmigration.

---

### Task 3: Stage synthetic auth and enforce the actual Codex version

**Files:**
- Modify: `internal/providers/codex/agent.go`
- Modify: `internal/providers/codex/constants.go`
- Modify or replace: placeholder helpers in `internal/credential/provider.go`
- Modify: `internal/deps/scripts/moat-init.sh` or the Codex launch wrapper
- Test: `internal/providers/codex/agent_test.go`
- Test: relevant init-script tests

- [ ] Preserve the existing fake-API-key staging path only when the resolved run auth is `openai`.
- [ ] For subscription auth, write the Task 0-verified equivalent of:

```json
{
  "auth_mode": "chatgptAuthTokens",
  "OPENAI_API_KEY": null,
  "tokens": {
    "id_token": "<synthetic parseable JWT>",
    "access_token": "<synthetic parseable JWT>",
    "refresh_token": "moat-proxy-injected",
    "account_id": "<stable synthetic account ID>"
  },
  "last_refresh": "<current RFC3339 timestamp>"
}
```

- [ ] Generate the synthetic access token and account ID deterministically from the stored synthetic identity so the daemon can calculate the exact expected placeholders without receiving container files.
- [ ] Copy only non-identifying feature/routing claims proven necessary. Use generated IDs and placeholder identity claims everywhere else.
- [ ] Give the synthetic access JWT a safe future expiry. External-host mode, not expiry, remains the control that prevents in-container refresh.
- [ ] Keep staged auth mode `0600`; preserve cleanup; assert no real credential value occurs anywhere in the staging tree.
- [ ] Check the resolved dependency version before creating the run. Reject unsupported `codex-cli@...` overrides when subscription auth is selected.
- [ ] Immediately before launching Codex, run a lightweight in-container version assertion. Refuse to launch if the installed executable differs from the supported version/digest. This is a compatibility check, not a defense against a process replacing Codex later.
- [ ] Test both-grants precedence and prove Codex selects ChatGPT auth even though an OpenAI grant is available to an explicitly scoped subprocess.

---

### Task 4: Add atomic, TLS-only credential bundles to Gatekeeper

**Repository:** `majorcontext/gatekeeper` first; consume the released version from Moat afterward.

Use a bundle rather than independent credential entries so authorization and account selection cannot be partially replaced.

Proposed semantic shape:

```go
type CredentialBundle struct {
    ID           string
    Grant        string
    Scope        CredentialScope
    Replacements []HeaderReplacement
    RequireAll   bool
}

type CredentialScope struct {
    RequireTLS   bool
    Origins      []string
    Methods      []string
    PathPrefixes []string
}

type HeaderReplacement struct {
    Name        string
    Placeholder string
    Value       string
}
```

- [ ] Add a public bundle setter/upsert without changing existing credential-setter behavior.
- [ ] Evaluate scheme/origin/method/path against the effective upstream request. `RequireTLS` rejects `http` and `ws`; it accepts verified intercepted HTTPS and secure WebSocket handshakes.
- [ ] Define canonical path matching once. Reject invalid escapes, encoded separators, dot segments, backslashes, and ambiguous `RawPath`/`Path` combinations. A prefix matches itself and descendants, not lookalike segments.
- [ ] With `RequireAll`, compare every expected header value before mutation. If any placeholder is absent or different, inject nothing from the bundle.
- [ ] Replace headers with `Header.Set`; never merge account identity or authorization values.
- [ ] Include scope and bundle ID in the upsert identity so refresh changes values without duplicating entries.
- [ ] Record injected header names and grant only after the entire bundle succeeds. Never log placeholders or replacement values.
- [ ] Apply identical semantics to plain forwarding, CONNECT/TLS interception, relays, SSE, and WebSocket upgrades.
- [ ] Add positive HTTPS/WSS tests and negative HTTP/WS, wrong origin, wrong method, adjacent prefix, dot-segment, percent-encoded traversal, missing placeholder, wrong placeholder, partial bundle, host-with-port, SSE, and WebSocket tests.
- [ ] Release Gatekeeper and update Moat from v0.13.0 to the reviewed release.

---

### Task 5: Add secret-free credential references to the daemon API

**Files:**
- Modify: `internal/credential/provider.go`
- Modify: `internal/provider/interfaces.go`
- Modify: `internal/daemon/api.go`
- Modify: `internal/daemon/runcontext.go`
- Modify: `internal/daemon/server.go`
- Modify: `internal/daemon/persist.go`
- Modify: `internal/run/manager_create.go`
- Modify: `internal/run/manager_monitor.go`
- Modify: `internal/run/run.go`
- Test: daemon API/context/persistence and run version-skew tests

- [ ] Add an optional `CredentialBundleConfigurer` interface instead of widening the base `ProxyConfigurer` interface for every provider.
- [ ] Add additive `CredentialRefSpec` and `CredentialBundleSpec` daemon types. A subscription reference contains the logical grant, internal store provider, credential profile, and non-secret adapter version—not token values.
- [ ] Add `CapCredentialRefs = "credential-refs"` and `CapCredentialBundles = "credential-bundles"` to daemon health.
- [ ] Before registration, reject subscription runs unless both capabilities are advertised. No legacy `CredentialSpec` fallback is allowed.
- [ ] Have the daemon load `codex-subscription-v1` from the encrypted store, validate its type, refresh synchronously if needed, and construct the scoped bundle itself.
- [ ] Keep the CLI's `ProxyRegReq` secret-free. On re-registration, the daemon resolves the latest encrypted credential instead of replaying initial access/account values.
- [ ] Persist only credential references/grants/profile/adapter version. On daemon restore, resolve current credentials and recompute bundle scope from the installed provider.
- [ ] Make subscription restore fail closed if the daemon cannot understand the internal store provider or adapter version.
- [ ] Verify downgrade safety: a pre-feature daemon sees no credential under its expected `codex`/`openai` store key and restores no subscription injection. It must not reinterpret a subscription as an API key.
- [ ] Test new CLI/old daemon, old CLI/new daemon, new-run/new-daemon restart, daemon downgrade/restore, missing encrypted credential, rotated credential, and tampered reference/provider/profile fields.

Existing providers may continue using credential-bearing registration until separately migrated. The no-secret registration requirement is mandatory for Codex subscription auth.

---

### Task 6: Install the subscription bundle on verified routes only

**Files:**
- Modify: `internal/providers/codex/provider.go`
- Modify: `internal/providers/codex/constants.go`
- Test: `internal/providers/codex/provider_test.go`
- Test: integration tests under `internal/daemon/` or `internal/run/`

- [ ] Restrict v1 to the exact default origin `https://chatgpt.com`. Reject user, project, or managed `chatgpt_base_url` overrides rather than registering a credential for them.
- [ ] Register one atomic bundle containing the real `Authorization: Bearer ...` and real `ChatGPT-Account-ID` replacements, matched against their exact synthetic values.
- [ ] Use only the methods and canonical segment-boundary paths recorded in Task 0. Do not approve all `/backend-api/**` traffic.
- [ ] Add `X-OpenAI-Fedramp` or other headers only if Task 0 proves they are required. Treat identity/routing headers as scoped replacements, not mergeable extras.
- [ ] Never proxy the ID token or refresh token.
- [ ] Keep API-key fallback behavior on `api.openai.com` separate from the subscription bundle.
- [ ] Narrow `NetworkHosts()` to observed requirements where possible. Document that allowed hosts are broader connectivity policy, while the credential bundle is the secret-use boundary.
- [ ] Confirm no second forwarding server and no `openai_base_url` rewrite is needed; Codex's verified ChatGPT auth mode must select its own backend.
- [ ] Test exact placeholders replaced together, missing/wrong placeholders inject nothing, adjacent ChatGPT paths receive only synthetic values, insecure transport injects nothing, and a malicious account header cannot influence upstream selection.

---

### Task 7: Centralize refresh and publish only persisted credentials

**Files:**
- Create: `internal/providers/codex/refresh.go`
- Modify: `internal/providers/codex/token.go`
- Modify: `internal/provider/interfaces.go`
- Rewrite relevant portions: `internal/daemon/refresh.go`
- Modify: daemon registry/server wiring as required
- Test: provider refresh and daemon coordinator tests

- [ ] Introduce a pure refresh interface that receives a credential and returns a complete updated credential without mutating a proxy or `RunContext`.
- [ ] Implement the Task 0-pinned refresh request to `https://auth.openai.com/oauth/token` with the official public Codex client ID and stored Moat-owned refresh token.
- [ ] Use a dedicated HTTPS client with explicit timeouts, bounded response bodies, strict JSON handling, and redirects disabled. Never forward the refresh request body to another origin.
- [ ] Merge optional response fields by retaining old values when omitted. Validate issuer/audience where possible, access expiry, and account consistency; preserve the synthetic identity.
- [ ] Map revoked/terminal auth responses to `provider.ErrTokenRevoked` without including response bodies or tokens in errors.
- [ ] Add a daemon-level coordinator keyed by credential profile plus internal store provider. Acquire the key lock, reload the latest encrypted value, and skip refresh outside the expiry skew.
- [ ] Refresh synchronously before initial bundle installation or restored-run registration when the access token is near expiry. Do not let the container race an asynchronous startup refresh.
- [ ] Persist every material change—access token, refresh token, expiry, account/routing metadata—using the store's atomic write before publishing the new bundle.
- [ ] If persistence fails after the OAuth server rotates the token, retain the new credential in guarded daemon memory, retry persistence, mark affected runs unhealthy, and surface remediation. Document that a host crash before persistence is an irreducible failure requiring regrant.
- [ ] After persistence, find every active run referencing that profile/store provider and atomically upsert its bundle. Do not rely on one refresh goroutine per run.
- [ ] Honor provider refresh intervals and keep a defensive expiry check. Stop duplicate per-run refresh loops for centrally managed credentials.
- [ ] Test fresh/no-op, nearly expired, refresh-token-only rotation, omitted fields, account mismatch, redirect refusal, oversized response, cancellation, concurrent runs, save failure, daemon restart, hot-swap without duplicates, and revoked-token status reporting.

---

### Task 8: Harden lifecycle, observability, and migration behavior

**Files:**
- Modify: `internal/providers/codex/doctor.go`
- Modify: daemon/run status types if required
- Modify: relevant logging and audit tests
- Test: restart, downgrade, cleanup, and redaction suites

- [ ] Expose subscription health as kind, supported adapter version, expiry state, last successful refresh time, and regrant-required status. Never expose token prefixes, account IDs, subjects, or emails.
- [ ] Surface terminal refresh failure to `moat list`, `moat doctor`, and the affected run instead of logging only at debug level.
- [ ] Verify daemon restart resolves the newest encrypted credential and cannot be overwritten by a stale CLI snapshot.
- [ ] Verify CLI exit while the container continues does not stop daemon-owned refresh.
- [ ] Verify run stop/destroy unregisters the run from bundle publication without deleting the reusable encrypted credential.
- [ ] Detect legacy OAuth-shaped values under `openai`/`codex` and print a one-time regrant instruction; do not move or refresh them.
- [ ] Add sentinel-secret scans covering temporary login files after cleanup, container env/mounts, generated config, CLI registration state, daemon persisted runs, logs, audit records, errors, and test output.

---

### Task 9: End-to-end verification and documentation

**Files:**
- Modify: `docs/content/guides/02-codex.md`
- Modify: `docs/content/concepts/02-credentials.md`
- Modify: `docs/content/reference/01-cli.md`
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/reference/04-grants.md`
- Modify: `internal/providers/codex/doc.go`
- Test: CLI help/snapshot tests and opt-in smoke tests

- [ ] Document `moat grant codex` as a separate host login, not an import of the user's normal Codex auth cache.
- [ ] Document the transient private host `auth.json`, encrypted steady-state storage, synthetic container file, and confidentiality-versus-capability distinction.
- [ ] Document the two grants, selection matrix, both-grants behavior, API billing fallback, supported Codex versions, default-origin-only limitation, and regrant recovery.
- [ ] Preserve and document `agent: openai` as an agent alias while removing statements that call `openai` a credential-provider alias.
- [ ] Add an automated constructed-container assertion proving no stored sentinel secret occurs in env, mounts, commands, generated files, registration snapshots, persisted runs, logs, or audit entries.
- [ ] Add an opt-in real-account smoke suite covering login, `codex exec`, interactive startup, SSE, WebSocket, refresh, two simultaneous active runs, daemon restart, and host Codex continuing to work independently.
- [ ] Manually verify subscription-only, API-key-only, and both-grants journeys.
- [ ] During a subscription run, confirm the container's `~/.codex/auth.json` is entirely synthetic, `/proc/*/environ` contains no real token, and no host Codex directory is mounted.
- [ ] Attempt insecure, adjacent-path, encoded-traversal, missing-placeholder, and wrong-placeholder requests and verify no real subscription header is injected.
- [ ] Run `make lint`, `make test-unit`, focused race tests for refresh/bundle updates, the Gatekeeper suite, and the opt-in release gate before shipping.

## Acceptance Criteria

- `moat grant codex` creates a separate Moat-owned ChatGPT/Codex login without reading or modifying the user's normal Codex credential cache.
- Host Codex and Moat can refresh independently without invalidating one another.
- The steady-state real credential exists only in Moat's encrypted store and daemon memory; the container sees only synthetic auth values.
- The CLI's registration/restart state and the daemon's persisted run registry contain references, never subscription token values.
- Codex selects subscription authentication and completes verified streaming/WebSocket flows on an explicitly supported CLI version.
- Authorization and account identity are replaced atomically only for the exact approved HTTPS origin, canonical methods/paths, and exact placeholder pair.
- Insecure transport, ambiguous paths, adjacent paths, different origins, and missing/wrong placeholders receive no real subscription credential.
- Subscription/API-key selection follows effective grants. A stored but ungranted subscription is never staged or installed, and both grants do not silently move Codex onto API billing.
- Active runs receive persisted token rotations without restart; daemon restart resolves the latest encrypted token rather than replaying a stale snapshot.
- A pre-feature or downgraded daemon cannot interpret `codex-subscription-v1` as an API key or install it host-wide.
- Existing `openai` API-key users, `agents: [codex]`, and `agents: [openai]` continue to work as documented.
- Unsupported Codex versions, custom/managed ChatGPT origins, keyring-forced temporary login, revoked auth, and missing daemon capabilities fail early with actionable errors.

## Out of Scope

- Depending on or launching `codex-auth-proxy`.
- Copying/importing the user's active Codex refresh token.
- Preventing another process inside an authorized run from exercising the run's Codex capability.
- Multi-account selection, quota polling, account failover, usage dashboards, or quota-based retries.
- Exposing subscription credentials to OpenAI SDKs, MCP servers, custom providers, or custom base URLs.
- Non-default, managed, or data-residency ChatGPT origins in v1.
- Direct OAuth implementation with no transient host auth file.
- Supporting unverified Codex CLI versions or silently adapting to auth-schema drift.

## References

- OpenAI Codex authentication and credential storage: <https://learn.chatgpt.com/docs/auth>
- OpenAI Codex non-interactive authentication: <https://learn.chatgpt.com/docs/non-interactive-mode>
- OpenAI Codex advanced configuration: <https://learn.chatgpt.com/docs/config-file/config-advanced>
- Codex v0.154 auth modes: <https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/protocol/src/auth.rs>
- Codex v0.154 token parsing: <https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/login/src/token_data.rs>
- Codex v0.154 auth management/refresh: <https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/login/src/auth/manager.rs>
- Codex v0.154 auth storage: <https://github.com/openai/codex/blob/rust-v0.154.0/codex-rs/login/src/auth/storage.rs>
- `codex-auth-proxy` upstream behavior (reference only): <https://github.com/he426100/codex-auth-proxy/blob/main/src/Proxy/UpstreamHeaderFactory.php>
- `codex-auth-proxy` target mapping (reference only): <https://github.com/he426100/codex-auth-proxy/blob/main/src/Proxy/UpstreamTarget.php>
- `codex-auth-proxy` token refresh (reference only): <https://github.com/he426100/codex-auth-proxy/blob/main/src/Auth/TokenRefresher.php>
