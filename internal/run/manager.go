package run

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/andybons/moat/internal/audit"
	"github.com/andybons/moat/internal/claude"
	"github.com/andybons/moat/internal/codex"
	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/container"
	"github.com/andybons/moat/internal/credential"
	"github.com/andybons/moat/internal/deps"
	"github.com/andybons/moat/internal/image"
	"github.com/andybons/moat/internal/log"
	"github.com/andybons/moat/internal/name"
	"github.com/andybons/moat/internal/proxy"
	"github.com/andybons/moat/internal/routing"
	"github.com/andybons/moat/internal/secrets"
	"github.com/andybons/moat/internal/snapshot"
	"github.com/andybons/moat/internal/sshagent"
	"github.com/andybons/moat/internal/storage"
	"github.com/andybons/moat/internal/term"
)

// Timing constants for run lifecycle operations.
const (
	// containerStartDelay is how long to wait after StartAttached begins before
	// updating run state to "running". This delay ensures the container process
	// has started and the TTY is attached before we report it as running.
	// The value is chosen to be long enough for the attach to establish but
	// short enough to not noticeably delay state updates.
	containerStartDelay = 100 * time.Millisecond
)

// getWorkspaceOwner returns the UID and GID of the workspace directory owner.
// This is used on Linux to run containers as the workspace owner, ensuring
// file permissions work correctly even when moat is run with sudo.
// Falls back to the current process UID/GID if stat fails.
func getWorkspaceOwner(workspace string) (uid, gid int) {
	info, err := os.Stat(workspace)
	if err != nil {
		// Fall back to process UID/GID
		return os.Getuid(), os.Getgid()
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		// Fall back to process UID/GID (non-Unix system)
		return os.Getuid(), os.Getgid()
	}
	return int(stat.Uid), int(stat.Gid)
}

// Manager handles run lifecycle operations.
type Manager struct {
	runtime        container.Runtime
	runs           map[string]*Run
	routes         *routing.RouteTable
	proxyLifecycle *routing.Lifecycle
	mu             sync.RWMutex
}

// NewManager creates a new run manager.
func NewManager() (*Manager, error) {
	rt, err := container.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("initializing container runtime: %w", err)
	}

	proxyDir := filepath.Join(config.GlobalConfigDir(), "proxy")

	globalCfg, _ := config.LoadGlobal()
	proxyPort := globalCfg.Proxy.Port

	lifecycle, err := routing.NewLifecycle(proxyDir, proxyPort)
	if err != nil {
		return nil, fmt.Errorf("initializing proxy lifecycle: %w", err)
	}

	m := &Manager{
		runtime:        rt,
		runs:           make(map[string]*Run),
		routes:         lifecycle.Routes(),
		proxyLifecycle: lifecycle,
	}

	// Load existing runs from disk and reconcile with container state
	if err := m.loadPersistedRuns(context.Background()); err != nil {
		log.Debug("loading persisted runs", "error", err)
		// Non-fatal - continue with empty runs map
	}

	return m, nil
}

// loadPersistedRuns loads run metadata from disk and reconciles with actual container state.
func (m *Manager) loadPersistedRuns(ctx context.Context) error {
	baseDir := storage.DefaultBaseDir()
	runIDs, err := storage.ListRunDirs(baseDir)
	if err != nil {
		return err
	}

	for _, runID := range runIDs {
		store, err := storage.NewRunStore(baseDir, runID)
		if err != nil {
			log.Debug("opening run store", "id", runID, "error", err)
			continue
		}

		meta, err := store.LoadMetadata()
		if err != nil {
			log.Debug("loading run metadata", "id", runID, "error", err)
			continue
		}

		// Skip runs with no container ID (incomplete/failed creation)
		if meta.ContainerID == "" {
			continue
		}

		// Check if the container actually exists and get its current state
		var runState State
		containerState, err := m.runtime.ContainerState(ctx, meta.ContainerID)
		if err != nil {
			// Container doesn't exist - mark run as stopped but still load it
			// so it can be cleaned up (storage removal via destroy)
			log.Debug("container not found", "id", runID, "container", meta.ContainerID)
			runState = StateStopped
		} else {
			// Map container state to run state
			// Note: Docker uses "exited"/"dead" for stopped containers,
			// while Apple containers use "stopped"
			switch containerState {
			case "running":
				runState = StateRunning
			case "exited", "dead", "stopped":
				runState = StateStopped
			case "created", "restarting":
				runState = StateCreated
			default:
				runState = State(meta.State)
			}
		}

		// Create run object from metadata
		r := &Run{
			ID:          runID,
			Name:        meta.Name,
			Workspace:   meta.Workspace,
			Grants:      meta.Grants,
			Ports:       meta.Ports,
			State:       runState,
			ContainerID: meta.ContainerID,
			Store:       store,
			Interactive: meta.Interactive,
			CreatedAt:   meta.CreatedAt,
			StartedAt:   meta.StartedAt,
			StoppedAt:   meta.StoppedAt,
			Error:       meta.Error,
		}

		// Update metadata if state changed
		if string(runState) != meta.State {
			_ = r.SaveMetadata()
		}

		m.mu.Lock()
		m.runs[runID] = r
		m.mu.Unlock()

		log.Debug("loaded persisted run", "id", runID, "name", meta.Name, "state", runState)
	}

	return nil
}

