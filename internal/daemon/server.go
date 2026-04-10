package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	keeplib "github.com/majorcontext/keep"

	"github.com/majorcontext/moat/internal/log"
	awsprov "github.com/majorcontext/moat/internal/providers/aws"
	gcloudprov "github.com/majorcontext/moat/internal/providers/gcloud"
	"github.com/majorcontext/moat/internal/routing"
)

// BuildCommit is the git commit hash of the running binary. Set by the CLI
// at startup so the health endpoint can report the daemon's version. This
// allows diagnosing version skew between daemon and CLI.
var BuildCommit string

// Server is the daemon's HTTP API server over a Unix socket.
type Server struct {
	sockPath     string
	proxyPort    int
	registry     *Registry
	routes       *routing.RouteTable
	server       *http.Server
	listener     net.Listener
	startedAt    time.Time
	persister    *RunPersister
	onRegister   func()             // called when a new run is registered
	onEmpty      func()             // called when last run is unregistered
	onUnregister func(runID string) // called when a run is unregistered (for resource cleanup)
	onShutdown   func()             // called when shutdown is requested via API
}

// NewServer creates a daemon API server that will listen on the given Unix socket path.
func NewServer(sockPath string, proxyPort int) *Server {
	s := &Server{
		sockPath:  sockPath,
		proxyPort: proxyPort,
		registry:  NewRegistry(),
		startedAt: time.Now(),
	}

	// Route registration for the daemon management API.
	//
	// IMPORTANT: This API must remain backwards-compatible across binary
	// versions. The daemon process may outlive the CLI that spawned it,
	// so older daemons must handle requests from newer CLIs and vice versa.
	// See the package doc comment in api.go for the compatibility rules.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("POST /v1/runs", s.handleRegisterRun)
	mux.HandleFunc("GET /v1/runs", s.handleListRuns)
	mux.HandleFunc("PATCH /v1/runs/", s.handleUpdateRun)
	mux.HandleFunc("DELETE /v1/runs/", s.handleUnregisterRun)
	mux.HandleFunc("POST /v1/routes/", s.handleRegisterRoutes)
	mux.HandleFunc("DELETE /v1/routes/", s.handleUnregisterRoutes)
	mux.HandleFunc("POST /v1/shutdown", s.handleShutdown)

	s.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	return s
}

// SetProxyPort updates the proxy port reported in API responses.
// Call after the credential proxy starts to set the actual port.
func (s *Server) SetProxyPort(port int) { s.proxyPort = port }

// Registry returns the server's run registry.
func (s *Server) Registry() *Registry { return s.registry }

// SetOnRegister sets a callback invoked when a new run is registered.
func (s *Server) SetOnRegister(fn func()) { s.onRegister = fn }

// SetOnEmpty sets a callback that is invoked when the last run is unregistered.
func (s *Server) SetOnEmpty(fn func()) { s.onEmpty = fn }

// SetOnUnregister sets a callback that is invoked when a run is unregistered.
// The callback receives the run ID for per-run resource cleanup.
func (s *Server) SetOnUnregister(fn func(runID string)) { s.onUnregister = fn }

// SetOnShutdown sets a callback that is invoked when shutdown is requested via the API.
// This should signal the main daemon loop to exit (e.g., by sending SIGTERM to self).
func (s *Server) SetOnShutdown(fn func()) { s.onShutdown = fn }

// SetPersister sets the run persister for saving registry state to disk.
func (s *Server) SetPersister(p *RunPersister) { s.persister = p }

// SetRoutes sets the route table used for route registration handlers.
func (s *Server) SetRoutes(rt *routing.RouteTable) { s.routes = rt }

// Start begins listening on the Unix socket. Any stale socket file is removed first.
func (s *Server) Start() error {
	os.Remove(s.sockPath) // remove stale socket
	listener, err := net.Listen("unix", s.sockPath)
	if err != nil {
		return err
	}
	s.listener = listener
	go func() { _ = s.server.Serve(listener) }()
	return nil
}

// Stop gracefully shuts down the server and removes the socket file.
func (s *Server) Stop(ctx context.Context) error {
	err := s.server.Shutdown(ctx)
	os.Remove(s.sockPath)
	return err
}

