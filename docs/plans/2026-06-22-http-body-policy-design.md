# HTTP request-body inspection for Keep policies

**Status:** Design approved (pending written-spec review)
**Date:** 2026-06-22
**Scope:** Expose parsed JSON request bodies to `http`-scope Keep policies as `params.body`, so rules can match on body content (exact-field REST checks and secret-bearing-field exfil denylists) instead of only host/method/path. Robust GraphQL mutation blocking is **not** a v1 goal (see Decisions) — it's a post-v1 fast-follow.

## Problem

Today Keep's HTTP enforcement sees only method/host/path. The call handed to the
engine is built with `keeplib.NewHTTPCall(method, host, path)`, which populates
`params.method/host/path` and nothing else (gatekeeper `proxy.go`). This means:

- GraphQL mutation denylists are impossible (the operation lives in the JSON body).
- Content-based REST exfil denylists are impossible (body invisible).
- Only coarse host/method/path allow/deny is available.

Request bodies *are* captured for observability (`storage.RequestBody`), but that
path is logging-only and never reaches policy evaluation.

## Goal (scope decision: generic surface)

A **generic** `params.body` CEL surface available to any `http`-scope policy.
v1 ships the primitive; users author rules against it. We pair it with an
*illustrative* GitHub-hardening example and commit to a **curated, tested,
maintained pack as a post-v1 fast-follow** (see Fast-follows). Rationale for
generic-first: the primitive is the reusable foundation every pack builds on, and
a curated pack authored before the primitives are proven (whole-body `hasSecrets`,
GraphQL semantics) would assert protection it can't yet deliver. The maintained
pack — where the "asserted-correct, safe-by-default" value lives — follows once
the primitives are solid.

Non-goals (v1):
- Robust/semantic GraphQL mutation blocking — deferred to the `graphqlOps()`
  upstream helper (fast-follow). The brittle substring example ships only as
  explicitly experimental.
- A maintained GitHub starter pack (fast-follow; the v1 `examples/` file is
  illustrative, not a maintained security control).
- Body-aware `moat.yaml` inline shorthand (`deny: [op]` stays path-only; body
  rules require file/pack policies with CEL `when:` clauses).
- Request-body rules on MCP (MCP already passes full `params`).

## Design

### Variable shape

Parsed body lands at `params.body` (CEL dynamic value). The engine exposes
`call.Params` as `params.*` and supports nested navigation (`params.body.x.y`)
and redaction. **`hasSecrets` in keep 0.5.0 has only string overloads** — it
matches a scalar field, and `hasSecrets(params.body)` over a parsed JSON object
compiles but always returns `false` (silently never denies). v1 therefore takes a
**keep dependency: add a map/list `hasSecrets` overload that recurses** over
objects/arrays so `hasSecrets(params.body)` works as written (ships in the next
keep release — see Component breakdown). Until that overload lands, rules must
enumerate scalar fields explicitly. Example rules:

```yaml
# REST exfil denylist — whole-body secret scan (requires the keep map/list
# hasSecrets overload; until it lands, enumerate scalar fields instead).
# NOTE: match the host in `when`, not via an `operation:` glob — keep lowercases
# the operation ("post host/path") and matches with path.Match (`*` won't cross
# `/`), so "POST api.github.com/*" never matches. See "Operation matching" below.
- name: deny-secret-in-body
  match: { when: "params.host == 'api.github.com' && params.body != null && hasSecrets(params.body)" }
  action: deny

# GraphQL mutation denylist (EXPERIMENTAL brittle stopgap — NOT a sole control;
# robust form is the post-v1 graphqlOps() helper; see Caveats)
- name: deny-graphql-mutation
  match: { when: "params.host == 'api.github.com' && params.path == '/graphql' && params.body != null && params.body.query.contains('mutation')" }
  action: deny
```

**Operation matching:** Keep matches `operation` case-insensitively against the
runtime string `"<method> <host><path>"` (method lowercased) using `path.Match`,
where `*` does not cross `/`. HTTP body rules therefore match on the lowercased
`params.host`/`params.method`/`params.path` in `when` and omit `operation`
(empty `operation` is a catch-all). A regression test
(`internal/keep`, `TestExamplePolicyBodyEnforces`) loads the shipped example and
asserts it actually denies a secret body — the operation-match + body-eval path
that v1 originally shipped broken.

Authoring note: `has(params.body)` is **always true** for body-carrying calls
(even a nil body), so test for a populated body with `params.body != null`, not
`has()`. An empty/whitespace request body (e.g. chunked with no payload) is
passed as `nil`, so `params.body != null` is false for it and a body rule will
not match. Because a rule that compiles can still evaluate falsy and fail open,
authors must validate that a rule actually *denies* a matching request — not just
that the policy loads.

