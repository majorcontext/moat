package daemon

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/routing"
)

// Server is the daemon's HTTP API server over a Unix socket.
type Server struct {
	sockPath     string
	proxyPort    int
	registry     *Registry
	routes       *routing.RouteTable
	server       *http.Server
	listener     net.Listener
	startedAt    time.Time
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
		PID:       os.Getpid(),
		ProxyPort: s.proxyPort,
		RunCount:  s.registry.Count(),
		StartedAt: s.startedAt.Format(time.RFC3339),
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

	// Set up token refresh BEFORE registering so the cancel function is
	// visible to concurrent readers (e.g., handleUnregisterRun) immediately.
	if len(req.Grants) > 0 {
		refreshCtx, cancel := context.WithCancel(context.Background())
		rc.SetRefreshCancel(cancel)
		StartTokenRefresh(refreshCtx, rc, req.Grants)
	}

	token := s.registry.Register(rc)

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
			ContainerID:  rc.ContainerID,
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

	w.WriteHeader(http.StatusNoContent)
}

// handleUnregisterRun removes a run from the registry.
func (s *Server) handleUnregisterRun(w http.ResponseWriter, r *http.Request) {
	token := extractToken(r.URL.Path, "/v1/runs/")
	if token == "" {
		http.Error(w, `{"error":"missing token"}`, http.StatusBadRequest)
		return
	}

	rc, ok := s.registry.Lookup(token)
	if !ok {
		http.Error(w, `{"error":"run not found"}`, http.StatusNotFound)
		return
	}

	// Cancel token refresh before unregistering
	rc.CancelRefresh()

	s.registry.Unregister(token)
	w.WriteHeader(http.StatusNoContent)

	if s.onUnregister != nil {
		s.onUnregister(rc.RunID)
	}
	if s.onEmpty != nil && s.registry.Count() == 0 {
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
