package run

import (
	"sync/atomic"
	"time"

	"github.com/andybons/moat/internal/audit"
	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/id"
	"github.com/andybons/moat/internal/proxy"
	"github.com/andybons/moat/internal/snapshot"
	"github.com/andybons/moat/internal/sshagent"
	"github.com/andybons/moat/internal/storage"
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
	ID             string
	Name           string // Human-friendly name (e.g., "myapp" or "fluffy-chicken")
	Workspace      string
	Grants         []string
	Ports          map[string]int // service name -> container port
	HostPorts      map[string]int // service name -> host port (after binding)
	State          State
	ContainerID    string
	ProxyServer    *proxy.Server     // Auth proxy for credential injection
	SSHAgentServer *sshagent.Server  // SSH agent proxy for SSH key access
	Store          *storage.RunStore // Run data storage
	storeRef       *atomic.Value     // Atomic reference for concurrent logger access
	AuditStore     *audit.Store      // Tamper-proof audit log
	SnapEngine     *snapshot.Engine  // Snapshot engine for workspace protection
	KeepContainer  bool              // If true, don't auto-remove container after run
	Interactive    bool              // If true, run was started in interactive mode
	CreatedAt      time.Time
	StartedAt      time.Time
	StoppedAt      time.Time
	Error          string

	// Firewall settings (set when network.policy is strict)
	FirewallEnabled bool
	ProxyHost       string // Host address for proxy (for firewall rules)
	ProxyPort       int    // Port number for proxy (for firewall rules)

	// ProviderCleanupPaths tracks paths to clean up for each provider when the run ends.
	// Keys are provider names, values are cleanup paths returned by ProviderSetup.ContainerMounts.
	ProviderCleanupPaths map[string]string

	// Snapshot settings
	DisablePreRunSnapshot bool // If true, skip pre-run snapshot creation

	// AWS credential provider (set when using aws grant)
	AWSCredentialProvider *proxy.AWSCredentialProvider

	// awsTempDir is the temp directory for AWS credential helper (cleaned up on destroy)
	awsTempDir string
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
	return r.Store.SaveMetadata(storage.Metadata{
		Name:        r.Name,
		Workspace:   r.Workspace,
		Grants:      r.Grants,
		Ports:       r.Ports,
		ContainerID: r.ContainerID,
		State:       string(r.State),
		Interactive: r.Interactive,
		CreatedAt:   r.CreatedAt,
		StartedAt:   r.StartedAt,
		StoppedAt:   r.StoppedAt,
		Error:       r.Error,
	})
}
