package oauth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscoverProtectedResourceMetadata(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/oauth-protected-resource" {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(ProtectedResourceMetadata{
			Resource:             srvURL,
			AuthorizationServers: []string{"https://auth.example.com"},
		})
	}))
	defer srv.Close()
	srvURL = srv.URL

	prm, err := discoverProtectedResourceMetadata(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prm.Resource != srv.URL {
		t.Errorf("resource = %q, want %q", prm.Resource, srv.URL)
	}
	if len(prm.AuthorizationServers) != 1 || prm.AuthorizationServers[0] != "https://auth.example.com" {
		t.Errorf("authorization_servers = %v, want [https://auth.example.com]", prm.AuthorizationServers)
	}
}

func TestDiscoverProtectedResourceMetadataPathBased(t *testing.T) {
	var requestedPaths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPaths = append(requestedPaths, r.URL.Path)
		if r.URL.Path == "/.well-known/oauth-protected-resource/v1/mcp" {
			json.NewEncoder(w).Encode(ProtectedResourceMetadata{
				Resource:             "test",
				AuthorizationServers: []string{"https://auth.example.com"},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	prm, err := discoverProtectedResourceMetadata(context.Background(), srv.URL+"/v1/mcp")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if prm.Resource != "test" {
		t.Errorf("resource = %q, want %q", prm.Resource, "test")
	}
	// Path-based should be tried first.
	if len(requestedPaths) < 1 || requestedPaths[0] != "/.well-known/oauth-protected-resource/v1/mcp" {
		t.Errorf("first request path = %v, want /.well-known/oauth-protected-resource/v1/mcp", requestedPaths)
	}
}

func TestDiscoverProtectedResourceMetadataNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()

	_, err := discoverProtectedResourceMetadata(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDiscoverAuthServerMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-authorization-server" {
			json.NewEncoder(w).Encode(AuthServerMetadata{
				AuthorizationEndpoint: "https://auth.example.com/authorize",
				TokenEndpoint:         "https://auth.example.com/token",
				RegistrationEndpoint:  "https://auth.example.com/register",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	asm, err := discoverAuthServerMetadata(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asm.AuthorizationEndpoint != "https://auth.example.com/authorize" {
		t.Errorf("authorization_endpoint = %q", asm.AuthorizationEndpoint)
	}
	if asm.TokenEndpoint != "https://auth.example.com/token" {
		t.Errorf("token_endpoint = %q", asm.TokenEndpoint)
	}
	if asm.RegistrationEndpoint != "https://auth.example.com/register" {
		t.Errorf("registration_endpoint = %q", asm.RegistrationEndpoint)
	}
}

func TestDiscoverAuthServerMetadataOIDCFallback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/openid-configuration" {
			json.NewEncoder(w).Encode(AuthServerMetadata{
				AuthorizationEndpoint: "https://auth.example.com/authorize",
				TokenEndpoint:         "https://auth.example.com/token",
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	asm, err := discoverAuthServerMetadata(context.Background(), srv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if asm.AuthorizationEndpoint != "https://auth.example.com/authorize" {
		t.Errorf("authorization_endpoint = %q", asm.AuthorizationEndpoint)
	}
}

func TestRegisterClient(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		json.Unmarshal(body, &req)

		if req["client_name"] != "moat" {
			t.Errorf("client_name = %v, want moat", req["client_name"])
		}
		if req["token_endpoint_auth_method"] != "none" {
			t.Errorf("token_endpoint_auth_method = %v, want none", req["token_endpoint_auth_method"])
		}

		grantTypes, ok := req["grant_types"].([]any)
		if !ok || len(grantTypes) != 1 || grantTypes[0] != "authorization_code" {
			t.Errorf("grant_types = %v, want [authorization_code]", req["grant_types"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"client_id": "new-client-123"})
	}))
	defer srv.Close()

	reg, err := registerClient(context.Background(), srv.URL, "moat", []string{"http://localhost/callback"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg.ClientID != "new-client-123" {
		t.Errorf("client_id = %q, want new-client-123", reg.ClientID)
	}
}

func TestRegisterClientFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	_, err := registerClient(context.Background(), srv.URL, "moat", []string{"http://localhost/callback"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestDiscoverFromMCPServer(t *testing.T) {
	// Auth server with ASM and DCR endpoints. Use NewUnstartedServer so
	// we can reference authSrv.URL in the handler.
	authSrv := httptest.NewUnstartedServer(nil)
	authMux := http.NewServeMux()
	authMux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(AuthServerMetadata{
			AuthorizationEndpoint: authSrv.URL + "/authorize",
			TokenEndpoint:         authSrv.URL + "/token",
			RegistrationEndpoint:  authSrv.URL + "/register",
		})
	})
	authMux.HandleFunc("/register", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]string{"client_id": "discovered-client"})
	})
	authSrv.Config.Handler = authMux
	authSrv.Start()
	defer authSrv.Close()

	// MCP server with PRM endpoint.
	var mcpSrvURL string
	mcpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/oauth-protected-resource" {
			json.NewEncoder(w).Encode(ProtectedResourceMetadata{
				Resource:             mcpSrvURL,
				AuthorizationServers: []string{authSrv.URL},
			})
			return
		}
		http.NotFound(w, r)
	}))
	defer mcpSrv.Close()
	mcpSrvURL = mcpSrv.URL

	cfg, resource, err := DiscoverFromMCPServer(context.Background(), mcpSrv.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.AuthURL != authSrv.URL+"/authorize" {
		t.Errorf("auth_url = %q", cfg.AuthURL)
	}
	if cfg.TokenURL != authSrv.URL+"/token" {
		t.Errorf("token_url = %q", cfg.TokenURL)
	}
	if cfg.ClientID != "discovered-client" {
		t.Errorf("client_id = %q, want discovered-client", cfg.ClientID)
	}
	if resource != mcpSrv.URL {
		t.Errorf("resource = %q, want %q", resource, mcpSrv.URL)
	}
}
