package run

// This file holds the SSH agent proxy setup used by Create.

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/sshagent"
)

// sshAgentSetup is the result of wiring an SSH agent proxy for a run's SSH
// grants: the started proxy server (nil when there are no SSH grants) plus the
// container env and mounts the run needs to reach it.
type sshAgentSetup struct {
	server *sshagent.Server
	env    []string
	mounts []container.MountConfig
}

// setupSSHAgent wires an SSH agent proxy for the run's SSH grants (e.g.
// `git clone git@github.com:...`), returning a zero-value setup when there are
// no SSH grants. On any error it returns a zero setup (hence the explicit
// `sshAgentSetup{}` returns, never a partially-populated one) after cleaning up
// the partial resources it created — stopping a started server, closing the
// upstream agent, removing the socket dir. The caller still owns rolling back
// resources allocated before this call.
func (m *Manager) setupSSHAgent(r *Run, opts Options, sshGrants []string, hostAddr string, openCredStore func() (*credential.FileStore, error)) (sshAgentSetup, error) {
	var setup sshAgentSetup
	if len(sshGrants) == 0 {
		return setup, nil
	}

	upstreamSocket := os.Getenv("SSH_AUTH_SOCK")
	if upstreamSocket == "" {
		return setup, fmt.Errorf("SSH grants require SSH_AUTH_SOCK to be set\n\n" +
			"Start your SSH agent with: eval \"$(ssh-agent -s)\" && ssh-add")
	}

	// Load SSH mappings for granted hosts
	store, err := openCredStore()
	if err != nil {
		return setup, err
	}

	sshMappings, err := store.GetSSHMappingsForHosts(sshGrants)
	if err != nil {
		return setup, fmt.Errorf("loading SSH mappings: %w", err)
	}
	if len(sshMappings) == 0 {
		return setup, fmt.Errorf("no SSH keys configured for hosts: %v\n\n"+
			"Grant SSH access first:\n"+
			"  moat grant ssh --host %s", sshGrants, sshGrants[0])
	}

	// Connect to upstream SSH agent
	upstreamAgent, err := sshagent.ConnectAgent(upstreamSocket)
	if err != nil {
		return setup, fmt.Errorf("connecting to SSH agent: %w", err)
	}

	// Create filtering proxy
	sshProxy := sshagent.NewProxy(upstreamAgent)
	for _, mapping := range sshMappings {
		sshProxy.AllowKey(mapping.KeyFingerprint, []string{mapping.Host})
	}

	// Unix sockets can't be shared across VM boundaries. This affects:
	// - Docker Desktop on macOS/Windows (containers run in a Linux VM)
	// - Apple containers (containers run in Virtualization.framework VMs)
	// For these cases, we use TCP instead: the host listens on TCP and the
	// container's moat-init script uses socat to bridge TCP to a local Unix socket.
	// For Docker on Linux, Unix sockets work fine via direct bind mounts.
	usesTCP := !m.defaultRuntime().SupportsHostNetwork()

	if usesTCP {
		// Use TCP server - container will use socat to bridge.
		// Apple containers access the host via gateway IP, so we must bind to all
		// interfaces. Docker Desktop also runs containers in a VM, so same applies.
		// Security: the SSH agent proxy filters keys by host, so binding to 0.0.0.0
		// doesn't expose credentials - only allowed key+host combinations are usable.
		setup.server = sshagent.NewTCPServer(sshProxy, "0.0.0.0:0") // :0 picks random port
		if err := setup.server.Start(); err != nil {
			upstreamAgent.Close()
			return sshAgentSetup{}, fmt.Errorf("starting SSH agent proxy (TCP): %w", err)
		}

		// Get the actual TCP address after binding.
		// hostAddr is set earlier from m.defaultRuntime().GetHostAddress() and may be
		// rewritten later for custom networks (replaceHostInEnv).
		tcpAddr := setup.server.TCPAddr()
		containerSSHDir := "/run/moat/ssh"

		// Extract port from TCP address (format is "host:port" or "[::]:port")
		_, tcpPort, err := net.SplitHostPort(tcpAddr)
		if err != nil {
			if stopErr := setup.server.Stop(); stopErr != nil {
				log.Debug("failed to stop SSH agent during cleanup", "error", stopErr)
			}
			upstreamAgent.Close()
			return sshAgentSetup{}, fmt.Errorf("parsing SSH proxy address %q: %w", tcpAddr, err)
		}
		containerTCPAddr := hostAddr + ":" + tcpPort

		// Set env vars for container to set up socat bridge
		// Container entrypoint will run: socat UNIX-LISTEN:/run/moat/ssh/agent.sock,fork TCP:host:port
		setup.env = append(setup.env,
			"MOAT_SSH_TCP_ADDR="+containerTCPAddr,
			"SSH_AUTH_SOCK="+containerSSHDir+"/agent.sock",
		)

		log.Debug("SSH agent proxy started (TCP mode)",
			"tcpAddr", tcpAddr,
			"containerAddr", containerTCPAddr,
			"hosts", sshGrants,
			"keys", len(sshMappings))
	} else {
		// Use Unix socket - can be mounted directly
		sshSocketDir := filepath.Join(config.GlobalConfigDir(), "sockets", r.ID)
		if err := os.MkdirAll(sshSocketDir, 0o755); err != nil {
			upstreamAgent.Close()
			return sshAgentSetup{}, fmt.Errorf("creating SSH socket directory: %w", err)
		}
		socketPath := filepath.Join(sshSocketDir, "agent.sock")

		setup.server = sshagent.NewServer(sshProxy, socketPath)
		if err := setup.server.Start(); err != nil {
			upstreamAgent.Close()
			os.RemoveAll(sshSocketDir)
			return sshAgentSetup{}, fmt.Errorf("starting SSH agent proxy: %w", err)
		}

		// Mount socket directory into container
		containerSSHDir := "/run/moat/ssh"
		setup.mounts = append(setup.mounts, container.MountConfig{
			Source:   sshSocketDir,
			Target:   containerSSHDir,
			ReadOnly: false,
		})

		// Set SSH_AUTH_SOCK for container
		setup.env = append(setup.env, "SSH_AUTH_SOCK="+containerSSHDir+"/agent.sock")

		log.Debug("SSH agent proxy started (Unix socket mode)",
			"socket", socketPath,
			"hosts", sshGrants,
			"keys", len(sshMappings))
	}

	// When both github and ssh:github.com grants are active, prefer SSH for
	// github.com git operations. HTTPS git works on its own (issue #370), so
	// this is a routing preference, not a workaround: it makes git use the
	// forwarded SSH key's identity rather than the github token's, which the
	// user opted into by granting ssh:github.com. The MOAT_GIT_SSH_GITHUB env
	// var drives the url.insteadOf rewrite in moat-init.sh.
	// Check if user explicitly set MOAT_GIT_SSH_GITHUB (e.g. =0 to opt out)
	gitSSHAlreadySet := false
	for _, e := range opts.Env {
		if strings.HasPrefix(e, "MOAT_GIT_SSH_GITHUB=") {
			gitSSHAlreadySet = true
			break
		}
	}
	if !gitSSHAlreadySet && slices.Contains(sshGrants, "github.com") && slices.Contains(opts.Grants, "github") {
		setup.env = append(setup.env, "MOAT_GIT_SSH_GITHUB=1")
	}

	return setup, nil
}
