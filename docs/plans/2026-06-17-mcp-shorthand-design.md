# Declarative MCP shorthand — design

**Date:** 2026-06-17
**Status:** Approved, ready for implementation plan

## Problem

Adding a well-known MCP server (Linear, Notion, PostHog, …) to an agent takes
more steps than it should. Today a user must:

1. Run `moat grant oauth linear` — easy; the registry already auto-discovers the
   OAuth endpoints from the server URL.
2. Hand-edit `moat.yaml` and repeat information the system already knows:

   ```yaml
   mcp:
     - name: linear
       url: https://mcp.linear.app/mcp   # registry already knows this
       auth:
         grant: oauth:linear              # always oauth:<name>
         header: Authorization            # almost always this
   ```

The user knows exactly one thing: the **service name**. Everything else (URL,
auth method, header, grant wiring) is derivable. The registry already maps
`name → url` for the OAuth grant flow; the config layer just doesn't use it.

There are also three overlapping "known service" systems a user shouldn't have
to distinguish between: the OAuth registry (`oauth/registry.yaml`), the API-key
config-provider defaults (`configprovider/defaults/*.yaml`), and manual
`moat grant mcp`. This design addresses only the MCP server path.

## Goal

Let a user add a known MCP server by name alone:

```yaml
mcp:
  - linear
  - notion
  - posthog
```

…and have `name + url + auth-config + required-grant` resolved from a registry.
Credentials continue to come from the existing `moat grant oauth <name>` flow.

## Non-goals (explicit scope boundaries)

- **API-key config-providers** (Sentry, Datadog, etc.) — these inject auth for
  the agent's own API calls to plain hosts; they are not MCP servers. Untouched.
- **Prompt to grant a missing credential at launch** — a separate, generic
  feature that applies to *any* grant, not just MCP. Tracked as a follow-up PR;
  nothing is designed for it here. Until it lands, a missing grant surfaces as
  the existing helpful error at run time.
- No new `moat mcp add` command in this PR. Declarative config is the surface.

## Design

### 1. Config syntax

`mcp:` list items become polymorphic — each item is either a **bare string**
(simplest case) or a **map** (when overrides or a policy are needed). The full
explicit form continues to work unchanged.

```yaml
mcp:
  - linear                       # bare string → {name: linear}
  - notion
  - name: posthog                # map form, for...
    policy: posthog-readonly     #   ...a keep policy or any field override
  - name: acme                   # fully custom, unknown to the registry
    url: https://mcp.acme.com/mcp
    auth: { grant: oauth:acme, header: Authorization }
```

Implemented via a custom `UnmarshalYAML` on `MCPServerConfig`:

- Scalar YAML node → `MCPServerConfig{Name: <string>}`.
- Mapping YAML node → decoded as today (into a type alias to avoid recursion).

Existing full entries are unaffected — **fully backward compatible**.

### 2. Registry / catalog

The well-known-server registry currently lives at
`internal/providers/oauth/registry.yaml` as `map[string]string` (`name → url`)
and is consumed by `moat grant oauth`. Two changes:

**a. Move it to a leaf package `internal/mcpcatalog`.**
`internal/providers/oauth` imports `internal/config`, so `internal/config`
cannot import `oauth` back (cycle). The catalog must be a dependency-free leaf
that both `config` (resolution) and `providers/oauth` (discovery) can import. It
returns plain strings (url / grant / header) and never references `config`
types, keeping it a true leaf.

**b. Make registry values polymorphic.** A plain string keeps its current
meaning — an OAuth server (grant `oauth:<name>`, header `Authorization`). An
object lets a non-OAuth MCP specify its own auth:

```yaml
linear: https://mcp.linear.app/mcp        # ⇒ url + grant oauth:linear + header Authorization
notion: https://mcp.notion.com/mcp
posthog: https://mcp.posthog.com/mcp
context7:                                  # api-key MCP — explicit auth
  url: https://mcp.context7.com/mcp
  auth: { grant: mcp-context7, header: CONTEXT7_API_KEY }
```

