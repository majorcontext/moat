package run

import (
	goruntime "runtime"
	"slices"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

func mgrWithRuntime(rt container.Runtime) *Manager {
	return &Manager{runtimePool: container.NewRuntimePoolWithDefault(rt)}
}

// noHostNetRuntime is a stubRuntime that reports no host-network support, to
// exercise the bridge-mode fallback path.
type noHostNetRuntime struct{ *stubRuntime }

func (noHostNetRuntime) SupportsHostNetwork() bool { return false }

func TestResolveNetworkConfig_NoneNeeded(t *testing.T) {
	m := &Manager{} // defaultRuntime is never reached on this path
	mode, hosts := m.resolveNetworkConfig(false, false, "127.0.0.1")
	if mode != "" || hosts != nil {
		t.Fatalf("expected empty config, got mode=%q hosts=%v", mode, hosts)
	}
}

func TestResolveNetworkConfig_HostMode(t *testing.T) {
	// stubRuntime supports host network; with proxy needed and no ports, host
	// mode lets the container reach 127.0.0.1.
	m := mgrWithRuntime(&stubRuntime{})
	mode, hosts := m.resolveNetworkConfig(false, true, "127.0.0.1")
	if mode != "host" {
		t.Fatalf("expected host mode, got %q", mode)
	}
	// Even in host mode the synthetic hosts are appended (no host.docker.internal,
	// which is bridge-only).
	wantSynth, _ := synthHostStrategy(container.RuntimeDocker, goruntime.GOOS, "127.0.0.1")
	if !slices.Equal(hosts, wantSynth) {
		t.Fatalf("host mode extraHosts = %v, want synth hosts %v", hosts, wantSynth)
	}
}

func TestResolveNetworkConfig_BridgeWithPortsAndProxy(t *testing.T) {
	// Proxy and ports needed simultaneously: ports force bridge mode.
	m := mgrWithRuntime(&stubRuntime{})
	mode, _ := m.resolveNetworkConfig(true, true, "127.0.0.1")
	if mode != "bridge" {
		t.Fatalf("expected bridge mode for proxy+ports, got %q", mode)
	}
}

func TestResolveNetworkConfig_BridgeWithPorts(t *testing.T) {
	// Published ports force bridge mode even when host network is supported.
	m := mgrWithRuntime(&stubRuntime{})
	mode, hosts := m.resolveNetworkConfig(true, false, "127.0.0.1")
	if mode != "bridge" {
		t.Fatalf("expected bridge mode, got %q", mode)
	}
	// host.docker.internal is mapped only for Docker on Linux.
	if goruntime.GOOS == "linux" && !slices.Contains(hosts, "host.docker.internal:host-gateway") {
		t.Fatalf("expected host.docker.internal mapping on linux, got %v", hosts)
	}
}

func TestResolveNetworkConfig_BridgeWhenNoHostNetwork(t *testing.T) {
	// A runtime without host-network support uses bridge mode even with no ports.
	m := mgrWithRuntime(noHostNetRuntime{&stubRuntime{}})
	mode, _ := m.resolveNetworkConfig(false, true, "127.0.0.1")
	if mode != "bridge" {
		t.Fatalf("expected bridge mode when host network unsupported, got %q", mode)
	}
}
