package run

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/id"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/majorcontext/moat/internal/snapshot"
	"github.com/majorcontext/moat/internal/sshagent"
	"github.com/majorcontext/moat/internal/storage"
)

// State represents the current state of a run.
type State string

const (
	StateCreated  State = "created"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

// Run represents an agent execution environment.
type Run struct {
	ID        string
	Name      string // Human-friendly name (e.g., "myapp" or "fluffy-chicken")
	Workspace string
	// Worktree tracking (set when created via moat wt or --wt flag)
	WorktreeBranch    string
	WorktreePath      string
	WorktreeRepoID    string
	Grants            []string
	Agent             string            // Agent type from config (e.g., "claude-code", "codex")
	Image             string            // Container image used for this run
	ProviderMeta      map[string]string // Provider-specific metadata (e.g., claude_session_id)
	Ports             map[string]int    // endpoint name -> container port
	HostPorts         map[string]int    // endpoint name -> host port (after binding)
	State             State
	ContainerID       string
	SSHAgentServer    *sshagent.Server  // SSH agent proxy for SSH key access
	Store             *storage.RunStore // Run data storage
	logsCaptured      atomic.Bool       // Track if logs have been captured (for idempotency)
	providerHooksDone atomic.Bool       // Track if provider stopped hooks have run (for idempotency)
	exitCh            chan struct{}     // Closed when container exits (signaled by monitorContainerExit)
	AuditStore        *audit.Store      // Tamper-proof audit log
	SnapEngine        *snapshot.Engine  // Snapshot engine for workspace protection
	KeepContainer     bool              // If true, don't auto-remove container after run
	Interactive       bool              // If true, run was started in interactive mode
	CreatedAt         time.Time
	StartedAt         time.Time
	StoppedAt         time.Time
	Error             string

	// Shutdown coordination to prevent race conditions
	sshAgentStopOnce sync.Once // Ensures SSHAgentServer.Stop() called only once

	// State protection - guards State, Error, StartedAt, StoppedAt fields
	// Use this lock when reading or modifying these fields to prevent races
	// between monitorContainerExit goroutine and user-facing methods
	stateMu sync.Mutex

	// Firewall settings (set when network.policy is strict)
	FirewallEnabled bool
	ProxyHost       string // Host address for proxy (for firewall rules)
	ProxyPort       int    // Port number for proxy (for firewall rules)
	ProxyAuthToken  string // Auth token for proxy daemon (set when run is registered with daemon)

	// ProviderCleanupPaths tracks paths to clean up for each provider when the run ends.
	// Keys are provider names, values are cleanup paths returned by ProviderSetup.ContainerMounts.
	ProviderCleanupPaths map[string]string

	// Snapshot settings
	DisablePreRunSnapshot bool // If true, skip pre-run snapshot creation

	// AWS credential provider (set when using aws grant)
	AWSCredentialProvider *proxy.AWSCredentialProvider

	// awsTempDir is the temp directory for AWS credential helper (cleaned up on destroy)
	awsTempDir string

	// ClaudeConfigTempDir is the temporary directory containing Claude configuration files
	// (settings.json, .mcp.json) that are mounted into the container. This should be
	// cleaned up when the run is stopped or destroyed.
	ClaudeConfigTempDir string

	// CodexConfigTempDir is the temporary directory containing Codex configuration files
	// (config.toml, auth.json) that are mounted into the container. This should be
	// cleaned up when the run is stopped or destroyed.
	CodexConfigTempDir string

	// GeminiConfigTempDir is the temporary directory containing Gemini configuration files
	// (settings.json, oauth_creds.json) that are mounted into the container. This should be
	// cleaned up when the run is stopped or destroyed.
	GeminiConfigTempDir string

	// BuildKit sidecar fields (docker:dind only)
	BuildkitContainerID string
	NetworkID           string

	// ServiceContainers maps service name to container ID (e.g., "postgres" -> "abc123").
	ServiceContainers map[string]string
}

// Options configures a new run.
type Options struct {
	Name          string // Optional explicit name (--name flag or from config)
	Workspace     string
	Grants        []string
	Cmd           []string       // Command to run (default: /bin/bash)
	Config        *config.Config // Optional agent.yaml config
	Env           []string       // Additional environment variables (KEY=VALUE)
	Rebuild       bool           // Force rebuild of container image (ignores cache)
	KeepContainer bool           // If true, don't auto-remove container after run
	Interactive   bool           // Keep stdin open for interactive input
	TTY           bool           // Allocate a pseudo-TTY
}

// generateID creates a unique run identifier.
func generateID() string {
	return id.Generate("run")
}

// SaveMetadata persists the run's current state to disk.
// This should be called after any state change.
func (r *Run) SaveMetadata() error {
	if r.Store == nil {
		return nil // No store configured
	}

	// Snapshot stateMu-protected fields under the lock to avoid races
	// between Stop() and monitorContainerExit() calling SaveMetadata concurrently.
	r.stateMu.Lock()
	state := r.State
	startedAt := r.StartedAt
	stoppedAt := r.StoppedAt
	errMsg := r.Error
	r.stateMu.Unlock()

	return r.Store.SaveMetadata(storage.Metadata{
		Name:                r.Name,
		Workspace:           r.Workspace,
		Grants:              r.Grants,
		Agent:               r.Agent,
		Image:               r.Image,
		Ports:               r.Ports,
		ContainerID:         r.ContainerID,
		State:               string(state),
		Interactive:         r.Interactive,
		CreatedAt:           r.CreatedAt,
		StartedAt:           startedAt,
		StoppedAt:           stoppedAt,
		Error:               errMsg,
		ProviderMeta:        r.ProviderMeta,
		WorktreeBranch:      r.WorktreeBranch,
		WorktreePath:        r.WorktreePath,
		WorktreeRepoID:      r.WorktreeRepoID,
		BuildkitContainerID: r.BuildkitContainerID,
		NetworkID:           r.NetworkID,
		ServiceContainers:   r.ServiceContainers,
	})
}

// stopProxyServer is a no-op. The proxy is managed by the daemon process
// and token refresh is handled by the daemon. Daemon run unregistration
// is handled separately by the Manager.
//
//nolint:unparam // error return kept for interface consistency with callers
func (r *Run) stopProxyServer(_ context.Context) error {
	return nil
}

// stopSSHAgentServer safely stops the SSH agent server exactly once.
// This method is safe to call concurrently from multiple goroutines.
func (r *Run) stopSSHAgentServer() error {
	var stopErr error
	r.sshAgentStopOnce.Do(func() {
		if r.SSHAgentServer != nil {
			stopErr = r.SSHAgentServer.Stop()
			r.SSHAgentServer = nil
		}
	})
	return stopErr
}

// GetState safely reads the run state (thread-safe).
func (r *Run) GetState() State {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	return r.State
}

// SetState safely updates the run state (thread-safe).
func (r *Run) SetState(state State) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.State = state
}

