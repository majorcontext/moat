package run

import (
	"sort"

	"github.com/majorcontext/moat/internal/deps"
)

// agentCLIDep maps an agent provider name to the dependency that installs its
// CLI binary into the container.
var agentCLIDep = map[string]string{
	"claude":  "claude-code",
	"codex":   "codex-cli",
	"copilot": "copilot-cli",
	"gemini":  "gemini-cli",
	"pi":      "pi-cli",
}

// computeJoinableAgents returns the agents moat provisioned into the container:
// those whose config was staged AND whose CLI was installed.
//
// Both halves matter. initProviders is grant-driven, so a run with a claude
// grant but no claude-code dependency has staged config and no binary; the
// intersection rejects it.
//
// The result is always non-nil. Callers persist it without omitempty so an
// empty set (no joinable agents) stays distinguishable from an absent field
// (a run created before capability tracking existed).
func computeJoinableAgents(initProviders []string, depList []deps.Dependency) []string {
	out := []string{}
	for _, agent := range initProviders {
		dep, ok := agentCLIDep[agent]
		if !ok {
			continue
		}
		if hasDep(depList, dep) {
			out = append(out, agent)
		}
	}
	sort.Strings(out)
	return out
}
