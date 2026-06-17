# Declarative MCP Shorthand Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users add a well-known MCP server to `moat.yaml` by name alone (`mcp: [linear, notion, posthog]`), resolving url + auth-config + required-grant from a registry.

**Architecture:** A new leaf package `internal/mcpcatalog` owns the (enriched, polymorphic) well-known-server registry. `internal/config` gains a polymorphic `MCPServerConfig` unmarshaler (bare string or map) plus a resolution pass that fills omitted url/auth from the catalog before validation. `internal/providers/oauth` delegates its existing `LookupServerURL` to the catalog (behavior unchanged). `moat grant oauth` prints the new shorthand snippet.

**Tech Stack:** Go, `gopkg.in/yaml.v3` (custom `UnmarshalYAML`), `go:embed`, standard `testing`.

**Coordination note (rebase):** This branch is off `main`, which lacks PR #382 (adds `posthog` to `oauth/registry.yaml` and fixes the `grant oauth` snippet's missing `auth.header`). This plan **moves** `registry.yaml` out of `oauth/` and **rewrites** the snippet print path, so both files will conflict on rebase. Resolution when rebasing: (a) add the `posthog: https://mcp.posthog.com/mcp` line to the new `internal/mcpcatalog/registry.yaml`; (b) keep this plan's shorthand snippet (it supersedes the header fix). This plan does **not** add `posthog` itself, to keep the two PRs independent.

---

## File Structure

- **Create** `internal/mcpcatalog/catalog.go` — `Entry` type, embedded registry, polymorphic value unmarshal, `Lookup`, `Names`.
- **Create** `internal/mcpcatalog/registry.yaml` — moved from `internal/providers/oauth/registry.yaml`, enriched with `context7` object entry.
- **Create** `internal/mcpcatalog/catalog_test.go` — unmarshal + lookup tests.
- **Modify** `internal/providers/oauth/registry.go` — delete embed + `registry` var + `init`; `LookupServerURL` delegates to `mcpcatalog`.
- **Delete** `internal/providers/oauth/registry.yaml` (moved).
- **Modify** `internal/providers/oauth/registry_test.go` — drop `TestRegistryNotEmpty` (its `registry` var is gone); keep `TestLookupServerURL`.
- **Modify** `internal/config/config.go` — `MCPServerConfig.UnmarshalYAML`; `resolveMCPShorthand`; call it in `Load`; reword unknown-name error.
- **Modify** `internal/config/config_test.go` (or new `mcp_shorthand_test.go`) — resolution tests.
- **Modify** `cmd/moat/cli/grant_oauth.go:179-180` — print shorthand snippet.
- **Modify** docs: `docs/content/guides/09-mcp.md`, `docs/content/reference/02-moat-yaml.md`, `docs/content/reference/01-cli.md`.
- **Modify** `CHANGELOG.md`.

---

## Task 1: Create the `mcpcatalog` leaf package

**Files:**
- Create: `internal/mcpcatalog/registry.yaml`
- Create: `internal/mcpcatalog/catalog.go`
- Test: `internal/mcpcatalog/catalog_test.go`

- [ ] **Step 1: Create the registry data file**

Create `internal/mcpcatalog/registry.yaml` (content copied from the current `internal/providers/oauth/registry.yaml`, with the `context7` object entry added):

```yaml
# Well-known MCP server catalog for OAuth auto-discovery and config shorthand.
#
# When a user runs `moat grant oauth <name>` or lists a bare `<name>` under
# `mcp:` in moat.yaml, the name is matched against this catalog.
#
# A plain string value is an OAuth server: it resolves to that URL, grant
# `oauth:<name>`, and header `Authorization`. An object value specifies its
# own auth (e.g. an API-key MCP server).

asana: https://mcp.asana.com/mcp
cloudflare: https://mcp.cloudflare.com/mcp
context7:
  url: https://mcp.context7.com/mcp
  auth:
    grant: mcp-context7
    header: CONTEXT7_API_KEY
hubspot: https://mcp.hubspot.com
linear: https://mcp.linear.app/mcp
notion: https://mcp.notion.com/mcp
stripe: https://mcp.stripe.com
```

