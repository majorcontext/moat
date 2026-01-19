package run

import (
	"crypto/rand"
	"encoding/hex"
	"sync/atomic"
	"time"

	"github.com/andybons/moat/internal/audit"
	"github.com/andybons/moat/internal/config"
	"github.com/andybons/moat/internal/proxy"
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
	ID            string
	Name          string // Human-friendly name (e.g., "myapp" or "fluffy-chicken")
	Workspace     string
	Grants        []string
	Ports         map[string]int // service name -> container port
	HostPorts     map[string]int // service name -> host port (after binding)
	State         State
	ContainerID   string
	ProxyServer   *proxy.Server     // Auth proxy for credential injection
	Store         *storage.RunStore // Run data storage
	storeRef      *atomic.Value     // Atomic reference for concurrent logger access
	AuditStore    *audit.Store      // Tamper-proof audit log
	KeepContainer bool              // If true, don't auto-remove container after run
	CreatedAt     time.Time
	StartedAt     time.Time
	StoppedAt     time.Time
	Error         string

	// Firewall settings (set when network.policy is strict)
	FirewallEnabled bool
	ProxyHost       string // Host address for proxy (for firewall rules)
	ProxyPort       int    // Port number for proxy (for firewall rules)
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
}

// generateID creates a unique run identifier.
func generateID() string {
	b := make([]byte, 6)
	if _, err := rand.Read(b); err != nil {
		// Fallback to timestamp-based ID if crypto/rand fails (extremely unlikely)
		return "run-" + hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return "run-" + hex.EncodeToString(b)
}
