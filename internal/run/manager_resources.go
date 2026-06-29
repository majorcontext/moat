package run

// This file holds container resource-limit resolution used by Create.

import (
	"sort"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/log"
)

// resolveResourceLimits extracts a run's container resource limits (memory,
// CPUs, DNS, ulimits). Priority: explicit moat.yaml > agent provider default >
// runtime fallback.
//
// On Apple containers, an AI-agent run with no explicit memory gets the agent
// default (8 GB) because Apple's 1 GB system default is too low for Claude
// Code, Codex, and Gemini CLI. Docker is left unlimited unless configured.
func (m *Manager) resolveResourceLimits(cfg *config.Config) (memoryMB, cpus int, dns []string, ulimits []container.Ulimit) {
	if cfg != nil {
		memoryMB = cfg.Container.Memory
		cpus = cfg.Container.CPUs
		dns = cfg.Container.DNS
		for name, spec := range cfg.Container.Ulimits {
			ulimits = append(ulimits, container.Ulimit{
				Name: name,
				Soft: spec.Soft,
				Hard: spec.Hard,
			})
		}
		sort.Slice(ulimits, func(i, j int) bool {
			return ulimits[i].Name < ulimits[j].Name
		})
	}

	if memoryMB == 0 && m.defaultRuntime().Type() == container.RuntimeApple && isAIAgent(cfg) {
		memoryMB = container.DefaultAgentMemoryMB
		log.Debug("using default agent memory for Apple container", "memoryMB", memoryMB)
	}
	return memoryMB, cpus, dns, ulimits
}