// Create initializes a new run without starting it.
func (m *Manager) Create(ctx context.Context, opts Options) (*Run, error) {
	// Resolve agent name
	agentName := opts.Name
	if agentName == "" {
		// Generate random name
		for i := 0; i < 3; i++ {
			agentName = name.Generate()
			if !m.routes.AgentExists(agentName) {
				break
			}
		}
		// If still colliding after 3 tries, append random suffix
		if m.routes.AgentExists(agentName) {
			agentName = agentName + "-" + generateID()[4:8]
		}
	} else {
		// Check for collision with explicit name
		if m.routes.AgentExists(agentName) {
			return nil, fmt.Errorf("agent %q is already running. Use --name to specify a different name, or stop the existing agent first", agentName)
		}
	}

	// Get ports from config
	var ports map[string]int
	if opts.Config != nil && len(opts.Config.Ports) > 0 {
		ports = opts.Config.Ports
	}

	r := &Run{
		ID:            generateID(),
		Name:          agentName,
		Workspace:     opts.Workspace,
		Grants:        opts.Grants,
		Ports:         ports,
		State:         StateCreated,
		KeepContainer: opts.KeepContainer,
		Interactive:   opts.Interactive,
		CreatedAt:     time.Now(),
	}

	// Default command
	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash"}
	}

	// Start proxy server for this run if grants are specified
	var proxyServer *proxy.Server
	var proxyEnv []string
	var providerEnv []string // Provider-specific env vars (e.g., dummy ANTHROPIC_API_KEY)
	var mounts []container.MountConfig

	// Always mount workspace
	mounts = append(mounts, container.MountConfig{
		Source:   opts.Workspace,
		Target:   "/workspace",
		ReadOnly: false,
	})

	// Add mounts from config
	if opts.Config != nil {
		for _, mountStr := range opts.Config.Mounts {
			mount, err := config.ParseMount(mountStr)
			if err != nil {
				return nil, fmt.Errorf("parsing mount %q: %w", mountStr, err)
			}
			// Resolve relative paths against workspace
			source := mount.Source
			if !filepath.IsAbs(source) {
				source = filepath.Join(opts.Workspace, source)
			}
			mounts = append(mounts, container.MountConfig{
				Source:   source,
				Target:   mount.Target,
				ReadOnly: mount.ReadOnly,
			})
		}
	}

	// Start proxy if we have grants (for credential injection) or strict network policy
	needsProxyForGrants := len(opts.Grants) > 0
	needsProxyForFirewall := opts.Config != nil && opts.Config.Network.Policy == "strict"

	// cleanupProxy is a helper to stop the proxy server and log any errors.
	// Used in error paths during run creation.
	cleanupProxy := func(ps *proxy.Server) {
		if ps != nil {
			if err := ps.Stop(context.Background()); err != nil {
				log.Debug("failed to stop proxy during cleanup", "error", err)
			}
		}
	}

	// cleanupSSH is a helper to stop the SSH agent server and log any errors.
	cleanupSSH := func(ss *sshagent.Server) {
		if ss != nil {
			if err := ss.Stop(); err != nil {
				log.Debug("failed to stop SSH agent during cleanup", "error", err)
			}
		}
	}

	// cleanupClaude is a helper to clean up Claude generated config and log any errors.
	cleanupClaude := func(cg *claude.GeneratedConfig) {
		if cg != nil {
			if err := cg.Cleanup(); err != nil {
				log.Debug("failed to cleanup Claude config during cleanup", "error", err)
			}
		}
	}

	// cleanupCodex is a helper to clean up Codex generated config and log any errors.
	cleanupCodex := func(cg *codex.GeneratedConfig) {
		if cg != nil {
			cg.Cleanup()
		}
	}

	if needsProxyForGrants || needsProxyForFirewall {
		p := proxy.NewProxy()

		// Create CA for TLS interception
		caDir := filepath.Join(credential.DefaultStoreDir(), "ca")
		ca, err := proxy.NewCA(caDir)
		if err != nil {
			return nil, fmt.Errorf("creating CA: %w", err)
		}
		p.SetCA(ca)

		// Load credentials for granted providers
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr != nil {
			return nil, fmt.Errorf("getting encryption key: %w", keyErr)
		}
		store, err := credential.NewFileStore(
			credential.DefaultStoreDir(),
			key,
		)
		if err == nil {
			for _, grant := range opts.Grants {
				provider := credential.Provider(strings.Split(grant, ":")[0])
				if cred, err := store.Get(provider); err == nil {
					// Use provider-specific setup if available
					if setup := credential.GetProviderSetup(provider); setup != nil {
						setup.ConfigureProxy(p, cred)
						providerEnv = append(providerEnv, setup.ContainerEnv(cred)...)
					} else if provider == credential.ProviderAWS {
						// AWS credentials are handled via credential endpoint, not header injection
						// Parse stored config: Token=roleARN, Scopes=[region, sessionDuration, externalID]
						awsConfig := credential.AWSConfig{
							RoleARN: cred.Token,
							Region:  "us-east-1",
						}
						if len(cred.Scopes) > 0 && cred.Scopes[0] != "" {
							awsConfig.Region = cred.Scopes[0]
						}
						if len(cred.Scopes) > 1 {
							awsConfig.SessionDurationStr = cred.Scopes[1]
						}
						if len(cred.Scopes) > 2 {
							awsConfig.ExternalID = cred.Scopes[2]
						}

						sessionDuration, err := awsConfig.SessionDuration()
						if err != nil {
							return nil, fmt.Errorf("invalid AWS session duration: %w", err)
						}

						awsProvider, err := proxy.NewAWSCredentialProvider(
							ctx,
							awsConfig.RoleARN,
							awsConfig.Region,
							sessionDuration,
							awsConfig.ExternalID,
							"moat-"+r.ID,
						)
						if err != nil {
							return nil, fmt.Errorf("creating AWS credential provider: %w", err)
						}
						// Store provider; handler will be set later after auth token is generated
						r.AWSCredentialProvider = awsProvider
					}
				}
			}
		}

		proxyServer = proxy.NewServer(p)

		// Apple containers access the host via gateway IP, so the proxy must
		// bind to all interfaces. Docker can use localhost since it has
		// host.docker.internal or host network mode.
		// When binding to all interfaces, we require authentication to prevent
		// unauthorized network access to credentials.
		var proxyAuthToken string
		if m.runtime.Type() == container.RuntimeApple {
			proxyServer.SetBindAddr("0.0.0.0")

			// Generate a secure random token for proxy authentication
			tokenBytes := make([]byte, 32)
			if _, err := rand.Read(tokenBytes); err != nil {
				return nil, fmt.Errorf("generating proxy auth token: %w", err)
			}
			proxyAuthToken = hex.EncodeToString(tokenBytes)
			p.SetAuthToken(proxyAuthToken)
		}

		// Set up AWS credential handler if AWS grant is active
		if r.AWSCredentialProvider != nil {
			// Use same auth token as the main proxy (if set)
			r.AWSCredentialProvider.SetAuthToken(proxyAuthToken)
			p.SetAWSHandler(r.AWSCredentialProvider.Handler())
		}

		// Set up request logging with atomic store reference for safe concurrent access.
		// The store is created later, so we use atomic.Value to avoid data races.
		var storeRef atomic.Value // holds *storage.RunStore
		p.SetLogger(func(data proxy.RequestLogData) {
			store, _ := storeRef.Load().(*storage.RunStore)
			if store == nil {
				// Store not yet initialized - early request during container startup.
				// This is expected and non-fatal; the request won't be logged.
				log.Debug("skipping network log: store not yet initialized",
					"method", data.Method,
					"url", data.URL)
				return
			}
			var errStr string
			if data.Err != nil {
				errStr = data.Err.Error()
			}
			// Best-effort logging; errors are non-fatal
			_ = store.WriteNetworkRequest(storage.NetworkRequest{
				Timestamp:       time.Now().UTC(),
				Method:          data.Method,
				URL:             data.URL,
				StatusCode:      data.StatusCode,
				Duration:        data.Duration.Milliseconds(),
				Error:           errStr,
				RequestHeaders:  proxy.FilterHeaders(data.RequestHeaders, data.AuthInjected, data.InjectedHeaderName),
				ResponseHeaders: proxy.FilterHeaders(data.ResponseHeaders, false, ""),
				RequestBody:     string(data.RequestBody),
				ResponseBody:    string(data.ResponseBody),
				BodyTruncated:   len(data.RequestBody) > proxy.MaxBodySize || len(data.ResponseBody) > proxy.MaxBodySize,
			})
		})
		r.storeRef = &storeRef // Save reference to update later

		// Configure network policy from agent.yaml
		if opts.Config != nil {
			p.SetNetworkPolicy(opts.Config.Network.Policy, opts.Config.Network.Allow, opts.Grants)
		}

		if err := proxyServer.Start(); err != nil {
			return nil, fmt.Errorf("starting proxy: %w", err)
		}

		// Get proxy host address (needed for both proxy URL and firewall setup)
		hostAddr := m.runtime.GetHostAddress()

		// Store proxy details for firewall setup (applied after container starts)
		if needsProxyForFirewall {
			r.FirewallEnabled = true
			r.ProxyHost = hostAddr
			proxyPortInt, _ := strconv.Atoi(proxyServer.Port())
			r.ProxyPort = proxyPortInt
		}

		// Determine proxy URL based on runtime's host address
		// Include authentication credentials in URL when token is set (Apple containers)
		proxyHost := hostAddr + ":" + proxyServer.Port()
		var proxyURL string
		if proxyAuthToken != "" {
			// Include auth credentials in URL: http://moat:token@host:port
			proxyURL = "http://moat:" + proxyAuthToken + "@" + proxyHost
		} else {
			proxyURL = "http://" + proxyHost
		}

		proxyEnv = []string{
			"HTTP_PROXY=" + proxyURL,
			"HTTPS_PROXY=" + proxyURL,
			"http_proxy=" + proxyURL,
			"https_proxy=" + proxyURL,
			// Terminal settings for TUI applications
			"TERM=xterm-256color",
		}

		// Mount CA certificate (not the private key) for container to trust.
		// We mount a directory (not just the file) because Apple container
		// only supports directory mounts, not individual file mounts.
		// The private key stays on the host - only the proxy needs it for signing.
		caCertOnlyDir := filepath.Join(caDir, "public")
		if err := ensureCACertOnlyDir(caDir, caCertOnlyDir); err != nil {
			return nil, fmt.Errorf("creating CA cert-only directory: %w", err)
		}
		mounts = append(mounts, container.MountConfig{
			Source:   caCertOnlyDir,
			Target:   "/etc/ssl/certs/moat-ca",
			ReadOnly: true,
		})

		// Set env vars for tools that support custom CA bundles.
		// This tells various tools to trust our TLS-intercepting proxy's CA certificate
		// so they can make HTTPS requests through the proxy for credential injection.
		// The CA cert is at ca.crt within the mounted directory.
		caCertInContainer := "/etc/ssl/certs/moat-ca/ca.crt"
		proxyEnv = append(proxyEnv, "SSL_CERT_FILE="+caCertInContainer)       // curl, wget, many others
		proxyEnv = append(proxyEnv, "REQUESTS_CA_BUNDLE="+caCertInContainer)  // Python requests
		proxyEnv = append(proxyEnv, "NODE_EXTRA_CA_CERTS="+caCertInContainer) // Node.js
		proxyEnv = append(proxyEnv, "GIT_SSL_CAINFO="+caCertInContainer)      // Git (for HTTPS clones)

		// Add provider-specific env vars (collected during credential loading)
		proxyEnv = append(proxyEnv, providerEnv...)

		// Set up AWS credential_process if AWS grant is active
		// Instead of static credential injection, we use credential_process for dynamic refresh.
		// A small binary inside the container fetches credentials from our proxy on demand.
		if r.AWSCredentialProvider != nil {
			// Create temp directory for credential helper and config
			awsDir, err := os.MkdirTemp("", "agentops-aws-*")
			if err != nil {
				return nil, fmt.Errorf("creating AWS credential helper directory: %w", err)
			}
			r.awsTempDir = awsDir // Track for cleanup

			// Write the credential helper script
			// Use 0700 permissions since the script contains the credential endpoint URL
			helperPath := filepath.Join(awsDir, "credentials")
			if err := os.WriteFile(helperPath, GetAWSCredentialHelper(), 0700); err != nil {
				return nil, fmt.Errorf("writing AWS credential helper: %w", err)
			}

			// Write AWS config file
			awsConfig := fmt.Sprintf(`[default]
credential_process = /agentops/aws/credentials
region = %s
`, r.AWSCredentialProvider.Region())
			configPath := filepath.Join(awsDir, "config")
			if err := os.WriteFile(configPath, []byte(awsConfig), 0644); err != nil {
				return nil, fmt.Errorf("writing AWS config: %w", err)
			}

			// Mount the directory
			mounts = append(mounts, container.MountConfig{
				Source:   awsDir,
				Target:   "/agentops/aws",
				ReadOnly: true,
			})

			// Build credential endpoint URL
			credentialURL := "http://" + proxyHost + "/_aws/credentials"

			// Set environment variables
			proxyEnv = append(proxyEnv,
				"AWS_CONFIG_FILE=/agentops/aws/config",
				"AGENTOPS_CREDENTIAL_URL="+credentialURL,
				"AWS_REGION="+r.AWSCredentialProvider.Region(),
				// AWS traffic goes through proxy for firewall/observability.
				// Tell AWS SDK to trust our CA for MITM SSL.
				"AWS_CA_BUNDLE="+caCertInContainer,
				// Disable pager - containers may not have 'less' installed
				"AWS_PAGER=",
			)

			// Include auth token if proxy requires it
			if proxyAuthToken != "" {
				proxyEnv = append(proxyEnv, "AGENTOPS_CREDENTIAL_TOKEN="+proxyAuthToken)
			}

			fmt.Printf("AWS credential_process configured (role: %s)\n",
				filepath.Base(r.AWSCredentialProvider.RoleARN()))
		}
	}

	// Set up SSH agent proxy for SSH grants (e.g., git clone git@github.com:...)
	var sshServer *sshagent.Server
	var sshSocketDir string // Track for cleanup on error
	sshGrants := filterSSHGrants(opts.Grants)
	if len(sshGrants) > 0 {
		upstreamSocket := os.Getenv("SSH_AUTH_SOCK")
		if upstreamSocket == "" {
			// Clean up HTTP proxy if it was started
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("SSH grants require SSH_AUTH_SOCK to be set\n\n" +
				"Start your SSH agent with: eval \"$(ssh-agent -s)\" && ssh-add")
		}

		// Load SSH mappings for granted hosts
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("getting encryption key: %w", keyErr)
		}
		store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("opening credential store: %w", err)
		}

		sshMappings, err := store.GetSSHMappingsForHosts(sshGrants)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("loading SSH mappings: %w", err)
		}
		if len(sshMappings) == 0 {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("no SSH keys configured for hosts: %v\n\n"+
				"Grant SSH access first:\n"+
				"  moat grant ssh --host %s", sshGrants, sshGrants[0])
		}

		// Connect to upstream SSH agent
		upstreamAgent, err := sshagent.ConnectAgent(upstreamSocket)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("connecting to SSH agent: %w", err)
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
		usesTCP := !m.runtime.SupportsHostNetwork()

		if usesTCP {
			// Use TCP server - container will use socat to bridge.
			// Apple containers access the host via gateway IP, so we must bind to all
			// interfaces. Docker Desktop also runs containers in a VM, so same applies.
			// Security: the SSH agent proxy filters keys by host, so binding to 0.0.0.0
			// doesn't expose credentials - only allowed key+host combinations are usable.
			sshServer = sshagent.NewTCPServer(sshProxy, "0.0.0.0:0") // :0 picks random port
			if err := sshServer.Start(); err != nil {
				upstreamAgent.Close()
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("starting SSH agent proxy (TCP): %w", err)
			}

			// Get the actual TCP address after binding
			tcpAddr := sshServer.TCPAddr()
			hostAddr := m.runtime.GetHostAddress()
			containerSSHDir := "/run/moat/ssh"

			// Extract port from TCP address (format is "host:port" or "[::]:port")
			_, tcpPort, err := net.SplitHostPort(tcpAddr)
			if err != nil {
				cleanupSSH(sshServer)
				upstreamAgent.Close()
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("parsing SSH proxy address %q: %w", tcpAddr, err)
			}
			containerTCPAddr := hostAddr + ":" + tcpPort

			// Set env vars for container to set up socat bridge
			// Container entrypoint will run: socat UNIX-LISTEN:/run/moat/ssh/agent.sock,fork TCP:host:port
			proxyEnv = append(proxyEnv,
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
			homeDir, _ := os.UserHomeDir()
			sshSocketDir = filepath.Join(homeDir, ".moat", "sockets", r.ID)
			if err := os.MkdirAll(sshSocketDir, 0755); err != nil {
				upstreamAgent.Close()
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("creating SSH socket directory: %w", err)
			}
			socketPath := filepath.Join(sshSocketDir, "agent.sock")

			sshServer = sshagent.NewServer(sshProxy, socketPath)
			if err := sshServer.Start(); err != nil {
				upstreamAgent.Close()
				os.RemoveAll(sshSocketDir)
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("starting SSH agent proxy: %w", err)
			}

			// Mount socket directory into container
			containerSSHDir := "/run/moat/ssh"
			mounts = append(mounts, container.MountConfig{
				Source:   sshSocketDir,
				Target:   containerSSHDir,
				ReadOnly: false,
			})

			// Set SSH_AUTH_SOCK for container
			proxyEnv = append(proxyEnv, "SSH_AUTH_SOCK="+containerSSHDir+"/agent.sock")

			log.Debug("SSH agent proxy started (Unix socket mode)",
				"socket", socketPath,
				"hosts", sshGrants,
				"keys", len(sshMappings))
		}
	}

	// Configure network mode and extra hosts based on runtime capabilities
	// We use bridge mode when:
	// 1. We have ports to publish (host mode doesn't support port publishing)
	// 2. We're on macOS/Windows (host mode not supported)
	// 3. We're using Apple container runtime
	// We only use host mode when we need proxy access AND don't have ports to publish on Linux.
	var networkMode string
	var extraHosts []string
	needsPorts := len(ports) > 0
	needsProxy := proxyServer != nil

	if needsProxy || needsPorts {
		if m.runtime.SupportsHostNetwork() && !needsPorts {
			// Docker on Linux without ports: use host network so container can reach 127.0.0.1
			networkMode = "host"
		} else {
			// Use bridge mode when we need port publishing, or on macOS/Windows/Apple
			networkMode = "bridge"
			// Docker needs extra host mapping to reach host from bridge network
			if m.runtime.Type() == container.RuntimeDocker {
				extraHosts = []string{"host.docker.internal:host-gateway"}
			}
		}
	}

	// Add config env vars
	if opts.Config != nil {
		for k, v := range opts.Config.Env {
			proxyEnv = append(proxyEnv, k+"="+v)
		}
	}

	// Resolve and add secrets
	// Track resolved secrets for audit logging (logged after store is created)
	type resolvedSecret struct {
		name   string
		scheme string
	}
	var resolvedSecrets []resolvedSecret
	if opts.Config != nil && len(opts.Config.Secrets) > 0 {
		resolved, err := secrets.ResolveAll(ctx, opts.Config.Secrets)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, err
		}
		for k, v := range resolved {
			proxyEnv = append(proxyEnv, k+"="+v)
			resolvedSecrets = append(resolvedSecrets, resolvedSecret{
				name:   k,
				scheme: secrets.ParseScheme(opts.Config.Secrets[k]),
			})
		}
	}

	// Add explicit env vars (highest priority - can override config)
	proxyEnv = append(proxyEnv, opts.Env...)

	// Build port bindings for exposed services
	// Use 0.0.0.0 to let Docker bind to all interfaces, then it assigns a random host port.
	// The routing proxy handles security by only listening on localhost.
	var portBindings map[int]string
	if len(ports) > 0 {
		portBindings = make(map[int]string)
		for _, containerPort := range ports {
			portBindings[containerPort] = "0.0.0.0"
		}
	}

	// Build MOAT_* environment variables for host injection
	if len(ports) > 0 {
		globalCfg, _ := config.LoadGlobal()
		proxyPort := globalCfg.Proxy.Port

		baseHost := fmt.Sprintf("%s.localhost:%d", agentName, proxyPort)
		proxyEnv = append(proxyEnv, "MOAT_HOST="+baseHost)
		proxyEnv = append(proxyEnv, "MOAT_URL=http://"+baseHost)

		for serviceName := range ports {
			upperName := strings.ToUpper(serviceName)
			serviceHost := fmt.Sprintf("%s.%s.localhost:%d", serviceName, agentName, proxyPort)
			proxyEnv = append(proxyEnv, fmt.Sprintf("MOAT_HOST_%s=%s", upperName, serviceHost))
			proxyEnv = append(proxyEnv, fmt.Sprintf("MOAT_URL_%s=http://%s", upperName, serviceHost))
		}
	}

	// Parse and validate dependencies
	var depList []deps.Dependency
	var allDeps []string
	if opts.Config != nil {
		allDeps = append(allDeps, opts.Config.Dependencies...)
	}

	// Add implied dependencies from grants (e.g., github grant implies gh and git)
	impliedDeps := credential.ImpliedDependencies(opts.Grants)
	allDeps = append(allDeps, impliedDeps...)

	if len(allDeps) > 0 {
		var err error
		depList, err = deps.ParseAll(allDeps)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("parsing dependencies: %w", err)
		}
		if err = deps.Validate(depList); err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("validating dependencies: %w", err)
		}
		// Resolve partial runtime versions (e.g., "go@1.22" -> "go@1.22.12")
		// Uses cached API results to avoid repeated network calls
		depList, err = deps.ResolveVersions(ctx, depList)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("resolving versions: %w", err)
		}
	}

	// Extract plugins from agent.yaml for image building.
	// IMPORTANT: We only use agent.yaml config here, not merged host settings.
	// Host settings should not be baked into container images - they are applied at runtime.
	var claudeMarketplaces []claude.MarketplaceConfig
	var claudePlugins []string
	if opts.Config != nil {
		// Extract marketplaces from agent.yaml
		for name, spec := range opts.Config.Claude.Marketplaces {
			if spec.Source == "directory" {
				continue // Skip directory sources
			}
			// Determine repo path.
			// If repo is not specified but URL is, extract the path from the URL.
			// Currently only GitHub HTTPS URLs are converted to owner/repo format.
			// Other URL formats (SSH, other hosts) are used as-is since the claude
			// CLI handles them directly.
			repo := spec.Repo
			if repo == "" && spec.URL != "" {
				repo = spec.URL
				if strings.HasPrefix(repo, "https://github.com/") {
					repo = strings.TrimPrefix(repo, "https://github.com/")
					repo = strings.TrimSuffix(repo, "/")
					repo = strings.TrimSuffix(repo, ".git")
				}
			}
			if repo == "" {
				continue
			}
			claudeMarketplaces = append(claudeMarketplaces, claude.MarketplaceConfig{
				Name:   name,
				Source: spec.Source,
				Repo:   repo,
			})
		}
		// Extract enabled plugins from agent.yaml
		for pluginKey, enabled := range opts.Config.Claude.Plugins {
			if enabled {
				claudePlugins = append(claudePlugins, pluginKey)
			}
		}
	}

	// Load merged Claude settings for runtime config (this includes host settings)
	var claudeSettings *claude.Settings
	if opts.Config != nil {
		var loadErr error
		claudeSettings, loadErr = claude.LoadAllSettings(opts.Workspace, opts.Config)
		if loadErr != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("loading Claude settings: %w", loadErr)
		}
	}

	// Determine if we need Claude init (for OAuth credentials and host files)
	// This is triggered by an anthropic grant with an OAuth token
	var needsClaudeInit bool
	var claudeStagingDir string
	for _, grant := range opts.Grants {
		provider := credential.Provider(strings.Split(grant, ":")[0])
		if provider == credential.ProviderAnthropic {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(provider); err == nil {
						if credential.IsOAuthToken(cred.Token) {
							needsClaudeInit = true
						}
					}
				}
			}
			break
		}
	}

	// Determine if we need Codex init (for OpenAI credentials - both API keys and subscription tokens)
	// This is triggered by an openai grant
	var needsCodexInit bool
	var codexStagingDir string
	for _, grant := range opts.Grants {
		provider := credential.Provider(strings.Split(grant, ":")[0])
		if provider == credential.ProviderOpenAI {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if _, err := store.Get(provider); err == nil {
						// We have OpenAI credentials - need Codex init for auth.json
						needsCodexInit = true
					}
				}
			}
			break
		}
	}

	// Resolve container image based on dependencies, SSH grants, init needs, and plugins
	hasSSHGrants := len(sshGrants) > 0
	containerImage := image.Resolve(depList, &image.ResolveOptions{
		NeedsSSH:        hasSSHGrants,
		NeedsClaudeInit: needsClaudeInit,
		NeedsCodexInit:  needsCodexInit,
		ClaudePlugins:   claudePlugins,
	})

	// Determine if we need a custom image
	needsCustomImage := len(depList) > 0 || hasSSHGrants || needsClaudeInit || needsCodexInit || len(claudePlugins) > 0

	// Handle --rebuild: delete existing image to force fresh build
	if opts.Rebuild && needsCustomImage {
		exists, _ := m.runtime.ImageExists(ctx, containerImage)
		if exists {
			fmt.Printf("Removing cached image %s...\n", containerImage)
			if err := m.runtime.RemoveImage(ctx, containerImage); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to remove image: %v\n", err)
			}
		}
	}

	// Build custom image if we have dependencies or SSH grants.
	// Both Docker and Apple containers support Dockerfile builds.
	var generatedDockerfile string
	if needsCustomImage {
		// Check if BuildKit is disabled (for CI compatibility)
		useBuildKit := os.Getenv("MOAT_DISABLE_BUILDKIT") != "1"

		// Always generate the Dockerfile so we can save it to the run directory
		dockerfile, err := deps.GenerateDockerfile(depList, &deps.DockerfileOptions{
			NeedsSSH:           hasSSHGrants,
			SSHHosts:           sshGrants,
			NeedsClaudeInit:    needsClaudeInit,
			NeedsCodexInit:     needsCodexInit,
			UseBuildKit:        &useBuildKit,
			ClaudeMarketplaces: claudeMarketplaces,
			ClaudePlugins:      claudePlugins,
		})
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("generating Dockerfile: %w", err)
		}
		generatedDockerfile = dockerfile

		exists, err := m.runtime.ImageExists(ctx, containerImage)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("checking image: %w", err)
		}

		if !exists {
			depNames := make([]string, len(depList))
			for i, d := range depList {
				depNames[i] = d.Name
			}

			// Build options from config
			buildOpts := container.BuildOptions{
				NoCache: opts.Rebuild,
			}
			if opts.Config != nil {
				buildOpts.DNS = opts.Config.Container.Apple.BuilderDNS
			}

			if err := m.runtime.BuildImage(ctx, dockerfile, containerImage, buildOpts); err != nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("building image with dependencies [%s]: %w",
					strings.Join(depNames, ", "), err)
			}
		}
	}

	// Mount Claude projects directory so logs appear in the right place on host.
	// This is enabled when:
	// - claude.sync_logs is explicitly true, OR
	// - anthropic grant is configured (automatic Claude Code integration)
	var containerHome string
	if hostHome, err := os.UserHomeDir(); err == nil {
		containerHome = m.runtime.GetImageHomeDir(ctx, containerImage)
		if opts.Config != nil && opts.Config.ShouldSyncClaudeLogs() {
			claudeDir := workspaceToClaudeDir(opts.Workspace)
			hostClaudeProjects := filepath.Join(hostHome, ".claude", "projects", claudeDir)

			// Ensure directory exists on host
			if err := os.MkdirAll(hostClaudeProjects, 0755); err != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to create Claude logs directory: %v\n", err)
			} else {
				// Container writes to ~/.claude/projects/-workspace/
				// Host sees it as ~/.claude/projects/<workspace-path-encoded>/
				containerClaudeProjects := filepath.Join(containerHome, ".claude", "projects", "-workspace")
				mounts = append(mounts, container.MountConfig{
					Source:   hostClaudeProjects,
					Target:   containerClaudeProjects,
					ReadOnly: false,
				})
			}
		}
	}

	// Set up provider-specific container mounts (e.g., credential files, state files)
	if containerHome != "" {
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr == nil {
			store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
			if storeErr == nil {
				for _, grant := range opts.Grants {
					provider := credential.Provider(strings.Split(grant, ":")[0])
					if cred, err := store.Get(provider); err == nil {
						if setup := credential.GetProviderSetup(provider); setup != nil {
							providerMounts, cleanupPath, mountErr := setup.ContainerMounts(cred, containerHome)
							if mountErr != nil {
								log.Debug("failed to set up provider mounts", "provider", provider, "error", mountErr)
							} else {
								mounts = append(mounts, providerMounts...)
								if cleanupPath != "" {
									if r.ProviderCleanupPaths == nil {
										r.ProviderCleanupPaths = make(map[string]string)
									}
									r.ProviderCleanupPaths[string(provider)] = cleanupPath
								}
							}
						}
					}
				}
			}
		}
	}

	// Set up Claude staging directory for init script
	// This includes OAuth credentials, host files, and optionally plugin settings
	var claudeGenerated *claude.GeneratedConfig
	if needsClaudeInit || (opts.Config != nil) {
		// claudeSettings was loaded earlier for plugin detection

		// Determine if we need to set up plugins/marketplaces
		hasPlugins := claudeSettings != nil && claudeSettings.HasPluginsOrMarketplaces()

		// Check if we're running Claude Code (need to copy onboarding state)
		isClaudeCode := opts.Config != nil && opts.Config.ShouldSyncClaudeLogs()

		// We need a staging directory if:
		// - needsClaudeInit (OAuth credentials to set up)
		// - hasPlugins (plugin settings to configure)
		// - isClaudeCode (need to copy onboarding state from host)
		if needsClaudeInit || hasPlugins || isClaudeCode {
			// Create staging directory
			var stagingErr error
			claudeStagingDir, stagingErr = os.MkdirTemp("", "moat-claude-staging-*")
			if stagingErr != nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("creating Claude staging directory: %w", stagingErr)
			}
			// Use a flag to track cleanup responsibility. The defer cleans up on error.
			// Once claudeGenerated is assigned, it takes over cleanup, so we set the flag false.
			stagingNeedsCleanup := true
			defer func() {
				if stagingNeedsCleanup && claudeStagingDir != "" {
					os.RemoveAll(claudeStagingDir)
				}
			}()

			// Write minimal Claude config to skip onboarding
			if err := claude.WriteClaudeConfig(claudeStagingDir); err != nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("writing Claude config: %w", err)
			}

			// Populate with OAuth credentials if needed (only for OAuth tokens)
			if needsClaudeInit {
				key, keyErr := credential.DefaultEncryptionKey()
				if keyErr == nil {
					store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
					if storeErr == nil {
						if cred, err := store.Get(credential.ProviderAnthropic); err == nil {
							anthropicSetup := &claude.AnthropicSetup{}
							if err := anthropicSetup.PopulateStagingDir(cred, claudeStagingDir); err != nil {
								cleanupProxy(proxyServer)
								return nil, fmt.Errorf("populating Claude staging directory: %w", err)
							}
						}
					}
				}
			}

			// Note: Plugins are now installed during image build (via Dockerfile RUN commands),
			// not at runtime. The hasPlugins flag is used only for logging.
			if hasPlugins {
				log.Debug("plugins baked into image",
					"plugins", len(claudeSettings.EnabledPlugins),
					"marketplaces", len(claudeSettings.ExtraKnownMarketplaces))
			}

			// Transfer cleanup responsibility to claudeGenerated.
			// The defer no longer needs to clean up since claudeGenerated.Cleanup() will handle it.
			stagingNeedsCleanup = false
			claudeGenerated = &claude.GeneratedConfig{
				StagingDir: claudeStagingDir,
				TempDir:    claudeStagingDir,
			}

			// Mount staging directory
			mounts = append(mounts, container.MountConfig{
				Source:   claudeStagingDir,
				Target:   claude.ClaudeInitMountPath,
				ReadOnly: true,
			})

			// Set env var for moat-init script
			proxyEnv = append(proxyEnv, "MOAT_CLAUDE_INIT="+claude.ClaudeInitMountPath)
		}
	}

	// Set up Codex staging directory for init script
	// This includes auth config for ChatGPT subscription tokens
	var codexGenerated *codex.GeneratedConfig
	if needsCodexInit || (opts.Config != nil && opts.Config.ShouldSyncCodexLogs()) {
		// Create staging directory
		var stagingErr error
		codexStagingDir, stagingErr = os.MkdirTemp("", "moat-codex-staging-*")
		if stagingErr != nil {
			cleanupProxy(proxyServer)
			cleanupClaude(claudeGenerated)
			return nil, fmt.Errorf("creating Codex staging directory: %w", stagingErr)
		}
		// Use a flag to track cleanup responsibility. The defer cleans up on error.
		codexStagingNeedsCleanup := true
		defer func() {
			if codexStagingNeedsCleanup && codexStagingDir != "" {
				os.RemoveAll(codexStagingDir)
			}
		}()

		// Write minimal Codex config
		if err := codex.WriteCodexConfig(codexStagingDir); err != nil {
			cleanupProxy(proxyServer)
			cleanupClaude(claudeGenerated)
			return nil, fmt.Errorf("writing Codex config: %w", err)
		}

		// Populate with auth credentials if needed (only for ChatGPT subscription tokens)
		if needsCodexInit {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(credential.ProviderOpenAI); err == nil {
						openaiSetup := &codex.OpenAISetup{}
						if err := openaiSetup.PopulateStagingDir(cred, codexStagingDir); err != nil {
							cleanupProxy(proxyServer)
							cleanupClaude(claudeGenerated)
							return nil, fmt.Errorf("populating Codex staging directory: %w", err)
						}
					}
				}
			}
		}

		// Transfer cleanup responsibility to codexGenerated
		codexStagingNeedsCleanup = false
		codexGenerated = &codex.GeneratedConfig{
			StagingDir: codexStagingDir,
			TempDir:    codexStagingDir,
		}

		// Mount staging directory
		mounts = append(mounts, container.MountConfig{
			Source:   codexStagingDir,
			Target:   codex.CodexInitMountPath,
			ReadOnly: true,
		})

		// Set env var for moat-init script
		proxyEnv = append(proxyEnv, "MOAT_CODEX_INIT="+codex.CodexInitMountPath)
	}

	// Add NET_ADMIN capability if firewall is enabled (needed for iptables)
	var capAdd []string
	if r.FirewallEnabled {
		capAdd = []string{"NET_ADMIN"}
	}

	// Determine container user
	// On Linux with native Docker, we need to run as the workspace owner's UID to ensure
	// file permissions work correctly. On macOS/Windows, Docker Desktop handles UID
	// translation automatically, so we can use the default moatuser (5000).
	const moatuserUID = 5000
	var containerUser string
	if goruntime.GOOS == "linux" {
		// Use the workspace owner's UID/GID, not the process UID.
		// This handles cases where moat is run with sudo or as a different user.
		workspaceUID, workspaceGID := getWorkspaceOwner(opts.Workspace)
		if workspaceUID != moatuserUID {
			// Run as workspace owner's UID:GID for correct file permissions
			containerUser = fmt.Sprintf("%d:%d", workspaceUID, workspaceGID)
			log.Debug("using workspace owner UID for container", "uid", workspaceUID, "gid", workspaceGID, "workspace", opts.Workspace)
		}
		// If workspace owner UID is 5000, we can use the image's default moatuser
	}
	// On macOS/Windows, leave containerUser empty to use the image default (moatuser)

	// Create container
	containerID, err := m.runtime.CreateContainer(ctx, container.Config{
		Name:         r.ID,
		Image:        containerImage,
		Cmd:          cmd,
		WorkingDir:   "/workspace",
		Env:          proxyEnv,
		User:         containerUser,
		ExtraHosts:   extraHosts,
		NetworkMode:  networkMode,
		Mounts:       mounts,
		PortBindings: portBindings,
		CapAdd:       capAdd,
		Interactive:  opts.Interactive,
		HasMoatUser:  needsCustomImage, // moat-built images have moatuser; base images don't
	})
	if err != nil {
		// Clean up proxy servers if container creation fails
		cleanupProxy(proxyServer)
		cleanupSSH(sshServer)
		cleanupClaude(claudeGenerated)
		cleanupCodex(codexGenerated)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID
	r.ProxyServer = proxyServer
	r.SSHAgentServer = sshServer
	if claudeGenerated != nil {
		r.ClaudeConfigTempDir = claudeGenerated.TempDir
	}
	if codexGenerated != nil {
		r.CodexConfigTempDir = codexGenerated.TempDir
	}

	// Ensure proxy is running if we have ports to expose
	if len(ports) > 0 {
		// Enable TLS on the routing proxy
		if _, tlsErr := m.proxyLifecycle.EnableTLS(); tlsErr != nil {
			// Clean up container
			if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupProxy(proxyServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("enabling TLS on routing proxy: %w", tlsErr)
		}
		if proxyErr := m.proxyLifecycle.EnsureRunning(); proxyErr != nil {
			// Clean up container
			if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupProxy(proxyServer)
			cleanupClaude(claudeGenerated)
			cleanupCodex(codexGenerated)
			return nil, fmt.Errorf("starting routing proxy: %w", proxyErr)
		}
	}

	// Create run storage
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		// Clean up container and proxy if storage creation fails
		if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
			log.Debug("failed to remove container during cleanup", "error", rmErr)
		}
		cleanupProxy(proxyServer)
		cleanupClaude(claudeGenerated)
		cleanupCodex(codexGenerated)
		return nil, fmt.Errorf("creating run storage: %w", err)
	}
	r.Store = store
	// Update atomic reference for concurrent logger access
	if r.storeRef != nil {
		r.storeRef.Store(store)
	}

	// Save the generated Dockerfile to the run directory for debugging/inspection
	if generatedDockerfile != "" {
		if saveErr := store.SaveDockerfile(generatedDockerfile); saveErr != nil {
			log.Debug("failed to save Dockerfile to run directory", "error", saveErr)
		}
	}

	// Open audit store for tamper-proof logging
	auditStore, err := audit.OpenStore(filepath.Join(store.Dir(), "audit.db"))
	if err != nil {
		// Clean up container, proxy, and storage if audit store fails
		if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
			log.Debug("failed to remove container during cleanup", "error", rmErr)
		}
		cleanupProxy(proxyServer)
		cleanupClaude(claudeGenerated)
		cleanupCodex(codexGenerated)
		return nil, fmt.Errorf("opening audit store: %w", err)
	}
	r.AuditStore = auditStore

	// Initialize snapshot engine if not disabled
	if opts.Config != nil && !opts.Config.Snapshots.Disabled {
		snapshotDir := filepath.Join(store.Dir(), "snapshots")
		snapEngine, snapErr := snapshot.NewEngine(opts.Workspace, snapshotDir, snapshot.EngineOptions{
			UseGitignore: !opts.Config.Snapshots.Exclude.IgnoreGitignore,
			Additional:   opts.Config.Snapshots.Exclude.Additional,
		})
		if snapErr != nil {
			// Log warning but don't fail - snapshots are best-effort
			log.Warn("failed to initialize snapshot engine", "error", snapErr)
		} else {
			r.SnapEngine = snapEngine
		}
		// Track trigger settings for use in Start()
		r.DisablePreRunSnapshot = opts.Config.Snapshots.Triggers.DisablePreRun
	}

	// Save initial metadata (best-effort; non-fatal if it fails)
	_ = r.SaveMetadata()

	// Log resolved secrets (best-effort; non-fatal if it fails)
	for _, secret := range resolvedSecrets {
		_ = store.WriteSecretResolution(storage.SecretResolution{
			Timestamp: time.Now().UTC(),
			Name:      secret.name,
			Backend:   secret.scheme,
		})
		// Also log to tamper-proof audit trail
		_, _ = auditStore.AppendSecret(audit.SecretData{
			Name:    secret.name,
			Backend: secret.scheme,
		})
	}

	// Wire up SSH audit logging if SSH server is active
	if sshServer != nil {
		sshServer.Proxy().SetAuditFunc(func(event sshagent.AuditEvent) {
			_, _ = auditStore.AppendSSH(audit.SSHData{
				Action:      event.Action,
				Host:        event.Host,
				Fingerprint: event.Fingerprint,
				Error:       event.Error,
			})
		})
	}

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	return r, nil
}

