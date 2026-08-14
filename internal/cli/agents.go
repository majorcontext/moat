package cli

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/netrules"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/ui"
)

// agentVariants maps documented agent-name variants onto their registered
// provider name. The provider registry has no alias for these, but they are
// long-standing valid moat.yaml values: the claude join gate accepts
// "claude-code" (internal/providers/claude/join.go) and storage metadata
// documents it as the example agent value.
var agentVariants = map[string]string{
	"claude-code": "claude",
}

// CanonicalAgent resolves an agent name to its registered provider name,
// accepting registry aliases (openai -> codex) and documented variants
// (claude-code -> claude). Returns "" when name is not a known agent.
//
// provider.ResolveName alone cannot validate: it returns unknown input
// unchanged. provider.GetAgent is the membership test, and it also excludes
// non-agent providers (github, aws, oauth, …) that a bare registry lookup
// would wrongly accept.
func CanonicalAgent(name string) string {
	if name == "" {
		return ""
	}
	if v, ok := agentVariants[name]; ok {
		name = v
	}
	resolved := provider.ResolveName(name)
	if provider.GetAgent(resolved) == nil {
		return ""
	}
	return resolved
}

// KnownAgentNames returns the sorted set of values accepted by `agent:`.
func KnownAgentNames() []string {
	seen := make(map[string]bool)
	for _, a := range provider.Agents() {
		seen[a.Name()] = true
	}
	for variant := range agentVariants {
		seen[variant] = true
	}
	for _, alias := range provider.AgentAliases() {
		seen[alias] = true
	}
	names := make([]string, 0, len(seen))
	for n := range seen {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// ValidateAgent warns and clears cfg.Agent when it names something that is not
// a known agent. It warns rather than failing: moat init and the reference docs
// have both generated project-shaped values in the wild, and those runs work
// today apart from the silent degradation. Clearing the field lets the CLI verb
// backfill a correct value, so the run self-heals without the user editing
// moat.yaml.
func ValidateAgent(cfg *config.Config) {
	if cfg == nil || cfg.Agent == "" {
		return
	}
	if CanonicalAgent(cfg.Agent) != "" {
		return
	}
	ui.Warnf("moat.yaml `agent: %s` is not a known agent (valid: %s) — ignoring.\n"+
		"Remove the field or set `agent: claude`.",
		cfg.Agent, strings.Join(KnownAgentNames(), ", "))
	cfg.Agent = ""
}

// ResolveAgentField normalizes cfg.Agent to name the run's PRIMARY agent — the
// one launched in the foreground that owns the container lifecycle. Every other
// entry in cfg.Agents is provisioned but reachable only via `moat join`.
//
// One rule: the CLI verb always names the primary when there is one; otherwise
// `agent:` names it; if neither is set, `moat run` falls back to Agents[0].
// verb is "" for `moat run`.
//
// Whichever path wins, the result is canonicalized on the way out — see
// canonicalizeAgentField.
func ResolveAgentField(cfg *config.Config, verb string) {
	if cfg == nil {
		return
	}
	defer canonicalizeAgentField(cfg)

	ValidateAgent(cfg)

	// This warning only holds on the no-verb (`moat run`) path: when a verb is
	// present, the block below immediately overwrites cfg.Agent with verb, and
	// RunProvider provisions the verb's own dependencies/grants/network hosts
	// independently of `agents:` — so the named agent genuinely is provisioned,
	// and warning here would just contradict the conflict warning that follows.
	if verb == "" && cfg.Agent != "" && len(cfg.Agents) > 0 && !agentsListContains(cfg.Agents, cfg.Agent) {
		ui.Warnf("moat.yaml `agent: %s` is not in `agents: %v`; it will run as the primary but "+
			"add it to `agents:` so its dependencies and grants are provisioned.",
			cfg.Agent, cfg.Agents)
	}

	if verb != "" {
		if cfg.Agent != "" && CanonicalAgent(cfg.Agent) != CanonicalAgent(verb) {
			ui.Warnf("moat.yaml `agent: %s` conflicts with `moat %s` — using %s.",
				cfg.Agent, verb, verb)
		}
		cfg.Agent = verb
		return
	}

	// No verb: `moat run`. Fall back to the first entry in agents:.
	if cfg.Agent == "" && len(cfg.Agents) > 0 {
		cfg.Agent = cfg.Agents[0]
	}
}

// canonicalizeAgentField rewrites cfg.Agent to its registered provider name, so
// registry aliases (openai -> codex, google -> gemini) and documented variants
// (claude-code -> claude) all collapse to one spelling.
//
// Every downstream consumer of cfg.Agent matches it with
// strings.HasPrefix(cfg.Agent, "<canonical>"): isAIAgent's container-memory
// default, agentImpliedDependencies, the language_servers gate, copilot init,
// and pi staging — all in internal/run/manager_create.go. "claude-code"
// satisfies those by prefix; "openai" and "google" do not. Without this,
// an alias that ValidateAgent accepts (and that KnownAgentNames advertises as
// valid) would sail through validation and then silently switch those defaults
// off — the exact degradation this file exists to prevent. `agents: [openai]`
// reaches the same place via the Agents[0] backfill above.
//
// A value that does not resolve is left alone: ValidateAgent has already
// cleared unknown moat.yaml values, and the verb path assigns a name the
// provider registry owns.
func canonicalizeAgentField(cfg *config.Config) {
	if canonical := CanonicalAgent(cfg.Agent); canonical != "" {
		cfg.Agent = canonical
	}
}

// agentsListContains reports whether agents contains agent, comparing by
// canonical name so documented variants (claude-code) and registry aliases
// (openai) match their canonical entry.
func agentsListContains(agents []string, agent string) bool {
	want := CanonicalAgent(agent)
	for _, a := range agents {
		if CanonicalAgent(a) == want {
			return true
		}
	}
	return false
}

// ExpandAgents expands moat.yaml's `agents:` list into the dependencies and
// network rules each named agent needs, deduping against what the config
// already declares. It mutates cfg.Dependencies and cfg.Network.Rules
// directly, but returns credential grants rather than appending them to
// cfg.Grants — callers must merge the return value into their own grants
// list. This keeps agent-derived grants out of cfg.Grants, which callers
// like buildGrants treat as "explicit" (user-written) and use to suppress an
// auto-detected credential; a derived grant is a fallback, not a user
// declaration, and must never win that suppression. See buildGrants in
// internal/cli/provider.go.
//
// It must run BEFORE grant resolution and the network-rule loop in
// RunProvider — an expansion that lands after either contributes nothing. The
// failure is fail-closed (the agent is absent from the capability set and join
// refuses) but opaque, so ordering is a requirement, not an accident.
//
// Unknown entries are a hard error, unlike `agent:`, which warns. There is no
// legacy corpus of hallucinated `agents:` values to stay compatible with, and a
// silently dropped entry leaves the container short a credential AND its
// firewall rules — surfacing much later as an opaque join refusal or a blocked
// request under a strict network policy.
func ExpandAgents(cfg *config.Config) ([]string, error) {
	if cfg == nil || len(cfg.Agents) == 0 {
		return nil, nil
	}
	var derivedGrants []string
	for _, entry := range cfg.Agents {
		if entry == "" {
			return nil, fmt.Errorf("moat.yaml `agents:` contains an empty entry; remove it or name an agent (valid: %s)",
				strings.Join(KnownAgentNames(), ", "))
		}
		canonical := CanonicalAgent(entry)
		if canonical == "" {
			return nil, fmt.Errorf("moat.yaml `agents: [%s]` is not a known agent (valid: %s)",
				entry, strings.Join(KnownAgentNames(), ", "))
		}
		agent := provider.GetAgent(canonical)
		rt, ok := agent.(provider.AgentRuntime)
		if !ok {
			return nil, fmt.Errorf("moat.yaml `agents: [%s]` cannot be provisioned declaratively; "+
				"run it with `moat %s` instead", entry, canonical)
		}

		for _, dep := range rt.DefaultDependencies() {
			name := dep
			if i := strings.IndexByte(dep, '@'); i >= 0 {
				name = dep[:i]
			}
			if !HasDependency(cfg.Dependencies, name) {
				cfg.Dependencies = append(cfg.Dependencies, dep)
			}
		}

		if grant := rt.CredentialGrant(); grant != "" && !slices.Contains(cfg.Grants, grant) && !slices.Contains(derivedGrants, grant) {
			derivedGrants = append(derivedGrants, grant)
		}

		for _, host := range rt.NetworkHosts() {
			if hasNetworkHost(cfg.Network.Rules, host) {
				continue
			}
			cfg.Network.Rules = append(cfg.Network.Rules,
				netrules.NetworkRuleEntry{HostRules: netrules.HostRules{Host: host}})
		}
	}
	return derivedGrants, nil
}

// AppendDerivedGrants appends the derived grants returned by ExpandAgents
// onto cfg.Grants, skipping any already present. Callers must invoke this
// AFTER grant precedence resolution has read cfg.Grants as the "explicit"
// bucket (buildGrants in internal/cli/provider.go, or the equivalent
// grants-defaulting block in `moat run`/`moat wt`) — never before. Writing
// derived grants into cfg.Grants earlier would let them re-enter that
// resolution as if the user had declared them, resurrecting the bug
// ExpandAgents' doc comment describes (a derived "claude" grant wrongly
// suppressing an auto-detected "anthropic" API-key credential).
//
// This exists because cfg.Grants has two downstream readers besides grant
// resolution — Config.ShouldSyncCodexLogs/ShouldSyncGeminiLogs and
// buildLocalMCPConfig's grant validation (internal/run/manager_agentinit.go)
// — that read cfg.Grants directly rather than the resolved grants list.
// Without this write-back, an agents:-derived grant (e.g. "openai" from
// `agents: [codex]`) is invisible to them: log sync silently stays off, and a
// local MCP server's `grant: openai` is rejected as "not declared in
// top-level grants list" even though the credential is provisioned.
func AppendDerivedGrants(cfg *config.Config, derivedGrants []string) {
	if cfg == nil {
		return
	}
	for _, g := range derivedGrants {
		if !slices.Contains(cfg.Grants, g) {
			cfg.Grants = append(cfg.Grants, g)
		}
	}
}

// hasNetworkHost reports whether rules already contains an entry for host.
func hasNetworkHost(rules []netrules.NetworkRuleEntry, host string) bool {
	for _, r := range rules {
		if r.Host == host {
			return true
		}
	}
	return false
}