// handleHealth responds with daemon health information.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	resp := HealthResponse{
		PID:          os.Getpid(),
		ProxyPort:    s.proxyPort,
		RunCount:     s.registry.Count(),
		StartedAt:    s.startedAt.Format(time.RFC3339),
		Commit:       BuildCommit,
		Capabilities: []string{"keep-policy"},
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleRegisterRun registers a new run and returns the auth token.
func (s *Server) handleRegisterRun(w http.ResponseWriter, r *http.Request) {
	var req RegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	rc := req.ToRunContext()

	// On Linux with Docker host networking, the host gateway is 127.0.0.1 and
	// the proxy also listens on 127.0.0.1. Implicitly allow the proxy port so
	// the proxy does not block its own traffic.
	addProxyPortForLoopback(rc, s.proxyPort)

	// Compile Keep policy engines from YAML and/or RuleSet specs.
	totalPolicies := len(req.PolicyYAML) + len(req.PolicyRuleSets)
	if totalPolicies > 0 {
		engines := make(map[string]*keeplib.Engine, totalPolicies)

		// Helper: build an audit hook for a given scope.
		auditHook := func(scopeName string) func(keeplib.AuditEntry) {
			return func(entry keeplib.AuditEntry) {
				fields := []any{
					"scope", scopeName,
					"op", entry.Operation,
					"decision", string(entry.Decision),
					"rule", entry.Rule,
					"message", entry.Message,
				}
				if entry.Decision == "deny" {
					log.Warn("keep policy deny", fields...)
				} else {
					log.Info("keep policy evaluation", fields...)
				}
			}
		}

		// Compile YAML-based policies (file/pack rules).
		for scope, policyBytes := range req.PolicyYAML {
			eng, err := keeplib.LoadFromBytes(policyBytes, keeplib.WithAuditHook(auditHook(scope)))
			if err != nil {
				for _, e := range engines {
					e.Close()
				}
				writeJSON(w, http.StatusBadRequest, RegisterResponse{
					Error: fmt.Sprintf("keep: compile scope %q: %v", scope, err),
				})
				return
			}
			engines[scope] = eng
		}

		// Compile RuleSet-based policies (inline deny lists).
		for _, spec := range req.PolicyRuleSets {
			rs := keeplib.NewRuleSet(spec.Scope, spec.Mode)
			rs.Deny(spec.Deny...)
			eng, err := rs.Compile(keeplib.WithAuditHook(auditHook(spec.Scope)))
			if err != nil {
				for _, e := range engines {
					e.Close()
				}
				writeJSON(w, http.StatusBadRequest, RegisterResponse{
					Error: fmt.Sprintf("keep: compile ruleset %q: %v", spec.Scope, err),
				})
				return
			}
			engines[spec.Scope] = eng
		}

		rc.KeepEngines = engines
	}

	// Create a per-run context for background work (token refresh, AWS).
	// This context outlives the HTTP request and is canceled when the run
	// is unregistered via CancelRefresh().
	runCtx, cancel := context.WithCancel(context.Background())
	rc.SetRefreshCancel(cancel)

	// Generate the auth token upfront so all setup (token refresh, AWS
	// credentials) can complete before the run is inserted into the registry.
	// This prevents a race where a fast-starting container sends its first
	// request through the proxy before the RunContext is fully initialized.
	var token string
	if req.AuthToken != "" {
		token = req.AuthToken
	} else {
		token = generateToken()
	}

	// Set up token refresh before registry insertion so the initial credential
	// refresh has a head start before the proxy can observe this run.
	if len(req.Grants) > 0 {
		StartTokenRefresh(runCtx, rc, req.Grants)
	}

	// Create AWS credential provider if configured. Uses the pre-generated
	// token for endpoint authentication.
	if req.AWSConfig != nil {
		awsProvider, awsErr := awsprov.NewCredentialProvider(
			runCtx,
			awsprov.CredentialProviderConfig{
				RoleARN:         req.AWSConfig.RoleARN,
				Region:          req.AWSConfig.Region,
				SessionDuration: req.AWSConfig.SessionDuration,
				ExternalID:      req.AWSConfig.ExternalID,
				Profile:         req.AWSConfig.Profile,
			},
			"moat-"+rc.RunID,
		)
		if awsErr != nil {
			log.Warn("failed to create AWS credential provider for run",
				"run_id", rc.RunID, "error", awsErr)
		} else {
			awsProvider.SetAuthToken(token)
			rc.SetAWSHandler(awsProvider.Handler())
		}
	}

	// Create gcloud credential provider if configured.
	if req.GCloudConfig != nil {
		gcloudCfg := &gcloudprov.Config{
			ProjectID:     req.GCloudConfig.ProjectID,
			Scopes:        req.GCloudConfig.Scopes,
			ImpersonateSA: req.GCloudConfig.ImpersonateSA,
			KeyFile:       req.GCloudConfig.KeyFile,
			Email:         req.GCloudConfig.Email,
		}
		gcloudCP, gcloudErr := gcloudprov.NewCredentialProvider(runCtx, gcloudCfg)
		if gcloudErr != nil {
			log.Warn("failed to create gcloud credential provider for run",
				"run_id", rc.RunID, "error", gcloudErr)
		} else {
			rc.SetGCloudHandler(gcloudprov.NewEndpointHandler(gcloudCP))
		}
	}

	// Register the fully-initialized RunContext so the proxy never sees
	// an incomplete run.
	s.registry.RegisterWithToken(rc, token)

	if s.persister != nil {
		s.persister.SaveDebounced()
	}

	if s.onRegister != nil {
		s.onRegister()
	}

	resp := RegisterResponse{
		AuthToken: token,
		ProxyPort: s.proxyPort,
	}
	writeJSON(w, http.StatusCreated, resp)
}