// StartOptions configures how a run is started.
type StartOptions struct {
	// StreamLogs controls whether container logs are streamed to stdout.
	// Set to false for interactive mode where attach handles I/O.
	StreamLogs bool
}

// Start begins execution of a run.
func (m *Manager) Start(ctx context.Context, runID string, opts StartOptions) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}
	r.State = StateStarting
	m.mu.Unlock()

	if err := m.runtime.StartContainer(ctx, r.ContainerID); err != nil {
		m.mu.Lock()
		r.State = StateFailed
		r.Error = err.Error()
		m.mu.Unlock()
		return err
	}

	// Set up firewall if enabled (strict network policy)
	// This blocks all outbound traffic except to the proxy
	if r.FirewallEnabled && r.ProxyPort > 0 {
		if err := m.runtime.SetupFirewall(ctx, r.ContainerID, r.ProxyHost, r.ProxyPort); err != nil {
			// Firewall setup failed - this is fatal for strict policy since the user
			// explicitly requested network isolation. Without iptables, only proxy-level
			// filtering applies, which can be bypassed by tools that ignore HTTP_PROXY.
			if stopErr := m.runtime.StopContainer(ctx, r.ContainerID); stopErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to stop container after firewall error: %v\n", stopErr)
			}
			return fmt.Errorf("firewall setup failed (required for strict network policy): %w", err)
		}
	}

	// Get actual port bindings after container starts
	if len(r.Ports) > 0 {
		// Retry a few times - Docker may need a moment to set up port bindings
		var bindings map[int]int
		var err error
		for i := 0; i < 5; i++ {
			bindings, err = m.runtime.GetPortBindings(ctx, r.ContainerID)
			if err != nil || len(bindings) >= len(r.Ports) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			// Log but don't fail - container is running
			fmt.Fprintf(os.Stderr, "Warning: getting port bindings: %v\n", err)
		} else {
			r.HostPorts = make(map[string]int)
			services := make(map[string]string)
			for serviceName, containerPort := range r.Ports {
				if hostPort, ok := bindings[containerPort]; ok {
					r.HostPorts[serviceName] = hostPort
					services[serviceName] = fmt.Sprintf("127.0.0.1:%d", hostPort)
				}
			}
			// Register routes
			if len(services) > 0 {
				if err := m.routes.Add(r.Name, services); err != nil {
					fmt.Fprintf(os.Stderr, "Warning: registering routes: %v\n", err)
				}
			}
		}
	}

	m.mu.Lock()
	r.State = StateRunning
	r.StartedAt = time.Now()
	m.mu.Unlock()

	// Save state to disk
	_ = r.SaveMetadata()

	// Create pre-run snapshot
	if r.SnapEngine != nil && !r.DisablePreRunSnapshot {
		if _, err := r.SnapEngine.Create(snapshot.TypePreRun, ""); err != nil {
			log.Warn("failed to create pre-run snapshot", "error", err)
		}
	}

	// Stream logs to stdout (unless disabled for interactive mode)
	if opts.StreamLogs {
		go m.streamLogs(context.Background(), r)
	}

	return nil
}

