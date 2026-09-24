package run

// This file holds container network-mode resolution used by Create.

import (
	"context"
	"net"
	goruntime "runtime"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
)

// resolveNetworkConfig picks the container network mode and any extra host
// mappings from runtime capabilities and whether the run needs published ports
// or proxy access. It returns ("", nil) when neither is needed.
//
// We use bridge mode when:
//  1. We have ports to publish (host mode doesn't support port publishing)
//  2. We're on macOS/Windows (host mode not supported)
//  3. We're using the Apple container runtime
//
// We only use host mode when we need proxy access AND don't have ports to
// publish on Linux.
func (m *Manager) resolveNetworkConfig(needsPorts, needsProxy bool, hostAddr string) (networkMode string, extraHosts []string) {
	if !needsProxy && !needsPorts {
		return "", nil
	}

	if m.defaultRuntime().SupportsHostNetwork() && !needsPorts {
		// Docker on Linux without ports: use host network so container can reach 127.0.0.1
		networkMode = "host"
	} else {
		// Use bridge mode when we need port publishing, or on macOS/Windows/Apple.
		networkMode = "bridge"
		// On Linux, Docker doesn't provide host.docker.internal by default, so
		// add it via host-gateway mapping. On macOS/Windows, Docker Desktop and
		// Rancher Desktop resolve it via built-in DNS — adding host-gateway
		// would override the correct IP with the bridge gateway (which is
		// unreachable on Rancher Desktop).
		if m.defaultRuntime().Type() == container.RuntimeDocker && goruntime.GOOS == "linux" {
			extraHosts = []string{"host.docker.internal:host-gateway"}
		}
	}

	// Add synthetic hostnames to --add-host on runtimes where Docker's
	// "host-gateway" substitution produces a reachable IP (Docker on Linux).
	// Apple has no --add-host equivalent, and Docker Desktop on macOS/Windows
	// must not use this path — "host-gateway" resolves to the docker0 bridge
	// gateway, which is unreachable from containers on custom networks (those
	// created by `services:`). Those runtimes instead rely on MOAT_EXTRA_HOSTS
	// written by moat-init.sh.
	//
	// "moat-proxy" is used for proxy traffic (in NO_PROXY).
	// "moat-host" is used for host service traffic (NOT in NO_PROXY, so it flows
	// through the proxy for network policy enforcement).
	//
	// We take only the --add-host entries here; the companion MOAT_EXTRA_HOSTS
	// env value (the discarded second return) is set separately by Create's own
	// synthHostStrategy call for runtimes that rely on it.
	synthHosts, _ := synthHostStrategy(m.defaultRuntime().Type(), goruntime.GOOS, hostAddr)
	extraHosts = append(extraHosts, synthHosts...)
	return networkMode, extraHosts
}

// serialBindAddr resolves the address serial listeners should bind to for a
// run: the address of this machine as the run's container sees it. RFC2217 has
// no authentication, so the bind address is the reachability control — a
// listener bound here is reachable from the run's container and from this
// machine, and refused from anywhere a packet would have to be routed to
// reach. It returns "" when no devices are requested.
//
// By runtime:
//
//   - Docker on Linux — the default bridge's gateway (docker0, e.g.
//     172.17.0.1). Measured: reachable from default-bridge containers, from
//     per-run `services:` networks, from buildkit networks, and from the host.
//     Loopback would exclude bridge containers; a routable interface would
//     advertise the device to the LAN. Note this narrows reach, it does not
//     bound it: 172.17.0.1 is a local address, and Linux's weak host model
//     accepts packets for any local address on any interface, so a sender that
//     can route a packet here still reaches the listener. Moat adds no host
//     INPUT rule; the guide tells operators to add one on untrusted networks.
//   - Docker on macOS/Windows (Docker Desktop) — "127.0.0.1". Desktop's VM
//     forwards host loopback to containers via host.docker.internal, and the
//     VM's bridge gateway is not a host address this CLI can bind.
//   - Apple containers — the default network's gateway, the same address
//     GetHostAddress returns.
//
// The serial URL advertised to the container carries this address literally,
// not the synthetic moat-host name: on Docker Linux, a run with `services:`
// rewrites moat-host to the per-run network's gateway after registration,
// and the docker0 gateway that the listener actually binds stays reachable
// from that network anyway.
//
// host-net Docker-Linux runs also use the docker0 gateway. The container
// could additionally reach the device via its shared loopback, but the URL
// names one address and the gateway is the one that works for every network
// mode, so the URL and the bind stay consistent.
func (m *Manager) serialBindAddr(ctx context.Context, cfg *config.Config) string {
	if cfg == nil || len(cfg.Devices) == 0 {
		return ""
	}
	rt := m.defaultRuntime()
	switch {
	case rt.Type() == container.RuntimeDocker && goruntime.GOOS == "linux":
		return m.defaultBridgeGateway(ctx)
	case rt.Type() == container.RuntimeDocker:
		// Docker Desktop (macOS/Windows): containers reach host loopback via
		// host.docker.internal, which forwards to 127.0.0.1 on this side.
		return "127.0.0.1"
	default:
		// Apple containers: the default network's gateway is a host address
		// the CLI can bind — GetHostAddress returns it for the same purpose.
		return rt.GetHostAddress()
	}
}

// serialAdvertiseHost returns the host a container dials to reach the serial
// listener, given the address the listener binds (from serialBindAddr). They
// coincide everywhere except Docker Desktop: there the listener binds host
// loopback (127.0.0.1), but a container reaches host loopback only through
// host.docker.internal — which is what GetHostAddress returns on Docker
// Desktop. Advertising the bind address 127.0.0.1 there would name the
// container's own loopback and leave the device unreachable.
//
// On Docker Linux and Apple the bind address is itself a host address the
// container can reach (the docker0 gateway, the network gateway), so it is
// advertised verbatim — deliberately not moat-host, whose /etc/hosts entry a
// `services:` run rewrites to the per-run network's gateway, which is not where
// the listener binds.
func (m *Manager) serialAdvertiseHost(bind string) string {
	rt := m.defaultRuntime()
	return serialAdvertiseFor(bind, rt.Type(), rt.GetHostAddress(), goruntime.GOOS == "linux")
}

// serialAdvertiseFor is the pure core of serialAdvertiseHost, split out so the
// per-platform choice can be tested without a real runtime or a particular
// GOOS. On Docker Desktop (Docker runtime, not Linux) the container-facing host
// is hostAddr (host.docker.internal), which forwards to the host loopback the
// listener bound; everywhere else the bind address is itself reachable.
func serialAdvertiseFor(bind string, rtType container.RuntimeType, hostAddr string, linux bool) string {
	if bind == "" {
		return ""
	}
	if rtType == container.RuntimeDocker && !linux {
		return hostAddr
	}
	return bind
}

// defaultBridgeGateway returns the IPv4 gateway of Docker's default bridge
// network ("bridge" / docker0). Serial listeners bind it because every
// container network a run can use — default bridge, per-run services
// networks, buildkit's — routes through it to reach the host.
func (m *Manager) defaultBridgeGateway(ctx context.Context) string {
	netMgr := m.defaultRuntime().NetworkManager()
	if netMgr == nil {
		return ""
	}
	probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	gw := netMgr.NetworkGateway(probeCtx, "bridge")
	if net.ParseIP(gw) == nil {
		return ""
	}
	return gw
}
