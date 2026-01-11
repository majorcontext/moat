package run

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/andybons/agentops/internal/credential"
	"github.com/andybons/agentops/internal/docker"
	"github.com/andybons/agentops/internal/image"
	"github.com/andybons/agentops/internal/proxy"
	"github.com/andybons/agentops/internal/storage"
)

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

	// Start proxy server for this run if grants are specified
	var proxyServer *proxy.Server
	var proxyEnv []string
	var caCertPath string
	var mounts []docker.MountConfig

	// Always mount workspace
	mounts = append(mounts, docker.MountConfig{
		Source:   opts.Workspace,
		Target:   "/workspace",
		ReadOnly: false,
	})

	if len(opts.Grants) > 0 {
		p := proxy.NewProxy()

		// Create CA for TLS interception
		caDir := filepath.Join(credential.DefaultStoreDir(), "ca")
		ca, err := proxy.NewCA(caDir)
		if err != nil {
			return nil, fmt.Errorf("creating CA: %w", err)
		}
		p.SetCA(ca)

		// Write CA cert to temp file for mounting in container
		caCertPath = filepath.Join(caDir, "ca.crt")

		// Load credentials for granted providers
		store, err := credential.NewFileStore(
			credential.DefaultStoreDir(),
			credential.DefaultEncryptionKey(),
		)
		if err == nil {
			for _, grant := range opts.Grants {
				provider := credential.Provider(strings.Split(grant, ":")[0])
				if cred, err := store.Get(provider); err == nil {
					// Map provider to host
					switch provider {
					case credential.ProviderGitHub:
						p.SetCredential("api.github.com", "Bearer "+cred.Token)
						p.SetCredential("github.com", "Bearer "+cred.Token)
					}
				}
			}
		}

		proxyServer = proxy.NewServer(p)

		// Set up request logging if storage is available
		// Note: r.Store will be created later, so we need to capture the pointer
		p.SetLogger(func(method, url string, statusCode int, duration time.Duration, reqErr error) {
			if r.Store == nil {
				return
			}
			errStr := ""
			if reqErr != nil {
				errStr = reqErr.Error()
			}
			r.Store.WriteNetworkRequest(storage.NetworkRequest{
				Timestamp:  time.Now().UTC(),
				Method:     method,
				URL:        url,
				StatusCode: statusCode,
				Duration:   duration.Milliseconds(),
				Error:      errStr,
			})
		})

		if err := proxyServer.Start(); err != nil {
			return nil, fmt.Errorf("starting proxy: %w", err)
		}

		// Use host.docker.internal to allow container to reach host's proxy
		proxyHost := "host.docker.internal:" + proxyServer.Port()
		proxyEnv = []string{
			"HTTP_PROXY=http://" + proxyHost,
			"HTTPS_PROXY=http://" + proxyHost,
			"http_proxy=http://" + proxyHost,
			"https_proxy=http://" + proxyHost,
		}

		// Mount CA cert for container to trust
		mounts = append(mounts, docker.MountConfig{
			Source:   caCertPath,
			Target:   "/etc/ssl/certs/agentops-ca.pem",
			ReadOnly: true,
		})

		// Set env vars for tools that support custom CA bundles
		// SSL_CERT_FILE is used by many tools (curl, wget, etc)
		proxyEnv = append(proxyEnv, "SSL_CERT_FILE=/etc/ssl/certs/agentops-ca.pem")
		proxyEnv = append(proxyEnv, "REQUESTS_CA_BUNDLE=/etc/ssl/certs/agentops-ca.pem")
		proxyEnv = append(proxyEnv, "NODE_EXTRA_CA_CERTS=/etc/ssl/certs/agentops-ca.pem")
	}

	// Configure extra hosts to enable host.docker.internal resolution
	// This works on Docker 20.10+ with host-gateway
	var extraHosts []string
	if proxyServer != nil {
		extraHosts = []string{"host.docker.internal:host-gateway"}
	}

	// Add config env vars
	if opts.Config != nil {
		for k, v := range opts.Config.Env {
			proxyEnv = append(proxyEnv, k+"="+v)
		}
	}

	// Add explicit env vars (highest priority - can override config)
	proxyEnv = append(proxyEnv, opts.Env...)

	// Create Docker container
	containerID, err := m.docker.CreateContainer(ctx, docker.ContainerConfig{
		Name:       r.ID,
		Image:      image.Resolve(opts.Config),
		Cmd:        cmd,
		WorkingDir: "/workspace",
		Env:        proxyEnv,
		ExtraHosts: extraHosts,
		Mounts:     mounts,
	})
	if err != nil {
		// Clean up proxy server if container creation fails
		if proxyServer != nil {
			proxyServer.Stop(context.Background())
		}
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID
	r.ProxyServer = proxyServer

	// Create run storage
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		// Clean up container and proxy if storage creation fails
		m.docker.RemoveContainer(ctx, containerID)
		if proxyServer != nil {
			proxyServer.Stop(context.Background())
		}
		return nil, fmt.Errorf("creating run storage: %w", err)
	}
	r.Store = store

	// Save initial metadata
	store.SaveMetadata(storage.Metadata{
		Agent:     opts.Agent,
		Workspace: opts.Workspace,
		Grants:    opts.Grants,
		CreatedAt: r.CreatedAt,
	})

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

// streamLogs streams container logs to stdout and storage.
func (m *Manager) streamLogs(ctx context.Context, r *Run) {
	logs, err := m.docker.ContainerLogs(ctx, r.ContainerID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting logs: %v\n", err)
		return
	}
	defer logs.Close()

	// Write to both stdout and storage
	var dest io.Writer = os.Stdout
	if r.Store != nil {
		if lw, err := r.Store.LogWriter(); err == nil {
			dest = io.MultiWriter(os.Stdout, lw)
			defer lw.Close()
		}
	}
	io.Copy(dest, logs)
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

	// Stop the proxy server if one was created
	if r.ProxyServer != nil {
		if err := r.ProxyServer.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
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
		// Stop the proxy server if one was created
		if r.ProxyServer != nil {
			if stopErr := r.ProxyServer.Stop(context.Background()); stopErr != nil {
				fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", stopErr)
			}
		}

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

	// Stop the proxy server if one was created and still running
	if r.ProxyServer != nil {
		if err := r.ProxyServer.Stop(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: stopping proxy: %v\n", err)
		}
	}

	m.mu.Lock()
	delete(m.runs, runID)
	m.mu.Unlock()

	return nil
}

// Close releases manager resources.
func (m *Manager) Close() error {
	// Stop all proxy servers
	m.mu.RLock()
	for _, r := range m.runs {
		if r.ProxyServer != nil {
			r.ProxyServer.Stop(context.Background())
		}
	}
	m.mu.RUnlock()

	return m.docker.Close()
}
