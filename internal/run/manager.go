package run

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/docker/docker/pkg/stdcopy"
	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/image"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/name"
	"github.com/majorcontext/moat/internal/provider"
	_ "github.com/majorcontext/moat/internal/providers" // register all credential providers
	awsprov "github.com/majorcontext/moat/internal/providers/aws"
	"github.com/majorcontext/moat/internal/providers/claude" // only for settings types (LoadAllSettings, Settings, MarketplaceConfig) - provider setup uses provider interfaces
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/majorcontext/moat/internal/routing"
	"github.com/majorcontext/moat/internal/secrets"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/sshagent"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/majorcontext/moat/internal/term"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
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

// ManagerOptions configures the run manager.
type ManagerOptions struct {
	// NoSandbox disables gVisor sandbox for Docker containers.
	// If nil, uses platform-aware defaults (gVisor on Linux, standard on macOS/Windows).
	NoSandbox *bool
}

// NewManagerWithOptions creates a new run manager with the given options.
func NewManagerWithOptions(opts ManagerOptions) (*Manager, error) {
	var runtimeOpts container.RuntimeOptions
	if opts.NoSandbox != nil {
		// User explicitly set --no-sandbox flag
		runtimeOpts.Sandbox = !*opts.NoSandbox
	} else {
		// Use platform-aware defaults
		runtimeOpts = container.DefaultRuntimeOptions()
	}

	rt, err := container.NewRuntimeWithOptions(runtimeOpts)
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

// NewManager creates a new run manager with default options.
func NewManager() (*Manager, error) {
	return NewManagerWithOptions(ManagerOptions{})
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
		// Filter service containers to only those that still exist
		serviceContainers := make(map[string]string, len(meta.ServiceContainers))
		for name, id := range meta.ServiceContainers {
			if _, scErr := m.runtime.ContainerState(ctx, id); scErr == nil {
				serviceContainers[name] = id
			}
		}

		r := &Run{
			ID:                runID,
			Name:              meta.Name,
			Workspace:         meta.Workspace,
			Grants:            meta.Grants,
			Agent:             meta.Agent,
			Image:             meta.Image,
			Ports:             meta.Ports,
			State:             runState,
			ContainerID:       meta.ContainerID,
			Store:             store,
			Interactive:       meta.Interactive,
			CreatedAt:         meta.CreatedAt,
			StartedAt:         meta.StartedAt,
			StoppedAt:         meta.StoppedAt,
			Error:             meta.Error,
			exitCh:            make(chan struct{}),
			ServiceContainers: serviceContainers,
			NetworkID:         meta.NetworkID,
			WorktreeBranch:    meta.WorktreeBranch,
			WorktreePath:      meta.WorktreePath,
			WorktreeRepoID:    meta.WorktreeRepoID,
		}

		// If container is already stopped, close exitCh immediately
		// so any Wait() calls don't hang, and clean up stale routes
		// so the name can be reused without requiring "moat clean".
		// Note: StateFailed is reachable via the default branch when
		// meta.State is "failed" and the container is in an unknown state.
		if runState == StateStopped || runState == StateFailed {
			close(r.exitCh)
			if r.Name != "" {
				if err := m.routes.Remove(r.Name); err != nil {
					log.Debug("removing stale route", "name", r.Name, "error", err)
				}
			}
		}

		// Update metadata if state changed
		if string(runState) != meta.State {
			_ = r.SaveMetadata()
		}

		m.mu.Lock()
		m.runs[runID] = r
		m.mu.Unlock()

		// For running containers, start background monitor to capture logs when they exit.
		// This handles the case where moat restarts while containers are running.
		if runState == StateRunning {
			go m.monitorContainerExit(context.Background(), r)
		}

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

	// Validate grants before allocating any resources (proxy, container, etc.)
	needsGrantValidation := len(opts.Grants) > 0 || (opts.Config != nil && len(opts.Config.MCP) > 0)
	if needsGrantValidation {
		key, keyErr := credential.DefaultEncryptionKey()
		if keyErr != nil {
			return nil, fmt.Errorf("getting encryption key: %w", keyErr)
		}
		store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
		if err != nil {
			return nil, fmt.Errorf("opening credential store: %w", err)
		}
		if err := validateGrants(opts.Grants, store); err != nil {
			return nil, err
		}
		if opts.Config != nil && len(opts.Config.MCP) > 0 {
			if err := validateMCPGrants(opts.Config, store); err != nil {
				return nil, err
			}
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
		exitCh:        make(chan struct{}),
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

	// If workspace is a git worktree, mount the main .git directory so git
	// operations work inside the container. The .git file in worktrees contains
	// an absolute host path; mounting the main .git at that same path makes
	// the reference resolve as-is.
	if info, err := worktree.ResolveGitDir(opts.Workspace); err != nil {
		log.Debug("failed to resolve worktree git dir", "error", err)
	} else if info != nil {
		mounts = append(mounts, container.MountConfig{
			Source:   info.MainGitDir,
			Target:   info.MainGitDir,
			ReadOnly: false,
		})
		log.Debug("mounted main git dir for worktree", "path", info.MainGitDir)
	}

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

	// Add volume mounts from config.
	// All runtimes use host-backed bind mounts (~/.moat/volumes/<agent>/<name>/)
	// so the directory is owned by the current user, matching the container user.
	if opts.Config != nil && len(opts.Config.Volumes) > 0 {
		for _, vol := range opts.Config.Volumes {
			volDir := config.VolumeDir(opts.Config.Name, vol.Name)
			if err := os.MkdirAll(volDir, 0755); err != nil {
				return nil, fmt.Errorf("creating volume directory %s: %w", volDir, err)
			}
			mounts = append(mounts, container.MountConfig{
				Source:   volDir,
				Target:   vol.Target,
				ReadOnly: vol.ReadOnly,
			})
			log.Debug("added volume mount", "dir", volDir, "target", vol.Target)
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

	// cleanupAgentConfig is a helper to clean up agent-generated config (via provider.ContainerConfig).
	cleanupAgentConfig := func(cfg *provider.ContainerConfig) {
		if cfg != nil && cfg.Cleanup != nil {
			cfg.Cleanup()
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

		// Collect refreshable targets during grant loop
		var refreshTargets []refreshTarget

		if err == nil {
			for _, grant := range opts.Grants {
				grantName := strings.Split(grant, ":")[0]

				// SSH grants are handled separately (SSH agent setup below)
				if grantName == "ssh" {
					continue
				}

				providerName := credential.Provider(grantName)
				log.Debug("processing grant", "grant", grant, "providerName", providerName)
				cred, getErr := store.Get(providerName)
				if getErr != nil {
					// Should not happen: validateGrants checks before resource allocation.
					cleanupProxy(proxyServer)
					return nil, fmt.Errorf("grant %q: credential not found: %w", grantName, getErr)
				}
				// Convert credential for new provider interface
				provCred := provider.FromLegacy(cred)

				// Use new provider registry (supports aliases like "anthropic" -> "claude")
				// MCP grants (e.g., "mcp-test") have no registered provider — they are
				// handled by the proxy MCP relay, not by provider.ConfigureProxy.
				prov := provider.Get(grantName)
				if prov == nil {
					log.Debug("grant has no registered provider (e.g. MCP grant), skipping proxy config", "grant", grantName)
					continue
				}
				prov.ConfigureProxy(p, provCred)
				envVars := prov.ContainerEnv(provCred)
				log.Debug("adding provider env vars", "provider", providerName, "vars", envVars)
				providerEnv = append(providerEnv, envVars...)

				// Check if this provider supports token refresh
				if rp, ok := prov.(provider.RefreshableProvider); ok && rp.CanRefresh(provCred) {
					refreshTargets = append(refreshTargets, refreshTarget{
						providerName: providerName,
						refresher:    rp,
						cred:         cred,
						store:        store,
					})
				}

				// Handle AWS endpoint provider
				if ep := provider.GetEndpoint(string(providerName)); ep != nil {
					// AWS credentials are handled via credential endpoint
					// Parse stored config from Metadata (new format) with fallback to Scopes (legacy)
					awsCfg, err := awsprov.ConfigFromCredential(provCred)
					if err != nil {
						return nil, fmt.Errorf("parsing AWS credential: %w", err)
					}

					awsProvider, err := proxy.NewAWSCredentialProvider(
						ctx,
						awsCfg.RoleARN,
						awsCfg.Region,
						awsCfg.SessionDuration,
						awsCfg.ExternalID,
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

		// Configure MCP servers for credential injection
		if opts.Config != nil && len(opts.Config.MCP) > 0 {
			proxyServer.Proxy().SetMCPServers(opts.Config.MCP)
			proxyServer.Proxy().SetCredentialStore(store)
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

		// Store proxy auth token (for tests and debugging)
		r.ProxyAuthToken = proxyAuthToken

		// Start background token refresh loop for refreshable grants
		if len(refreshTargets) > 0 {
			refreshCtx, refreshCancel := context.WithCancel(context.Background())
			r.tokenRefreshCancel = refreshCancel
			go m.runTokenRefreshLoop(refreshCtx, r, p, refreshTargets)
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

		// Exclude proxy's own address from proxying to prevent circular references
		// This is critical for MCP relay endpoint which is on the proxy itself
		// Also exclude BuildKit sidecar hostname to allow direct gRPC connections
		noProxy := hostAddr + ",localhost,127.0.0.1,buildkit"

		proxyEnv = []string{
			"HTTP_PROXY=" + proxyURL,
			"HTTPS_PROXY=" + proxyURL,
			"http_proxy=" + proxyURL,
			"https_proxy=" + proxyURL,
			"NO_PROXY=" + noProxy,
			"no_proxy=" + noProxy,
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
			if err := os.WriteFile(helperPath, awsprov.GetCredentialHelper(), 0700); err != nil {
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

	// Pass pre_run hook command to moat-init via env var
	if opts.Config != nil && opts.Config.Hooks.PreRun != "" {
		proxyEnv = append(proxyEnv, "MOAT_PRE_RUN="+opts.Config.Hooks.PreRun)
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

		for endpointName := range ports {
			upperName := strings.ToUpper(endpointName)
			endpointHost := fmt.Sprintf("%s.%s.localhost:%d", endpointName, agentName, proxyPort)
			proxyEnv = append(proxyEnv, fmt.Sprintf("MOAT_HOST_%s=%s", upperName, endpointHost))
			proxyEnv = append(proxyEnv, fmt.Sprintf("MOAT_URL_%s=http://%s", upperName, endpointHost))
		}
	}

	// Parse and validate dependencies
	var depList []deps.Dependency
	var allDeps []string
	if opts.Config != nil {
		allDeps = append(allDeps, opts.Config.Dependencies...)
	}

	// Add implied dependencies from grants (e.g., github grant implies gh and git)
	for _, grant := range opts.Grants {
		grantName := strings.Split(grant, ":")[0]
		if prov := provider.Get(grantName); prov != nil {
			allDeps = append(allDeps, prov.ImpliedDependencies()...)
		}
	}

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

	// Inject host git identity when git is a dependency.
	gitEnv, hasGit := hostGitIdentity(depList)
	proxyEnv = append(proxyEnv, gitEnv...)

	// Split dependencies into installable and services
	serviceDeps := deps.FilterServices(depList)
	installableDeps := deps.FilterInstallable(depList)

	// Resolve docker dependency if present
	// This validates that Apple containers are not used with docker:host dependency,
	// and returns the appropriate config for the mode (socket mount for host, privileged for dind).
	dockerConfig, dockerErr := ResolveDockerDependency(depList, m.runtime.Type())
	if dockerErr != nil {
		cleanupProxy(proxyServer)
		cleanupSSH(sshServer)
		return nil, dockerErr
	}
	// Compute BuildKit configuration (automatic with docker:dind)
	buildkitCfg := computeBuildKitConfig(dockerConfig, r.ID)

	if dockerConfig != nil {
		switch dockerConfig.Mode {
		case deps.DockerModeHost:
			// Host mode: mount Docker socket and pass GID for group setup
			mounts = append(mounts, dockerConfig.SocketMount)
			proxyEnv = append(proxyEnv, "MOAT_DOCKER_GID="+dockerConfig.GroupID)
		case deps.DockerModeDind:
			// Dind mode: signal moat-init to start dockerd
			proxyEnv = append(proxyEnv, "MOAT_DOCKER_DIND=1")
			if !buildkitCfg.Enabled {
				// Disable BuildKit if not using sidecar (fallback case)
				proxyEnv = append(proxyEnv, "DOCKER_BUILDKIT=0")
				proxyEnv = append(proxyEnv, "MOAT_DISABLE_BUILDKIT=1")
			}
		}
	}

	// Load merged Claude settings which includes:
	// - ~/.claude/plugins/known_marketplaces.json (marketplace URLs)
	// - ~/.claude/settings.json (enabled plugins)
	// - ~/.moat/claude/settings.json (moat user defaults)
	// - <workspace>/.claude/settings.json (project settings)
	// - agent.yaml claude.* fields (run overrides)
	var claudeSettings *claude.Settings
	if opts.Config != nil {
		var loadErr error
		claudeSettings, loadErr = claude.LoadAllSettings(opts.Workspace, opts.Config)
		if loadErr != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("loading Claude settings: %w", loadErr)
		}
	}

	// Extract plugins and marketplaces for image building.
	// We use the merged settings which includes both agent.yaml config and host settings.
	// This allows plugins configured on the host to work in containers.
	var claudeMarketplaces []claude.MarketplaceConfig
	var claudePlugins []string

	if claudeSettings != nil {
		// Build a map of marketplace name -> repo URL from merged settings.
		// The claude CLI accepts marketplace repos in several formats:
		// - GitHub shorthand: owner/repo
		// - HTTPS URLs: https://github.com/owner/repo.git
		// - SSH URLs: git@github.com:owner/repo.git
		// We normalize GitHub HTTPS URLs to owner/repo format for cleaner output.
		// Other URL formats are passed through unchanged.
		marketplaceRepos := make(map[string]string)
		for name, entry := range claudeSettings.ExtraKnownMarketplaces {
			if entry.Source.URL != "" {
				// Convert GitHub HTTPS URL to owner/repo format
				repo := entry.Source.URL
				if strings.HasPrefix(repo, "https://github.com/") {
					repo = strings.TrimPrefix(repo, "https://github.com/")
					repo = strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
				}
				marketplaceRepos[name] = repo
				claudeMarketplaces = append(claudeMarketplaces, claude.MarketplaceConfig{
					Name:   name,
					Source: entry.Source.Source,
					Repo:   repo,
				})
			}
		}

		// Extract enabled plugins, but only those with known marketplace URLs.
		// Note: We use LastIndexByte to handle the case where plugin names contain @.
		// Invalid plugin key formats (e.g., missing @, multiple @) are caught later
		// during Dockerfile generation by validPluginKey regex (defense-in-depth).
		for pluginKey, enabled := range claudeSettings.EnabledPlugins {
			if !enabled {
				continue
			}
			// Extract marketplace name from plugin key (format: "plugin@marketplace")
			if idx := strings.LastIndexByte(pluginKey, '@'); idx >= 0 {
				marketplace := pluginKey[idx+1:]
				if _, hasRepo := marketplaceRepos[marketplace]; hasRepo {
					claudePlugins = append(claudePlugins, pluginKey)
				} else {
					// Use warning for agent.yaml plugins, debug for auto-discovered host settings
					if claudeSettings.PluginSources != nil &&
						claudeSettings.PluginSources[pluginKey] == claude.SourceAgentYAML {
						ui.Warnf("Skipping plugin %q: marketplace %q is not configured. Add it to agent.yaml under claude.marketplaces.", pluginKey, marketplace)
						log.Debug("skipping plugin from agent.yaml with unknown marketplace",
							"plugin", pluginKey,
							"marketplace", marketplace)
					} else {
						log.Debug("skipping plugin with unknown marketplace",
							"plugin", pluginKey,
							"marketplace", marketplace)
					}
				}
			} else {
				log.Debug("skipping plugin with invalid format (missing @marketplace)",
					"plugin", pluginKey)
			}
		}
	}

	// Determine if we need Claude init (for OAuth credentials and host files)
	// This is triggered by:
	// - a claude/anthropic grant with an OAuth token, OR
	// - claude-code in the dependencies list (user may run Claude without credential injection)
	var needsClaudeInit bool
	for _, grant := range opts.Grants {
		providerName := credential.Provider(strings.Split(grant, ":")[0])
		// Check for both "claude" (new) and "anthropic" (legacy) provider names
		if providerName == "claude" || providerName == credential.ProviderAnthropic {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(providerName); err == nil {
						if credential.IsOAuthToken(cred.Token) {
							needsClaudeInit = true
						}
					}
				}
			}
			break
		}
	}
	if !needsClaudeInit {
		for _, d := range depList {
			if d.Name == "claude-code" {
				needsClaudeInit = true
				break
			}
		}
	}

	// Determine if we need Codex init (for OpenAI credentials - both API keys and subscription tokens)
	// This is triggered by an openai grant
	var needsCodexInit bool
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

	// Determine if we need Gemini init (for Gemini credentials - both OAuth and API keys)
	// This is triggered by:
	// - a gemini grant, OR
	// - gemini-cli in the dependencies list
	var needsGeminiInit bool
	for _, grant := range opts.Grants {
		provider := credential.Provider(strings.Split(grant, ":")[0])
		if provider == credential.ProviderGemini {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if _, err := store.Get(provider); err == nil {
						needsGeminiInit = true
					}
				}
			}
			break
		}
	}
	if !needsGeminiInit {
		for _, d := range depList {
			if d.Name == "gemini-cli" {
				needsGeminiInit = true
				break
			}
		}
	}

	// Hooks config for image hashing, Dockerfile generation, and pre_run
	var hooks *deps.HooksConfig
	if opts.Config != nil && (opts.Config.Hooks.PostBuild != "" || opts.Config.Hooks.PostBuildRoot != "" || opts.Config.Hooks.PreRun != "") {
		hooks = &deps.HooksConfig{
			PostBuild:     opts.Config.Hooks.PostBuild,
			PostBuildRoot: opts.Config.Hooks.PostBuildRoot,
			PreRun:        opts.Config.Hooks.PreRun,
		}
	}

	// Resolve container image based on dependencies, SSH grants, init needs, plugins, and build hooks
	hasSSHGrants := len(sshGrants) > 0
	containerImage := image.Resolve(installableDeps, &image.ResolveOptions{
		NeedsSSH:        hasSSHGrants,
		NeedsClaudeInit: needsClaudeInit,
		NeedsCodexInit:  needsCodexInit,
		NeedsGeminiInit: needsGeminiInit,
		ClaudePlugins:   claudePlugins,
		Hooks:           hooks,
	})

	// Set agent and image for logging context
	if opts.Config != nil && opts.Config.Agent != "" {
		r.Agent = opts.Config.Agent
	}
	r.Image = containerImage

	// Determine if we need a custom image
	hasHooks := hooks != nil
	needsCustomImage := len(installableDeps) > 0 || hasSSHGrants || needsClaudeInit || needsCodexInit || needsGeminiInit || len(claudePlugins) > 0 || hasHooks

	// Handle --rebuild: delete existing image to force fresh build
	if opts.Rebuild && needsCustomImage {
		exists, _ := m.runtime.BuildManager().ImageExists(ctx, containerImage)
		if exists {
			fmt.Printf("Removing cached image %s...\n", containerImage)
			if err := m.runtime.RemoveImage(ctx, containerImage); err != nil {
				ui.Warnf("Failed to remove image: %v", err)
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
		result, err := deps.GenerateDockerfile(installableDeps, &deps.DockerfileOptions{
			NeedsSSH:           hasSSHGrants,
			SSHHosts:           sshGrants,
			NeedsClaudeInit:    needsClaudeInit,
			NeedsCodexInit:     needsCodexInit,
			NeedsGeminiInit:    needsGeminiInit,
			NeedsFirewall:      needsProxyForFirewall,
			NeedsGitIdentity:   hasGit,
			UseBuildKit:        &useBuildKit,
			ClaudeMarketplaces: claudeMarketplaces,
			ClaudePlugins:      claudePlugins,
			Hooks:              hooks,
		})
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("generating Dockerfile: %w", err)
		}
		generatedDockerfile = result.Dockerfile

		exists, err := m.runtime.BuildManager().ImageExists(ctx, containerImage)
		if err != nil {
			cleanupProxy(proxyServer)
			return nil, fmt.Errorf("checking image: %w", err)
		}

		if !exists {
			depNames := make([]string, len(installableDeps))
			for i, d := range installableDeps {
				depNames[i] = d.Name
			}

			// Build options from config
			buildOpts := container.BuildOptions{
				NoCache: opts.Rebuild,
			}
			if opts.Config != nil {
				buildOpts.DNS = opts.Config.Container.DNS
			}

			buildMgr := m.runtime.BuildManager()
			if buildMgr == nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("cannot build image: runtime %s does not support building", m.runtime.Type())
			}

			buildOpts.ContextFiles = result.ContextFiles
			if err := buildMgr.BuildImage(ctx, result.Dockerfile, containerImage, buildOpts); err != nil {
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
		imageHome := m.runtime.BuildManager().GetImageHomeDir(ctx, containerImage)
		containerHome = resolveContainerHome(needsCustomImage, imageHome)
		if opts.Config != nil && opts.Config.ShouldSyncClaudeLogs() {
			claudeDir := workspaceToClaudeDir(opts.Workspace)
			hostClaudeProjects := filepath.Join(hostHome, ".claude", "projects", claudeDir)

			// Ensure directory exists on host
			if err := os.MkdirAll(hostClaudeProjects, 0755); err != nil {
				ui.Warnf("Failed to create Claude logs directory: %v", err)
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
					providerName := credential.Provider(strings.Split(grant, ":")[0])
					if cred, err := store.Get(providerName); err == nil {
						if prov := provider.Get(string(providerName)); prov != nil {
							provCred := provider.FromLegacy(cred)
							providerMounts, cleanupPath, mountErr := prov.ContainerMounts(provCred, containerHome)
							if mountErr != nil {
								log.Debug("failed to set up provider mounts", "provider", providerName, "error", mountErr)
							} else {
								mounts = append(mounts, providerMounts...)
								if cleanupPath != "" {
									if r.ProviderCleanupPaths == nil {
										r.ProviderCleanupPaths = make(map[string]string)
									}
									r.ProviderCleanupPaths[string(providerName)] = cleanupPath
								}
							}
						}
					}
				}
			}
		}
	}

	// Set up Claude staging directory for init script using the provider interface.
	// This includes OAuth credentials, host files, and MCP server configuration.
	var claudeConfig *provider.ContainerConfig
	if needsClaudeInit || (opts.Config != nil) {
		// claudeSettings was loaded earlier for plugin detection
		hasPlugins := claudeSettings != nil && claudeSettings.HasPluginsOrMarketplaces()
		isClaudeCode := opts.Config != nil && opts.Config.ShouldSyncClaudeLogs()

		// We need PrepareContainer if:
		// - needsClaudeInit (OAuth credentials to set up)
		// - hasPlugins (plugin settings to configure)
		// - isClaudeCode (need to copy onboarding state from host)
		if needsClaudeInit || hasPlugins || isClaudeCode {
			claudeProvider := provider.GetAgent("claude")
			if claudeProvider == nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("claude provider not registered")
			}

			// Build MCP server configuration for .claude.json
			// Use proxy relay URLs instead of direct MCP server URLs to work around
			// Claude Code's MCP client not respecting HTTP_PROXY environment variables.
			mcpServers := make(map[string]provider.MCPServerConfig)
			if opts.Config != nil && len(opts.Config.MCP) > 0 {
				proxyAddr := fmt.Sprintf("%s:%s", m.runtime.GetHostAddress(), proxyServer.Port())
				for _, mcp := range opts.Config.MCP {
					if mcp.Auth == nil {
						continue // Skip servers without auth
					}
					relayURL := fmt.Sprintf("http://%s/mcp/%s", proxyAddr, mcp.Name)
					mcpServers[mcp.Name] = provider.MCPServerConfig{
						URL: relayURL,
						Headers: map[string]string{
							mcp.Auth.Header: "moat-stub-" + mcp.Auth.Grant,
						},
					}
				}
			}

			// Get Claude credential for PrepareContainer
			var claudeCred *provider.Credential
			if needsClaudeInit {
				key, keyErr := credential.DefaultEncryptionKey()
				if keyErr == nil {
					store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
					if storeErr == nil {
						// Try "claude" first, fall back to "anthropic" (legacy)
						cred, err := store.Get(credential.Provider("claude"))
						if err != nil {
							cred, err = store.Get(credential.ProviderAnthropic)
						}
						if err == nil {
							claudeCred = provider.FromLegacy(cred)
						}
					}
				}
			}

			// Build local MCP server config from claude.mcp entries
			var claudeLocalMCP map[string]provider.LocalMCPServerConfig
			if opts.Config != nil && len(opts.Config.Claude.MCP) > 0 {
				claudeLocalMCP = make(map[string]provider.LocalMCPServerConfig)
				for name, spec := range opts.Config.Claude.MCP {
					claudeLocalMCP[name] = provider.LocalMCPServerConfig{
						Command: spec.Command,
						Args:    spec.Args,
						Env:     spec.Env,
						Cwd:     spec.Cwd,
					}
				}
			}

			// Call provider to prepare container config
			var prepErr error
			claudeConfig, prepErr = claudeProvider.PrepareContainer(ctx, provider.PrepareOpts{
				Credential:      claudeCred,
				ContainerHome:   containerHome,
				MCPServers:      mcpServers,
				LocalMCPServers: claudeLocalMCP,
				// HostConfig is read automatically by the provider if nil
			})
			if prepErr != nil {
				cleanupProxy(proxyServer)
				return nil, fmt.Errorf("preparing Claude container config: %w", prepErr)
			}

			// Add mounts and env vars from provider
			mounts = append(mounts, claudeConfig.Mounts...)
			proxyEnv = append(proxyEnv, claudeConfig.Env...)

			// Note: Plugins are now installed during image build (via Dockerfile RUN commands),
			// not at runtime. The hasPlugins flag is used only for logging.
			if hasPlugins {
				log.Debug("plugins baked into image",
					"plugins", len(claudeSettings.EnabledPlugins),
					"marketplaces", len(claudeSettings.ExtraKnownMarketplaces))
			}
		}
	}

	// Set up Codex staging directory for init script using the provider interface.
	// This includes auth config for OpenAI tokens.
	var codexConfig *provider.ContainerConfig
	if needsCodexInit || (opts.Config != nil && opts.Config.ShouldSyncCodexLogs()) {
		codexProvider := provider.GetAgent("codex")
		if codexProvider == nil {
			cleanupProxy(proxyServer)
			cleanupAgentConfig(claudeConfig)
			return nil, fmt.Errorf("codex provider not registered")
		}

		// Get Codex credential for PrepareContainer
		var codexCred *provider.Credential
		if needsCodexInit {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(credential.ProviderOpenAI); err == nil {
						codexCred = provider.FromLegacy(cred)
					}
				}
			}
		}

		// Build local MCP server config from codex.mcp entries
		var codexLocalMCP map[string]provider.LocalMCPServerConfig
		if opts.Config != nil && len(opts.Config.Codex.MCP) > 0 {
			codexLocalMCP = make(map[string]provider.LocalMCPServerConfig)
			for name, spec := range opts.Config.Codex.MCP {
				codexLocalMCP[name] = provider.LocalMCPServerConfig{
					Command: spec.Command,
					Args:    spec.Args,
					Env:     spec.Env,
					Cwd:     spec.Cwd,
				}
			}
		}

		// Call provider to prepare container config
		var prepErr error
		codexConfig, prepErr = codexProvider.PrepareContainer(ctx, provider.PrepareOpts{
			Credential:      codexCred,
			ContainerHome:   containerHome,
			LocalMCPServers: codexLocalMCP,
		})
		if prepErr != nil {
			cleanupProxy(proxyServer)
			cleanupAgentConfig(claudeConfig)
			return nil, fmt.Errorf("preparing Codex container config: %w", prepErr)
		}

		// Add mounts and env vars from provider
		mounts = append(mounts, codexConfig.Mounts...)
		proxyEnv = append(proxyEnv, codexConfig.Env...)
	}

	// Set up Gemini staging directory for init script using the provider interface.
	// This includes settings.json and optionally oauth_creds.json.
	var geminiConfig *provider.ContainerConfig
	if needsGeminiInit || (opts.Config != nil && opts.Config.ShouldSyncGeminiLogs()) {
		geminiProvider := provider.GetAgent("gemini")
		if geminiProvider == nil {
			cleanupProxy(proxyServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("gemini provider not registered")
		}

		// Get Gemini credential for PrepareContainer
		var geminiCred *provider.Credential
		if needsGeminiInit {
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr == nil {
				store, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key)
				if storeErr == nil {
					if cred, err := store.Get(credential.ProviderGemini); err == nil {
						geminiCred = provider.FromLegacy(cred)
					}
				}
			}
		}

		// Build local MCP server config from gemini.mcp entries
		var geminiLocalMCP map[string]provider.LocalMCPServerConfig
		if opts.Config != nil && len(opts.Config.Gemini.MCP) > 0 {
			geminiLocalMCP = make(map[string]provider.LocalMCPServerConfig)
			for name, spec := range opts.Config.Gemini.MCP {
				geminiLocalMCP[name] = provider.LocalMCPServerConfig{
					Command: spec.Command,
					Args:    spec.Args,
					Env:     spec.Env,
					Cwd:     spec.Cwd,
				}
			}
		}

		// Call provider to prepare container config
		var prepErr error
		geminiConfig, prepErr = geminiProvider.PrepareContainer(ctx, provider.PrepareOpts{
			Credential:      geminiCred,
			ContainerHome:   containerHome,
			LocalMCPServers: geminiLocalMCP,
		})
		if prepErr != nil {
			cleanupProxy(proxyServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("preparing Gemini container config: %w", prepErr)
		}

		// Add mounts and env vars from provider
		mounts = append(mounts, geminiConfig.Mounts...)
		proxyEnv = append(proxyEnv, geminiConfig.Env...)
	}

	// MCP servers are now configured via .claude.json in the staging directory
	// (handled by the claude provider's PrepareContainer), not via environment variables.

	// Add NET_ADMIN capability if firewall is enabled (needed for iptables)
	var capAdd []string
	if r.FirewallEnabled {
		capAdd = []string{"NET_ADMIN"}
	}

	// Build supplementary groups for container process
	// Only needed for docker:host mode to access the Docker socket
	var groupAdd []string
	if dockerConfig != nil && dockerConfig.Mode == deps.DockerModeHost {
		groupAdd = append(groupAdd, dockerConfig.GroupID)
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

	// Determine if container needs privileged mode (only for docker:dind)
	var privileged bool
	if dockerConfig != nil && dockerConfig.Privileged {
		privileged = true
		if goruntime.GOOS == "darwin" {
			ui.Warn("Creating privileged container for docker:dind. On macOS, the Docker Desktop VM provides host protection.")
			log.Debug("creating privileged container for docker:dind",
				"platform", "macOS",
				"isolation", "Docker Desktop VM boundary provides host protection")
		} else {
			ui.Warn("Creating privileged container for docker:dind on Linux. This grants direct host kernel access. See https://majorcontext.com/moat/concepts/sandboxing#docker-access-modes")
			log.Debug("creating privileged container for docker:dind",
				"platform", "Linux",
				"risk", "privileged mode grants direct host kernel access")
		}
	}

	// Create network and start BuildKit sidecar if enabled
	var networkID string
	if buildkitCfg.Enabled {
		log.Debug("creating network for buildkit sidecar", "network", buildkitCfg.NetworkName)
		netMgr := m.runtime.NetworkManager()
		if netMgr == nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (networks not supported by %s)", m.runtime.Type())
		}
		netID, netErr := netMgr.CreateNetwork(ctx, buildkitCfg.NetworkName)
		if netErr != nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("failed to create Docker network for buildkit sidecar: %w", netErr)
		}
		networkID = netID

		// Start BuildKit sidecar
		log.Debug("starting buildkit sidecar", "image", buildkitCfg.SidecarImage)
		sidecarCfg := container.SidecarConfig{
			Image:      buildkitCfg.SidecarImage,
			Name:       buildkitCfg.SidecarName,
			Hostname:   "buildkit",
			NetworkID:  networkID,
			Cmd:        []string{"--addr", "tcp://0.0.0.0:1234"},
			Privileged: true, // BuildKit needs privileged mode for bind mounts
			RunID:      r.ID, // For orphan cleanup if moat crashes
			Mounts: []container.MountConfig{
				{
					// Mount dind's Docker socket so BuildKit can export images to the daemon.
					// This is the dind container's socket, NOT the host's socket.
					// BuildKit uses this to export built images via the "docker" exporter type.
					Source:   "/var/run/docker.sock",
					Target:   "/var/run/docker.sock",
					ReadOnly: false,
				},
				{
					// Mount /tmp so BuildKit can access build contexts created by the main container.
					// Both containers share the same /tmp directory for build context synchronization.
					Source:   "/tmp",
					Target:   "/tmp",
					ReadOnly: false,
				},
			},
		}

		sidecarMgr := m.runtime.SidecarManager()
		if sidecarMgr == nil {
			netMgr := m.runtime.NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (sidecars not supported by %s)", m.runtime.Type())
		}
		buildkitContainerID, sidecarErr := sidecarMgr.StartSidecar(ctx, sidecarCfg)
		if sidecarErr != nil {
			// Clean up network on failure
			netMgr := m.runtime.NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("failed to start buildkit sidecar: %w\n\nEnsure Docker can access Docker Hub to pull %s", sidecarErr, buildkitCfg.SidecarImage)
		}

		// Wait for BuildKit to be ready (up to 10 seconds)
		log.Debug("waiting for buildkit sidecar to be ready")
		ready := false
		for i := 0; i < 10; i++ {
			time.Sleep(1 * time.Second)
			inspect, inspectErr := sidecarMgr.InspectContainer(ctx, buildkitContainerID)
			if inspectErr == nil && inspect.State != nil && inspect.State.Running {
				ready = true
				break
			}
		}
		if !ready {
			_ = m.runtime.StopContainer(ctx, buildkitContainerID) //nolint:errcheck
			netMgr := m.runtime.NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("buildkit sidecar failed to become ready within 10 seconds")
		}

		// Store buildkit IDs in run metadata
		r.BuildkitContainerID = buildkitContainerID
		r.NetworkID = networkID

		// Set network mode to use the buildkit network
		networkMode = networkID
	}

	// Start service dependencies
	if len(serviceDeps) > 0 {
		svcMgr := m.runtime.ServiceManager()
		if svcMgr == nil {
			cleanupProxy(proxyServer)
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("service dependencies require a runtime with service support\n\n" +
				"Either:\n  - Use Docker or Apple container runtime\n  - Install services on your host and set MOAT_*_URL manually")
		}

		// Validate services config
		if opts.Config != nil {
			serviceNames := make([]string, len(serviceDeps))
			for i, d := range serviceDeps {
				serviceNames[i] = d.Name
			}
			if err := opts.Config.ValidateServices(serviceNames); err != nil {
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, err
			}
		}

		// Ensure network exists (share with BuildKit if present)
		if networkID == "" {
			netMgr := m.runtime.NetworkManager()
			if netMgr == nil {
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("service dependencies require network support")
			}
			networkName := fmt.Sprintf("moat-%s", r.ID)
			var netErr error
			networkID, netErr = netMgr.CreateNetwork(ctx, networkName)
			if netErr != nil {
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("creating service network: %w", netErr)
			}
			r.NetworkID = networkID
		}

		// Set network on service manager
		svcMgr.SetNetworkID(networkID)

		// Start services
		r.ServiceContainers = make(map[string]string)
		var serviceInfos []container.ServiceInfo

		cleanupServices := func() {
			for _, info := range serviceInfos {
				_ = svcMgr.StopService(ctx, info)
			}
		}

		for _, dep := range serviceDeps {
			var userSpec *config.ServiceSpec
			if opts.Config != nil {
				if s, ok := opts.Config.Services[dep.Name]; ok {
					userSpec = &s
				}
			}

			svcCfg, err := buildServiceConfig(dep, r.ID, userSpec)
			if err != nil {
				cleanupServices()
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("configuring %s service: %w", dep.Name, err)
			}

			info, err := svcMgr.StartService(ctx, svcCfg)
			if err != nil {
				cleanupServices()
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("starting %s service: %w", dep.Name, err)
			}

			serviceInfos = append(serviceInfos, info)
			r.ServiceContainers[dep.Name] = info.ID
		}

		// Wait for readiness
		for i, dep := range serviceDeps {
			wait := true
			if opts.Config != nil {
				if s, ok := opts.Config.Services[dep.Name]; ok {
					wait = s.ServiceWait()
				}
			}
			if !wait {
				continue
			}

			info := serviceInfos[i]
			log.Debug("waiting for service to be ready", "service", dep.Name)
			if err := waitForServiceReady(ctx, svcMgr, info); err != nil {
				cleanupServices()
				cleanupProxy(proxyServer)
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("%s service failed to become ready: %w\n\n"+
					"Service container logs:\n  moat logs %s --service %s\n\n"+
					"Or disable wait:\n  services:\n    %s:\n      wait: false",
					dep.Name, err, r.ID, dep.Name, dep.Name)
			}
		}

		// Inject MOAT_* env vars
		for i, dep := range serviceDeps {
			spec, _ := deps.GetSpec(dep.Name)
			var userSpec *config.ServiceSpec
			if opts.Config != nil {
				if s, ok := opts.Config.Services[dep.Name]; ok {
					userSpec = &s
				}
			}
			svcEnv := generateServiceEnv(spec.Service, serviceInfos[i], userSpec)

			// Sort env var keys for deterministic ordering
			envKeys := make([]string, 0, len(svcEnv))
			for k := range svcEnv {
				envKeys = append(envKeys, k)
			}
			sort.Strings(envKeys)

			for _, k := range envKeys {
				proxyEnv = append(proxyEnv, k+"="+svcEnv[k])
			}
		}

		// Use network for main container
		networkMode = networkID
	}

	// Add BuildKit env vars if enabled
	buildkitEnv := computeBuildKitEnv(buildkitCfg.Enabled)
	proxyEnv = append(proxyEnv, buildkitEnv...)

	// Extract container resource limits from config (applies to both Docker and Apple)
	var memoryMB, cpus int
	var dns []string
	if opts.Config != nil {
		memoryMB = opts.Config.Container.Memory
		cpus = opts.Config.Container.CPUs
		dns = opts.Config.Container.DNS
	}

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
		GroupAdd:     groupAdd,
		Privileged:   privileged,
		Interactive:  opts.Interactive,
		HasMoatUser:  needsCustomImage, // moat-built images have moatuser; base images don't
		MemoryMB:     memoryMB,
		CPUs:         cpus,
		DNS:          dns,
	})
	if err != nil {
		// Clean up BuildKit resources on failure
		if buildkitCfg.Enabled && r.BuildkitContainerID != "" {
			_ = m.runtime.StopContainer(ctx, r.BuildkitContainerID)   //nolint:errcheck
			_ = m.runtime.RemoveContainer(ctx, r.BuildkitContainerID) //nolint:errcheck
			netMgr := m.runtime.NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, r.NetworkID) //nolint:errcheck
			}
		}
		// Clean up proxy servers if container creation fails
		cleanupProxy(proxyServer)
		cleanupSSH(sshServer)
		cleanupAgentConfig(claudeConfig)
		cleanupAgentConfig(codexConfig)
		cleanupAgentConfig(geminiConfig)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID
	r.ProxyServer = proxyServer
	r.SSHAgentServer = sshServer
	if claudeConfig != nil {
		r.ClaudeConfigTempDir = claudeConfig.StagingDir
	}
	if codexConfig != nil {
		r.CodexConfigTempDir = codexConfig.StagingDir
	}
	if geminiConfig != nil {
		r.GeminiConfigTempDir = geminiConfig.StagingDir
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
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("enabling TLS on routing proxy: %w", tlsErr)
		}
		if proxyErr := m.proxyLifecycle.EnsureRunning(); proxyErr != nil {
			// Clean up container
			if rmErr := m.runtime.RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupProxy(proxyServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
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
		cleanupAgentConfig(claudeConfig)
		cleanupAgentConfig(codexConfig)
		cleanupAgentConfig(geminiConfig)
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
		cleanupAgentConfig(claudeConfig)
		cleanupAgentConfig(codexConfig)
		cleanupAgentConfig(geminiConfig)
		return nil, fmt.Errorf("opening audit store: %w", err)
	}
	r.AuditStore = auditStore

	// Log container creation event, including privileged mode for security compliance
	containerAuditData := audit.ContainerData{Action: "created"}
	if privileged {
		containerAuditData.Privileged = true
		// Determine reason for privileged mode
		if dockerConfig != nil && dockerConfig.Privileged {
			containerAuditData.Reason = "docker:dind"
		} else {
			containerAuditData.Reason = "unknown"
		}
	}
	containerAuditData.BuildKitEnabled = buildkitCfg.Enabled
	containerAuditData.BuildKitContainerID = r.BuildkitContainerID
	containerAuditData.BuildKitNetworkID = r.NetworkID
	_, _ = auditStore.AppendContainer(containerAuditData)

	// Initialize snapshot engine if not disabled
	if opts.Config != nil && !opts.Config.Snapshots.Disabled {
		snapshotDir := filepath.Join(store.Dir(), "snapshots")
		snapEngine, snapErr := snapshot.NewEngine(opts.Workspace, snapshotDir, snapshot.EngineOptions{
			UseGitignore: !opts.Config.Snapshots.Exclude.IgnoreGitignore,
			Additional:   opts.Config.Snapshots.Exclude.Additional,
		})
		if snapErr != nil {
			// Log debug but don't fail - snapshots are best-effort
			log.Debug("failed to initialize snapshot engine", "error", snapErr)
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

	// Set run context in logger for correlation
	log.SetRunContext(log.RunContext{
		RunID:     runID,
		RunName:   r.Name,
		Agent:     r.Agent,
		Workspace: filepath.Base(r.Workspace),
		Image:     r.Image,
		Grants:    r.Grants,
	})

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
				ui.Warnf("Failed to stop container after firewall error: %v", stopErr)
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
			ui.Warnf("Getting port bindings: %v", err)
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
					ui.Warnf("Registering routes: %v", err)
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
			log.Debug("failed to create pre-run snapshot", "error", err)
		}
	}

	// Stream logs to stdout (unless disabled for interactive mode)
	if opts.StreamLogs {
		go m.streamLogs(context.Background(), r)
	}

	// Start background monitor to capture logs when container exits.
	// This is critical for detached runs where Wait() is never called.
	go m.monitorContainerExit(context.Background(), r)

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

	// Set run context in logger for correlation
	log.SetRunContext(log.RunContext{
		RunID:     runID,
		RunName:   r.Name,
		Agent:     r.Agent,
		Workspace: filepath.Base(r.Workspace),
		Image:     r.Image,
		Grants:    r.Grants,
	})

	// Start with attachment - this ensures TTY is connected before process starts.
	// TTY mode must match how the container was created (see CreateContainer in
	// docker.go and apple.go). Both runtimes only enable TTY when os.Stdin is a
	// real terminal, so we use the same check here.
	useTTY := term.IsTerminal(os.Stdin)

	// For interactive mode, tee output to a buffer so we can capture logs.
	// This is necessary because:
	// 1. TTY mode: output goes through PTY, not container logs
	// 2. Non-TTY interactive: we may still want to capture for tests/programmatic use
	var logBuffer bytes.Buffer
	var teeStdout, teeStderr io.Writer
	teeStdout = stdout
	teeStderr = stderr

	if r.Interactive && r.Store != nil {
		// Tee stdout and stderr to capture for logs.jsonl
		teeStdout = io.MultiWriter(stdout, &logBuffer)
		if stderr != stdout {
			teeStderr = io.MultiWriter(stderr, &logBuffer)
		} else {
			// stdout and stderr are the same writer - don't duplicate
			teeStderr = teeStdout
		}
	}

	attachOpts := container.AttachOptions{
		Stdin:  stdin,
		Stdout: teeStdout,
		Stderr: teeStderr,
		TTY:    useTTY,
	}

	// Pass initial terminal size so the container can be resized immediately
	// after starting, before the process queries terminal dimensions.
	if useTTY && term.IsTerminal(os.Stdout) {
		width, height := term.GetSize(os.Stdout)
		if width > 0 && height > 0 {
			// #nosec G115 -- width/height are validated positive above
			attachOpts.InitialWidth = uint(width)
			attachOpts.InitialHeight = uint(height)
		}
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

	// Get actual port bindings after container starts
	if len(r.Ports) > 0 {
		var bindings map[int]int
		var bindErr error
		for i := 0; i < 5; i++ {
			bindings, bindErr = m.runtime.GetPortBindings(ctx, r.ContainerID)
			if bindErr != nil || len(bindings) >= len(r.Ports) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if bindErr != nil {
			ui.Warnf("Getting port bindings: %v", bindErr)
		} else {
			r.HostPorts = make(map[string]int)
			services := make(map[string]string)
			for serviceName, containerPort := range r.Ports {
				if hostPort, ok := bindings[containerPort]; ok {
					r.HostPorts[serviceName] = hostPort
					services[serviceName] = fmt.Sprintf("127.0.0.1:%d", hostPort)
				}
			}
			if len(services) > 0 {
				if routeErr := m.routes.Add(r.Name, services); routeErr != nil {
					ui.Warnf("Registering routes: %v", routeErr)
				}
			}
		}
	}

	// Save state to disk
	_ = r.SaveMetadata()

	// Set up firewall if enabled (do this after container starts)
	if r.FirewallEnabled && r.ProxyPort > 0 {
		if err := m.runtime.SetupFirewall(ctx, r.ContainerID, r.ProxyHost, r.ProxyPort); err != nil {
			// Firewall setup failed - this is fatal for strict policy
			if stopErr := m.runtime.StopContainer(ctx, r.ContainerID); stopErr != nil {
				ui.Warnf("Failed to stop container after firewall error: %v", stopErr)
			}
			return fmt.Errorf("firewall setup failed (required for strict network policy): %w", err)
		}
	}

	// Wait for the attachment to complete (container exits or context canceled)
	attachErr := <-attachDone

	// For Apple containers in interactive mode, write captured output directly to logs.jsonl.
	// (Apple TTY output doesn't go through container runtime logs)
	// For Docker, captureLogs will handle it via ContainerLogsAll (works even in TTY mode).
	// Always create the file for audit completeness, even if empty.
	if r.Interactive && r.Store != nil && m.runtime.Type() == container.RuntimeApple {
		// Use CompareAndSwap to ensure single write
		if r.logsCaptured.CompareAndSwap(false, true) {
			if lw, err := r.Store.LogWriter(); err == nil {
				if logBuffer.Len() > 0 {
					_, _ = lw.Write(logBuffer.Bytes())
				}
				lw.Close()
			} else {
				// Failed to create file - reset flag so captureLogs can try
				r.logsCaptured.Store(false)
			}
		}
	}

	// Capture logs after container exits (critical for audit/observability)
	// For non-interactive mode, this fetches from container runtime logs
	// For interactive mode with tee, this is a no-op (logsCaptured flag is already set)
	m.captureLogs(r)

	return attachErr
}

// streamLogs streams container logs to stdout for real-time feedback.
// Note: Final log capture to storage is handled by captureLogs() which is called
// from all container exit paths (Wait, StartAttached, Stop) to ensure complete
// logs are captured even for fast-exiting containers.
func (m *Manager) streamLogs(ctx context.Context, r *Run) {
	logs, err := m.runtime.ContainerLogs(ctx, r.ContainerID)
	if err != nil {
		ui.Errorf("Getting logs: %v", err)
		return
	}
	defer logs.Close()

	// Stream to stdout only for real-time feedback
	// Storage is handled by Wait() after container exits
	//
	// Note: streamLogs is only called for non-interactive runs (see exec.go).
	// Interactive runs use StartAttached which handles I/O directly.
	// Non-interactive Docker containers use multiplexed streams (no TTY),
	// so we must demultiplex to avoid 8-byte headers leaking into output.
	if m.runtime.Type() == container.RuntimeDocker {
		// Docker non-interactive container: demultiplex the stream
		_, _ = stdcopy.StdCopy(os.Stdout, os.Stderr, logs)
	} else {
		// Apple container: output is already raw
		_, _ = io.Copy(os.Stdout, logs)
	}
}

// Stop terminates a running run.
func (m *Manager) Stop(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}

	// Check state (thread-safe)
	currentState := r.GetState()
	if currentState != StateRunning && currentState != StateStarting {
		m.mu.Unlock()
		return nil // Already stopped
	}

	r.SetState(StateStopping)
	buildkitContainerID := r.BuildkitContainerID
	networkID := r.NetworkID
	serviceContainers := r.ServiceContainers
	m.mu.Unlock()

	// Stop service containers
	if len(serviceContainers) > 0 {
		svcMgr := m.runtime.ServiceManager()
		if svcMgr != nil {
			for svcName, containerID := range serviceContainers {
				log.Debug("stopping service", "service", svcName, "container_id", containerID)
				if err := svcMgr.StopService(ctx, container.ServiceInfo{ID: containerID}); err != nil {
					log.Debug("failed to stop service", "service", svcName, "error", err)
				}
			}
		}
	}

	// Stop and remove BuildKit sidecar if present (before main container).
	// Must remove before network cleanup — Docker refuses to remove networks
	// with connected containers (#131).
	if buildkitContainerID != "" {
		log.Debug("stopping buildkit sidecar", "container_id", buildkitContainerID)
		if err := m.runtime.StopContainer(ctx, buildkitContainerID); err != nil {
			log.Debug("failed to stop buildkit sidecar", "error", err)
		}
		if err := m.runtime.RemoveContainer(ctx, buildkitContainerID); err != nil {
			log.Debug("failed to remove buildkit sidecar", "error", err)
		}
	}

	if err := m.runtime.StopContainer(ctx, r.ContainerID); err != nil {
		// Log but don't fail - container might already be stopped
		ui.Warnf("%v", err)
		log.Debug("failed to stop container", "container_id", r.ContainerID, "error", err)
	}

	// Capture logs after container stops (defense-in-depth).
	// monitorContainerExit should also capture these when exitCh is signaled,
	// but capturing here provides a safety net if moat crashes before that.
	// captureLogs is idempotent so multiple calls are safe.
	m.captureLogs(r)

	// Stop the proxy server if one was created
	if err := r.stopProxyServer(ctx); err != nil {
		ui.Warnf("Stopping proxy: %v", err)
	}

	// Stop the SSH agent server if one was created
	if err := r.stopSSHAgentServer(); err != nil {
		ui.Warnf("Stopping SSH agent proxy: %v", err)
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
			ui.Warnf("Removing container: %v", rmErr)
		}
	}

	// Clean up provider resources
	for providerName, cleanupPath := range providerCleanupPaths {
		if prov := provider.Get(providerName); prov != nil {
			prov.Cleanup(cleanupPath)
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

	// Clean up Gemini config temp directory
	m.mu.RLock()
	geminiConfigTempDir := r.GeminiConfigTempDir
	m.mu.RUnlock()
	if geminiConfigTempDir != "" {
		if err := os.RemoveAll(geminiConfigTempDir); err != nil {
			log.Debug("failed to remove Gemini config temp dir", "path", geminiConfigTempDir, "error", err)
		}
	}

	// Remove network if present (with force-disconnect fallback)
	if networkID != "" {
		log.Debug("removing docker network", "network_id", networkID)
		netMgr := m.runtime.NetworkManager()
		if netMgr != nil {
			if err := netMgr.RemoveNetwork(ctx, networkID); err != nil {
				log.Debug("network removal failed, trying force removal", "network_id", networkID, "error", err)
				if forceErr := netMgr.ForceRemoveNetwork(ctx, networkID); forceErr != nil {
					log.Debug("force network removal also failed", "network_id", networkID, "error", forceErr)
				}
			}
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

	// Wait for container to exit (signaled by monitorContainerExit) or context cancellation
	// NOTE: We don't call WaitContainer here to avoid race condition - monitorContainerExit
	// is the only goroutine that waits on the container and will close exitCh when done.
	select {
	case <-r.exitCh:
		// Container has exited (monitorContainerExit already captured logs and updated state)
		// Capture logs again here (idempotent) for defense-in-depth
		m.captureLogs(r)

		// Get final error (thread-safe read)
		var err error
		r.stateMu.Lock()
		if r.Error != "" {
			err = fmt.Errorf("%s", r.Error)
		}
		r.stateMu.Unlock()

		// Stop the proxy server if one was created
		if stopErr := r.stopProxyServer(context.Background()); stopErr != nil {
			ui.Warnf("Stopping proxy: %v", stopErr)
		}

		// Stop the SSH agent server if one was created
		if stopErr := r.stopSSHAgentServer(); stopErr != nil {
			ui.Warnf("Stopping SSH agent proxy: %v", stopErr)
		}

		// Unregister routes for this agent
		if r.Name != "" {
			_ = m.routes.Remove(r.Name)
		}

		// NOTE: We don't update r.State, r.StoppedAt, or r.Error here because
		// monitorContainerExit already set them when the container exited.
		// Overwriting would lose StateFailed state and error details.

		// Get values needed for cleanup
		m.mu.RLock()
		keepContainer := r.KeepContainer
		providerCleanupPaths := r.ProviderCleanupPaths
		m.mu.RUnlock()

		// Auto-remove container unless --keep was specified
		if !keepContainer {
			if rmErr := m.runtime.RemoveContainer(context.Background(), containerID); rmErr != nil {
				ui.Warnf("Removing container: %v", rmErr)
			}
		}

		// Clean up provider resources
		for providerName, cleanupPath := range providerCleanupPaths {
			if prov := provider.Get(providerName); prov != nil {
				prov.Cleanup(cleanupPath)
			}
		}

		// Clean up AWS temp directory
		if r.awsTempDir != "" {
			if rmErr := os.RemoveAll(r.awsTempDir); rmErr != nil {
				ui.Warnf("Removing AWS temp dir: %v", rmErr)
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

		// Clean up Gemini config temp directory
		if r.GeminiConfigTempDir != "" {
			if rmErr := os.RemoveAll(r.GeminiConfigTempDir); rmErr != nil {
				log.Debug("failed to remove Gemini config temp dir", "path", r.GeminiConfigTempDir, "error", rmErr)
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

// captureLogs captures container logs to logs.jsonl for audit/observability.
// This method is idempotent and safe to call multiple times - it will only
// write logs once. It creates logs.jsonl even if the container produced no
// output (important for audit trail completeness).
//
// This should be called whenever a container exits, regardless of how:
// - Normal exit (Wait)
// - Interactive exit (StartAttached)
// - Explicit stop (Stop)
// - Detached completion (background monitor)
func (m *Manager) captureLogs(r *Run) {
	if r.Store == nil {
		return
	}

	// For interactive mode, logs are captured differently by runtime:
	// - Docker: Container runtime logs work even in TTY mode, so use ContainerLogsAll
	// - Apple: TTY output doesn't go to container logs, so StartAttached uses tee
	// Only skip container logs for Apple containers in interactive mode.
	if r.Interactive && m.runtime.Type() == container.RuntimeApple {
		return
	}

	// Use CompareAndSwap to ensure only one goroutine captures logs at a time.
	// We DON'T check Load() first because if a previous attempt failed to create
	// the file, we want to retry. The flag is only truly "set" after successful
	// file creation below.
	if !r.logsCaptured.CompareAndSwap(false, true) {
		// Another goroutine is currently capturing or has completed.
		// Check if file exists - if so, we're done.
		logsPath := filepath.Join(r.Store.Dir(), "logs.jsonl")
		if _, err := os.Stat(logsPath); err == nil {
			log.Debug("logs already captured, skipping", "runID", r.ID)
			return
		}
		// File doesn't exist - previous attempt must have failed.
		// Reset flag and try again (we'll race with other goroutines, that's fine).
		r.logsCaptured.Store(false)
		if !r.logsCaptured.CompareAndSwap(false, true) {
			log.Debug("another goroutine is capturing logs, skipping", "runID", r.ID)
			return
		}
	}

	// Fetch all logs from the container.
	// Use a background context since the container may already be stopped.
	allLogs, logErr := m.runtime.ContainerLogsAll(context.Background(), r.ContainerID)
	if logErr != nil {
		log.Warn("failed to fetch container logs - creating empty logs.jsonl for audit", "runID", r.ID, "error", logErr)
		// Still create empty logs.jsonl for audit completeness
		allLogs = []byte{}
	}

	// Write logs to storage - this creates the file even if empty
	lw, lwErr := r.Store.LogWriter()
	if lwErr != nil {
		// Failed to create log file - reset flag so another goroutine can try
		r.logsCaptured.Store(false)
		log.Warn("failed to open log writer - resetting capture flag", "runID", r.ID, "error", lwErr)
		return
	}
	defer lw.Close()
	// File is now created (O_CREATE flag in LogWriter). The flag stays true.

	if len(allLogs) > 0 {
		if _, writeErr := lw.Write(allLogs); writeErr != nil {
			log.Debug("failed to write logs", "runID", r.ID, "error", writeErr)
		}
	}

	log.Debug("logs captured successfully", "runID", r.ID, "bytes", len(allLogs))
}

// monitorContainerExit watches for container exit and captures logs.
// This runs in the background for ALL runs to ensure logs are captured
// even in detached mode where Wait() is never called.
// It's safe to call multiple times - captureLogs is idempotent.
func (m *Manager) monitorContainerExit(ctx context.Context, r *Run) {
	// Wait for container to exit (no timeout - let it run as long as needed)
	// This is the ONLY place that calls WaitContainer to avoid race conditions
	exitCode, err := m.runtime.WaitContainer(ctx, r.ContainerID)

	// CRITICAL: Capture logs IMMEDIATELY after container exits, BEFORE signaling.
	// Docker may start removing/cleaning the container at any moment after exit.
	// We must get the logs while the container is still in "exited" state.
	m.captureLogs(r)

	// Now signal that container has exited (and logs are captured)
	close(r.exitCh)

	// Update run state (use state lock to prevent races with concurrent access)
	currentState := r.GetState()
	if currentState == StateRunning || currentState == StateStarting {
		if err != nil || exitCode != 0 {
			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			} else {
				errMsg = fmt.Sprintf("exit code %d", exitCode)
			}
			r.SetStateWithError(StateFailed, errMsg)
			r.SetStateWithTime(StateFailed, time.Now())
		} else {
			r.SetStateWithTime(StateStopped, time.Now())
		}
	}

	// Save updated state
	_ = r.SaveMetadata()

	// Stop the proxy server if one was created
	if stopErr := r.stopProxyServer(context.Background()); stopErr != nil {
		log.Debug("stopping proxy after container exit", "error", stopErr)
	}

	// Stop the SSH agent server if one was created
	if stopErr := r.stopSSHAgentServer(); stopErr != nil {
		log.Debug("stopping SSH agent proxy after container exit", "error", stopErr)
	}

	// Stop service containers
	if len(r.ServiceContainers) > 0 {
		svcMgr := m.runtime.ServiceManager()
		if svcMgr != nil {
			for svcName, svcContainerID := range r.ServiceContainers {
				log.Debug("stopping service after container exit", "service", svcName, "container_id", svcContainerID)
				if stopErr := svcMgr.StopService(context.Background(), container.ServiceInfo{ID: svcContainerID}); stopErr != nil {
					log.Debug("failed to stop service", "service", svcName, "error", stopErr)
				}
			}
		}
	}

	// Remove containers before network — Docker refuses to remove networks
	// with connected containers, causing network leaks (#131).
	if r.BuildkitContainerID != "" {
		log.Debug("removing buildkit sidecar after container exit", "container_id", r.BuildkitContainerID)
		if stopErr := m.runtime.StopContainer(context.Background(), r.BuildkitContainerID); stopErr != nil {
			log.Debug("failed to stop buildkit sidecar", "error", stopErr)
		}
		if rmErr := m.runtime.RemoveContainer(context.Background(), r.BuildkitContainerID); rmErr != nil {
			log.Debug("failed to remove buildkit sidecar", "error", rmErr)
		}
	}
	if !r.KeepContainer {
		if rmErr := m.runtime.RemoveContainer(context.Background(), r.ContainerID); rmErr != nil {
			log.Debug("failed to remove main container after exit", "error", rmErr)
		}
	}

	// Remove network (with force-disconnect fallback)
	if r.NetworkID != "" {
		netMgr := m.runtime.NetworkManager()
		if netMgr != nil {
			if removeErr := netMgr.RemoveNetwork(context.Background(), r.NetworkID); removeErr != nil {
				log.Debug("network removal failed, trying force removal", "network", r.NetworkID, "error", removeErr)
				if forceErr := netMgr.ForceRemoveNetwork(context.Background(), r.NetworkID); forceErr != nil {
					log.Debug("force network removal also failed", "network", r.NetworkID, "error", forceErr)
				}
			}
		}
	}

	// Unregister routes
	if r.Name != "" {
		_ = m.routes.Remove(r.Name)
	}
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
		ui.Warnf("%v", err)
	}

	// Remove service containers
	for svcName, svcContainerID := range r.ServiceContainers {
		if err := m.runtime.RemoveContainer(ctx, svcContainerID); err != nil {
			ui.Warnf("Removing %s service container: %v", svcName, err)
		}
	}

	// Remove BuildKit sidecar container if present
	if r.BuildkitContainerID != "" {
		if err := m.runtime.RemoveContainer(ctx, r.BuildkitContainerID); err != nil {
			ui.Warnf("Removing BuildKit sidecar: %v", err)
		}
	}

	// Remove network if present (with force-disconnect fallback)
	if r.NetworkID != "" {
		netMgr := m.runtime.NetworkManager()
		if netMgr != nil {
			if err := netMgr.RemoveNetwork(ctx, r.NetworkID); err != nil {
				log.Debug("network removal failed, trying force removal", "network", r.NetworkID, "error", err)
				if forceErr := netMgr.ForceRemoveNetwork(ctx, r.NetworkID); forceErr != nil {
					log.Debug("force network removal also failed", "network", r.NetworkID, "error", forceErr)
				}
			}
		}
	}

	// Stop the proxy server if one was created and still running
	if err := r.stopProxyServer(ctx); err != nil {
		ui.Warnf("Stopping proxy: %v", err)
	}

	// Stop the SSH agent server if one was created and still running
	if err := r.stopSSHAgentServer(); err != nil {
		ui.Warnf("Stopping SSH agent proxy: %v", err)
	}

	// Unregister routes for this agent
	if r.Name != "" {
		if err := m.routes.Remove(r.Name); err != nil {
			ui.Warnf("Removing routes: %v", err)
		}
	}

	// Check if we should stop the routing proxy (no more agents with ports)
	if m.proxyLifecycle.ShouldStop() {
		if err := m.proxyLifecycle.Stop(ctx); err != nil {
			ui.Warnf("Stopping routing proxy: %v", err)
		}
	}

	// Close audit store
	if r.AuditStore != nil {
		if err := r.AuditStore.Close(); err != nil {
			ui.Warnf("Closing audit store: %v", err)
		}
	}

	// Clean up provider resources
	for providerName, cleanupPath := range r.ProviderCleanupPaths {
		if prov := provider.Get(providerName); prov != nil {
			prov.Cleanup(cleanupPath)
		}
	}

	// Clean up AWS temp directory
	if r.awsTempDir != "" {
		if err := os.RemoveAll(r.awsTempDir); err != nil {
			ui.Warnf("Removing AWS temp dir: %v", err)
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

	// Clean up Gemini config temp directory
	if r.GeminiConfigTempDir != "" {
		if err := os.RemoveAll(r.GeminiConfigTempDir); err != nil {
			log.Debug("failed to remove Gemini config temp dir", "path", r.GeminiConfigTempDir, "error", err)
		}
	}

	// Remove run storage directory (logs, traces, metadata)
	if r.Store != nil {
		if err := r.Store.Remove(); err != nil {
			ui.Warnf("Removing storage: %v", err)
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

// RuntimeType returns the container runtime type (docker or apple).
func (m *Manager) RuntimeType() string {
	return string(m.runtime.Type())
}

// Close releases manager resources.
func (m *Manager) Close() error {
	// Stop all proxy servers
	m.mu.RLock()
	for _, r := range m.runs {
		if err := r.stopProxyServer(context.Background()); err != nil {
			log.Debug("failed to stop proxy during manager close", "run", r.ID, "error", err)
		}
		if err := r.stopSSHAgentServer(); err != nil {
			log.Debug("failed to stop SSH agent during manager close", "run", r.ID, "error", err)
		}
	}
	m.mu.RUnlock()

	return m.runtime.Close()
}

// resolveContainerHome returns the home directory to use for container mounts.
// Most moat runs build a custom image (needsCustomImage=true) which always creates
// moatuser and runs as that user, so the home is /home/moatuser. We use this
// directly rather than inspecting the image because init-based images don't set
// USER moatuser in the Dockerfile — the init script drops privileges at runtime,
// so GetImageHomeDir incorrectly returns "/root".
//
// The only case where needsCustomImage is false is a minimal agent.yaml with no
// dependencies, grants, or plugins — the base image is used as-is with no
// Dockerfile generated, so we fall back to the image's detected home.
func resolveContainerHome(needsCustomImage bool, imageHome string) string {
	if needsCustomImage {
		return "/home/moatuser"
	}
	return imageHome
}

// workspaceToClaudeDir converts an absolute workspace path to Claude's project directory format.
// Example: /home/alice/projects/myapp -> -home-alice-projects-myapp
func workspaceToClaudeDir(absPath string) string {
	// Normalize to forward slashes for cross-platform consistency
	normalized := filepath.ToSlash(absPath)
	cleaned := strings.TrimPrefix(normalized, "/")
	return "-" + strings.ReplaceAll(cleaned, "/", "-")
}

// hostGitIdentity reads the host's git user.name and user.email and returns
// env vars for injecting them into the container. Returns nil if git is not
// in the dependency list or the host has no identity configured.
//
// The env vars are consumed by moat-init.sh which writes them via
// "git config --system". When the container runs as non-root (Linux
// --user mode), --system writes to /etc/gitconfig which requires root
// and silently fails. This is a pre-existing limitation shared with the
// safe.directory config — both rely on the init script running as root
// before dropping to moatuser.
func hostGitIdentity(depList []deps.Dependency) (env []string, hasGit bool) {
	for _, d := range depList {
		if d.Name == "git" {
			hasGit = true
			break
		}
	}
	if !hasGit {
		return nil, false
	}
	if gitName, err := exec.Command("git", "config", "user.name").Output(); err == nil {
		if v := strings.TrimSpace(string(gitName)); v != "" {
			env = append(env, "MOAT_GIT_USER_NAME="+v)
		}
	}
	if gitEmail, err := exec.Command("git", "config", "user.email").Output(); err == nil {
		if v := strings.TrimSpace(string(gitEmail)); v != "" {
			env = append(env, "MOAT_GIT_USER_EMAIL="+v)
		}
	}
	return env, true
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

// refreshTarget holds the state for a single refreshable credential.
type refreshTarget struct {
	providerName credential.Provider
	refresher    provider.RefreshableProvider
	cred         *credential.Credential
	store        credential.Store

	// Retry state for exponential backoff
	nextRetryAfter time.Time
	retryDelay     time.Duration
	revoked        bool
}

const (
	refreshRetryMin = 30 * time.Second
	refreshRetryMax = 5 * time.Minute
)

// runTokenRefreshLoop periodically re-acquires tokens from their original source.
// It performs an immediate refresh at startup, then refreshes on the shortest
// provider interval. Exits when ctx is canceled or the run's exitCh closes.
func (m *Manager) runTokenRefreshLoop(ctx context.Context, r *Run, p credential.ProxyConfigurer, targets []refreshTarget) {
	// Immediate refresh at startup — get a fresh token before the session begins
	for i := range targets {
		m.refreshToken(ctx, r, p, &targets[i])
	}

	interval := minRefreshInterval(targets)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-r.exitCh:
			return
		case <-ticker.C:
			for i := range targets {
				m.refreshToken(ctx, r, p, &targets[i])
			}
		}
	}
}

// refreshToken attempts to refresh a single credential target.
// On failure, it applies exponential backoff. On revocation, it stops retrying.
func (m *Manager) refreshToken(ctx context.Context, r *Run, p credential.ProxyConfigurer, target *refreshTarget) {
	if target.revoked {
		return
	}
	if !target.nextRetryAfter.IsZero() && time.Now().Before(target.nextRetryAfter) {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	provCred := provider.FromLegacy(target.cred)
	newCred, err := target.refresher.Refresh(ctx, p, provCred)

	if err != nil {
		if errors.Is(err, provider.ErrTokenRevoked) {
			target.revoked = true
			ui.Warnf("Token for %s has been revoked. Run 'moat grant %s' to re-authenticate.", target.providerName, target.providerName)
			log.Warn("refresh token revoked, stopping refresh",
				"provider", target.providerName, "error", err)
			return
		}

		// Exponential backoff
		if target.retryDelay == 0 {
			target.retryDelay = refreshRetryMin
		} else {
			target.retryDelay *= 2
			if target.retryDelay > refreshRetryMax {
				target.retryDelay = refreshRetryMax
			}
		}
		target.nextRetryAfter = time.Now().Add(target.retryDelay)

		ui.Warnf("Token refresh failed for %s. The existing token will continue to be used.", target.providerName)
		log.Debug("token refresh failed, keeping existing token",
			"provider", target.providerName, "error", err, "retry_in", target.retryDelay)
		return
	}

	// Reset backoff on success
	target.retryDelay = 0
	target.nextRetryAfter = time.Time{}

	// Convert back to credential.Credential
	var updated *credential.Credential
	if newCred != nil {
		updated = &credential.Credential{
			Provider:  target.providerName,
			Token:     newCred.Token,
			Scopes:    newCred.Scopes,
			ExpiresAt: newCred.ExpiresAt,
			CreatedAt: newCred.CreatedAt,
			Metadata:  newCred.Metadata,
		}
	}

	// Token unchanged — no-op
	if updated == nil || updated.Token == target.cred.Token {
		return
	}

	// Update in-memory credential for next cycle
	target.cred = updated

	// Persist to store so crash recovery uses the latest token
	if target.store != nil {
		if err := target.store.Save(*updated); err != nil {
			log.Warn("failed to persist refreshed token",
				"provider", target.providerName, "error", err)
		}
	}

	log.Debug("token refreshed", "provider", target.providerName, "run_id", r.ID)
}

// minRefreshInterval returns the shortest refresh interval across all targets.
func minRefreshInterval(targets []refreshTarget) time.Duration {
	var min time.Duration
	for i, t := range targets {
		d := t.refresher.RefreshInterval()
		if i == 0 || (d > 0 && d < min) {
			min = d
		}
	}
	if min == 0 {
		min = 30 * time.Minute // Default fallback
	}
	return min
}
