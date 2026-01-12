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

	"github.com/andybons/agentops/internal/config"
	"github.com/andybons/agentops/internal/container"
	"github.com/andybons/agentops/internal/credential"
	"github.com/andybons/agentops/internal/image"
	"github.com/andybons/agentops/internal/proxy"
	"github.com/andybons/agentops/internal/storage"
)

// Manager handles run lifecycle operations.
type Manager struct {
	runtime container.Runtime
	runs    map[string]*Run
	mu      sync.RWMutex
}

// NewManager creates a new run manager.
func NewManager() (*Manager, error) {
	rt, err := container.NewRuntime()
	if err != nil {
		return nil, fmt.Errorf("initializing container runtime: %w", err)
	}

	return &Manager{
		runtime: rt,
		runs:    make(map[string]*Run),
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

	if len(opts.Grants) > 0 {
		p := proxy.NewProxy()

		// Create CA for TLS interception
		caDir := filepath.Join(credential.DefaultStoreDir(), "ca")
		ca, err := proxy.NewCA(caDir)
		if err != nil {
			return nil, fmt.Errorf("creating CA: %w", err)
		}
		p.SetCA(ca)

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
			// Best-effort logging; errors are non-fatal
			_ = r.Store.WriteNetworkRequest(storage.NetworkRequest{
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

		// Determine proxy host based on runtime's host address
		proxyHost := m.runtime.GetHostAddress() + ":" + proxyServer.Port()

		proxyEnv = []string{
			"HTTP_PROXY=http://" + proxyHost,
			"HTTPS_PROXY=http://" + proxyHost,
			"http_proxy=http://" + proxyHost,
			"https_proxy=http://" + proxyHost,
		}

		// Mount CA directory for container to trust
		// We mount the directory (not just the file) because Apple container
		// only supports directory mounts, not individual file mounts.
		mounts = append(mounts, container.MountConfig{
			Source:   caDir,
			Target:   "/etc/ssl/certs/agentops-ca",
			ReadOnly: true,
		})

		// Set env vars for tools that support custom CA bundles
		// SSL_CERT_FILE is used by many tools (curl, wget, etc)
		// The CA cert is at ca.crt within the mounted directory
		caCertInContainer := "/etc/ssl/certs/agentops-ca/ca.crt"
		proxyEnv = append(proxyEnv, "SSL_CERT_FILE="+caCertInContainer)
		proxyEnv = append(proxyEnv, "REQUESTS_CA_BUNDLE="+caCertInContainer)
		proxyEnv = append(proxyEnv, "NODE_EXTRA_CA_CERTS="+caCertInContainer)
	}

	// Configure network mode and extra hosts based on runtime capabilities
	var networkMode string
	var extraHosts []string
	if proxyServer != nil {
		if m.runtime.SupportsHostNetwork() {
			// Docker on Linux: use host network so container can reach 127.0.0.1
			networkMode = "host"
		} else {
			// Docker on macOS/Windows or Apple container: use bridge
			networkMode = "bridge"
			// Only Docker needs the extra host mapping
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

	// Add explicit env vars (highest priority - can override config)
	proxyEnv = append(proxyEnv, opts.Env...)

	// Create container
	containerID, err := m.runtime.CreateContainer(ctx, container.Config{
		Name:        r.ID,
		Image:       image.Resolve(opts.Config),
		Cmd:         cmd,
		WorkingDir:  "/workspace",
		Env:         proxyEnv,
		ExtraHosts:  extraHosts,
		NetworkMode: networkMode,
		Mounts:      mounts,
	})
	if err != nil {
		// Clean up proxy server if container creation fails
		if proxyServer != nil {
			_ = proxyServer.Stop(context.Background())
		}
		return nil, fmt.Errorf("creating container: %w", err)
	}

	r.ContainerID = containerID
	r.ProxyServer = proxyServer

	// Create run storage
	store, err := storage.NewRunStore(storage.DefaultBaseDir(), r.ID)
	if err != nil {
		// Clean up container and proxy if storage creation fails
		_ = m.runtime.RemoveContainer(ctx, containerID)
		if proxyServer != nil {
			_ = proxyServer.Stop(context.Background())
		}
		return nil, fmt.Errorf("creating run storage: %w", err)
	}
	r.Store = store

	// Save initial metadata (best-effort; non-fatal if it fails)
	_ = store.SaveMetadata(storage.Metadata{
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

	if err := m.runtime.StartContainer(ctx, r.ContainerID); err != nil {
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
	logs, err := m.runtime.ContainerLogs(ctx, r.ContainerID)
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
	_, _ = io.Copy(dest, logs)
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
	if err := m.runtime.RemoveContainer(ctx, r.ContainerID); err != nil {
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
			_ = r.ProxyServer.Stop(context.Background())
		}
	}
	m.mu.RUnlock()

	return m.runtime.Close()
}
