package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/majorcontext/moat/internal/log"
)

// discoveryClient is the shared HTTP client for all discovery requests.
var discoveryClient = &http.Client{Timeout: 5 * time.Second}

// ProtectedResourceMetadata represents RFC 9728 protected resource metadata.
type ProtectedResourceMetadata struct {
	Resource             string   `json:"resource"`
	AuthorizationServers []string `json:"authorization_servers"`
}

// AuthServerMetadata represents RFC 8414 authorization server metadata.
type AuthServerMetadata struct {
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	RegistrationEndpoint  string `json:"registration_endpoint,omitempty"`
}

// ClientRegistration holds the result of RFC 7591 dynamic client registration.
type ClientRegistration struct {
	ClientID string `json:"client_id"`
}

// DiscoverFromMCPServer performs full OAuth discovery from an MCP server URL.
// It returns the discovered Config, the resource identifier (for RFC 8707), and any error.
func DiscoverFromMCPServer(ctx context.Context, mcpServerURL string) (*Config, string, error) {
	log.Debug("discovering OAuth config from MCP server", "url", mcpServerURL)

	prm, err := discoverProtectedResourceMetadata(ctx, mcpServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("protected resource metadata discovery: %w", err)
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, "", fmt.Errorf("no authorization servers in protected resource metadata")
	}

	authServerURL := prm.AuthorizationServers[0]
	log.Debug("discovered authorization server", "url", authServerURL)

	asm, err := discoverAuthServerMetadata(ctx, authServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("auth server metadata discovery: %w", err)
	}

	cfg := &Config{
		AuthURL:  asm.AuthorizationEndpoint,
		TokenURL: asm.TokenEndpoint,
	}

	if asm.RegistrationEndpoint != "" {
		log.Debug("performing dynamic client registration", "endpoint", asm.RegistrationEndpoint)
		reg, err := registerClient(ctx, asm.RegistrationEndpoint, "moat", []string{"http://localhost/callback"})
		if err != nil {
			return nil, "", fmt.Errorf("dynamic client registration: %w", err)
		}
		cfg.ClientID = reg.ClientID
		log.Debug("registered client", "client_id", reg.ClientID)
	}

	return cfg, prm.Resource, nil
}

// discoverProtectedResourceMetadata fetches RFC 9728 protected resource metadata.
// It tries the path-based well-known URL first, then falls back to root.
func discoverProtectedResourceMetadata(ctx context.Context, serverURL string) (*ProtectedResourceMetadata, error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return nil, fmt.Errorf("parsing server URL: %w", err)
	}

	origin := u.Scheme + "://" + u.Host

	// Try path-based first: {origin}/.well-known/oauth-protected-resource{path}
	if u.Path != "" && u.Path != "/" {
		pathURL := origin + "/.well-known/oauth-protected-resource" + u.Path
		log.Debug("trying path-based PRM discovery", "url", pathURL)
		prm, pathErr := fetchJSON[ProtectedResourceMetadata](ctx, pathURL)
		if pathErr == nil {
			return prm, nil
		}
		log.Debug("path-based PRM discovery failed", "error", pathErr)
	}

	// Fall back to root.
	rootURL := origin + "/.well-known/oauth-protected-resource"
	log.Debug("trying root PRM discovery", "url", rootURL)
	prm, rootErr := fetchJSON[ProtectedResourceMetadata](ctx, rootURL)
	if rootErr != nil {
		return nil, fmt.Errorf("fetching protected resource metadata: %w", rootErr)
	}
	return prm, nil
}

// discoverAuthServerMetadata fetches RFC 8414 authorization server metadata.
// It tries the standard endpoint first, then falls back to OpenID Connect discovery.
func discoverAuthServerMetadata(ctx context.Context, authServerURL string) (*AuthServerMetadata, error) {
	// Try RFC 8414 first.
	asmURL := authServerURL + "/.well-known/oauth-authorization-server"
	log.Debug("trying OAuth AS metadata", "url", asmURL)
	asm, asmErr := fetchJSON[AuthServerMetadata](ctx, asmURL)
	if asmErr == nil {
		if valErr := validateAuthServerMetadata(asm); valErr != nil {
			return nil, valErr
		}
		return asm, nil
	}
	log.Debug("OAuth AS metadata failed, trying OIDC", "error", asmErr)

	// Fall back to OIDC.
	oidcURL := authServerURL + "/.well-known/openid-configuration"
	log.Debug("trying OIDC discovery", "url", oidcURL)
	asm, oidcErr := fetchJSON[AuthServerMetadata](ctx, oidcURL)
	if oidcErr != nil {
		return nil, fmt.Errorf("fetching auth server metadata: %w", oidcErr)
	}
	if valErr := validateAuthServerMetadata(asm); valErr != nil {
		return nil, valErr
	}
	return asm, nil
}

// validateAuthServerMetadata checks that required fields are present.
func validateAuthServerMetadata(asm *AuthServerMetadata) error {
	if asm.AuthorizationEndpoint == "" {
		return fmt.Errorf("auth server metadata missing authorization_endpoint")
	}
	if asm.TokenEndpoint == "" {
		return fmt.Errorf("auth server metadata missing token_endpoint")
	}
	return nil
}

// registerClient performs RFC 7591 dynamic client registration.
func registerClient(ctx context.Context, endpoint, clientName string, redirectURIs []string) (*ClientRegistration, error) {
	body := map[string]any{
		"client_name":                clientName,
		"redirect_uris":              redirectURIs,
		"grant_types":                []string{"authorization_code"},
		"response_types":             []string{"code"},
		"token_endpoint_auth_method": "none",
	}

	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshaling registration request: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("creating registration request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := discoveryClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("registration request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("registration failed with status %d", resp.StatusCode)
	}

	var reg ClientRegistration
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		return nil, fmt.Errorf("decoding registration response: %w", err)
	}
	return &reg, nil
}

// fetchJSON fetches a URL and decodes the JSON response into a value of type T.
func fetchJSON[T any](ctx context.Context, url string) (*T, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request for %s: %w", url, err)
	}

	resp, err := discoveryClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, url)
	}

	var result T
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decoding response from %s: %w", url, err)
	}
	return &result, nil
}