- [ ] **Step 2: Write the failing test**

Create `internal/mcpcatalog/catalog_test.go`:

```go
package mcpcatalog

import (
	"reflect"
	"testing"
)

func TestLookup(t *testing.T) {
	tests := []struct {
		name  string
		want  Entry
		wantOK bool
	}{
		// String entry → OAuth defaults synthesized.
		{"linear", Entry{URL: "https://mcp.linear.app/mcp", Grant: "oauth:linear", Header: "Authorization"}, true},
		{"notion", Entry{URL: "https://mcp.notion.com/mcp", Grant: "oauth:notion", Header: "Authorization"}, true},
		// Object entry → explicit auth preserved, no defaulting.
		{"context7", Entry{URL: "https://mcp.context7.com/mcp", Grant: "mcp-context7", Header: "CONTEXT7_API_KEY"}, true},
		// Unknown.
		{"nonexistent", Entry{}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := Lookup(tt.name)
			if ok != tt.wantOK {
				t.Fatalf("Lookup(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Lookup(%q) = %+v, want %+v", tt.name, got, tt.want)
			}
		})
	}
}

func TestNamesSortedAndNonEmpty(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() is empty")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] > names[i] {
			t.Errorf("Names() not sorted: %q before %q", names[i-1], names[i])
		}
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `go test ./internal/mcpcatalog/`
Expected: FAIL — package/`Entry`/`Lookup`/`Names` not defined (build error).

- [ ] **Step 4: Write the implementation**

Create `internal/mcpcatalog/catalog.go`:

```go
// Package mcpcatalog holds the registry of well-known MCP servers, used both
// for `moat grant oauth` auto-discovery and for resolving bare `mcp:` names in
// moat.yaml. It is a dependency-free leaf package so config and provider
// packages can import it without an import cycle.
package mcpcatalog

import (
	_ "embed"
	"sort"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

// Entry is a resolved well-known MCP server.
type Entry struct {
	URL    string
	Grant  string
	Header string
}

// rawEntry is the on-disk YAML value: either a scalar URL string (an OAuth
// server) or a mapping with an explicit url and auth block.
type rawEntry struct {
	URL  string `yaml:"url"`
	Auth struct {
		Grant  string `yaml:"grant"`
		Header string `yaml:"header"`
	} `yaml:"auth"`
}

func (r *rawEntry) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&r.URL)
	}
	type alias rawEntry
	return node.Decode((*alias)(r))
}

var registry map[string]rawEntry

func init() {
	registry = make(map[string]rawEntry)
	if err := yaml.Unmarshal(registryData, &registry); err != nil {
		// Embedded data is compile-time constant — a parse failure is a bug.
		panic("mcpcatalog: invalid registry.yaml: " + err.Error())
	}
}

// Lookup returns the resolved entry for a name, ok=false if unknown. String
// (OAuth) entries default to grant "oauth:<name>" and header "Authorization".
func Lookup(name string) (Entry, bool) {
	r, ok := registry[name]
	if !ok {
		return Entry{}, false
	}
	e := Entry{URL: r.URL, Grant: r.Auth.Grant, Header: r.Auth.Header}
	if e.Grant == "" {
		e.Grant = "oauth:" + name
	}
	if e.Header == "" {
		e.Header = "Authorization"
	}
	return e, true
}

// Names returns the sorted list of known server names (for error messages).
func Names() []string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 5: Run the test to verify it passes**

Run: `go test ./internal/mcpcatalog/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/mcpcatalog/
git commit -m "feat(mcpcatalog): add well-known MCP server catalog leaf package"
```

---

## Task 2: Point `oauth` at the catalog; remove the duplicate registry

**Files:**
- Modify: `internal/providers/oauth/registry.go`
- Delete: `internal/providers/oauth/registry.yaml`
- Modify: `internal/providers/oauth/registry_test.go`

- [ ] **Step 1: Replace `registry.go` with a delegating implementation**

Replace the entire contents of `internal/providers/oauth/registry.go` with:

```go
package oauth

import (
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/mcpcatalog"
)

// LookupServerURL returns the well-known MCP server URL for a named OAuth
// grant, or "" if the name is not in the catalog.
func LookupServerURL(name string) string {
	e, ok := mcpcatalog.Lookup(name)
	if !ok {
		log.Debug("no catalog entry for OAuth name", "name", name)
		return ""
	}
	return e.URL
}
```

- [ ] **Step 2: Delete the moved registry data file**

Run:
```bash
git rm internal/providers/oauth/registry.yaml
```

- [ ] **Step 3: Update `registry_test.go`**

In `internal/providers/oauth/registry_test.go`, delete the `TestRegistryNotEmpty` function entirely (it references the now-removed package-level `registry` variable). Keep `TestLookupServerURL` unchanged — it still exercises the delegated path. The file's `TestLookupServerURL` table currently is:

```go
func TestLookupServerURL(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{"notion", "https://mcp.notion.com/mcp"},
		{"linear", "https://mcp.linear.app/mcp"},
		{"cloudflare", "https://mcp.cloudflare.com/mcp"},
		{"hubspot", "https://mcp.hubspot.com"},
		{"stripe", "https://mcp.stripe.com"},
		{"asana", "https://mcp.asana.com/mcp"},
		{"nonexistent", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LookupServerURL(tt.name)
			if got != tt.want {
				t.Errorf("LookupServerURL(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}
```

Leave it as-is after removing `TestRegistryNotEmpty`.

- [ ] **Step 4: Verify the oauth package builds and tests pass**

Run: `go test ./internal/providers/oauth/`
Expected: PASS.

- [ ] **Step 5: Verify nothing else referenced the old registry internals**

Run: `grep -rn "registryData\|LookupServerURL\|oauth.registry" internal/ cmd/ --include=*.go | grep -v _test`
Expected: only the new delegating `LookupServerURL` in `oauth/registry.go` and its callsite in `cmd/moat/cli/grant_oauth.go`. No references to a package-level `registry`/`registryData` outside `mcpcatalog`.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/oauth/
git commit -m "refactor(oauth): delegate server-URL lookup to mcpcatalog"
```

---

## Task 3: Polymorphic `MCPServerConfig` unmarshal (bare string or map)

**Files:**
- Modify: `internal/config/config.go` (near the `MCPServerConfig` type, ~line 146)
- Test: `internal/config/mcp_shorthand_test.go` (new)

- [ ] **Step 1: Write the failing test**

Create `internal/config/mcp_shorthand_test.go`:

```go
package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestMCPServerConfigUnmarshal_BareString(t *testing.T) {
	var c struct {
		MCP []MCPServerConfig `yaml:"mcp"`
	}
	src := "mcp:\n  - linear\n  - name: acme\n    url: https://mcp.acme.com/mcp\n"
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(c.MCP) != 2 {
		t.Fatalf("got %d entries, want 2", len(c.MCP))
	}
	if c.MCP[0].Name != "linear" || c.MCP[0].URL != "" {
		t.Errorf("bare string entry = %+v, want {Name:linear}", c.MCP[0])
	}
	if c.MCP[1].Name != "acme" || c.MCP[1].URL != "https://mcp.acme.com/mcp" {
		t.Errorf("map entry = %+v, want name=acme url set", c.MCP[1])
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/config/ -run TestMCPServerConfigUnmarshal_BareString -v`
Expected: FAIL — bare string `- linear` produces a YAML type error (cannot unmarshal `!!str` into struct), so the test errors on unmarshal.

- [ ] **Step 3: Add the custom unmarshaler**

In `internal/config/config.go`, immediately after the `MCPServerConfig` struct definition (currently ends at line 151), add:

```go
// UnmarshalYAML lets an mcp[] entry be either a bare service name (string) or a
// full mapping. A bare string resolves its url/auth from the well-known catalog
// during config load (see resolveMCPShorthand).
func (m *MCPServerConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&m.Name)
	}
	// Use an alias to decode the mapping without recursing into this method,
	// while preserving the nested unmarshalers for Auth and Policy.
	type alias MCPServerConfig
	return node.Decode((*alias)(m))
}
```

Confirm `gopkg.in/yaml.v3` is already imported in `config.go` (it is — used at line 493). No new import needed.

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/config/ -run TestMCPServerConfigUnmarshal_BareString -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/config/config.go internal/config/mcp_shorthand_test.go
git commit -m "feat(config): accept bare MCP server name in mcp: list"
```

