package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/andybons/agentops/internal/docker"
)

// Default container image for agents
const defaultImage = "ubuntu:22.04"

// Manager handles run lifecycle operations.
type Manager struct {
	docker *docker.Client
	runs   map[string]*Run
	mu     sync.RWMutex
}

// NewManager creates a new run manager.
func NewManager() (*Manager, error) {
	dockerClient, err := docker.NewClient()
	if err != nil {
		return nil, fmt.Errorf("initializing docker: %w", err)
	}

	// Verify Docker is accessible
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := dockerClient.Ping(ctx); err != nil {
		return nil, err
	}

	return &Manager{
		docker: dockerClient,
		runs:   make(map[string]*Run),
	}, nil
}

// Create initializes a new run without starting it.
func (m *Manager) Create(ctx context.Context, opts Options) (*Run, error) {
	r := &Run{
		ID:        generateID(),
		Agent:     opts.Agent,
		Workspace: opts.Workspace,
		Grants:    opts.Grants,
		State:     StateCreated,
		CreatedAt: time.Now(),
	}

	// Default command
	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = []string{"/bin/bash"}
	}

	// Create Docker container
	containerID, err := m.docker.CreateContainer(ctx, docker.ContainerConfig{
		Name:       r.ID,
		Image:      defaultImage,
		Cmd:        cmd,
		WorkingDir: "/workspace",
		Mounts: []docker.MountConfig{
			{
				Source:   opts.Workspace,
				Target:   "/workspace",
				ReadOnly: false,
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID

	m.mu.Lock()
	m.runs[r.ID] = r
	m.mu.Unlock()

	return r, nil
}

// Start begins execution of a run.
func (m *Manager) Start(ctx context.Context, runID string) error {
	m.mu.Lock()
	r, ok := m.runs[runID]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("run %s not found", runID)
	}
	r.State = StateStarting
	m.mu.Unlock()

	if err := m.docker.StartContainer(ctx, r.ContainerID); err != nil {
		m.mu.Lock()
		r.State = StateFailed
		r.Error = err.Error()
		m.mu.Unlock()
		return err
	}

	m.mu.Lock()
	r.State = StateRunning
	r.StartedAt = time.Now()
	m.mu.Unlock()

	// Stream logs to stdout
	go m.streamLogs(context.Background(), r)

	return nil
}

// streamLogs streams container logs to stdout.
func (m *Manager) streamLogs(ctx context.Context, r *Run) {
	logs, err := m.docker.ContainerLogs(ctx, r.ContainerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting logs: %v\n", err)
		return
	}
	defer logs.Close()
	io.Copy(os.Stdout, logs)
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

	if err := m.docker.StopContainer(ctx, r.ContainerID); err != nil {
		// Log but don't fail - container might already be stopped
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	m.mu.Lock()
	r.State = StateStopped
	r.StoppedAt = time.Now()
	m.mu.Unlock()

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
		exitCode, err := m.docker.WaitContainer(ctx, containerID)
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
		m.mu.Lock()
		r.State = StateStopped
		r.StoppedAt = time.Now()
		if err != nil {
			r.Error = err.Error()
		}
		m.mu.Unlock()
		return err
	case <-ctx.Done():
		return m.Stop(context.Background(), runID)
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
	if err := m.docker.RemoveContainer(ctx, r.ContainerID); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: %v\n", err)
	}

	m.mu.Lock()
	delete(m.runs, runID)
	m.mu.Unlock()

	return nil
}

// Close releases manager resources.
func (m *Manager) Close() error {
	return m.docker.Close()
}
