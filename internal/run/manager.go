package run

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/andybons/agentops/internal/deps"
	"github.com/andybons/agentops/internal/image"
	"github.com/andybons/agentops/internal/name"
	"github.com/andybons/agentops/internal/proxy"
	"github.com/andybons/agentops/internal/routing"
	"github.com/andybons/agentops/internal/storage"
)

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

	return &Manager{
		runtime:        rt,
		runs:           make(map[string]*Run),
		routes:         lifecycle.Routes(),
		proxyLifecycle: lifecycle,
	}, nil
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
		ID:        generateID(),
		Name:      agentName,
		Workspace: opts.Workspace,
		Grants:    opts.Grants,
		Ports:     ports,
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
					case credential.ProviderAnthropic:
						// Anthropic uses x-api-key header, not Authorization
						p.SetCredentialHeader("api.anthropic.com", "x-api-key", cred.Token)
						// Set a dummy ANTHROPIC_API_KEY so Claude Code doesn't error
						// The real key is injected by the proxy at the network layer
						providerEnv = append(providerEnv, "ANTHROPIC_API_KEY=agentops-proxy-injected")
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

		// Set up request logging if storage is available.
		// r.Store is captured by pointer and created later.
		p.SetLogger(func(data proxy.RequestLogData) {
			if r.Store == nil {
				return
			}
			var errStr string
			if data.Err != nil {
				errStr = data.Err.Error()
			}
			// Best-effort logging; errors are non-fatal
			_ = r.Store.WriteNetworkRequest(storage.NetworkRequest{
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
				BodyTruncated:   len(data.RequestBody) >= proxy.MaxBodySize || len(data.ResponseBody) >= proxy.MaxBodySize,
			})
		})

		if err := proxyServer.Start(); err != nil {
			return nil, fmt.Errorf("starting proxy: %w", err)
		}

		// Determine proxy URL based on runtime's host address
		// Include authentication credentials in URL when token is set (Apple containers)
		proxyHost := m.runtime.GetHostAddress() + ":" + proxyServer.Port()
		var proxyURL string
		if proxyAuthToken != "" {
			// Include auth credentials in URL: http://agentops:token@host:port
			proxyURL = "http://agentops:" + proxyAuthToken + "@" + proxyHost
		} else {
			proxyURL = "http://" + proxyHost
		}

		proxyEnv = []string{
			"HTTP_PROXY=" + proxyURL,
			"HTTPS_PROXY=" + proxyURL,
			"http_proxy=" + proxyURL,
			"https_proxy=" + proxyURL,
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

		// Add provider-specific env vars (collected during credential loading)
		proxyEnv = append(proxyEnv, providerEnv...)
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

	// Build AGENTOPS_* environment variables for host injection
	if len(ports) > 0 {
		globalCfg, _ := config.LoadGlobal()
		proxyPort := globalCfg.Proxy.Port

		baseHost := fmt.Sprintf("%s.localhost:%d", agentName, proxyPort)
		proxyEnv = append(proxyEnv, "AGENTOPS_HOST="+baseHost)
		proxyEnv = append(proxyEnv, "AGENTOPS_URL=http://"+baseHost)

		for serviceName := range ports {
			upperName := strings.ToUpper(serviceName)
			serviceHost := fmt.Sprintf("%s.%s.localhost:%d", serviceName, agentName, proxyPort)
			proxyEnv = append(proxyEnv, fmt.Sprintf("AGENTOPS_HOST_%s=%s", upperName, serviceHost))
			proxyEnv = append(proxyEnv, fmt.Sprintf("AGENTOPS_URL_%s=http://%s", upperName, serviceHost))
		}
	}

	// Parse and validate dependencies
	var depList []deps.Dependency
	if opts.Config != nil && len(opts.Config.Dependencies) > 0 {
		var err error
		depList, err = deps.ParseAll(opts.Config.Dependencies)
		if err != nil {
			// Clean up proxy server if parsing fails
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, fmt.Errorf("parsing dependencies: %w", err)
		}
		if err := deps.Validate(depList); err != nil {
			// Clean up proxy server if validation fails
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, fmt.Errorf("validating dependencies: %w", err)
		}
	}

	// Resolve container image based on dependencies
	containerImage := image.Resolve(depList)

	// Build image if we have dependencies (Docker only)
	// Apple containers use install scripts instead
	var installScript string
	if len(depList) > 0 {
		if m.runtime.Type() == container.RuntimeApple {
			// For Apple containers, generate install script to run at container start
			script, err := deps.GenerateInstallScript(depList)
			if err != nil {
				if proxyServer != nil {
					_ = proxyServer.Stop(context.Background())
				}
				return nil, fmt.Errorf("generating install script: %w", err)
			}
			installScript = script
		} else {
			// For Docker, build a custom image with dependencies pre-installed
			exists, err := m.runtime.ImageExists(ctx, containerImage)
			if err != nil {
				if proxyServer != nil {
					_ = proxyServer.Stop(context.Background())
				}
				return nil, fmt.Errorf("checking image: %w", err)
			}

			if !exists {
				dockerfile, err := deps.GenerateDockerfile(depList)
				if err != nil {
					if proxyServer != nil {
						_ = proxyServer.Stop(context.Background())
					}
					return nil, fmt.Errorf("generating Dockerfile: %w", err)
				}

				depNames := make([]string, len(depList))
				for i, d := range depList {
					depNames[i] = d.Name
				}
				if err := m.runtime.BuildImage(ctx, dockerfile, containerImage); err != nil {
					if proxyServer != nil {
						_ = proxyServer.Stop(context.Background())
					}
					return nil, fmt.Errorf("building image with dependencies [%s]: %w",
						strings.Join(depNames, ", "), err)
				}
			}
		}
	}
	_ = installScript // TODO: pass to container config when Apple container support is complete

	// Create container
	containerID, err := m.runtime.CreateContainer(ctx, container.Config{
		Name:         r.ID,
		Image:        containerImage,
		Cmd:          cmd,
		WorkingDir:   "/workspace",
		Env:          proxyEnv,
		ExtraHosts:   extraHosts,
		NetworkMode:  networkMode,
		Mounts:       mounts,
		PortBindings: portBindings,
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

	// Ensure proxy is running if we have ports to expose
	if len(ports) > 0 {
		// Enable TLS on the routing proxy
		if _, tlsErr := m.proxyLifecycle.EnableTLS(); tlsErr != nil {
			// Clean up container
			_ = m.runtime.RemoveContainer(ctx, containerID)
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, fmt.Errorf("enabling TLS on routing proxy: %w", tlsErr)
		}
		if proxyErr := m.proxyLifecycle.EnsureRunning(); proxyErr != nil {
			// Clean up container
			_ = m.runtime.RemoveContainer(ctx, containerID)
			if proxyServer != nil {
				_ = proxyServer.Stop(context.Background())
			}
			return nil, fmt.Errorf("starting routing proxy: %w", proxyErr)
		}
	}

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
		Name:      r.Name,
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

	// Stream logs to stdout
	go m.streamLogs(context.Background(), r)

	return nil
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

	// Unregister routes for this agent
	if r.Name != "" {
		_ = m.routes.Remove(r.Name)
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
