package run

import (
	"context"
	goruntime "runtime"
	"slices"
	"testing"

	"github.com/majorcontext/moat/internal/config"
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

// stubNetworkManager answers NetworkGateway from a fixed table.
type stubNetworkManager struct {
	gateways map[string]string
}

func (stubNetworkManager) CreateNetwork(context.Context, string) (string, error) {
	panic("not implemented")
}

func (stubNetworkManager) RemoveNetwork(context.Context, string) error {
	panic("not implemented")
}

func (stubNetworkManager) ListNetworks(context.Context) ([]container.NetworkInfo, error) {
	panic("not implemented")
}

func (m stubNetworkManager) NetworkGateway(_ context.Context, networkID string) string {
	return m.gateways[networkID]
}

func (stubNetworkManager) ForceRemoveNetwork(context.Context, string) error {
	panic("not implemented")
}

// netMgrRuntime embeds stubRuntime and answers a stub NetworkManager.
type netMgrRuntime struct {
	*stubRuntime
	gateways map[string]string
}

func (r netMgrRuntime) NetworkManager() container.NetworkManager {
	return stubNetworkManager{gateways: r.gateways}
}

func TestSerialBindAddrDockerLinuxIsTheDefaultBridgeGateway(t *testing.T) {
	// RFC2217 has no authentication, so where the listener binds is the
	// reachability control. On Docker Linux every container network a run can
	// use — default bridge, services networks, buildkit — routes through the
	// default bridge gateway to reach the host, so the listener binds it.
	if goruntime.GOOS != "linux" {
		t.Skip("docker-linux topology")
	}
	rt := netMgrRuntime{stubRuntime: &stubRuntime{}}
	rt.gateways = map[string]string{"bridge": "172.17.0.1"}
	m := mgrWithRuntime(rt)

	got := m.serialBindAddr(context.Background(), &config.Config{Devices: []config.DeviceEntry{{Name: "esp32"}}})
	if got != "172.17.0.1" {
		t.Fatalf("serialBindAddr = %q, want the default bridge gateway 172.17.0.1", got)
	}
	// Companion: no devices requested, nothing to resolve — the register
	// request must stay empty so the daemon never binds on a device-less run.
	if got := m.serialBindAddr(context.Background(), &config.Config{}); got != "" {
		t.Fatalf("serialBindAddr with no devices = %q, want empty", got)
	}
	if got := m.serialBindAddr(context.Background(), nil); got != "" {
		t.Fatalf("serialBindAddr with nil config = %q, want empty", got)
	}
}

func TestSerialBindAddrRefusesWhenGatewayUnresolvable(t *testing.T) {
	// Companion of the happy path: a gateway that cannot be resolved must
	// yield "" (the caller refuses the device with an actionable error), never
	// a silent fallback to wildcard.
	if goruntime.GOOS != "linux" {
		t.Skip("docker-linux topology")
	}
	for _, tc := range []struct {
		name     string
		gateways map[string]string
	}{
		{"no network manager", nil},                    // stubRuntime returns nil NetworkManager
		{"no gateway for bridge", map[string]string{}}, // inspect finds nothing
		{"non-ip gateway", map[string]string{"bridge": "not-an-ip"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rt container.Runtime
			if tc.gateways == nil {
				rt = &stubRuntime{} // NetworkManager() == nil
			} else {
				nr := netMgrRuntime{stubRuntime: &stubRuntime{}}
				nr.gateways = tc.gateways
				rt = nr
			}
			m := mgrWithRuntime(rt)
			got := m.serialBindAddr(context.Background(), &config.Config{Devices: []config.DeviceEntry{{Name: "esp32"}}})
			if got != "" {
				t.Fatalf("serialBindAddr = %q, want empty (refuse, don't fall back to wildcard)", got)
			}
		})
	}
}

func TestSerialAdvertiseForPerPlatform(t *testing.T) {
	// The advertised host coincides with the bind address everywhere except
	// Docker Desktop, where the listener binds host loopback but the container
	// reaches it only via host.docker.internal. Advertising the bind address
	// 127.0.0.1 there names the container's own loopback and the device is
	// unreachable — the regression this guards.
	for _, tc := range []struct {
		name     string
		bind     string
		rtType   container.RuntimeType
		hostAddr string
		linux    bool
		want     string
	}{
		{"docker desktop advertises host.docker.internal", "127.0.0.1", container.RuntimeDocker, "host.docker.internal", false, "host.docker.internal"},
		{"docker linux advertises the bound gateway", "172.17.0.1", container.RuntimeDocker, "127.0.0.1", true, "172.17.0.1"},
		{"apple advertises the bound gateway", "192.168.64.1", container.RuntimeApple, "192.168.64.1", false, "192.168.64.1"},
		{"no devices stays empty", "", container.RuntimeDocker, "host.docker.internal", false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serialAdvertiseFor(tc.bind, tc.rtType, tc.hostAddr, tc.linux); got != tc.want {
				t.Fatalf("serialAdvertiseFor(%q, %v, %q, linux=%v) = %q, want %q",
					tc.bind, tc.rtType, tc.hostAddr, tc.linux, got, tc.want)
			}
		})
	}
}

// The firewall's destination and the container's URL must come from the same
// place. Create populates Run.SerialHostAddr for the firewall rule and the
// MOAT_SERIAL_*_URL env var from the same advertised host; populating the rule
// from the bind address instead is invisible on Docker-on-Linux and Apple,
// where the two coincide, and silently breaks Docker Desktop, where they do
// not. This pins the one case that distinguishes them, so a "simplification"
// that collapses the two concepts fails here rather than in the field.
func TestAdvertisedHostDiffersFromBindOnDockerDesktop(t *testing.T) {
	const bind = "127.0.0.1"
	advertise := serialAdvertiseFor(bind, container.RuntimeDocker, "host.docker.internal", false)
	if advertise == bind {
		t.Fatalf("advertised host == bind address (%q) on Docker Desktop; a firewall rule "+
			"scoped to the bind address would match only the container's own loopback", bind)
	}
	// And they must coincide where the runtime has no separate gateway name,
	// or the firewall rule would name something the container never dials.
	for _, tc := range []struct {
		name   string
		bind   string
		rtType container.RuntimeType
		linux  bool
	}{
		{"docker linux", "172.17.0.1", container.RuntimeDocker, true},
		{"apple", "192.168.64.1", container.RuntimeApple, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := serialAdvertiseFor(tc.bind, tc.rtType, "host.docker.internal", tc.linux); got != tc.bind {
				t.Fatalf("serialAdvertiseFor = %q, want the bind address %q", got, tc.bind)
			}
		})
	}
}

func TestSerialBindAddrAppleIsTheDefaultNetworkGateway(t *testing.T) {
	// Apple containers: the default network's gateway is the host-facing
	// address, the same one GetHostAddress returns.
	m := mgrWithRuntime(appleRuntimeWithAddress{&stubRuntime{}, "192.168.64.1"})
	got := m.serialBindAddr(context.Background(), &config.Config{Devices: []config.DeviceEntry{{Name: "esp32"}}})
	if got != "192.168.64.1" {
		t.Fatalf("serialBindAddr = %q, want 192.168.64.1", got)
	}
}

// appleRuntimeWithAddress overrides GetHostAddress, which appleRuntime does not.
type appleRuntimeWithAddress struct {
	*stubRuntime
	addr string
}

func (r appleRuntimeWithAddress) Type() container.RuntimeType { return container.RuntimeApple }
func (r appleRuntimeWithAddress) GetHostAddress() string      { return r.addr }
func (r appleRuntimeWithAddress) SupportsHostNetwork() bool   { return false }