// StartAttached starts a run with stdin/stdout/stderr attached from the beginning.
// This is required for TUI applications (like Codex CLI) that need the terminal
// connected before the process starts to properly detect terminal capabilities.
// Unlike Start + Attach, this ensures the TTY is ready when the container command begins.
func (m *Manager) StartAttached(ctx context.Context, runID string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}
	r.State = StateStarting
	containerID := r.ContainerID
	m.mu.Unlock()

	// Start with attachment - this ensures TTY is connected before process starts.
	// TTY mode must match how the container was created (see CreateContainer in
	// docker.go and apple.go). Both runtimes only enable TTY when os.Stdin is a
	// real terminal, so we use the same check here.
	useTTY := term.IsTerminal(os.Stdin)
	attachOpts := container.AttachOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		TTY:    useTTY,
	}

	// Channel to receive the attach result
	attachDone := make(chan error, 1)

	go func() {
		attachDone <- m.runtime.StartAttached(ctx, containerID, attachOpts)
	}()

	// Give the container a moment to start before checking state.
	// See containerStartDelay for rationale.
	time.Sleep(containerStartDelay)

	// Update state to running (the container has started)
	m.mu.Lock()
	if r.State == StateStarting {
		r.State = StateRunning
		r.StartedAt = time.Now()
	}
	m.mu.Unlock()

	// Save state to disk
	_ = r.SaveMetadata()

	// Set up firewall if enabled (do this after container starts)
	if r.FirewallEnabled && r.ProxyPort > 0 {
		if err := m.runtime.SetupFirewall(ctx, r.ContainerID, r.ProxyHost, r.ProxyPort); err != nil {
			// Firewall setup failed - this is fatal for strict policy
			if stopErr := m.runtime.StopContainer(ctx, r.ContainerID); stopErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: failed to stop container after firewall error: %v\n", stopErr)
			}
			return fmt.Errorf("firewall setup failed (required for strict network policy): %w", err)
		}
	}

	// Wait for the attachment to complete (container exits or context canceled)
	return <-attachDone
}

