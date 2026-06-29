package run

import (
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// appleRuntime is a stubRuntime that reports the Apple container runtime type.
type appleRuntime struct{ *stubRuntime }

func (appleRuntime) Type() container.RuntimeType { return container.RuntimeApple }

func TestResolveResourceLimits_NilConfig(t *testing.T) {
	m := mgrWithRuntime(&stubRuntime{}) // Docker
	mem, cpus, dns, ulimits := m.resolveResourceLimits(nil)
	if mem != 0 || cpus != 0 || dns != nil || ulimits != nil {
		t.Fatalf("expected zero limits for nil config, got mem=%d cpus=%d dns=%v ulimits=%v", mem, cpus, dns, ulimits)
	}
}

func TestResolveResourceLimits_FromConfig(t *testing.T) {
	m := mgrWithRuntime(&stubRuntime{})
	cfg := &config.Config{}
	cfg.Container.Memory = 2048
	cfg.Container.CPUs = 3
	cfg.Container.DNS = []string{"1.1.1.1"}
	cfg.Container.Ulimits = map[string]config.UlimitSpec{
		"nofile": {Soft: 1024, Hard: 2048},
		"core":   {Soft: 0, Hard: 0},
	}
	mem, cpus, dns, ulimits := m.resolveResourceLimits(cfg)
	if mem != 2048 || cpus != 3 || len(dns) != 1 || dns[0] != "1.1.1.1" {
		t.Fatalf("config values not propagated: mem=%d cpus=%d dns=%v", mem, cpus, dns)
	}
	// Ulimits are sorted by name: core before nofile.
	if len(ulimits) != 2 || ulimits[0].Name != "core" || ulimits[1].Name != "nofile" {
		t.Fatalf("ulimits not sorted by name: %v", ulimits)
	}
}

func TestResolveResourceLimits_AppleAIDefault(t *testing.T) {
	m := mgrWithRuntime(appleRuntime{&stubRuntime{}})
	mem, _, _, _ := m.resolveResourceLimits(&config.Config{Agent: "claude"})
	if mem != container.DefaultAgentMemoryMB {
		t.Fatalf("expected Apple agent default %d, got %d", container.DefaultAgentMemoryMB, mem)
	}
}

func TestResolveResourceLimits_DockerAINoDefault(t *testing.T) {
	// The 8 GB default is Apple-only; Docker leaves an AI-agent run unlimited.
	m := mgrWithRuntime(&stubRuntime{}) // Docker
	mem, _, _, _ := m.resolveResourceLimits(&config.Config{Agent: "claude"})
	if mem != 0 {
		t.Fatalf("Docker should not apply the agent memory default, got %d", mem)
	}
}

func TestResolveResourceLimits_AppleNonAINoDefault(t *testing.T) {
	m := mgrWithRuntime(appleRuntime{&stubRuntime{}})
	mem, _, _, _ := m.resolveResourceLimits(&config.Config{Agent: "bash"})
	if mem != 0 {
		t.Fatalf("expected no default memory for non-AI agent, got %d", mem)
	}
}

func TestResolveResourceLimits_AppleExplicitMemoryKept(t *testing.T) {
	m := mgrWithRuntime(appleRuntime{&stubRuntime{}})
	cfg := &config.Config{Agent: "claude"}
	cfg.Container.Memory = 4096
	mem, _, _, _ := m.resolveResourceLimits(cfg)
	if mem != 4096 {
		t.Fatalf("explicit memory should be kept over the Apple default, got %d", mem)
	}
}