### Trigger (when we buffer)

Body buffering is conditional on `Engine.RequiresBody(scope) bool` (keep 0.5.0),
which is true iff any rule in the scope references `params.body`. gatekeeper only
buffers + parses when this is true. Policies with no body rules stay zero-cost;
bodyless requests (GET, empty POST) never pay. `RequiresBody` returns `true` for
an unknown scope, so a scope-name mismatch fails safe (buffers) rather than
silently skipping body rules.

### Failure matrix (fail-closed)

Engaged only when `RequiresBody` is true:

| Request | Behavior |
|---|---|
| No body / empty body | Use `NewHTTPCall` (no `params.body`); rules referencing it resolve falsy. **Not denied.** |
| `application/json`, parses, ≤ `MaxBodySize` | `NewHTTPCallWithBody` with parsed value → evaluate. Body re-attached to `req` for upstream. |
| `application/json`, malformed **or** > `MaxBodySize` | **Deny** (can't inspect a body that must be inspected). |
| `application/json` with duplicate keys | **Deny** — ambiguous parse; gatekeeper rejects after `json.Unmarshal`. |
| Any `Content-Encoding` (e.g. gzip) | **Deny** — gatekeeper does not decode encoded request bodies, so a client that gzips a JSON POST to a policed host is blocked. |
| Non-JSON body (octet-stream, multipart, …) | **Deny** — an uninspectable body can't be allowed past a body rule. (Decision: option (a), most secure.) |

**Scope-global, not per-host (important).** The trigger is `RequiresBody("http")`,
true if *any* rule in the `http` scope references `params.body`. Once true, the
deny rows above apply to **every host in the `http` scope** — gatekeeper denies the
non-JSON / encoded / duplicate-key / oversized request *before* any per-host `when`
clause runs. So adding one GraphQL body rule for `api.github.com` will start denying
multipart uploads, octet-stream pushes, and gzip'd payloads to *unrelated* policed
hosts. Scope body rules deliberately, and warn operators that introducing any body
rule converts the whole http scope to JSON-only, uncompressed bodies.

### Component breakdown

**keep (`github.com/majorcontext/keep`) — DONE, v0.6.0** (moat pins v0.6.0)
- `NewHTTPCallWithBody(method, host, path string, body any)` — sets `params.body`.
  `body` is `any` (object/array/scalar); unmarshal into an `any`. *(v0.5.0)*
- `Engine.RequiresBody(scope) bool` — trigger signal. *(v0.5.0)*
- **Breaking:** `Evaluate`/`SafeEvaluate` now take a leading `context.Context`. *(v0.5.0)*
- map/list `hasSecrets` overload that recurses over objects/arrays so
  `hasSecrets(params.body)` works as a whole-body scan. *(v0.6.0 — the v1 dependency,
  now shipped and pinned in go.mod)*

**gatekeeper (`github.com/majorcontext/gatekeeper`) — DONE, v0.13.0**
- In the CONNECT + TLS-interception path (`handleConnectWithInterception` →
  `buildHTTPCall`), before `RoundTrip`: when an `http`-scope engine exists and
  `eng.RequiresBody("http")`, gate on `Content-Type`, read+buffer up to
  `MaxBodySize`, `json.Unmarshal` into `any`, build the call via
  `NewHTTPCallWithBody`, and replace `req.Body` with a fresh reader. Apply the
  failure matrix. Reuses the existing `MaxBodySize` and fail-closed deny path.
- **HTTPS-only (scope limit).** Body inspection runs only on the intercepted
  CONNECT/TLS path. The plain-HTTP handler (`handleHTTP`) does **not** call any
  Keep engine, so plain `http://` requests bypass body rules entirely. This is
  tolerable only because the network policy is expected to disallow plaintext
  egress to sensitive hosts — but it MUST be documented (see Caveats). Covering
  plain HTTP would be a separate gatekeeper change.

**moat (this repo) — TO DO (small)**
1. `go.mod`: bump `keep` → v0.5.0, `gatekeeper` → v0.13.0 (+ transitive bumps).
2. `internal/keep/evaluate.go`: thread `context.Context` through the `SafeEvaluate`
   wrapper and the `Normalize*` helpers (and their tests). This is the **only**
   compile break from the bump — the rest of gatekeeper's API surface moat uses
   is stable across 0.2.0 → 0.13.0.
3. **Daemon capability gate** (version-skew safety, per the daemon back-compat
   invariant in CLAUDE.md):
   - `internal/daemon/server.go`: advertise a new capability `"keep-body-policy"`
     alongside `"keep-policy"` / `"host-gateway-v2"`.
   - `internal/run/manager.go` (~L1035): when a resolved policy references
     `params.body`, require the `keep-body-policy` capability; otherwise error
     with the existing "run `moat proxy restart` to upgrade" guidance. Detect
     body-usage by compiling the policy locally (`keeplib.LoadFromBytes`) and
     calling `Engine.RequiresBody` during the validation step that already runs
     `ValidateRuleBytes`. **Pass the same scope key used for that policy's daemon
     registration** — `"http"` for `network.keep_policy`, `"mcp-"+name` for MCP,
     `"llm-gateway"` for the gateway — NOT a hardcoded `"http"`. `RequiresBody`
     returns `true` for an unknown scope, so calling it with the wrong scope on a
     non-body policy spuriously demands the capability and hard-blocks the run.
     Note file policies pass their declared scope through verbatim (only inline/
     pack policies have their scope rewritten), so an http body **file** policy
     must declare `scope: http` to match how gatekeeper calls `RequiresBody("http")`
     / `Evaluate(ctx, call, "http")`.
   - Rationale: an older daemon built with old gatekeeper would silently ignore
     `params.body` rules and **under-enforce** — a security regression. The gate
     converts silent under-enforcement into a clear upgrade error.
4. **Docs:**
   - `docs/content/reference/02-moat-yaml.md`: body rules, JSON-only, fail-closed,
     scope-global non-JSON deny, uncovered channels, `params.body != null` idiom.
   - `CHANGELOG.md` entry (Added) with PR link.
   - Example policy file under `examples/` — labeled **illustrative, not a
     maintained security pack** (the maintained pack is a fast-follow).

## Caveats

- **GraphQL mutation detection is brittle in v1 — not a sole security control.**
  `params.body.query.contains('mutation')` false-positives on field names / string
  literals and false-negatives on aliased or whitespace-normalized operations. Use
  it for defense-in-depth paired with host/path rules, never as the only gate. A
  robust denylist needs a GraphQL-aware CEL helper (e.g. `graphqlOps(params.body.query)`
  returning operation types) — proposed as a future **upstream Keep function**, not
  moat code. Resolved: GraphQL is **not** a v1 goal — the example ships
  experimental-only; robust blocking waits for `graphqlOps()` (see Fast-follows).
- **Adding any body rule makes the whole http scope JSON-only.** Per the failure
  matrix, once one rule references `params.body` the http scope denies non-JSON,
  `Content-Encoding`'d, duplicate-key, malformed, and oversized bodies for *all*
  hosts — not just the targeted host. Most-secure default for v1; revisit if too
  aggressive.
- **Body inspection does not cover all egress channels.** `params` exposes only
  `method`, `host`, `path`, and `body` — it does **not** see URL query parameters
  (`GET ?secret=…`), request headers, response bodies, WebSocket frames, or
  non-HTTP egress. `hasSecrets(params.body)` gives no protection against query- or
  header-based exfil. Pair body rules with strict host/path rules and a restrictive
  network policy as the primary exfil control. Body policies are a narrow opt-in
  hardening primitive, **not** a completeness-guaranteeing DLP control.

## Testing

- `internal/keep`: ctx-signature wrapper tests updated; `RequiresBody`-driven
  branch covered both directions (policy with body rule → requires; without → not).
- Capability gate: companion-case coverage per CLAUDE.md invariant #1 — body-policy
  + capability-present → allowed; body-policy + capability-absent → clear error;
  non-body policy + capability-absent → still allowed.
- E2E (optional): a body-denylist policy blocks a matching JSON POST and allows a
  non-matching one.

## Decisions (resolved 2026-06-22 doc review)

- **GraphQL mutation blocking is not a v1 goal.** The substring match is too leaky
  to claim as a delivered control; it ships as an explicitly experimental example.
  Robust blocking is the `graphqlOps()` fast-follow.
- **Generic-first; curated pack is a fast-follow.** v1 ships the primitive plus an
  illustrative example; a maintained, tested pack follows once primitives are solid.
- **Fix whole-body `hasSecrets` upstream.** Add a map/list overload in keep (v1
  dependency) so `hasSecrets(params.body)` works rather than forcing field
  enumeration.

## Fast-follows (post-v1)

- **`graphqlOps(params.body.query)` upstream Keep helper** — returns operation
  types for policy-grade GraphQL mutation blocking; promotes the experimental
  example to a real control.
- **Maintained, tested GitHub-hardening pack** — promote the illustrative
  `examples/` file to an owned, asserted-correct starter pack with companion-case
  tests.