// streamLogs streams container logs to stdout for real-time feedback.
// Note: Final log capture to storage is handled by Wait() using ContainerLogsAll
// to ensure complete logs are captured even for fast-exiting containers.
func (m *Manager) streamLogs(ctx context.Context, r *Run) {
	logs, err := m.runtime.ContainerLogs(ctx, r.ContainerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting logs: %v\n", err)
		return
	}
	defer logs.Close()

	// Stream to stdout only for real-time feedback
	// Storage is handled by Wait() after container exits
	_, _ = io.Copy(os.Stdout, logs)
}

// Stop terminates a running run.
func (m *Manager) Stop(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}

	if r.State != StateRunning && r.State != StateStarting {
		m.mu.Unlock()
		return nil // Already stopped
	}

	r.State = StateStopping
	m.mu.Unlock()

	if err := m.runtime.StopContainer(ctx, r.ContainerID); err != nil {
		// Log but don't fail - container might already be stopped
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Stop the proxy server if one was created
	if r.ProxyServer != nil {
		if err := r.ProxyServer.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
	}

	// Stop the SSH agent server if one was created
	if r.SSHAgentServer != nil {
		if err := r.SSHAgentServer.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping SSH agent proxy: %v\n", err)
		}
	}

	// Unregister routes for this agent
	if r.Name != "" {
		_ = m.routes.Remove(r.Name)
	}

	m.mu.Lock()
	r.State = StateStopped
	r.StoppedAt = time.Now()
	keepContainer := r.KeepContainer
	containerID := r.ContainerID
	providerCleanupPaths := r.ProviderCleanupPaths
	m.mu.Unlock()

	// Save state to disk
	_ = r.SaveMetadata()

	// Auto-remove container unless --keep was specified
	if !keepContainer {
		if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
			fmt.Fprintf(os.Stderr, "Removing container: %v\n", rmErr)
		}
	}

	// Clean up provider resources
	for provider, cleanupPath := range providerCleanupPaths {
		if setup := credential.GetProviderSetup(credential.Provider(provider)); setup != nil {
			setup.Cleanup(cleanupPath)
		}
	}

	// Clean up Claude config temp directory
	m.mu.RLock()
	claudeConfigTempDir := r.ClaudeConfigTempDir
	m.mu.RUnlock()
	if claudeConfigTempDir != "" {
		if err := os.RemoveAll(claudeConfigTempDir); err != nil {
			log.Debug("failed to remove Claude config temp dir", "path", claudeConfigTempDir, "error", err)
		}
	}

	return nil
}

