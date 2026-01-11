package run

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/proxy"
	"github.com/andybons/agentops/internal/storage"
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
	ID          string
	Agent       string
	Workspace   string
	Grants      []string
	State       State
	ContainerID string
	ProxyServer *proxy.Server   // Auth proxy for credential injection
	Store       *storage.RunStore // Run data storage
	CreatedAt   time.Time
	StartedAt   time.Time
	StoppedAt   time.Time
	Error       string
}

// Options configures a new run.
type Options struct {
	Agent     string
	Workspace string
	Grants    []string
	Cmd       []string         // Command to run (default: /bin/bash)
	Config    *config.Config   // Optional agent.yaml config
	Env       []string         // Additional environment variables (KEY=VALUE)
}

// generateID creates a unique run identifier.
func generateID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "run-" + hex.EncodeToString(b)
}