// SetStateWithError safely updates the run state and error (thread-safe).
func (r *Run) SetStateWithError(state State, err string) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.State = state
	r.Error = err
}

// SetStateWithTime safely updates the run state and timestamp (thread-safe).
func (r *Run) SetStateWithTime(state State, timestamp time.Time) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.State = state
	if state == StateRunning {
		r.StartedAt = timestamp
	} else if state == StateStopped || state == StateFailed {
		r.StoppedAt = timestamp
	}
}

// SetStateFailedAt atomically sets state to StateFailed with both error and
// timestamp in a single lock acquisition. This prevents a concurrent reader
// from observing StateFailed with no StoppedAt set.
func (r *Run) SetStateFailedAt(errMsg string, timestamp time.Time) {
	r.stateMu.Lock()
	defer r.stateMu.Unlock()
	r.State = StateFailed
	r.Error = errMsg
	r.StoppedAt = timestamp
}

// validateGrants checks that all requested grants have credentials available.
// Returns an error with actionable fix commands if any are missing.
//
// Some grant types are validated by their own specialized code paths and are
// skipped here:
//   - "ssh" / "ssh:<host>" — validated by the SSH agent setup in Create()
//   - "mcp-*" — validated by validateMCPGrants
//
// For all other grants, we check that (1) the provider is registered and
// (2) the credential exists and can be decrypted from the store.
func validateGrants(grants []string, store *credential.FileStore) error {
	var errs []string
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]

		// Skip grants validated by dedicated code paths
		if grantName == "ssh" || strings.HasPrefix(grantName, "mcp-") {
			continue
		}

		// Check provider exists in registry (catches typos)
		if provider.Get(grantName) == nil {
			errs = append(errs, fmt.Sprintf("  - %s: unknown provider (available: %s)",
				grantName, strings.Join(provider.Names(), ", ")))
			continue
		}

		// Resolve to canonical provider name for credential lookup.
		credName := credential.Provider(provider.ResolveName(grantName))

		// Check credential exists and can be decrypted
		_, err := store.Get(credName)
		if err != nil {
			errMsg := err.Error()
			switch {
			case strings.Contains(errMsg, "credential not found"):
				errs = append(errs, fmt.Sprintf("  - %s: not configured\n    Run: moat grant %s", grantName, grantName))
			case strings.Contains(errMsg, "decrypting credential"):
				errs = append(errs, fmt.Sprintf("  - %s: encryption key changed\n    Run: moat grant %s", grantName, grantName))
			default:
				errs = append(errs, fmt.Sprintf("  - %s: %v\n    Run: moat grant %s", grantName, err, grantName))
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("missing grants:\n%s\n\nConfigure the grants above, then run again.",
			strings.Join(errs, "\n"))
	}
	return nil
}

// validateMCPGrants checks that all required MCP grants exist.
func validateMCPGrants(cfg *config.Config, store *credential.FileStore) error {
	for _, mcp := range cfg.MCP {
		if mcp.Auth == nil {
			continue // No auth required
		}

		_, err := store.Get(credential.Provider(mcp.Auth.Grant))
		if err != nil {
			return fmt.Errorf(`MCP server '%s' requires grant '%s' but it's not configured

To fix:
  moat grant mcp %s

Then run again.`, mcp.Name, mcp.Auth.Grant, strings.TrimPrefix(mcp.Auth.Grant, "mcp-"))
		}
	}
	return nil
}