// Wait blocks until the run completes.
func (m *Manager) Wait(ctx context.Context, runID string) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	// Wait for container to exit or context cancellation
	done := make(chan error, 1)
	go func() {
		exitCode, err := m.runtime.WaitContainer(ctx, containerID)
		if err != nil {
			done <- err
			return
		}
		if exitCode != 0 {
			done <- fmt.Errorf("container exited with code %d", exitCode)
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		// Capture all logs after container exits to ensure we don't miss any
		// (the streaming goroutine may not have captured everything for fast containers)
		if r.Store != nil {
			if allLogs, logErr := m.runtime.ContainerLogsAll(context.Background(), containerID); logErr == nil && len(allLogs) > 0 {
				if lw, lwErr := r.Store.LogWriter(); lwErr == nil {
					_, _ = lw.Write(allLogs)
					lw.Close()
				}
			}
		}

		// Stop the proxy server if one was created
		if r.ProxyServer != nil {
			if stopErr := r.ProxyServer.Stop(context.Background()); stopErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", stopErr)
			}
		}

		// Stop the SSH agent server if one was created
		if r.SSHAgentServer != nil {
			if stopErr := r.SSHAgentServer.Stop(); stopErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: stopping SSH agent proxy: %v\n", stopErr)
			}
		}

		// Unregister routes for this agent
		if r.Name != "" {
			_ = m.routes.Remove(r.Name)
		}

		m.mu.Lock()
		r.State = StateStopped
		r.StoppedAt = time.Now()
		if err != nil {
			r.Error = err.Error()
		}
		keepContainer := r.KeepContainer
		providerCleanupPaths := r.ProviderCleanupPaths
		m.mu.Unlock()

		// Save state to disk
		_ = r.SaveMetadata()

		// Auto-remove container unless --keep was specified
		if !keepContainer {
			if rmErr := m.runtime.RemoveContainer(context.Background(), containerID); rmErr != nil {
				fmt.Fprintf(os.Stderr, "Removing container: %v\n", rmErr)
			}
		}

		// Clean up provider resources
		for provider, cleanupPath := range providerCleanupPaths {
			if setup := credential.GetProviderSetup(credential.Provider(provider)); setup != nil {
				setup.Cleanup(cleanupPath)
			}
		}

		// Clean up AWS temp directory
		if r.awsTempDir != "" {
			if rmErr := os.RemoveAll(r.awsTempDir); rmErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: removing AWS temp dir: %v\n", rmErr)
			}
		}

		// Clean up Claude config temp directory
		if r.ClaudeConfigTempDir != "" {
			if rmErr := os.RemoveAll(r.ClaudeConfigTempDir); rmErr != nil {
				log.Debug("failed to remove Claude config temp dir", "path", r.ClaudeConfigTempDir, "error", rmErr)
			}
		}

		// Clean up Codex config temp directory
		if r.CodexConfigTempDir != "" {
			if rmErr := os.RemoveAll(r.CodexConfigTempDir); rmErr != nil {
				log.Debug("failed to remove Codex config temp dir", "path", r.CodexConfigTempDir, "error", rmErr)
			}
		}

		return err
	case <-ctx.Done():
		// Context canceled - caller chose to detach, don't stop the run
		return ctx.Err()
	}
}

