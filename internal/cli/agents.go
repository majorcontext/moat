package cli

import (
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/config"
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
