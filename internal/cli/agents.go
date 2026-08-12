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
func ResolveAgentField(cfg *config.Config, verb string) {
	if cfg == nil {
		return
	}
	ValidateAgent(cfg)

	if cfg.Agent != "" && len(cfg.Agents) > 0 && !agentsListContains(cfg.Agents, cfg.Agent) {
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

// ExpandAgents expands moat.yaml's `agents:` list into the dependencies,
// grants, and network rules each named agent needs, deduping against what the
// config already declares.
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
func ExpandAgents(cfg *config.Config) error {
	if cfg == nil || len(cfg.Agents) == 0 {
		return nil
	}
	for _, entry := range cfg.Agents {
		if entry == "" {
			return fmt.Errorf("moat.yaml `agents:` contains an empty entry; remove it or name an agent (valid: %s)",
				strings.Join(KnownAgentNames(), ", "))
		}
		canonical := CanonicalAgent(entry)
		if canonical == "" {
			return fmt.Errorf("moat.yaml `agents: [%s]` is not a known agent (valid: %s)",
				entry, strings.Join(KnownAgentNames(), ", "))
		}
		agent := provider.GetAgent(canonical)
		rt, ok := agent.(provider.AgentRuntime)
		if !ok {
			return fmt.Errorf("moat.yaml `agents: [%s]` cannot be provisioned declaratively; "+
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

		if grant := rt.CredentialGrant(); grant != "" && !slices.Contains(cfg.Grants, grant) {
			cfg.Grants = append(cfg.Grants, grant)
		}

		for _, host := range rt.NetworkHosts() {
			if hasNetworkHost(cfg.Network.Rules, host) {
				continue
			}
			cfg.Network.Rules = append(cfg.Network.Rules,
				netrules.NetworkRuleEntry{HostRules: netrules.HostRules{Host: host}})
		}
	}
	return nil
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