---

## Task 4: Resolution pass + unknown-name error

**Files:**
- Modify: `internal/config/config.go` (add `resolveMCPShorthand`; call in `Load` after line 495; reword `validateTopLevelMCPServerSpec`)
- Test: `internal/config/mcp_shorthand_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/config/mcp_shorthand_test.go`:

```go
func loadConfigFromString(t *testing.T, src string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(src), 0o644); err != nil {
		t.Fatalf("write moat.yaml: %v", err)
	}
	return Load(dir)
}

func TestResolveMCPShorthand_BareNameResolves(t *testing.T) {
	cfg, err := loadConfigFromString(t, "mcp:\n  - linear\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.MCP[0]
	if m.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("url = %q, want linear MCP url", m.URL)
	}
	if m.Auth == nil || m.Auth.Grant != "oauth:linear" || m.Auth.Header != "Authorization" {
		t.Errorf("auth = %+v, want oauth:linear / Authorization", m.Auth)
	}
}

func TestResolveMCPShorthand_PolicyPreserved(t *testing.T) {
	cfg, err := loadConfigFromString(t, "mcp:\n  - name: linear\n    policy: linear-readonly\n")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	m := cfg.MCP[0]
	if m.URL != "https://mcp.linear.app/mcp" {
		t.Errorf("url not resolved: %q", m.URL)
	}
	if m.Policy == nil {
		t.Error("policy was dropped during resolution")
	}
}

func TestResolveMCPShorthand_ExplicitFieldsWin(t *testing.T) {
	src := "mcp:\n  - name: linear\n    url: https://custom.example.com/mcp\n"
	cfg, err := loadConfigFromString(t, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP[0].URL != "https://custom.example.com/mcp" {
		t.Errorf("explicit url overridden: %q", cfg.MCP[0].URL)
	}
}

func TestResolveMCPShorthand_UnknownNameErrors(t *testing.T) {
	_, err := loadConfigFromString(t, "mcp:\n  - bogusservice\n")
	if err == nil {
		t.Fatal("expected error for unknown bare name, got nil")
	}
	if !strings.Contains(err.Error(), "bogusservice") || !strings.Contains(err.Error(), "known name") {
		t.Errorf("error = %q, want it to name the service and known names", err.Error())
	}
}

func TestResolveMCPShorthand_CustomFullEntryUntouched(t *testing.T) {
	src := "mcp:\n  - name: acme\n    url: https://mcp.acme.com/mcp\n    auth:\n      grant: oauth:acme\n      header: Authorization\n"
	cfg, err := loadConfigFromString(t, src)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCP[0].Auth.Grant != "oauth:acme" {
		t.Errorf("custom auth mutated: %+v", cfg.MCP[0].Auth)
	}
}
```