// Get retrieves a run by ID.
func (m *Manager) Get(runID string) (*Run, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	r, ok := m.runs[runID]
	if !ok {
		return nil, fmt.Errorf("run %s not found", runID)
	}
	return r, nil
}

// List returns all runs.
func (m *Manager) List() []*Run {
	m.mu.RLock()
	defer m.mu.RUnlock()

	runs := make([]*Run, 0, len(m.runs))
	for _, r := range m.runs {
		runs = append(runs, r)
	}
	return runs
}

// Destroy removes a run and its resources.
func (m *Manager) Destroy(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}

	if r.State == StateRunning {
		m.mu.Unlock()
		return fmt.Errorf("cannot destroy running run %s; stop it first", runID)
	}
	m.mu.Unlock()

	// Remove container
	if err := m.runtime.RemoveContainer(ctx, r.ContainerID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	// Stop the proxy server if one was created and still running
	if r.ProxyServer != nil {
		if err := r.ProxyServer.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
	}

	// Stop the SSH agent server if one was created and still running
	if r.SSHAgentServer != nil {
		if err := r.SSHAgentServer.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping SSH agent proxy: %v\n", err)
		}
	}

	// Unregister routes for this agent
	if r.Name != "" {
		if err := m.routes.Remove(r.Name); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing routes: %v\n", err)
		}
	}

	// Check if we should stop the routing proxy (no more agents with ports)
	if m.proxyLifecycle.ShouldStop() {
		if err := m.proxyLifecycle.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping routing proxy: %v\n", err)
		}
	}

	// Close audit store
	if r.AuditStore != nil {
		if err := r.AuditStore.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: closing audit store: %v\n", err)
		}
	}

	// Clean up provider resources
	for provider, cleanupPath := range r.ProviderCleanupPaths {
		if setup := credential.GetProviderSetup(credential.Provider(provider)); setup != nil {
			setup.Cleanup(cleanupPath)
		}
	}

	// Clean up AWS temp directory
	if r.awsTempDir != "" {
		if err := os.RemoveAll(r.awsTempDir); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing AWS temp dir: %v\n", err)
		}
	}

	// Clean up Claude config temp directory
	if r.ClaudeConfigTempDir != "" {
		if err := os.RemoveAll(r.ClaudeConfigTempDir); err != nil {
			log.Debug("failed to remove Claude config temp dir", "path", r.ClaudeConfigTempDir, "error", err)
		}
	}

	// Clean up Codex config temp directory
	if r.CodexConfigTempDir != "" {
		if err := os.RemoveAll(r.CodexConfigTempDir); err != nil {
			log.Debug("failed to remove Codex config temp dir", "path", r.CodexConfigTempDir, "error", err)
		}
	}

	// Remove run storage directory (logs, traces, metadata)
	if r.Store != nil {
		if err := r.Store.Remove(); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: removing storage: %v\n", err)
		}
	}

	m.mu.Lock()
	delete(m.runs, runID)
	m.mu.Unlock()

	return nil
}