Catalog API (names illustrative):

```go
type Entry struct {
    URL    string
    Grant  string  // e.g. "oauth:linear" or "mcp-context7"
    Header string  // e.g. "Authorization"
}

// Lookup returns the catalog entry for a name, ok=false if unknown.
func Lookup(name string) (Entry, bool)
```

For a string-valued registry entry `name: url`, `Lookup` synthesizes
`Entry{URL: url, Grant: "oauth:"+name, Header: "Authorization"}`. A custom
`UnmarshalYAML` on the value type accepts either a scalar or an object.

`oauth.LookupServerURL` is preserved (delegates to `mcpcatalog.Lookup(...).URL`)
so the `moat grant oauth` behavior is unchanged.

### 3. Resolution + validation

A normalization pass runs **after YAML parse, before validation** (so the rest
of the codebase continues to see fully-populated `MCPServerConfig` values):

For each `mcp` entry:
1. If both `url` and `auth` are already set explicitly, leave it (custom server).
2. Otherwise `Lookup(entry.Name)`:
   - Found → fill any **omitted** fields (`url`, `auth.grant`, `auth.header`)
     from the entry. Explicitly-written fields always win.
   - Not found **and** `url` empty → error:
     `unknown MCP server "foo": provide a url, or use a known name (linear, notion, posthog, …)`.

After resolution, the existing `ValidateMCP` runs against the completed struct
(it still requires `url` and, when `auth` is present, `auth.header` — now
satisfied by resolution).

`appendMCPGrants` already auto-registers each `mcp[].auth.grant` into the
grants list, so there is no separate `grants:` entry to write. If the required
grant is absent at run time, the existing error path fires (auto-prompt is the
separate follow-up).

### 4. `moat grant oauth` printed snippet

With shorthand available, the command stops printing the 6-line `mcp:` block and
prints the shorthand instead:

```
Use in moat.yaml:

mcp:
  - linear
```

This closes the loop on the earlier snippet bug (the omitted `auth.header`) by
removing the hand-written auth block entirely.

## Affected code

- `internal/mcpcatalog/` (new leaf package): `registry.yaml` (moved + enriched),
  `Entry`, `Lookup`, polymorphic value `UnmarshalYAML`.
- `internal/providers/oauth/registry.go`: delegate `LookupServerURL` to the
  catalog; drop the embedded `registry.yaml` here.
- `internal/config/config.go`: polymorphic `MCPServerConfig.UnmarshalYAML`;
  resolution pass; updated `ValidateMCP` error messaging for unknown names.
- `cmd/moat/cli/grant_oauth.go`: print the shorthand snippet.
- Docs: `docs/content/guides/09-mcp.md`, `docs/content/reference/02-moat-yaml.md`,
  `docs/content/reference/01-cli.md`.
- `CHANGELOG.md`: Added entry.

## Testing

- **Catalog:** value unmarshal for both string and object forms; `Lookup`
  synthesizes oauth defaults for string entries; unknown name returns ok=false.
- **Config unmarshal:** bare string → `{Name}`; map form decodes; mixed list.
- **Resolution:** bare string resolves url+auth; name-only map + `policy`
  preserves policy; explicit field overrides registry; unknown name with no url
  errors with the actionable message; custom full entry passes through untouched.
- **Validation:** resolved entry passes `ValidateMCP`.
- **Snippet:** `grant oauth` prints the shorthand form.
- Backward compat: existing full `mcp:` entries and existing
  `oauth/registry.yaml` string entries behave identically.

## Decisions locked during brainstorming

- Syntax: **bare string + name-only map** (not map-only).
- Registry: **polymorphic value**, string = OAuth default, object = explicit auth.
- Registry **moved to `internal/mcpcatalog`** (leaf, avoids import cycle).
- **`context7` migrated** into the registry as the first object-form entry, so
  the object path ships exercised.
