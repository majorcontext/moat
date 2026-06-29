package run

// This file holds Create -- the run setup pipeline that builds the container,
// loads credentials, registers proxy routes, and starts services -- along with
// its host-side helper functions.

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/image"

	internalkeep "github.com/majorcontext/moat/internal/keep"
	"github.com/majorcontext/moat/internal/langserver"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/name"
	"github.com/majorcontext/moat/internal/provider"
	awsprov "github.com/majorcontext/moat/internal/providers/aws"
	"github.com/majorcontext/moat/internal/providers/claude" // only for settings types (LoadAllSettings, Settings, MarketplaceConfig) - provider setup uses provider interfaces
	"github.com/majorcontext/moat/internal/runctx"
	"github.com/majorcontext/moat/internal/secrets"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/sshagent"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
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
			// The route may be stale (leftover from a crashed process).
			// Probe the registered endpoints — if none are reachable, clean up.
			if !m.routes.RemoveIfStale(agentName) {
				return nil, fmt.Errorf("agent %q is already running. Use --name to specify a different name, or stop the existing agent first", agentName)
			}
			log.Debug("removed stale route for agent", "name", agentName)
		}
	}

	// Reject `type: volume` on the Apple container runtime as early as possible —
	// before any resources are staged — so this error return needs no cleanup.
	// Config load already rejects an explicit `runtime: apple`; this also catches
	// the auto-detected case where moat.yaml has no runtime: set.
	if opts.Config != nil {
		isApple := m.defaultRuntime().Type() == container.RuntimeApple
		if err := config.CheckVolumeRuntimeSupport(opts.Config.Volumes, isApple); err != nil {
			return nil, err
		}
	}

	// Auto-include MCP auth grants so the credential processing loop loads
	// them into the RunContext. Without this, users would need to duplicate
	// each mcp[].auth.grant in the top-level grants: list.
	opts.Grants = appendMCPGrants(opts.Grants, opts.Config)

	// openCredStore opens the run's credential store at most once and memoizes
	// the result; deriving the key can touch the OS keychain, so re-opening at
	// each site below was wasteful. credKeyFailed marks a key-derivation failure
	// (fatal for some callers) versus a store-open failure (others degrade).
	var (
		credStoreCache *credential.FileStore
		credStoreErr   error
		credKeyFailed  bool
		credStoreDone  bool
	)
	openCredStore := func() (*credential.FileStore, error) {
		if !credStoreDone {
			credStoreDone = true
			key, keyErr := credential.DefaultEncryptionKey()
			if keyErr != nil {
				credKeyFailed = true
				credStoreErr = fmt.Errorf("getting encryption key: %w", keyErr)
			} else if s, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key); storeErr != nil {
				credStoreErr = fmt.Errorf("opening credential store: %w", storeErr)
			} else {
				credStoreCache = s
			}
		}
		return credStoreCache, credStoreErr
	}

	// Validate grants before allocating any resources (proxy, container, etc.)
	needsGrantValidation := len(opts.Grants) > 0 || (opts.Config != nil && len(opts.Config.MCP) > 0)
	if needsGrantValidation {
		store, err := openCredStore()
		if err != nil {
			return nil, err
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

	// Create the run directory before any network/container operations so that
	// concurrent orphan sweeps (in another process's NewManager) treat this
	// run's network as alive even before metadata.json is written.
	runDir := filepath.Join(storage.DefaultBaseDir(), r.ID)
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return nil, fmt.Errorf("creating run directory: %w", err)
	}
	// Remove the empty run dir if Create fails before successfully returning —
	// otherwise we'd leak `~/.moat/runs/<id>/` directories with no metadata.json
	// that don't surface in `moat list` or `moat clean`. Set to false on
	// the success path before returning.
	cleanupRunDir := true
	defer func() {
		if cleanupRunDir {
			_ = os.RemoveAll(runDir)
		}
	}()

	// Default command
	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash"}
	}

	// Proxy environment and mount configuration
	var proxyEnv []string
	var providerEnv []string // Provider-specific env vars (e.g., dummy ANTHROPIC_API_KEY)
	var hostAddr string      // Host address for proxy (may be rewritten for custom networks)
	var mounts []container.MountConfig
	var tmpfsMounts []container.TmpfsMount

	// Volume mode replaces the host /workspace bind with a named Docker volume
	// and a read-only staging bind. moat-init.sh populates the volume from the
	// staging tree, then chowns it and drops privileges.
	volumeMode := opts.WorkspaceMode == config.WorkspaceModeVolume
	var workspaceVolumeName string
	if volumeMode {
		if err := GuardVolumeWorkspace(opts.Workspace, m.defaultRuntime().Type()); err != nil {
			return nil, err
		}
		if err := config.ValidateNoGitExclude(workspaceExcludeList(opts.Config)); err != nil {
			return nil, err
		}
		workspaceVolumeName = WorkspaceVolumeName(r.ID)
		r.WorkspaceMode = string(config.WorkspaceModeVolume)
		r.WorkspaceVolume = workspaceVolumeName
	}

	// Check if any config mount explicitly targets /workspace.
	// If so, skip the implicit workspace mount (the explicit one replaces it).
	hasExplicitWorkspace := ConfigHasExplicitWorkspaceMount(opts.Config)

	switch {
	case volumeMode:
		// Staging bind (host tree, read-only) + named volume at /workspace.
		mounts = append(mounts, VolumeWorkspaceMounts(opts.Workspace, workspaceVolumeName)...)
	case !hasExplicitWorkspace:
		// Mount workspace (unless replaced by an explicit mount)
		mounts = append(mounts, container.MountConfig{
			Source:   opts.Workspace,
			Target:   "/workspace",
			ReadOnly: false,
		})
	}

	// If workspace is a git worktree, mount the main .git directory so git
	// operations work inside the container. The .git file in worktrees contains
	// an absolute host path; mounting the main .git at that same path makes
	// the reference resolve as-is. Skipped in volume mode, which rejects
	// worktrees outright (GuardVolumeWorkspace above).
	if !volumeMode {
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
	}

	// Add mounts from config
	if opts.Config != nil {
		for _, me := range opts.Config.Mounts {
			// In volume mode the named volume owns /workspace. A /workspace mount
			// entry is consulted only for its exclude: list (applied during
			// copy-in by workspaceExcludes) — do not add a bind (it would
			// duplicate the volume mount) and do not create tmpfs overlays
			// (excludes are filtered at copy-in, not overlaid at runtime).
			if volumeMode && me.Target == "/workspace" {
				continue
			}
			// Resolve relative paths against workspace
			source := me.Source
			if !filepath.IsAbs(source) {
				source = filepath.Join(opts.Workspace, source)
			}
			mounts = append(mounts, container.MountConfig{
				Source:   source,
				Target:   me.Target,
				ReadOnly: me.ReadOnly,
			})
			// Resolve excludes to tmpfs mounts
			for _, exc := range me.Exclude {
				tmpfsMounts = append(tmpfsMounts, container.TmpfsMount{
					Target: path.Join(me.Target, exc),
				})
			}
		}
	}

	// Add global mounts from <MOAT_HOME>/config.yaml.
	// These are personal read-only mounts that apply to every run.
	globalCfg, globalErr := config.LoadGlobal()
	if globalErr != nil {
		ui.Warnf("Failed to load global config (%s): %v", filepath.Join(config.GlobalConfigDir(), "config.yaml"), globalErr)
	} else if len(globalCfg.Mounts) > 0 {
		for _, gm := range globalCfg.Mounts {
			mounts = append(mounts, container.MountConfig{
				Source:   gm.Source,
				Target:   gm.Target,
				ReadOnly: gm.ReadOnly,
			})
			log.Debug("added global mount", "source", gm.Source, "target", gm.Target)
		}
	}

	// Add volume mounts from config. type: bind (default) → host bind mount at
	// ~/.moat/volumes/<agent>/<name>/ (owned by the current user, matching the
	// container user). type: volume → native in-VM Docker named volume (Docker
	// auto-creates it on mount). Its root is created root-owned and chowned to the
	// run user by one of two paths, depending on whether the entrypoint is root:
	//   - root entrypoint (containerUser == ""): moat-init chowns it to moatuser,
	//     driven by the MOAT_VOLUME_CHOWN env var injected below.
	//   - non-root (containerUser set, e.g. Linux workspace UID): the Docker
	//     runtime's initNamedVolumeOwnership helper chowns it to that UID before start.
	var volumeChownPaths []string
	if opts.Config != nil && len(opts.Config.Volumes) > 0 {
		for _, vol := range opts.Config.Volumes {
			mc, isVolume := volumeMount(opts.Config.Name, vol)
			if isVolume {
				volumeChownPaths = append(volumeChownPaths, vol.Target)
				log.Debug("added named volume mount", "volume", mc.Source, "target", mc.Target)
			} else {
				if err := os.MkdirAll(mc.Source, 0o755); err != nil {
					return nil, fmt.Errorf("creating volume directory %s: %w", mc.Source, err)
				}
				log.Debug("added bind volume mount", "dir", mc.Source, "target", mc.Target)
			}
			mounts = append(mounts, mc)
		}
	}

	// Start proxy if we have grants (for credential injection) or strict network policy
	needsProxyForGrants := len(opts.Grants) > 0
	needsProxyForFirewall := opts.Config != nil && opts.Config.Network.Policy == "strict"
	// Start proxy for any feature that the proxy is responsible for enforcing
	// or relaying, even when there are no grants and the policy is permissive.
	// Without this, setting `network.host`, `network.rules`, MCP servers, or
	// Keep policies on a grant-less run would silently do nothing.
	needsProxyForConfig := false
	if opts.Config != nil {
		needsProxyForConfig = len(opts.Config.Network.Host) > 0 ||
			len(opts.Config.Network.Rules) > 0 ||
			len(opts.Config.MCP) > 0 ||
			opts.Config.Network.KeepPolicy != nil ||
			(opts.Config.Claude.LLMGateway != nil && opts.Config.Claude.LLMGateway.Policy != nil)
	}

	// Clipboard bridging is resolved by the caller (ExecuteRun).
	needsClipboard := opts.Clipboard
	r.Clipboard = needsClipboard

	// cleanupDaemonRun is a helper to unregister the run from the proxy daemon.
	// Used in error paths during run creation.
	cleanupDaemonRun := func() {
		if r.ProxyAuthToken != "" && m.daemonClient != nil {
			if err := m.daemonClient.UnregisterRun(context.Background(), r.ProxyAuthToken); err != nil {
				log.Debug("failed to unregister run from daemon", "error", err)
			}
			r.ProxyAuthToken = ""
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

	if needsProxyForGrants || needsProxyForFirewall || needsProxyForConfig {
		// Daemon directory for proxy state (CA certs, lock file, socket)
		daemonDir := filepath.Join(config.GlobalConfigDir(), "proxy")

		// Ensure daemon is running and get a client
		daemonCl, daemonErr := daemon.EnsureRunning(daemonDir, 0)
		if daemonErr != nil {
			return nil, fmt.Errorf("starting proxy daemon: %w", daemonErr)
		}
		m.mu.Lock()
		m.daemonClient = daemonCl
		m.mu.Unlock()

		// Capture daemon build commit and capabilities for version skew detection.
		var daemonCapabilities []string
		if health, healthErr := daemonCl.Health(ctx); healthErr == nil {
			r.DaemonCommit = health.Commit
			daemonCapabilities = health.Capabilities
		} else {
			log.Warn("daemon health check failed", "error", healthErr)
		}

		// Create a RunContext that implements credential.ProxyConfigurer.
		// Providers will configure their credentials on this context.
		runCtx := daemon.NewRunContext(r.ID)

		// Load credentials for granted providers
		store, err := openCredStore()
		if err != nil && credKeyFailed {
			return nil, err
		}

		// Track Anthropic/Claude credential for base URL proxy setup
		var anthropicCred *provider.Credential

		if err == nil {
			for _, grant := range opts.Grants {
				grantName := strings.Split(grant, ":")[0]

				// SSH grants are handled separately (SSH agent setup below)
				if grantName == "ssh" {
					continue
				}

				// Map grant name to credential store key (handles aliases like
				// "openai" → codex provider but credential stored under "openai").
				credName := credentialStoreKey(grantName, grant)
				log.Debug("processing grant", "grant", grant, "credName", credName)
				cred, getErr := store.Get(credName)
				if getErr != nil {
					// Should not happen: validateGrants checks before resource allocation.
					cleanupDaemonRun()
					return nil, fmt.Errorf("grant %q: credential not found: %w", grantName, getErr)
				}
				// Convert credential for new provider interface
				provCred := provider.FromLegacy(cred)

				// Store MCP credential on RunContext so the daemon proxy can
				// resolve it by grant name during MCP relay requests. This
				// runs for ALL grants (not just provider-less ones) because
				// grants like "oauth:notion" have a registered provider but
				// still need their credential stored for the MCP relay.
				if opts.Config != nil {
					for _, mcp := range opts.Config.MCP {
						if mcp.Auth != nil && mcp.Auth.Grant == grant {
							serverHost := mcp.URL
							if u, parseErr := url.Parse(mcp.URL); parseErr == nil {
								serverHost = u.Host
							}
							runCtx.SetCredentialWithGrant(serverHost, mcp.Auth.Header, provCred.Token, grant)
						}
					}
				}

				// Use new provider registry (supports aliases like "anthropic" -> "claude")
				// MCP grants (e.g., "mcp:test") have no registered provider — they are
				// handled by the proxy MCP relay, not by provider.ConfigureProxy.
				prov := provider.Get(grantName)
				if prov == nil {
					continue
				}
				// Configure the RunContext (which implements ProxyConfigurer)
				prov.ConfigureProxy(runCtx, provCred)
				envVars := prov.ContainerEnv(provCred)
				log.Debug("adding provider env vars", "provider", credName, "vars", envVars)
				providerEnv = append(providerEnv, envVars...)

				// Capture Anthropic/Claude credential for base URL proxy setup
				if credName == credential.ProviderClaude || credName == credential.ProviderAnthropic {
					anthropicCred = provCred
				}

				// Handle AWS endpoint provider
				if ep := provider.GetEndpoint(string(credName)); ep != nil {
					// AWS credentials are handled via credential endpoint
					// Parse stored config from Metadata (new format) with fallback to Scopes (legacy)
					awsCfg, err := awsprov.ConfigFromCredential(provCred)
					if err != nil {
						return nil, fmt.Errorf("parsing AWS credential: %w", err)
					}

					awsProvider, err := awsprov.NewCredentialProvider(
						ctx,
						awsprov.CredentialProviderConfig{
							RoleARN:         awsCfg.RoleARN,
							Region:          awsCfg.Region,
							SessionDuration: awsCfg.SessionDuration,
							ExternalID:      awsCfg.ExternalID,
							Profile:         awsCfg.Profile,
						},
						"moat-"+r.ID,
					)
					if err != nil {
						return nil, fmt.Errorf("creating AWS credential provider: %w", err)
					}
					// Store provider for later AWS credential_process setup
					r.AWSCredentialProvider = awsProvider

					// Store config for daemon registration so the daemon can
					// create its own AWSCredentialProvider.
					runCtx.AWSConfig = &daemon.AWSConfig{
						RoleARN:         awsCfg.RoleARN,
						Region:          awsCfg.Region,
						SessionDuration: awsCfg.SessionDuration,
						ExternalID:      awsCfg.ExternalID,
						Profile:         awsCfg.Profile,
					}
				}
			}
		}

		// Configure network policy on the RunContext
		if opts.Config != nil {
			runCtx.NetworkPolicy = opts.Config.Network.Policy
			// Convert NetworkRuleEntry to HostRules for the daemon.
			// Also populate NetworkAllow with host strings for backwards
			// compatibility with older daemon binaries that don't know
			// about network_rules.
			for _, entry := range opts.Config.Network.Rules {
				runCtx.NetworkRules = append(runCtx.NetworkRules, entry.HostRules)
				runCtx.NetworkAllow = append(runCtx.NetworkAllow, entry.Host)
			}
			runCtx.AllowedHostPorts = opts.Config.Network.Host
		}

		// Configure MCP servers on the RunContext
		if opts.Config != nil && len(opts.Config.MCP) > 0 {
			runCtx.MCPServers = opts.Config.MCP
		}

		// Resolve Keep policies for daemon registration.
		// Inline deny-list policies use the RuleSet builder (no YAML round-trip).
		// File/pack policies are validated with ValidateRuleBytes and passed as YAML.
		var policyYAML map[string][]byte
		var policyRuleSets []daemon.PolicyRuleSetSpec
		if opts.Config != nil {
			for _, mcp := range opts.Config.MCP {
				if mcp.Policy == nil {
					continue
				}
				if mcp.Policy.IsInline() {
					mode := mcp.Policy.Mode
					if mode == "" {
						mode = "enforce"
					}
					policyRuleSets = append(policyRuleSets, daemon.PolicyRuleSetSpec{
						Scope: "mcp-" + mcp.Name, // Prefix avoids key collisions with "http" and "llm-gateway".
						Mode:  mode,
						Deny:  mcp.Policy.Deny,
					})
				} else {
					if policyYAML == nil {
						policyYAML = make(map[string][]byte)
					}
					yamlBytes, err := internalkeep.ResolvePolicyYAML(mcp.Policy, "mcp-"+mcp.Name, opts.Workspace)
					if err != nil {
						return nil, fmt.Errorf("MCP server %q policy: %w", mcp.Name, err)
					}
					if err := keeplib.ValidateRuleBytes(yamlBytes); err != nil {
						return nil, fmt.Errorf("MCP server %q policy validation: %w", mcp.Name, err)
					}
					policyYAML["mcp-"+mcp.Name] = yamlBytes
				}
			}

			// Resolve network keep_policy if configured.
			if opts.Config.Network.KeepPolicy != nil {
				if opts.Config.Network.KeepPolicy.IsInline() {
					mode := opts.Config.Network.KeepPolicy.Mode
					if mode == "" {
						mode = "enforce"
					}
					policyRuleSets = append(policyRuleSets, daemon.PolicyRuleSetSpec{
						Scope: "http",
						Mode:  mode,
						Deny:  opts.Config.Network.KeepPolicy.Deny,
					})
				} else {
					if policyYAML == nil {
						policyYAML = make(map[string][]byte)
					}
					yamlBytes, err := internalkeep.ResolvePolicyYAML(opts.Config.Network.KeepPolicy, "http", opts.Workspace)
					if err != nil {
						return nil, fmt.Errorf("network keep_policy: %w", err)
					}
					if err := keeplib.ValidateRuleBytes(yamlBytes); err != nil {
						return nil, fmt.Errorf("network keep_policy validation: %w", err)
					}
					policyYAML["http"] = yamlBytes
				}
			}

			// Resolve LLM gateway policy if configured.
			if opts.Config.Claude.LLMGateway != nil && opts.Config.Claude.LLMGateway.Policy != nil {
				gwPolicy := opts.Config.Claude.LLMGateway.Policy
				if gwPolicy.IsInline() {
					mode := gwPolicy.Mode
					if mode == "" {
						mode = "enforce"
					}
					policyRuleSets = append(policyRuleSets, daemon.PolicyRuleSetSpec{
						Scope: "llm-gateway",
						Mode:  mode,
						Deny:  gwPolicy.Deny,
					})
				} else {
					if policyYAML == nil {
						policyYAML = make(map[string][]byte)
					}
					yamlBytes, err := internalkeep.ResolvePolicyYAML(gwPolicy, "llm-gateway", opts.Workspace)
					if err != nil {
						return nil, fmt.Errorf("llm-gateway policy: %w", err)
					}
					if err := keeplib.ValidateRuleBytes(yamlBytes); err != nil {
						return nil, fmt.Errorf("llm-gateway policy validation: %w", err)
					}
					policyYAML["llm-gateway"] = yamlBytes
				}
			}
		}

		// Determine whether any file/pack policy inspects the request body
		// (params.body). Body rules can only appear in file/pack policies — inline
		// deny-lists match the operation path only — so only policyYAML needs
		// checking.
		requiresBodyPolicy := policyRequiresBody(policyYAML)

		// Verify the daemon supports the Keep-policy features this run needs.
		if len(policyYAML) > 0 || len(policyRuleSets) > 0 {
			if err := checkKeepPolicyCapabilities(daemonCapabilities, requiresBodyPolicy); err != nil {
				return nil, err
			}
		}

		// Verify the daemon supports the synthetic-hostname gateway semantics.
		// All runs that register with the proxy now rely on HostGateway=moat-host
		// and a separate HostGatewayIP — older daemons ignore HostGatewayIP and
		// don't route moat-host traffic correctly, which silently breaks host
		// access and the network-host bypass fix. Fail fast with an actionable
		// message rather than letting the run register and misbehave.
		if !slices.Contains(daemonCapabilities, daemon.CapHostGatewayV2) {
			return nil, fmt.Errorf("proxy daemon is too old for this CLI (missing 'host-gateway-v2' capability); run 'moat proxy restart' to upgrade")
		}

		// Get proxy host address — needed for registration, proxy URL, and firewall.
		// Must be set before buildRegisterRequest so HostGateway is included.
		hostAddr = m.defaultRuntime().GetHostAddress()
		// Always use synthetic hostnames so that user-supplied NO_PROXY=<ip>
		// cannot bypass the proxy. On Docker they resolve via --add-host; on
		// Apple they resolve via /etc/hosts entries written by moat-init.sh
		// (see MOAT_EXTRA_HOSTS below).
		runCtx.HostGateway = syntheticHostGateway
		// HostGatewayIP is the address the proxy itself uses to forward allowed
		// host-bound traffic. Since the proxy runs on the host, host services
		// are reachable via 127.0.0.1 regardless of how the container reaches
		// the host.
		runCtx.HostGatewayIP = "127.0.0.1"

		// Build RegisterRequest from the RunContext
		regReq := buildRegisterRequest(runCtx, opts.Grants)
		regReq.PolicyYAML = policyYAML
		regReq.PolicyRuleSets = policyRuleSets

		// Save registration request for re-registration after proxy restart
		r.ProxyRegReq = &regReq

		// Register with daemon — returns auth token and proxy port
		regResp, regErr := m.daemonClient.RegisterRun(ctx, regReq)
		if regErr != nil {
			return nil, fmt.Errorf("registering run with proxy daemon: %w", regErr)
		}
		if regResp.Error != "" {
			return nil, fmt.Errorf("policy compilation failed: %s", regResp.Error)
		}

		// Store proxy details from daemon response
		r.ProxyAuthToken = regResp.AuthToken
		r.ProxyPort = regResp.ProxyPort
		r.ProxyHost = hostAddr

		// Store proxy details for firewall setup (applied after container starts)
		if needsProxyForFirewall {
			r.FirewallEnabled = true
		}

		// Build proxy environment using synthetic hostnames on all runtimes.
		// Host-network mode is used on Docker Linux when no ports need publishing.
		// In that mode, the container shares the host loopback, so localhost
		// must NOT be in NO_PROXY (otherwise it bypasses network.host enforcement).
		isHostNet := m.defaultRuntime().SupportsHostNetwork() && (opts.Config == nil || len(opts.Config.Ports) == 0)
		proxyEnv = buildProxyEnv(regResp.AuthToken, regResp.ProxyPort, isHostNet)
		proxyHost := syntheticProxyHost + ":" + strconv.Itoa(regResp.ProxyPort)

		// Docker-on-Linux resolves the synthetic hostnames via --add-host (set
		// further down). Every other runtime (Apple; Docker Desktop on macOS
		// and Windows) cannot use --add-host for this: Apple has no such flag,
		// and on Docker Desktop --add-host:host-gateway resolves to the
		// docker0 bridge IP which is unreachable from a custom bridge network
		// (created whenever moat.yaml defines services). For those runtimes
		// we pass the host map via env so moat-init.sh writes /etc/hosts.
		if _, env := synthHostStrategy(m.defaultRuntime().Type(), goruntime.GOOS, hostAddr); env != "" {
			proxyEnv = append(proxyEnv, "MOAT_EXTRA_HOSTS="+env)
		}

		// Mount CA certificate (not the private key) for container to trust.
		// We mount a directory (not just the file) because Apple container
		// only supports directory mounts, not individual file mounts.
		// The private key stays on the host - only the proxy needs it for signing.
		// The daemon's CA is stored under the daemon directory.
		caDir := filepath.Join(daemonDir, "ca")
		caCertOnlyDir := filepath.Join(caDir, "public")
		if err := ensureCACertOnlyDir(caDir, caCertOnlyDir); err != nil {
			cleanupDaemonRun()
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

		// Configure custom base URL for Claude Code LLM proxy (e.g., Headroom).
		// Uses a relay pattern: ANTHROPIC_BASE_URL points to a relay endpoint on
		// the Moat proxy, which forwards to the actual host-side LLM proxy with
		// credentials injected. This avoids the NO_PROXY issue where the rewritten
		// base URL host would bypass the proxy (it's the same hostAddr).
		if opts.Config != nil && opts.Config.Claude.BaseURL != "" && anthropicCred == nil {
			ui.Warn("claude.base_url is set but no anthropic or claude grant is active — ANTHROPIC_BASE_URL will not be set")
		}
		if opts.Config != nil && opts.Config.Claude.BaseURL != "" && anthropicCred != nil {
			baseURL, parseErr := url.Parse(opts.Config.Claude.BaseURL)
			if parseErr != nil {
				// Should not happen: config.Load() validates the URL.
				log.Warn("invalid claude.base_url, skipping relay setup",
					"url", opts.Config.Claude.BaseURL, "error", parseErr)
			} else {
				// Register credential injection for the base URL host on the RunContext
				claude.ConfigureBaseURLProxy(runCtx, anthropicCred, baseURL.Host)

				// The relay endpoint runs on the daemon's proxy.
				// Set ANTHROPIC_BASE_URL to the relay endpoint.
				// Since proxyHost is in NO_PROXY, Claude Code connects directly
				// to the proxy's HTTP handler (not through the CONNECT tunnel),
				// which routes /relay/anthropic/ to the relay handler.
				relayURL := fmt.Sprintf("http://%s/relay/anthropic", proxyHost)
				proxyEnv = append(proxyEnv, "ANTHROPIC_BASE_URL="+relayURL)

				log.Debug("configured base URL relay for Claude Code",
					"baseURL", opts.Config.Claude.BaseURL,
					"relayURL", relayURL)
			}
		}

		// Set up AWS credential_process if AWS grant is active
		// Instead of static credential injection, we use credential_process for dynamic refresh.
		// A small binary inside the container fetches credentials from our proxy on demand.
		if r.AWSCredentialProvider != nil {
			// Create temp directory for credential helper and config
			awsDir, err := os.MkdirTemp("", "moat-aws-*")
			if err != nil {
				cleanupDaemonRun()
				return nil, fmt.Errorf("creating AWS credential helper directory: %w", err)
			}
			r.awsTempDir = awsDir // Track for cleanup

			// Write the credential helper script
			// Use 0700 permissions since the script contains the credential endpoint URL
			helperPath := filepath.Join(awsDir, "credentials")
			if err := os.WriteFile(helperPath, awsprov.GetCredentialHelper(), 0o700); err != nil {
				cleanupDaemonRun()
				return nil, fmt.Errorf("writing AWS credential helper: %w", err)
			}

			// Write AWS config file
			awsConfig := fmt.Sprintf(`[default]
credential_process = /moat/aws/credentials
region = %s
`, r.AWSCredentialProvider.Region())
			configPath := filepath.Join(awsDir, "config")
			if err := os.WriteFile(configPath, []byte(awsConfig), 0o644); err != nil {
				cleanupDaemonRun()
				return nil, fmt.Errorf("writing AWS config: %w", err)
			}

			// Mount the directory
			mounts = append(mounts, container.MountConfig{
				Source:   awsDir,
				Target:   "/moat/aws",
				ReadOnly: true,
			})

			// Build credential endpoint URL
			credentialURL := "http://" + proxyHost + "/_aws/credentials"

			// Set environment variables
			proxyEnv = append(proxyEnv,
				"AWS_CONFIG_FILE=/moat/aws/config",
				"MOAT_AWS_CREDENTIAL_URL="+credentialURL,
				"AWS_REGION="+r.AWSCredentialProvider.Region(),
				// AWS traffic goes through proxy for firewall/observability.
				// Tell AWS SDK to trust our CA for MITM SSL.
				"AWS_CA_BUNDLE="+caCertInContainer,
				// Disable pager - containers may not have 'less' installed
				"AWS_PAGER=",
			)

			// Include auth token if proxy requires it
			if regResp.AuthToken != "" {
				proxyEnv = append(proxyEnv, "MOAT_AWS_CREDENTIAL_TOKEN="+regResp.AuthToken)
			}

			fmt.Printf("AWS credential_process configured (role: %s)\n",
				filepath.Base(r.AWSCredentialProvider.RoleARN()))
		}
	}

	// Set up SSH agent proxy for SSH grants (e.g., git clone git@github.com:...)
	sshGrants := filterSSHGrants(opts.Grants)
	var sshServer *sshagent.Server
	sshSetup, sshErr := m.setupSSHAgent(r, opts, sshGrants, hostAddr, openCredStore)
	if sshErr != nil {
		cleanupDaemonRun()
		return nil, sshErr
	}
	sshServer = sshSetup.server
	proxyEnv = append(proxyEnv, sshSetup.env...)
	mounts = append(mounts, sshSetup.mounts...)

	// Configure network mode and extra hosts based on runtime capabilities
	// We use bridge mode when:
	// 1. We have ports to publish (host mode doesn't support port publishing)
	// 2. We're on macOS/Windows (host mode not supported)
	// 3. We're using Apple container runtime
	// We only use host mode when we need proxy access AND don't have ports to publish on Linux.
	var networkMode string
	var extraHosts []string
	needsPorts := len(ports) > 0
	needsProxy := r.ProxyAuthToken != ""

	if needsProxy || needsPorts {
		if m.defaultRuntime().SupportsHostNetwork() && !needsPorts {
			// Docker on Linux without ports: use host network so container can reach 127.0.0.1
			networkMode = "host"
		} else {
			// Use bridge mode when we need port publishing, or on macOS/Windows/Apple
			networkMode = "bridge"
			// On Linux, Docker doesn't provide host.docker.internal by default,
			// so add it via host-gateway mapping. On macOS/Windows, Docker
			// Desktop and Rancher Desktop resolve it via built-in DNS — adding
			// host-gateway would override the correct IP with the bridge
			// gateway (which is unreachable on Rancher Desktop).
			if m.defaultRuntime().Type() == container.RuntimeDocker && goruntime.GOOS == "linux" {
				extraHosts = []string{"host.docker.internal:host-gateway"}
			}
		}
		// Add synthetic hostnames to --add-host on runtimes where Docker's
		// "host-gateway" substitution produces a reachable IP (Docker on
		// Linux). Apple has no --add-host equivalent, and Docker Desktop on
		// macOS/Windows must not use this path — "host-gateway" resolves to
		// the docker0 bridge gateway, which is unreachable from containers on
		// custom networks (those created by `services:`). Those runtimes
		// instead rely on MOAT_EXTRA_HOSTS written by moat-init.sh (see above).
		//
		// "moat-proxy" is used for proxy traffic (in NO_PROXY).
		// "moat-host" is used for host service traffic (NOT in NO_PROXY,
		// so it flows through the proxy for network policy enforcement).
		synthHosts, _ := synthHostStrategy(m.defaultRuntime().Type(), goruntime.GOOS, hostAddr)
		extraHosts = append(extraHosts, synthHosts...)
	}

	// Add config env vars, filtering out proxy-related variables that would
	// override moat's proxy settings and re-open the host traffic bypass.
	if opts.Config != nil {
		for k, v := range opts.Config.Env {
			if needsProxy && isMoatOwnedProxyVar(k) {
				ui.Warnf("ignoring %s in moat.yaml env — overriding proxy settings would bypass network policy enforcement", k)
				continue
			}
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
			cleanupDaemonRun()
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

	// Add clipboard bridging env vars (before explicit env so they can be overridden)
	if needsClipboard {
		proxyEnv = append(proxyEnv, "MOAT_CLIPBOARD=1", "DISPLAY=:99")
	}

	// Add explicit env vars (highest priority - can override config),
	// but filter proxy-related vars when proxy is active.
	for _, e := range opts.Env {
		if needsProxy {
			if idx := strings.IndexByte(e, '='); idx >= 0 && isMoatOwnedProxyVar(e[:idx]) {
				ui.Warnf("ignoring %s in env — overriding proxy settings would bypass network policy enforcement", e[:idx])
				continue
			}
		}
		proxyEnv = append(proxyEnv, e)
	}

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

	// Add dependencies from language servers (e.g., gopls requires go).
	// Language servers are only supported with Claude Code agent.
	if opts.Config != nil && len(opts.Config.LanguageServers) > 0 && strings.HasPrefix(opts.Config.Agent, "claude") {
		allDeps = append(allDeps, langserver.AllDependencies(opts.Config.LanguageServers)...)
	}

	// Add dependencies implied by the agent itself (e.g., Claude needs python3).
	// Appended after the config dependencies so a user-specified version takes
	// precedence (deps.ParseAll dedupes by name, keeping the first occurrence).
	if opts.Config != nil {
		allDeps = append(allDeps, agentImpliedDependencies(opts.Config.Agent)...)
	}

	if len(allDeps) > 0 {
		var err error
		depList, err = deps.ParseAll(allDeps)
		if err != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("parsing dependencies: %w", err)
		}
		if err = deps.Validate(depList); err != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("validating dependencies: %w", err)
		}
		// Resolve partial runtime versions (e.g., "go@1.22" -> "go@1.22.12")
		// Uses cached API results to avoid repeated network calls
		depList, err = deps.ResolveVersions(ctx, depList)
		if err != nil {
			cleanupDaemonRun()
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
	dockerConfig, dockerErr := ResolveDockerDependency(depList, m.defaultRuntime().Type())
	if dockerErr != nil {
		cleanupDaemonRun()
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
	// - moat.yaml claude.* fields (run overrides)
	var claudeSettings *claude.Settings
	if opts.Config != nil {
		var loadErr error
		claudeSettings, loadErr = claude.LoadAllSettings(opts.Workspace, opts.Config)
		if loadErr != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("loading Claude settings: %w", loadErr)
		}
	}

	// Extract plugins and marketplaces for image building.
	// Only relevant when claude-code is a dependency (explicit or implied by agent).
	// Host marketplace settings are loaded for all runs, but the claude binary
	// is only present in claude-code containers.
	hasClaudeCode := hasDep(depList, "claude-code")
	var claudeMarketplaces []claude.MarketplaceConfig
	var claudePlugins []string
	marketplaceRepos := make(map[string]string)

	if claudeSettings != nil && hasClaudeCode {
		// Build a map of marketplace name -> repo identity from merged settings.
		// MarketplaceConfig.Repo carries the value matching the source shape:
		// an "owner/repo" shorthand for source "github", a full URL for source
		// "git". Preserving the original shape lets GenerateKnownMarketplaces
		// emit the same {source, repo|url} pair the entry was registered with,
		// which matters for strictKnownMarketplaces allowlist matching (the
		// allowlist compares source/repo and source/url as exact pairs).
		for name, entry := range claudeSettings.ExtraKnownMarketplaces {
			var repo string
			switch entry.Source.Source {
			case "github":
				if entry.Source.Repo == "" {
					continue
				}
				repo = entry.Source.Repo
			case "git":
				if entry.Source.URL == "" {
					continue
				}
				repo = entry.Source.URL
			default:
				continue
			}
			marketplaceRepos[name] = repo
			claudeMarketplaces = append(claudeMarketplaces, claude.MarketplaceConfig{
				Name:   name,
				Source: entry.Source.Source,
				Repo:   repo,
			})
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
					// Use warning for moat.yaml plugins, debug for auto-discovered host settings
					if claudeSettings.PluginSources != nil &&
						claudeSettings.PluginSources[pluginKey] == claude.SourceMoatYAML {
						ui.Warnf("Skipping plugin %q: marketplace %q is not configured. Add it to moat.yaml under claude.marketplaces.", pluginKey, marketplace)
						log.Debug("skipping plugin from moat.yaml with unknown marketplace",
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

	// Inject language server plugins into the plugin baking flow.
	// Language servers use Claude Code plugins instead of MCP stdio processes.
	hasLangServers := opts.Config != nil && len(opts.Config.LanguageServers) > 0
	if hasLangServers && !strings.HasPrefix(opts.Config.Agent, "claude") {
		ui.Warnf("language_servers are currently only supported with Claude Code agent; ignoring for %s", opts.Config.Agent)
		hasLangServers = false
	}
	if hasLangServers {
		lsPlugins := langserver.Plugins(opts.Config.LanguageServers)
		claudePlugins = append(claudePlugins, lsPlugins...)
		// Ensure claude-plugins-official marketplace is registered
		if _, exists := marketplaceRepos["claude-plugins-official"]; !exists {
			marketplaceRepos["claude-plugins-official"] = "anthropics/claude-plugins-official"
			claudeMarketplaces = append(claudeMarketplaces, claude.MarketplaceConfig{
				Name:   "claude-plugins-official",
				Source: "github",
				Repo:   "anthropics/claude-plugins-official",
			})
		}
	}

	// Resolve which agents need init and which providers need init files.
	// This opens the credential store once and walks grants in a single pass.
	imgNeeds := resolveImageNeeds(opts.Grants, depList)
	needsClaudeInit := slices.Contains(imgNeeds.initProviders, "claude")
	needsCodexInit := slices.Contains(imgNeeds.initProviders, "codex")
	needsGeminiInit := slices.Contains(imgNeeds.initProviders, "gemini")

	// Hooks config for image hashing, Dockerfile generation, and pre_run
	var hooks *deps.HooksConfig
	if opts.Config != nil && (opts.Config.Hooks.PostBuild != "" || opts.Config.Hooks.PostBuildRoot != "" || opts.Config.Hooks.PreRun != "") {
		hooks = &deps.HooksConfig{
			PostBuild:     opts.Config.Hooks.PostBuild,
			PostBuildRoot: opts.Config.Hooks.PostBuildRoot,
			PreRun:        opts.Config.Hooks.PreRun,
		}
	}

	// Build the image spec — single source of truth for image resolution,
	// tag generation, and Dockerfile generation.
	hasSSHGrants := len(sshGrants) > 0
	// Only enable BuildKit-specific Dockerfile features (--mount=type=cache) when
	// we're certain BuildKit is available. With BUILDKIT_HOST set, a standalone
	// BuildKit daemon is guaranteed. Without it, Docker may fall back to the legacy
	// builder, which can fail to parse BuildKit syntax (e.g., --mount=type=cache
	// confuses legacy parser line counting, causing "unknown instruction" errors).
	useBuildKit := os.Getenv("BUILDKIT_HOST") != "" && os.Getenv("MOAT_DISABLE_BUILDKIT") != "1"
	var baseImage string
	if opts.Config != nil {
		baseImage = opts.Config.BaseImage
	}
	// NeedsGitIdentity (hasGit) also gates whether moat-init.sh is deployed, which
	// is what sets git http.proxyAuthMethod=basic for HTTPS git through the proxy
	// (#370). The github grant implies the git dep, so a bare `--grant github` run
	// gets the init script via this path. Keep that chain intact when refactoring
	// (covered by TestProvider_ImpliedDependencies + TestImageSpecNeedsInit's
	// GitIdentity case).
	imageSpec := &deps.ImageSpec{
		BaseImage:          baseImage,
		NeedsSSH:           hasSSHGrants,
		SSHHosts:           sshGrants,
		InitProviders:      imgNeeds.initProviders,
		NeedsFirewall:      needsProxyForFirewall,
		NeedsGitIdentity:   hasGit,
		NeedsInitFiles:     imgNeeds.initFiles,
		NeedsClipboard:     needsClipboard,
		UseBuildKit:        &useBuildKit,
		ClaudeMarketplaces: claudeMarketplaces,
		ClaudePlugins:      claudePlugins,
		HasNamedVolumes:    configHasNamedVolumes(opts.Config),
		Hooks:              hooks,
		// Volume mode requires the moat-init entrypoint to populate + chown the
		// named volume as root; force a custom image with init even when the run
		// has no deps/grants (otherwise the volume is silently left empty).
		NeedsWorkspaceVolume: volumeMode,
	}

	// Resolve container image based on dependencies and image spec
	hasDeps := len(installableDeps) > 0
	containerImage := image.Resolve(installableDeps, imageSpec)

	// Set agent and image for logging context
	if opts.Config != nil && opts.Config.Agent != "" {
		r.Agent = opts.Config.Agent
	}
	r.Image = containerImage
	r.Runtime = string(m.defaultRuntime().Type())

	needsCustomImage := imageSpec.NeedsCustomImage(hasDeps)

	// Handle --rebuild: delete existing image to force fresh build
	if opts.Rebuild && needsCustomImage {
		exists, _ := m.defaultRuntime().BuildManager().ImageExists(ctx, containerImage)
		if exists {
			fmt.Printf("Removing cached image %s...\n", containerImage)
			if err := m.defaultRuntime().RemoveImage(ctx, containerImage); err != nil {
				ui.Warnf("Failed to remove image: %v", err)
			}
		}
	}

	// Build custom image if we have dependencies or SSH grants.
	// Both Docker and Apple containers support Dockerfile builds.
	var generatedDockerfile string
	if needsCustomImage {
		// Always generate the Dockerfile so we can save it to the run directory
		result, err := deps.GenerateDockerfile(installableDeps, imageSpec)
		if err != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("generating Dockerfile: %w", err)
		}
		generatedDockerfile = result.Dockerfile

		exists, err := m.defaultRuntime().BuildManager().ImageExists(ctx, containerImage)
		if err != nil {
			cleanupDaemonRun()
			return nil, fmt.Errorf("checking image: %w", err)
		}

		if !exists {
			// Clone marketplace repos on host only when we need to build.
			// When the image is cached this avoids unnecessary git clones.
			cloneResult := cloneMarketplacesOnHost(ctx, claudeMarketplaces)
			defer func() {
				for _, dir := range cloneResult.cleanupDirs {
					os.RemoveAll(dir)
				}
			}()

			// Apply pre-clone info back to marketplace configs so the
			// regenerated Dockerfile uses COPY instead of clone commands.
			for _, p := range cloneResult.precloned {
				claudeMarketplaces[p.index].PreCloned = p.contextPrefix
				claudeMarketplaces[p.index].CommitTime = p.commitTime
			}

			// Regenerate Dockerfile with pre-cloned marketplace info.
			if len(cloneResult.contextFiles) > 0 {
				result, err = deps.GenerateDockerfile(installableDeps, imageSpec)
				if err != nil {
					cleanupDaemonRun()
					return nil, fmt.Errorf("generating Dockerfile: %w", err)
				}
				generatedDockerfile = result.Dockerfile
			}

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

			buildMgr := m.defaultRuntime().BuildManager()
			if buildMgr == nil {
				cleanupDaemonRun()
				return nil, fmt.Errorf("cannot build image: runtime %s does not support building", m.defaultRuntime().Type())
			}

			// Merge pre-cloned marketplace files into build context.
			// These are added alongside the files from Dockerfile generation
			// (which includes known_marketplaces.json via ExtraContextFiles).
			if len(cloneResult.contextFiles) > 0 {
				if result.ContextFiles == nil {
					result.ContextFiles = make(map[string][]byte)
				}
				for path, content := range cloneResult.contextFiles {
					result.ContextFiles[path] = content
				}
			}
			buildOpts.ContextFiles = result.ContextFiles
			if err := buildMgr.BuildImage(ctx, result.Dockerfile, containerImage, buildOpts); err != nil {
				cleanupDaemonRun()
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
		imageHome := m.defaultRuntime().BuildManager().GetImageHomeDir(ctx, containerImage)
		containerHome = resolveContainerHome(needsCustomImage, imageHome)
		if opts.Config != nil && opts.Config.ShouldSyncClaudeLogs() {
			hostClaudeProjects := claudeProjectsHostDir(hostHome, opts.Workspace)

			// Ensure directory exists on host
			if hostClaudeProjects == "" {
				log.Warn("skipping Claude log sync mount: empty workspace path")
			} else if err := os.MkdirAll(hostClaudeProjects, 0o755); err != nil {
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

	// Set up provider-specific container mounts and init files.
	// Init files are written to disk by moat-init.sh at container startup,
	// avoiding bind mounts for config dirs that tools need to write to.
	initFiles := make(map[string]string)
	if containerHome != "" {
		if store, storeErr := openCredStore(); storeErr == nil {
			for _, grant := range opts.Grants {
				grantName := strings.Split(grant, ":")[0]
				credName := credential.Provider(provider.ResolveName(grantName))
				if cred, err := store.Get(credName); err == nil {
					if prov := provider.Get(grantName); prov != nil {
						provCred := provider.FromLegacy(cred)
						providerMounts, cleanupPath, mountErr := prov.ContainerMounts(provCred, containerHome)
						if mountErr != nil {
							log.Debug("failed to set up provider mounts", "provider", credName, "error", mountErr)
						} else {
							mounts = append(mounts, providerMounts...)
							if cleanupPath != "" {
								if r.ProviderCleanupPaths == nil {
									r.ProviderCleanupPaths = make(map[string]string)
								}
								r.ProviderCleanupPaths[string(credName)] = cleanupPath
							}
						}
						// Collect init files from providers that implement InitFileProvider
						if ifp, ok := prov.(provider.InitFileProvider); ok {
							for p, content := range ifp.ContainerInitFiles(provCred, containerHome) {
								cleaned := filepath.Clean(p)
								if !filepath.IsAbs(cleaned) || !strings.HasPrefix(cleaned, containerHome+string(filepath.Separator)) {
									log.Warn("init file path outside container home, skipping", "provider", credName, "path", p)
									continue
								}
								initFiles[cleaned] = content
							}
						}
					}
				}
			}
		}
	}
	if len(initFiles) > 0 {
		var buf strings.Builder
		for initPath, content := range initFiles {
			buf.WriteString(initPath)
			buf.WriteByte('\t')
			buf.WriteString(base64.StdEncoding.EncodeToString([]byte(content)))
			buf.WriteByte('\n')
		}
		proxyEnv = append(proxyEnv, "MOAT_INIT_FILES="+buf.String())
	}

	// Build and render runtime context for agent instruction files.
	var renderedContext string
	if opts.Config != nil {
		rc := runctx.BuildFromConfig(opts.Config, r.ID)
		renderedContext = runctx.Render(rc)
	}

	// Set up Claude staging directory for init script using the provider interface.
	// This includes OAuth credentials, host files, and MCP server configuration.
	var claudeConfig *provider.ContainerConfig
	if needsClaudeInit || (opts.Config != nil) {
		// claudeSettings was loaded earlier for plugin detection
		hasPlugins := claudeSettings != nil && claudeSettings.HasPluginsOrMarketplaces()
		isClaudeCode := opts.Config != nil && opts.Config.ShouldSyncClaudeLogs()

		hasClaudeLocalMCP := opts.Config != nil && len(opts.Config.Claude.MCP) > 0
		// We need PrepareContainer if:
		// - needsClaudeInit (OAuth credentials to set up)
		// - hasPlugins (plugin settings to configure)
		// - isClaudeCode (need to copy onboarding state from host)
		// - hasClaudeLocalMCP (local MCP servers to configure)
		if needsClaudeInit || hasPlugins || isClaudeCode || hasClaudeLocalMCP {
			claudeProvider := provider.GetAgent("claude")
			if claudeProvider == nil {
				cleanupDaemonRun()
				return nil, fmt.Errorf("claude provider not registered")
			}

			// Build MCP server configuration for .claude.json
			// Use proxy relay URLs instead of direct MCP server URLs to work around
			// Claude Code's MCP client not respecting HTTP_PROXY environment variables.
			// This also bridges host-local MCP servers (localhost/127.0.0.1) which
			// the container cannot reach directly.
			mcpServers := make(map[string]provider.MCPServerConfig)
			if opts.Config != nil && len(opts.Config.MCP) > 0 {
				// Use the synthetic proxy host (in NO_PROXY) so the MCP client
				// connects directly and the proxy strips the per-run token via
				// handleDirectMCPRelay. GetHostAddress is not in NO_PROXY, so it would
				// route through the CONNECT tunnel to the wrong handler → 404.
				proxyAddr := fmt.Sprintf("%s:%d", syntheticProxyHost, r.ProxyPort)
				for _, mcp := range opts.Config.MCP {
					relayURL := fmt.Sprintf("http://%s/mcp/%s/%s", proxyAddr, r.ProxyAuthToken, mcp.Name)
					mcpCfg := provider.MCPServerConfig{
						URL: relayURL,
					}
					if mcp.Auth != nil {
						mcpCfg.Headers = map[string]string{
							mcp.Auth.Header: "moat-stub-" + mcp.Auth.Grant,
						}
					}
					mcpServers[mcp.Name] = mcpCfg
				}
			}

			// Get Claude credential for PrepareContainer
			// Preference: claude > anthropic (for backward compatibility)
			var claudeCred *provider.Credential
			if needsClaudeInit {
				if store, storeErr := openCredStore(); storeErr == nil {
					// Try claude first, fall back to anthropic
					cred, err := store.Get(credential.ProviderClaude)
					if err != nil {
						cred, err = store.Get(credential.ProviderAnthropic)
					}
					if err == nil {
						claudeCred = provider.FromLegacy(cred)
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
			var claudeSubType, claudeRateTier string
			if opts.Config != nil {
				claudeSubType = opts.Config.Claude.SubscriptionType
				claudeRateTier = opts.Config.Claude.RateLimitTier
			}
			claudeConfig, prepErr = claudeProvider.PrepareContainer(ctx, provider.PrepareOpts{
				Credential:       claudeCred,
				ContainerHome:    containerHome,
				MCPServers:       mcpServers,
				RuntimeContext:   renderedContext,
				LocalMCPServers:  claudeLocalMCP,
				SubscriptionType: claudeSubType,
				RateLimitTier:    claudeRateTier,
				// HostConfig is read automatically by the provider if nil
			})
			if prepErr != nil {
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				return nil, fmt.Errorf("preparing Claude container config: %w", prepErr)
			}

			// Add mounts and env vars from provider
			mounts = append(mounts, claudeConfig.Mounts...)
			proxyEnv = append(proxyEnv, claudeConfig.Env...)

			// Write settings.json to suppress startup prompts, pin the renderer,
			// persist bypass-permissions mode, and configure plugins.
			// moat-init.sh copies $MOAT_CLAUDE_INIT/settings.json to ~/.claude/settings.json.
			skipPrompt := opts.Config != nil && opts.Config.Claude.SkipPermissionsPrompt
			if hasPlugins || skipPrompt || hasClaudeCode {
				if claudeSettings == nil {
					claudeSettings = &claude.Settings{}
				}
				// Pin the renderer and (for bypass runs) persist bypass mode so
				// Claude Code's fullscreen-renderer re-exec can't silently drop the
				// --dangerously-skip-permissions flag. See Settings.ApplyRunPolicy.
				if policyErr := claudeSettings.ApplyRunPolicy(hasClaudeCode, skipPrompt); policyErr != nil {
					ui.Warnf("Failed to persist bypass-permissions mode in settings.json (check the 'permissions' value in ~/.moat/claude/settings.json): %v", policyErr)
				}
				settingsPath := filepath.Join(claudeConfig.StagingDir, "settings.json")
				settingsJSON, jsonErr := json.MarshalIndent(claudeSettings, "", "  ")
				if jsonErr != nil {
					// MarshalIndent cannot fail for Settings (no channels, funcs, or cycles);
					// log.Warn for defense-in-depth only.
					log.Warn("failed to marshal settings.json", "error", jsonErr)
				} else if writeErr := os.WriteFile(settingsPath, settingsJSON, 0o644); writeErr != nil {
					ui.Warnf("Failed to write Claude settings to container: %v", writeErr)
				} else {
					log.Debug("wrote settings.json to staging dir",
						"plugins", len(claudeSettings.EnabledPlugins),
						"marketplaces", len(claudeSettings.ExtraKnownMarketplaces))
				}
			}
		}
	}

	// Set up Codex staging directory for init script using the provider interface.
	// This includes auth config for OpenAI tokens.
	var codexConfig *provider.ContainerConfig
	hasCodexLocalMCP := opts.Config != nil && len(opts.Config.Codex.MCP) > 0
	if needsCodexInit || hasCodexLocalMCP || (opts.Config != nil && opts.Config.ShouldSyncCodexLogs()) {
		codexProvider := provider.GetAgent("codex")
		if codexProvider == nil {
			cleanupDaemonRun()
			cleanupAgentConfig(claudeConfig)
			return nil, fmt.Errorf("codex provider not registered")
		}

		// Get Codex credential for PrepareContainer
		var codexCred *provider.Credential
		if needsCodexInit {
			if store, storeErr := openCredStore(); storeErr == nil {
				if cred, err := store.Get(credential.ProviderOpenAI); err == nil {
					codexCred = provider.FromLegacy(cred)
				}
			}
		}

		// Build local MCP server config from codex.mcp entries
		var codexLocalMCP map[string]provider.LocalMCPServerConfig
		if opts.Config != nil && len(opts.Config.Codex.MCP) > 0 {
			codexLocalMCP = make(map[string]provider.LocalMCPServerConfig)
			for name, spec := range opts.Config.Codex.MCP {
				env := spec.Env
				if spec.Grant != "" {
					v, ok := grantToEnvVar(spec.Grant)
					if !ok {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						return nil, fmt.Errorf("codex.mcp.%s: unknown grant %q (supported: github, openai, anthropic, gemini)", name, spec.Grant)
					}
					if !hasGrant(opts.Config.Grants, spec.Grant) {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						return nil, fmt.Errorf("codex.mcp.%s: grant %q not declared in top-level grants list — add 'grants: [%s]' to agent.yaml", name, spec.Grant, spec.Grant)
					}
					if env == nil {
						env = make(map[string]string)
					} else {
						// Copy to avoid mutating the original config
						envCopy := make(map[string]string, len(env)+1)
						for k, v := range env {
							envCopy[k] = v
						}
						env = envCopy
					}
					env[v] = grantToPlaceholder(spec.Grant)
				}
				codexLocalMCP[name] = provider.LocalMCPServerConfig{
					Command: spec.Command,
					Args:    spec.Args,
					Env:     env,
					Cwd:     spec.Cwd,
				}
			}
		}

		// Call provider to prepare container config
		var prepErr error
		codexConfig, prepErr = codexProvider.PrepareContainer(ctx, provider.PrepareOpts{
			Credential:      codexCred,
			ContainerHome:   containerHome,
			RuntimeContext:  renderedContext,
			LocalMCPServers: codexLocalMCP,
		})
		if prepErr != nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
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
	hasGeminiLocalMCP := opts.Config != nil && len(opts.Config.Gemini.MCP) > 0
	if needsGeminiInit || hasGeminiLocalMCP || (opts.Config != nil && opts.Config.ShouldSyncGeminiLogs()) {
		geminiProvider := provider.GetAgent("gemini")
		if geminiProvider == nil {
			cleanupDaemonRun()
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("gemini provider not registered")
		}

		// Get Gemini credential for PrepareContainer
		var geminiCred *provider.Credential
		if needsGeminiInit {
			if store, storeErr := openCredStore(); storeErr == nil {
				if cred, err := store.Get(credential.ProviderGemini); err == nil {
					geminiCred = provider.FromLegacy(cred)
				}
			}
		}

		// Build local MCP server config from gemini.mcp entries
		var geminiLocalMCP map[string]provider.LocalMCPServerConfig
		if opts.Config != nil && len(opts.Config.Gemini.MCP) > 0 {
			geminiLocalMCP = make(map[string]provider.LocalMCPServerConfig)
			for name, spec := range opts.Config.Gemini.MCP {
				env := spec.Env
				if spec.Grant != "" {
					v, ok := grantToEnvVar(spec.Grant)
					if !ok {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						cleanupAgentConfig(codexConfig)
						return nil, fmt.Errorf("gemini.mcp.%s: unknown grant %q (supported: github, openai, anthropic, gemini)", name, spec.Grant)
					}
					if !hasGrant(opts.Config.Grants, spec.Grant) {
						cleanupDaemonRun()
						cleanupSSH(sshServer)
						cleanupAgentConfig(claudeConfig)
						cleanupAgentConfig(codexConfig)
						return nil, fmt.Errorf("gemini.mcp.%s: grant %q not declared in top-level grants list — add 'grants: [%s]' to agent.yaml", name, spec.Grant, spec.Grant)
					}
					if env == nil {
						env = make(map[string]string)
					} else {
						envCopy := make(map[string]string, len(env)+1)
						for k, v := range env {
							envCopy[k] = v
						}
						env = envCopy
					}
					env[v] = grantToPlaceholder(spec.Grant)
				}
				geminiLocalMCP[name] = provider.LocalMCPServerConfig{
					Command: spec.Command,
					Args:    spec.Args,
					Env:     env,
					Cwd:     spec.Cwd,
				}
			}
		}

		// Call provider to prepare container config
		var prepErr error
		geminiConfig, prepErr = geminiProvider.PrepareContainer(ctx, provider.PrepareOpts{
			Credential:      geminiCred,
			ContainerHome:   containerHome,
			RuntimeContext:  renderedContext,
			LocalMCPServers: geminiLocalMCP,
		})
		if prepErr != nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
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
	if volumeMode {
		// Volume mode runs as root so moat-init.sh can populate the freshly
		// created volume (root-owned) and chown /workspace before dropping
		// privileges to the workspace user.
		containerUser = "0:0"
	} else if goruntime.GOOS == "linux" {
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
		netMgr := m.defaultRuntime().NetworkManager()
		if netMgr == nil {
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (networks not supported by %s)", m.defaultRuntime().Type())
		}
		netID, netErr := netMgr.CreateNetwork(ctx, buildkitCfg.NetworkName)
		if netErr != nil {
			cleanupDaemonRun()
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

		sidecarMgr := m.defaultRuntime().SidecarManager()
		if sidecarMgr == nil {
			netMgr := m.defaultRuntime().NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("BuildKit requires Docker runtime (sidecars not supported by %s)", m.defaultRuntime().Type())
		}
		buildkitContainerID, sidecarErr := sidecarMgr.StartSidecar(ctx, sidecarCfg)
		if sidecarErr != nil {
			// Clean up network on failure
			netMgr := m.defaultRuntime().NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupDaemonRun()
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
			_ = m.defaultRuntime().StopContainer(ctx, buildkitContainerID) //nolint:errcheck
			netMgr := m.defaultRuntime().NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, networkID) //nolint:errcheck
			}
			cleanupDaemonRun()
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
		svcMgr := m.defaultRuntime().ServiceManager()
		if svcMgr == nil {
			cleanupDaemonRun()
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
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, err
			}
		}

		// Ensure network exists (share with BuildKit if present)
		if networkID == "" {
			netMgr := m.defaultRuntime().NetworkManager()
			if netMgr == nil {
				cleanupDaemonRun()
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
				cleanupDaemonRun()
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
		var svcConfigs []container.ServiceConfig

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
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("configuring %s service: %w", dep.Name, err)
			}

			svcConfigs = append(svcConfigs, svcCfg)

			// Create cache directory if needed
			if svcCfg.CacheHostPath != "" {
				if mkdirErr := os.MkdirAll(svcCfg.CacheHostPath, 0o700); mkdirErr != nil {
					cleanupServices()
					cleanupDaemonRun()
					cleanupSSH(sshServer)
					cleanupAgentConfig(claudeConfig)
					cleanupAgentConfig(codexConfig)
					cleanupAgentConfig(geminiConfig)
					return nil, fmt.Errorf("creating cache directory for %s: %w", dep.Name, mkdirErr)
				}
			}

			info, err := svcMgr.StartService(ctx, svcCfg)
			if err != nil {
				cleanupServices()
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("starting %s service: %w", dep.Name, err)
			}

			serviceInfos = append(serviceInfos, info)
			r.ServiceContainers[dep.Name] = info.ID
		}

		// Create run storage early so provision output can be captured in logs.
		// NewRunStore is idempotent (uses MkdirAll), so it's safe to call now
		// even though the main container hasn't been created yet.
		store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if err != nil {
			cleanupServices()
			cleanupDaemonRun()
			cleanupSSH(sshServer)
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, fmt.Errorf("creating run storage: %w", err)
		}
		r.Store = store

		// Wait for readiness
		for i, dep := range serviceDeps {
			wait := true
			if opts.Config != nil {
				if s, ok := opts.Config.Services[dep.Name]; ok {
					wait = s.ServiceWait()
				}
			}
			if !wait {
				// Reject wait: false when provisions are declared — models can't
				// be pulled until the service is ready.
				if svcConfigs[i].ProvisionCmd != "" && len(svcConfigs[i].Provisions) > 0 {
					cleanupServices()
					cleanupDaemonRun()
					cleanupSSH(sshServer)
					cleanupAgentConfig(claudeConfig)
					cleanupAgentConfig(codexConfig)
					cleanupAgentConfig(geminiConfig)
					return nil, fmt.Errorf("%s: wait: false is incompatible with provisioning — "+
						"items cannot be pulled until the service is ready\n\n"+
						"Either remove wait: false or remove the provisioned items",
						dep.Name)
				}
				continue
			}

			info := serviceInfos[i]
			fmt.Fprintf(os.Stderr, "Waiting for %s to be ready...\n", dep.Name)
			log.Debug("waiting for service to be ready", "service", dep.Name)
			if err := waitForServiceReady(ctx, svcMgr, info); err != nil {
				cleanupServices()
				cleanupDaemonRun()
				cleanupSSH(sshServer)
				cleanupAgentConfig(claudeConfig)
				cleanupAgentConfig(codexConfig)
				cleanupAgentConfig(geminiConfig)
				return nil, fmt.Errorf("%s service failed to become ready: %w\n\n"+
					"Check run logs:\n  moat logs %s\n\n"+
					"Or disable wait:\n  services:\n    %s:\n      wait: false",
					dep.Name, err, r.ID, dep.Name)
			}

			// Provision items (e.g., pull models) if configured
			if svcConfigs[i].ProvisionCmd != "" && len(svcConfigs[i].Provisions) > 0 {
				fmt.Fprintf(os.Stderr, "Pulling %d item(s) for %s: %s\n",
					len(svcConfigs[i].Provisions), dep.Name, strings.Join(svcConfigs[i].Provisions, ", "))
				log.Debug("provisioning service", "service", dep.Name, "items", svcConfigs[i].Provisions)
				// IIFE so defer lw.Close() fires after provisionService, not at function exit.
				// Without this, multiple provision-capable services would accumulate deferred
				// closes until the outer function returns.
				provErr := func() error {
					provOut := io.Writer(os.Stderr)
					if lw, lwErr := store.LogWriter(); lwErr == nil {
						defer lw.Close()
						provOut = io.MultiWriter(os.Stderr, lw)
					}
					return provisionService(ctx, svcMgr, info, svcConfigs[i], provOut)
				}()
				if err := provErr; err != nil {
					cleanupServices()
					cleanupDaemonRun()
					cleanupSSH(sshServer)
					cleanupAgentConfig(claudeConfig)
					cleanupAgentConfig(codexConfig)
					cleanupAgentConfig(geminiConfig)
					return nil, fmt.Errorf("%s service provisioning failed: %w\n\n"+
						"Check run logs:\n  moat logs %s",
						dep.Name, err, r.ID)
				}
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

	// When a custom network is used (for services or BuildKit), the container
	// is on a different subnet than the default network. The proxy host address
	// (derived from the default network gateway) may be unreachable.
	//
	// With synthetic hostnames, the proxy env vars use "moat-proxy" instead of
	// an IP, so replaceHostInEnv no longer rewrites them. Instead, update the
	// --add-host entries so "moat-proxy" and "moat-host" resolve to the custom
	// network's gateway IP (which can reach the host) instead of "host-gateway"
	// (which resolves to the docker0 bridge IP, unreachable from custom networks).
	//
	// Also rewrite any remaining IP-based env vars (e.g., MOAT_SSH_TCP_ADDR)
	// that still reference the old hostAddr.
	if networkID != "" && net.ParseIP(hostAddr) != nil {
		netMgr := m.defaultRuntime().NetworkManager()
		if netMgr == nil {
			ui.Warnf("cannot resolve gateway for custom network %q — proxy may be unreachable from container", networkID)
		} else if gw := netMgr.NetworkGateway(ctx, networkID); gw == "" {
			ui.Warnf("custom network %q has no gateway — proxy may be unreachable from container", networkID)
		} else if gw != hostAddr {
			log.Debug("rewriting proxy host for custom network",
				"old", hostAddr, "new", gw, "network", networkID)
			// Rewrite IP-based env vars (e.g., MOAT_SSH_TCP_ADDR).
			proxyEnv = replaceHostInEnv(proxyEnv, hostAddr, gw)
			r.ProxyHost = gw
			// Rewrite --add-host entries so synthetic hostnames resolve
			// to the custom network gateway instead of the default gateway.
			// Match on hostname prefix to handle both "host-gateway" (Docker)
			// and IP-based targets (Apple containers).
			proxyPrefix := syntheticProxyHost + ":"
			hostPrefix := syntheticHostGateway + ":"
			for i, h := range extraHosts {
				if strings.HasPrefix(h, proxyPrefix) {
					extraHosts[i] = proxyPrefix + gw
				} else if strings.HasPrefix(h, hostPrefix) {
					extraHosts[i] = hostPrefix + gw
				}
			}
		}
	}

	// Add BuildKit env vars if enabled
	buildkitEnv := computeBuildKitEnv(buildkitCfg.Enabled)
	proxyEnv = append(proxyEnv, buildkitEnv...)

	// Extract container resource limits from config (applies to both Docker and Apple).
	// Priority: explicit moat.yaml > agent provider default > runtime fallback.
	var memoryMB, cpus int
	var dns []string
	var ulimits []container.Ulimit
	if opts.Config != nil {
		memoryMB = opts.Config.Container.Memory
		cpus = opts.Config.Container.CPUs
		dns = opts.Config.Container.DNS
		for name, spec := range opts.Config.Container.Ulimits {
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

	// On Apple containers, if moat.yaml didn't set memory and we're running an
	// AI agent, use the agent default (8 GB). Apple's system default of 1 GB is
	// too low for Claude Code, Codex, and Gemini CLI.
	// Docker containers are left unlimited unless explicitly configured.
	if memoryMB == 0 && m.defaultRuntime().Type() == container.RuntimeApple && isAIAgent(opts.Config) {
		memoryMB = container.DefaultAgentMemoryMB
		log.Debug("using default agent memory for Apple container", "memoryMB", memoryMB)
	}

	// Named-volume roots are chowned to the run user by one of two mutually
	// exclusive mechanisms (see volumeChownEnv): moat-init on the root-entrypoint
	// path (driven by MOAT_VOLUME_CHOWN), or the runtime's initNamedVolumeOwnership
	// helper on the non-root path.
	if env, ok := volumeChownEnv(containerUser, volumeChownPaths); ok {
		proxyEnv = append(proxyEnv, env)
	}

	// In volume mode, tell moat-init.sh to populate /workspace from the staging
	// bind, and create the per-run named volume before the container so the
	// named-volume mount resolves at container-create time.
	if volumeMode {
		proxyEnv = append(proxyEnv,
			"MOAT_WORKSPACE_VOLUME=1",
			"MOAT_WORKSPACE_STAGING="+stagingPath,
		)
		if excludes := workspaceExcludes(opts.Config); excludes != "" {
			proxyEnv = append(proxyEnv, "MOAT_WORKSPACE_EXCLUDES="+excludes)
		}
		if err := m.defaultRuntime().VolumeCreate(ctx, workspaceVolumeName); err != nil {
			return nil, fmt.Errorf("creating workspace volume %s: %w", workspaceVolumeName, err)
		}
		// Remove the freshly-created volume if Create fails before returning
		// successfully (container create or a later step errors out), so a
		// partially-created run leaves no orphaned volume. cleanupRunDir is the
		// existing success sentinel — it is set to false only on the success path
		// at the end of Create. Best-effort: cleanup failure is logged, not fatal.
		volCleanupRT := m.defaultRuntime()
		volCleanupName := workspaceVolumeName
		defer func() {
			if cleanupRunDir {
				// Use a fresh context: if Create failed because the caller's ctx
				// was canceled (e.g. Ctrl-C), removing the volume with that same
				// canceled ctx would be rejected and leak the volume. Matches the
				// cleanup pattern in VolumeExport.
				if err := volCleanupRT.VolumeRemove(context.Background(), volCleanupName, true); err != nil {
					log.Debug("create: failed to remove workspace volume after failure", "volume", volCleanupName, "error", err)
				}
			}
		}()
	}

	// Create container
	containerID, err := m.defaultRuntime().CreateContainer(ctx, container.Config{
		Name:         r.ID,
		Image:        containerImage,
		Cmd:          cmd,
		WorkingDir:   "/workspace",
		Env:          proxyEnv,
		User:         containerUser,
		ExtraHosts:   extraHosts,
		NetworkMode:  networkMode,
		Mounts:       mounts,
		TmpfsMounts:  tmpfsMounts,
		PortBindings: portBindings,
		CapAdd:       capAdd,
		GroupAdd:     groupAdd,
		Privileged:   privileged,
		Interactive:  opts.Interactive,
		HasMoatUser:  needsCustomImage, // moat-built images have moatuser; base images don't
		MemoryMB:     memoryMB,
		CPUs:         cpus,
		DNS:          dns,
		Ulimits:      ulimits,
	})
	if err != nil {
		// Clean up BuildKit resources on failure
		if buildkitCfg.Enabled && r.BuildkitContainerID != "" {
			_ = m.defaultRuntime().StopContainer(ctx, r.BuildkitContainerID)   //nolint:errcheck
			_ = m.defaultRuntime().RemoveContainer(ctx, r.BuildkitContainerID) //nolint:errcheck
			netMgr := m.defaultRuntime().NetworkManager()
			if netMgr != nil {
				_ = netMgr.RemoveNetwork(ctx, r.NetworkID) //nolint:errcheck
			}
		}
		// Clean up proxy servers if container creation fails
		cleanupDaemonRun()
		cleanupSSH(sshServer)
		cleanupAgentConfig(claudeConfig)
		cleanupAgentConfig(codexConfig)
		cleanupAgentConfig(geminiConfig)
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID
	r.SSHAgentServer = sshServer

	// Update daemon with the container ID (phase 2 of registration)
	if r.ProxyAuthToken != "" && m.daemonClient != nil {
		if updErr := m.daemonClient.UpdateRun(ctx, r.ProxyAuthToken, containerID); updErr != nil {
			log.Debug("failed to update daemon with container ID", "error", updErr)
		}
	}

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
			if rmErr := m.defaultRuntime().RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupDaemonRun()
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("enabling TLS on routing proxy: %w", tlsErr)
		}
		if proxyErr := m.proxyLifecycle.EnsureRunning(); proxyErr != nil {
			// Clean up container
			if rmErr := m.defaultRuntime().RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupDaemonRun()
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			return nil, fmt.Errorf("starting routing proxy: %w", proxyErr)
		}
	}

	// Ensure run storage exists (may have been created early for service provisioning,
	// or needs to be created now for runs without services).
	if r.Store == nil {
		runStore, storeErr := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
		if storeErr != nil {
			// Clean up container and proxy if storage creation fails
			if rmErr := m.defaultRuntime().RemoveContainer(ctx, containerID); rmErr != nil {
				log.Debug("failed to remove container during cleanup", "error", rmErr)
			}
			cleanupDaemonRun()
			cleanupAgentConfig(claudeConfig)
			cleanupAgentConfig(codexConfig)
			cleanupAgentConfig(geminiConfig)
			return nil, fmt.Errorf("creating run storage: %w", storeErr)
		}
		r.Store = runStore
	}

	// Save the generated Dockerfile to the run directory for debugging/inspection
	if generatedDockerfile != "" {
		if saveErr := r.Store.SaveDockerfile(generatedDockerfile); saveErr != nil {
			log.Debug("failed to save Dockerfile to run directory", "error", saveErr)
		}
	}

	// Open audit store for tamper-proof logging
	auditStore, err := audit.OpenStore(filepath.Join(r.Store.Dir(), "audit.db"))
	if err != nil {
		// Clean up container, proxy, and storage if audit store fails
		if rmErr := m.defaultRuntime().RemoveContainer(ctx, containerID); rmErr != nil {
			log.Debug("failed to remove container during cleanup", "error", rmErr)
		}
		cleanupDaemonRun()
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
		snapshotDir := filepath.Join(r.Store.Dir(), "snapshots")
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
		_ = r.Store.WriteSecretResolution(storage.SecretResolution{
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

	cleanupRunDir = false
	return r, nil
}

// replaceHostInEnv replaces all occurrences of oldHost with newHost in the
// value portion of env vars (after the first '='). This is used to rewrite
// proxy URLs when a container is placed on a custom network whose gateway
// differs from the default network gateway.
func replaceHostInEnv(env []string, oldHost, newHost string) []string {
	result := make([]string, len(env))
	for i, e := range env {
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			result[i] = e[:idx+1] + strings.ReplaceAll(e[idx+1:], oldHost, newHost)
		} else {
			result[i] = e
		}
	}
	return result
}

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

// isAIAgent returns true if the config specifies an AI coding agent
// (claude, codex, or gemini). Used to apply agent-specific defaults
// like the 8 GB memory limit on Apple containers.
func isAIAgent(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.HasPrefix(cfg.Agent, "claude") ||
		strings.HasPrefix(cfg.Agent, "codex") ||
		strings.HasPrefix(cfg.Agent, "gemini")
}

// resolveContainerHome returns the home directory to use for container mounts.
// Most moat runs build a custom image (needsCustomImage=true) which always creates
// moatuser and runs as that user, so the home is /home/moatuser. We use this
// directly rather than inspecting the image because init-based images don't set
// USER moatuser in the Dockerfile — the init script drops privileges at runtime,
// so GetImageHomeDir incorrectly returns "/root".
//
// The only case where needsCustomImage is false is a minimal moat.yaml with no
// dependencies, grants, or plugins — the base image is used as-is with no
// Dockerfile generated, so we fall back to the image's detected home.
func resolveContainerHome(needsCustomImage bool, imageHome string) string {
	if needsCustomImage {
		return "/home/moatuser"
	}
	return imageHome
}

// claudeProjectsHostDir returns the host-side ~/.claude/projects/<dir> path to
// bind-mount for workspace, or "" if the Claude log-sync mount should be skipped.
//
// An empty workspace slugifies to "" and would collapse the filepath.Join to
// ~/.claude/projects, bind-mounting the host's entire projects tree (every
// project's session history) into the container. ResolveWorkspacePath makes an
// empty workspace unreachable today, but the consequence is severe enough to
// guard against directly.
func claudeProjectsHostDir(hostHome, workspace string) string {
	claudeDir := claude.WorkspaceToClaudeDir(workspace)
	if claudeDir == "" {
		return ""
	}
	return filepath.Join(hostHome, ".claude", "projects", claudeDir)
}

// agentImpliedDependencies returns dependencies implicitly required by an agent.
// Claude Code's security-guidance feature shells out to python3, so a Python
// interpreter must be present whenever the Claude agent runs. See issue #369.
func agentImpliedDependencies(agent string) []string {
	if strings.HasPrefix(agent, "claude") {
		return []string{"python"}
	}
	return nil
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
	if err = os.MkdirAll(certOnlyDir, 0o755); err != nil {
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

	if err = os.WriteFile(certDst, srcContent, 0o644); err != nil {
		return fmt.Errorf("writing CA certificate: %w", err)
	}

	return nil
}

// synthHostStrategy decides how the container learns the IP addresses for
// the synthetic moat-proxy and moat-host hostnames. It returns the entries
// to append to Docker's --add-host flag and, separately, the value for the
// MOAT_EXTRA_HOSTS env var (empty when not used). Exactly one of the two is
// non-empty per call.
//
// Strategy by runtime + OS:
//
//   - Docker on Linux — entries via --add-host with the "host-gateway"
//     sentinel. Docker's daemon substitutes the host's gateway IP at
//     container-create time, and Linux's routing reaches it from any bridge.
//
//     NOTE: "Docker on Linux" here means native Docker Engine. Docker
//     Desktop for Linux runs Docker Engine inside a VM and exhibits the
//     same docker0-unreachable-from-custom-network behavior as Docker
//     Desktop on macOS/Windows — those users would still hit the bug this
//     strategy exists to avoid. Distinguishing Docker Desktop from native
//     Engine requires a `docker info` probe (Docker Desktop reports
//     OperatingSystem "Docker Desktop") or a host-side resolvability
//     check for host.docker.internal; both are out of scope here. Known
//     gap — tracked as a follow-up.
//
//   - Docker Desktop on macOS / Windows — entries via MOAT_EXTRA_HOSTS,
//     processed by moat-init.sh at container start. --add-host:host-gateway
//     resolves to the docker0 bridge gateway (e.g. 172.17.0.1), which is
//     unreachable from a custom bridge network (one is created whenever the
//     moat.yaml defines `services:`). host.docker.internal is the correct
//     target, but it is a container-only DNS name — the host side cannot
//     resolve it, so --add-host cannot consume it. We pass the name through
//     with an "@" prefix; moat-init.sh resolves it inside the container
//     where Docker Desktop's embedded DNS answers.
//
//   - Apple runtime — entries via MOAT_EXTRA_HOSTS. Apple's container CLI
//     has no --add-host equivalent, and Apple's GetHostAddress() already
//     returns a literal IP, so the env carries it directly (no sentinel).
//
// hostAddr is whatever the runtime's GetHostAddress() returns; it may be an
// IP or a hostname. If it's an IP we emit it literally; if it's a hostname
// we prefix it with "@" so moat-init.sh knows to resolve it.
func synthHostStrategy(runtimeType container.RuntimeType, goos, hostAddr string) (dockerExtraHosts []string, extraHostsEnv string) {
	if runtimeType == container.RuntimeDocker && goos == "linux" {
		return []string{
			syntheticProxyHost + ":host-gateway",
			syntheticHostGateway + ":host-gateway",
		}, ""
	}
	// For MOAT_EXTRA_HOSTS: literal IPs pass through, hostnames get the
	// resolve sentinel so moat-init.sh defers resolution to container DNS.
	target := hostAddr
	if net.ParseIP(hostAddr) == nil {
		target = "@" + hostAddr
	}
	return nil, syntheticProxyHost + ":" + target + " " + syntheticHostGateway + ":" + target
}

// buildProxyEnv constructs the environment variables that configure the container's
// HTTP proxy settings.
//
// The proxy is always addressed as syntheticProxyHost ("moat-proxy"), and
// MOAT_HOST_GATEWAY is always syntheticHostGateway ("moat-host"). On Docker
// these are resolved via --add-host; on Apple they are resolved via /etc/hosts
// injection from moat-init.sh. syntheticProxyHost is included in NO_PROXY so
// that relay/AWS traffic connects directly without going through the CONNECT
// tunnel, while syntheticHostGateway is intentionally NOT in NO_PROXY so
// host-bound traffic flows through the proxy for network policy enforcement.
//
// In host-network mode (Docker on Linux without ports), localhost and
// 127.0.0.1 are intentionally NOT in NO_PROXY because the container
// shares the host loopback — excluding them would let container processes
// bypass the proxy (and network.host enforcement) by connecting to
// localhost:<port> directly.
//
// In bridge/Apple mode the container has an isolated network namespace,
// so its localhost is private. Keeping loopback in NO_PROXY lets
// intra-container HTTP (e.g., a dev server on localhost:3000 consumed by
// the same container) work without routing through the proxy.
func buildProxyEnv(authToken string, proxyPort int, hostNetworkMode bool) []string {
	proxyAddr := syntheticProxyHost + ":" + strconv.Itoa(proxyPort)
	var proxyURL string
	if authToken != "" {
		proxyURL = "http://moat:" + authToken + "@" + proxyAddr
	} else {
		proxyURL = "http://" + proxyAddr
	}

	noProxy := syntheticProxyHost + ",buildkit"
	if !hostNetworkMode {
		// In bridge/Apple mode the container's loopback is isolated from
		// the host, so keep localhost out of the proxy to allow
		// intra-container HTTP traffic.
		noProxy += ",localhost,127.0.0.1"
	}

	return []string{
		"HTTP_PROXY=" + proxyURL,
		"HTTPS_PROXY=" + proxyURL,
		"http_proxy=" + proxyURL,
		"https_proxy=" + proxyURL,
		"NO_PROXY=" + noProxy,
		"no_proxy=" + noProxy,
		"TERM=xterm-256color",
		"MOAT_HOST_GATEWAY=" + syntheticHostGateway,
	}
}

// isMoatOwnedProxyVar returns true if the given environment variable name is
// one that moat owns and sets for the container. User-supplied values for any
// of these would override moat's proxy configuration and could bypass network
// policy enforcement, so they are filtered (with a warning) from moat.yaml env
// and -e flags.
//
// The ALL_PROXY / all_proxy / CURL_ALL_PROXY family is included because curl,
// wget, python-requests, and other libcurl-based tools honor those as a fallback
// or override of HTTP_PROXY/HTTPS_PROXY. Leaving them user-controllable would
// let a moat.yaml env: { ALL_PROXY: socks5://attacker:1080 } entry route all
// traffic around the moat proxy.
func isMoatOwnedProxyVar(name string) bool {
	upper := strings.ToUpper(name)
	switch upper {
	case "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY",
		"ALL_PROXY", "CURL_ALL_PROXY",
		"MOAT_HOST_GATEWAY", "MOAT_EXTRA_HOSTS":
		return true
	}
	return false
}

// buildRegisterRequest converts a daemon.RunContext into a daemon.RegisterRequest
// suitable for sending to the daemon API.
func buildRegisterRequest(rc *daemon.RunContext, grants []string) daemon.RegisterRequest {
	req := daemon.RegisterRequest{
		RunID:            rc.RunID,
		NetworkPolicy:    rc.NetworkPolicy,
		NetworkAllow:     rc.NetworkAllow,
		NetworkRules:     rc.NetworkRules,
		HostGateway:      rc.HostGateway,
		HostGatewayIP:    rc.HostGatewayIP,
		AllowedHostPorts: rc.AllowedHostPorts,
		MCPServers:       rc.MCPServers,
		Grants:           grants,
		AWSConfig:        rc.AWSConfig,
		CredProfile:      credential.ActiveProfile,
	}

	for host, creds := range rc.Credentials {
		for _, cred := range creds {
			req.Credentials = append(req.Credentials, daemon.CredentialSpec{
				Host:   host,
				Header: cred.Name,
				Value:  cred.Value,
				Grant:  cred.Grant,
			})
		}
	}

	for host, headers := range rc.ExtraHeaders {
		for _, h := range headers {
			req.ExtraHeaders = append(req.ExtraHeaders, daemon.ExtraHeaderSpec{
				Host:       host,
				HeaderName: h.Name,
				Value:      h.Value,
			})
		}
	}

	for host, headers := range rc.RemoveHeaders {
		for _, headerName := range headers {
			req.RemoveHeaders = append(req.RemoveHeaders, daemon.RemoveHeaderSpec{
				Host:       host,
				HeaderName: headerName,
			})
		}
	}

	for host, ts := range rc.TokenSubstitutions {
		req.TokenSubstitutions = append(req.TokenSubstitutions, daemon.TokenSubstitutionSpec{
			Host:        host,
			Placeholder: ts.Placeholder,
			RealToken:   ts.RealToken,
		})
	}

	// Derive transformer specs from response transformers.
	// Response transformers are Go functions (not serializable), so we convert
	// them to well-known specs that the daemon can reconstruct.
	// - Hosts with token substitutions use "response-scrub" (token redaction)
	// - Hosts without use "oauth-endpoint-workaround" (403 graceful degradation)
	for host := range rc.ResponseTransformers {
		kind := "oauth-endpoint-workaround"
		if _, hasTS := rc.TokenSubstitutions[host]; hasTS {
			kind = "response-scrub"
		}
		req.ResponseTransformers = append(req.ResponseTransformers, daemon.TransformerSpec{
			Host: host,
			Kind: kind,
		})
	}

	return req
}

// preclonedInfo holds the result of successfully cloning a single marketplace.
type preclonedInfo struct {
	index         int    // index into the original MarketplaceConfig slice
	contextPrefix string // build-context-relative path prefix (e.g., "marketplaces/name")
	commitTime    string // ISO 8601 timestamp of the last commit
}

// marketplaceCloneResult holds all outputs from cloning marketplace repos on the host.
type marketplaceCloneResult struct {
	cleanupDirs  []string          // temporary directories to remove after build
	contextFiles map[string][]byte // files to add to the Docker build context
	precloned    []preclonedInfo   // which marketplaces were successfully pre-cloned
}

// cloneMarketplacesOnHost clones marketplace repos on the host so private repos
// are accessible without passing credentials into the build context. The host's
// git credentials (gh auth, SSH keys, credential helpers) handle authentication.
func cloneMarketplacesOnHost(ctx context.Context, marketplaces []claude.MarketplaceConfig) marketplaceCloneResult {
	var result marketplaceCloneResult
	for i, m := range marketplaces {
		if !claude.ValidMarketplaceName(m.Name) {
			log.Warn("skipping marketplace with invalid name", "name", m.Name)
			continue
		}
		clonedDir, commitTime, cloneErr := claude.CloneMarketplace(ctx, m.Repo)
		if cloneErr != nil {
			ui.Warnf("Could not clone marketplace %q on host — the build will attempt to clone it inside the container, "+
				"but this will fail for private repos.\n"+
				"  To fix: run 'gh auth login' or configure SSH keys for git on the host.\n"+
				"  Repo: %s\n"+
				"  Error: %v", m.Name, m.Repo, cloneErr)
			continue
		}
		result.cleanupDirs = append(result.cleanupDirs, clonedDir)

		contextKey, tarData, collectErr := claude.CollectMarketplaceTar(clonedDir, m.Name)
		if collectErr != nil {
			ui.Warnf("Could not package marketplace %q after cloning (likely a filesystem or permissions issue) — the build will attempt to clone it inside the container: %v", m.Name, collectErr)
			continue
		}

		if len(tarData) == 0 {
			log.Warn("marketplace has no files, skipping pre-clone", "name", m.Name)
			continue
		}
		if result.contextFiles == nil {
			result.contextFiles = make(map[string][]byte)
		}
		result.contextFiles[contextKey] = tarData

		result.precloned = append(result.precloned, preclonedInfo{
			index:         i,
			contextPrefix: contextKey, // now a tar filename like "marketplace-name.tar"
			commitTime:    commitTime,
		})
		log.Info("pre-cloned marketplace on host", "name", m.Name)
	}
	return result
}

// grantToEnvVar maps a grant name to the environment variable that local MCP
// servers expect. The env var is set to a proxy placeholder so the proxy can
// intercept and substitute the real credential.
func grantToEnvVar(grant string) (string, bool) {
	switch grant {
	case "github":
		return "GITHUB_TOKEN", true
	case "openai":
		return "OPENAI_API_KEY", true
	case "anthropic":
		return "ANTHROPIC_API_KEY", true
	case "gemini":
		return "GEMINI_API_KEY", true
	default:
		return "", false
	}
}

// grantToPlaceholder returns a format-valid placeholder value for the given
// grant. Some SDKs validate credential format before making HTTP requests
// (e.g. gh CLI requires ghp_ prefix, OpenAI SDK requires sk- prefix), so
// ProxyInjectedPlaceholder would fail their format check before the proxy
// can inject the real token.
func grantToPlaceholder(grant string) string {
	switch grant {
	case "anthropic":
		return credential.AnthropicAPIKeyPlaceholder
	case "gemini":
		return credential.GeminiAPIKeyPlaceholder
	case "github":
		return credential.GitHubTokenPlaceholder
	case "openai":
		return credential.OpenAIAPIKeyPlaceholder
	default:
		return credential.ProxyInjectedPlaceholder
	}
}

// hasGrant checks whether a grant name appears in the grants list.
func hasGrant(grants []string, name string) bool {
	for _, g := range grants {
		if strings.Split(g, ":")[0] == name {
			return true
		}
	}
	return false
}

// policyRequiresBody reports whether any compiled file/pack policy in policyYAML
// references the request body (params.body). Each policy is compiled under its
// registration scope (the map key, which is the scope gatekeeper evaluates
// against at runtime); keeplib.Engine.RequiresBody fails safe (returns true) on
// an unknown scope. Inline deny-list policies are excluded by construction —
// they match the operation path only and cannot reference params.body, so they
// are never present in policyYAML.
func policyRequiresBody(policyYAML map[string][]byte) bool {
	for scope, yamlBytes := range policyYAML {
		// Wrap in a closure so eng.Close() runs via defer even if RequiresBody
		// panics — the per-iteration engine is always released.
		requires := func() bool {
			eng, err := keeplib.LoadFromBytes(yamlBytes)
			if err != nil {
				// Unexpected: ValidateRuleBytes already passed on these bytes
				// upstream. Fail safe — treat an uncompilable policy as requiring
				// the body capability rather than silently bypassing the gate.
				// (The daemon rejects the same bytes at registration, so this
				// never relaxes enforcement.)
				log.Warn("keep: policy failed to compile during body-capability detection; assuming body inspection required",
					"scope", scope, "error", err)
				return true
			}
			defer eng.Close()
			return eng.RequiresBody(scope)
		}()
		if requires {
			return true
		}
	}
	return false
}

// checkKeepPolicyCapabilities verifies the running daemon advertises the
// capabilities required to enforce this run's Keep policies. requiresBody is
// true when any policy references params.body (see policyRequiresBody). An older
// daemon that lacks a capability would silently under-enforce, so this fails
// fast with an actionable upgrade message instead of registering and misbehaving.
func checkKeepPolicyCapabilities(daemonCapabilities []string, requiresBody bool) error {
	if !slices.Contains(daemonCapabilities, daemon.CapKeepPolicy) {
		return fmt.Errorf("proxy daemon does not support Keep policies (missing 'keep-policy' capability); run 'moat proxy restart' to upgrade")
	}
	if requiresBody && !slices.Contains(daemonCapabilities, daemon.CapKeepBodyPolicy) {
		return fmt.Errorf("proxy daemon does not support request-body Keep policies (missing 'keep-body-policy' capability); run 'moat proxy restart' to upgrade")
	}
	return nil
}