Add the needed imports to the top of `mcp_shorthand_test.go`: `"os"`, `"path/filepath"`, `"strings"` (keep `"testing"` and `gopkg.in/yaml.v3`).

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/config/ -run TestResolveMCPShorthand -v`
Expected: FAIL — `TestResolveMCPShorthand_BareNameResolves` fails because `Load` does not yet resolve (url empty → current validation returns `mcp[0]: 'url' is required`); unknown-name test fails because the message doesn't match.

- [ ] **Step 3: Add the resolution function**

In `internal/config/config.go`, add the import `"github.com/majorcontext/moat/internal/mcpcatalog"` to the import block, then add this function (e.g. just below `validateTopLevelMCPServerSpec`):

```go
// resolveMCPShorthand fills omitted url/auth on each mcp[] entry from the
// well-known catalog, keyed by name. Explicitly-set fields always win. A bare
// name unknown to the catalog (and with no url) is an error.
func resolveMCPShorthand(cfg *Config) error {
	for i := range cfg.MCP {
		m := &cfg.MCP[i]
		entry, ok := mcpcatalog.Lookup(m.Name)
		if !ok {
			if m.URL == "" {
				return fmt.Errorf("mcp[%d]: unknown MCP server %q: provide a url, or use a known name (%s)",
					i, m.Name, strings.Join(mcpcatalog.Names(), ", "))
			}
			continue // custom server identified by url
		}
		if m.URL == "" {
			m.URL = entry.URL
		}
		if m.Auth == nil {
			m.Auth = &MCPAuthConfig{Grant: entry.Grant, Header: entry.Header}
		} else {
			if m.Auth.Grant == "" {
				m.Auth.Grant = entry.Grant
			}
			if m.Auth.Header == "" {
				m.Auth.Header = entry.Header
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Call it during `Load`**

In `internal/config/config.go`, immediately after the `yaml.Unmarshal` block (currently lines 492-495, right before the runtime-field validation at line 497), insert:

```go
	// Resolve bare/partial mcp[] entries against the well-known catalog before
	// validation so downstream code sees fully-populated servers.
	if err := resolveMCPShorthand(&cfg); err != nil {
		return nil, err
	}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/config/ -run "TestResolveMCPShorthand|TestMCPServerConfigUnmarshal" -v`
Expected: PASS (all six).

- [ ] **Step 6: Run the full config package tests (regression)**

Run: `go test ./internal/config/`
Expected: PASS — existing full `mcp:` entries still validate unchanged.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/mcp_shorthand_test.go
git commit -m "feat(config): resolve bare mcp: names from the well-known catalog"
```

---

## Task 5: Print the shorthand snippet from `moat grant oauth`

**Files:**
- Modify: `cmd/moat/cli/grant_oauth.go:179-180`

- [ ] **Step 1: Replace the printed snippet**

In `cmd/moat/cli/grant_oauth.go`, the lines currently are:

```go
	fmt.Printf("\nUse in moat.yaml:\n\n")
	fmt.Printf("grants:\n  - oauth:%s\n\nmcp:\n  - name: %s\n    url: %s\n    auth:\n      grant: oauth:%s\n\n", name, name, serverURL, name)
```

Replace with logic that prefers the shorthand when the name is in the catalog, and falls back to the explicit block (with `header`) otherwise:

```go
	fmt.Printf("\nUse in moat.yaml:\n\n")
	if _, known := mcpcatalog.Lookup(name); known {
		fmt.Printf("mcp:\n  - %s\n\n", name)
	} else {
		fmt.Printf("mcp:\n  - name: %s\n    url: %s\n    auth:\n      grant: oauth:%s\n      header: Authorization\n\n", name, serverURL, name)
	}
```

Add `"github.com/majorcontext/moat/internal/mcpcatalog"` to the import block in `grant_oauth.go`.

- [ ] **Step 2: Build to verify it compiles**

Run: `go build ./cmd/...`
Expected: success, no errors.

- [ ] **Step 3: Manually verify the printed forms**

Run:
```bash
go vet ./cmd/moat/cli/
```
Expected: no errors. (The branch is exercised at runtime; a known name prints `mcp:\n  - <name>`, an unknown name prints the explicit block including `header: Authorization`.)

- [ ] **Step 4: Commit**

```bash
git add cmd/moat/cli/grant_oauth.go
git commit -m "feat(oauth): print mcp shorthand snippet after grant oauth"
```

---

## Task 6: Documentation + changelog

**Files:**
- Modify: `docs/content/guides/09-mcp.md`
- Modify: `docs/content/reference/02-moat-yaml.md`
- Modify: `docs/content/reference/01-cli.md`
- Modify: `CHANGELOG.md`

- [ ] **Step 1: Document shorthand in the MCP guide**

In `docs/content/guides/09-mcp.md`, in the OAuth section (after the `### Configure in moat.yaml` block around line 430-444), add a subsection:

```markdown
### Shorthand for well-known servers

For servers in Moat's built-in catalog, list the name alone — Moat fills in the
URL and auth from the catalog:

```yaml
mcp:
  - linear
  - notion
  - posthog
```

This is equivalent to the full form above. To attach a [policy](#policies) or
override a field, use the map form and omit what the catalog provides:

```yaml
mcp:
  - name: linear
    policy: linear-readonly
```

Run `moat grant oauth <name>` once to authorize; the credential is stored and
injected at run time. Servers not in the catalog still require the full
`url` + `auth` form.
```

- [ ] **Step 2: Document shorthand in the moat.yaml reference**

In `docs/content/reference/02-moat-yaml.md`, in the top-level `mcp:` section (near the example at lines 1336-1349), add a note after the existing examples:

```markdown
Each `mcp:` entry may be a bare service name (a string) when the server is in
Moat's built-in catalog (e.g. `linear`, `notion`, `posthog`). The bare name
resolves to its `url` and `auth` automatically; switch to the map form to add a
`policy` or override a field. Unknown names require an explicit `url`.
```

- [ ] **Step 3: Note the shorthand snippet in the CLI reference**

In `docs/content/reference/01-cli.md`, in the `### moat grant oauth` section (after the examples around line 631), add:

```markdown
After a successful grant for a well-known server, the command prints a ready-to-paste shorthand:

```yaml
mcp:
  - linear
```
```

- [ ] **Step 4: Add the changelog entry**

In `CHANGELOG.md`, under `## Unreleased` → `### Added`, add as the first bullet:

```markdown
- **Declarative MCP shorthand** — list a well-known MCP server in `moat.yaml` by name alone (`mcp:\n  - linear`), and Moat resolves the URL, auth header, and required grant from its built-in catalog. The map form (`- name: linear`) still works for attaching a policy or overriding fields, and unknown servers still take an explicit `url` + `auth`. `moat grant oauth` now prints this shorthand. ([#NNN](https://github.com/majorcontext/moat/pull/NNN))
```

(Replace `#NNN` with the PR number once the PR is opened.)

- [ ] **Step 5: Verify docs build / links**

Run: `grep -rn "mcp:" docs/content/guides/09-mcp.md | head`
Expected: the new shorthand block is present. Visually confirm code fences are balanced.

- [ ] **Step 6: Commit**

```bash
git add docs/ CHANGELOG.md
git commit -m "docs(mcp): document declarative MCP shorthand"
```

---

## Task 7: Full verification

- [ ] **Step 1: Build everything**

Run: `go build ./...`
Expected: success.

- [ ] **Step 2: Run unit tests with the race detector**

Run: `make test-unit`
Expected: all packages PASS, including `internal/mcpcatalog`, `internal/config`, `internal/providers/oauth`.

- [ ] **Step 3: Lint**

Run: `make lint`
Expected: `0 issues`. (Falls back to `go vet ./...` if golangci-lint is unavailable.)

- [ ] **Step 4: End-to-end sanity (manual)**

Create a scratch `moat.yaml` with `mcp:\n  - linear` and confirm it loads without error:

```bash
printf 'mcp:\n  - linear\n' > /tmp/moat-shorthand-check.yaml
# Optional: add a tiny Go test or use an existing command that calls config.Load
# against /tmp to confirm resolution; the Task 4 tests already cover this path.
```

Expected: Task 4 tests already prove `config.Load` resolves `- linear`; this is a spot check only.

---

## Self-Review

- **Spec coverage:** §1 config syntax → Tasks 3-4; §2 registry/catalog → Tasks 1-2; §3 resolution+validation → Task 4; §4 grant-oauth snippet → Task 5; docs/changelog/tests → Tasks 6-7. All spec sections mapped.
- **Type consistency:** `Entry{URL,Grant,Header}` defined in Task 1 is used identically in Tasks 2, 4, 5. `mcpcatalog.Lookup`/`Names` signatures match across tasks. `MCPAuthConfig{Grant,Header}` matches the existing struct (config.go:156-159). `resolveMCPShorthand(*Config) error` defined and called consistently.
- **Placeholder scan:** No TBD/TODO except the intentional `#NNN` changelog PR placeholder (resolved at PR-open time) — flagged inline.
- **Scope:** Single subsystem (MCP config resolution). No decomposition needed.