// Attach connects stdin/stdout/stderr to a running container.
func (m *Manager) Attach(ctx context.Context, runID string, stdin io.Reader, stdout, stderr io.Writer) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	return m.runtime.Attach(ctx, containerID, container.AttachOptions{
		Stdin:  stdin,
		Stdout: stdout,
		Stderr: stderr,
		TTY:    true, // Default to TTY mode for now
	})
}

// ResizeTTY resizes the container's TTY to the given dimensions.
func (m *Manager) ResizeTTY(ctx context.Context, runID string, height, width uint) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	return m.runtime.ResizeTTY(ctx, containerID, height, width)
}

// FollowLogs streams container logs to the provided writer.
// This is more reliable than Attach for output-only mode on already-running containers.
func (m *Manager) FollowLogs(ctx context.Context, runID string, w io.Writer) error {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	logs, err := m.runtime.ContainerLogs(ctx, containerID)
	if err != nil {
		return fmt.Errorf("getting container logs: %w", err)
	}
	defer logs.Close()

	_, err = io.Copy(w, logs)
	return err
}

// RecentLogs returns the last n lines of container logs.
// Used to show context when re-attaching to a running container.
func (m *Manager) RecentLogs(runID string, lines int) (string, error) {
	m.mu.RLock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.RUnlock()
		return "", fmt.Errorf("run %s not found", runID)
	}
	containerID := r.ContainerID
	m.mu.RUnlock()

	// Get all logs (non-following)
	allLogs, err := m.runtime.ContainerLogsAll(context.Background(), containerID)
	if err != nil {
		return "", err
	}

	// Return last n lines
	return lastNLines(string(allLogs), lines), nil
}

// lastNLines returns the last n lines of a string.
func lastNLines(s string, n int) string {
	if n <= 0 {
		return ""
	}

	// Find line boundaries from the end
	end := len(s)
	count := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '\n' {
			count++
			if count == n+1 {
				return s[i+1 : end]
			}
		}
	}
	// Fewer than n lines, return all
	return s
}

// Close releases manager resources.
func (m *Manager) Close() error {
	// Stop all proxy servers
	m.mu.RLock()
	for _, r := range m.runs {
		if r.ProxyServer != nil {
			if err := r.ProxyServer.Stop(context.Background()); err != nil {
				log.Debug("failed to stop proxy during manager close", "run", r.ID, "error", err)
			}
		}
		if r.SSHAgentServer != nil {
			if err := r.SSHAgentServer.Stop(); err != nil {
				log.Debug("failed to stop SSH agent during manager close", "run", r.ID, "error", err)
			}
		}
	}
	m.mu.RUnlock()

	return m.runtime.Close()
}

// workspaceToClaudeDir converts an absolute workspace path to Claude's project directory format.
// Example: /home/alice/projects/myapp -> -home-alice-projects-myapp
func workspaceToClaudeDir(absPath string) string {
	// Normalize to forward slashes for cross-platform consistency
	normalized := filepath.ToSlash(absPath)
	cleaned := strings.TrimPrefix(normalized, "/")
	return "-" + strings.ReplaceAll(cleaned, "/", "-")
}

// filterSSHGrants extracts SSH host grants from the grants list.
// SSH grants have the format "ssh:<host>" (e.g., "ssh:github.com").
func filterSSHGrants(grants []string) []string {
	var hosts []string
	for _, g := range grants {
		if strings.HasPrefix(g, "ssh:") {
			hosts = append(hosts, strings.TrimPrefix(g, "ssh:"))
		}
	}
	return hosts
}

// ensureCACertOnlyDir creates a directory containing only the CA certificate,
// not the private key. This is used to mount into containers so they can trust
// the proxy's TLS certificates without exposing the signing key.
//
// SECURITY: This function removes any files other than ca.crt from the directory
// to prevent accidental exposure of the private key if it was mistakenly copied.
func ensureCACertOnlyDir(caDir, certOnlyDir string) error {
	certSrc := filepath.Join(caDir, "ca.crt")
	certDst := filepath.Join(certOnlyDir, "ca.crt")

	// Read source certificate
	srcContent, err := os.ReadFile(certSrc)
	if err != nil {
		return fmt.Errorf("CA certificate not found: %w", err)
	}
	srcHash := sha256.Sum256(srcContent)

	// Create directory if it doesn't exist
	if err = os.MkdirAll(certOnlyDir, 0755); err != nil {
		return fmt.Errorf("creating directory: %w", err)
	}

	// SECURITY: Remove any files that shouldn't be in this directory.
	// This prevents accidental exposure of ca.key if it was mistakenly copied.
	entries, err := os.ReadDir(certOnlyDir)
	if err != nil {
		return fmt.Errorf("reading directory: %w", err)
	}
	for _, entry := range entries {
		if entry.Name() != "ca.crt" {
			staleFile := filepath.Join(certOnlyDir, entry.Name())
			if err = os.Remove(staleFile); err != nil {
				return fmt.Errorf("removing stale file %s: %w", entry.Name(), err)
			}
		}
	}

	// Check if destination already has the same content (by hash)
	if dstContent, readErr := os.ReadFile(certDst); readErr == nil {
		dstHash := sha256.Sum256(dstContent)
		if srcHash == dstHash {
			return nil // Already up to date
		}
	}

	if err = os.WriteFile(certDst, srcContent, 0644); err != nil {
		return fmt.Errorf("writing CA certificate: %w", err)
	}

	return nil
}
