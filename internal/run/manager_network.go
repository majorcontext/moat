package run

// This file holds container network-mode resolution used by Create.

import (
	goruntime "runtime"

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