// handleListRuns returns all registered runs.
func (s *Server) handleListRuns(w http.ResponseWriter, _ *http.Request) {
	runs := s.registry.List()
	infos := make([]RunInfo, len(runs))
	for i, rc := range runs {
		infos[i] = RunInfo{
			RunID:        rc.RunID,
			ContainerID:  rc.GetContainerID(),
			RegisteredAt: rc.RegisteredAt.Format(time.RFC3339),
		}
	}
	writeJSON(w, http.StatusOK, infos)
}

// handleUpdateRun updates a run's container ID.
func (s *Server) handleUpdateRun(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r.URL.Path, "/v1/runs/")
	if token == "" {
		http.Error(w, `{"error":"missing token"}`, http.StatusBadRequest)
		return
	}

	var req UpdateRunRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	if !s.registry.UpdateContainerID(token, req.ContainerID) {
		http.Error(w, `{"error":"run not found"}`, http.StatusNotFound)
		return
	}

	if s.persister != nil {
		s.persister.SaveDebounced()
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleUnregisterRun removes a run from the registry.
func (s *Server) handleUnregisterRun(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r.URL.Path, "/v1/runs/")
	if token == "" {
		http.Error(w, `{"error":"missing token"}`, http.StatusBadRequest)
		return
	}

	rc, ok, remaining := s.registry.UnregisterAndGetWithCount(token)
	if !ok {
		http.Error(w, `{"error":"run not found"}`, http.StatusNotFound)
		return
	}

	// Close Keep engines and cancel token refresh after unregistering.
	rc.Close()
	rc.CancelRefresh()

	if s.persister != nil {
		s.persister.SaveDebounced()
	}

	w.WriteHeader(http.StatusNoContent)

	if s.onUnregister != nil {
		s.onUnregister(rc.RunID)
	}
	if s.onEmpty != nil && remaining == 0 {
		s.onEmpty()
	}
}

// handleRegisterRoutes registers service routes for an agent.
func (s *Server) handleRegisterRoutes(w http.ResponseWriter, r *http.Request) {
	agent := extractToken(r.URL.Path, "/v1/routes/")
	if agent == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}
	var reg RouteRegistration
	if err := json.NewDecoder(r.Body).Decode(&reg); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}
	if s.routes == nil {
		http.Error(w, `{"error":"routing not configured"}`, http.StatusServiceUnavailable)
		return
	}
	if err := s.routes.Add(agent, reg.Services); err != nil {
		http.Error(w, `{"error":"failed to register routes"}`, http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUnregisterRoutes removes service routes for an agent.
func (s *Server) handleUnregisterRoutes(w http.ResponseWriter, r *http.Request) {
	agent := extractToken(r.URL.Path, "/v1/routes/")
	if agent == "" {
		http.Error(w, `{"error":"missing agent name"}`, http.StatusBadRequest)
		return
	}
	if s.routes != nil {
		_ = s.routes.Remove(agent)
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleShutdown initiates a graceful server shutdown.
func (s *Server) handleShutdown(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "shutting down"})

	if s.onShutdown != nil {
		go s.onShutdown()
	}
}

// addProxyPortForLoopback adds the proxy port to AllowedHostPorts when the host
// gateway is 127.0.0.1 (Linux Docker with host networking). This prevents
// the proxy from blocking its own traffic.
func addProxyPortForLoopback(rc *RunContext, proxyPort int) {
	if rc.HostGateway != "127.0.0.1" {
		return
	}
	for _, p := range rc.AllowedHostPorts {
		if p == proxyPort {
			return
		}
	}
	rc.AllowedHostPorts = append(rc.AllowedHostPorts, proxyPort)
}

// extractToken extracts the token from a URL path by stripping the prefix.
func extractToken(path, prefix string) string {
	token := strings.TrimPrefix(path, prefix)
	// Remove any trailing slash.
	token = strings.TrimSuffix(token, "/")
	return token
}

// writeJSON marshals v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
