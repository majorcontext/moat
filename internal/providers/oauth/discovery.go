package oauth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
	Issuer                string `json:"issuer"`
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
	if err := requireHTTPS(mcpServerURL, "MCP server URL"); err != nil {
		return nil, "", err
	}

	log.Debug("discovering OAuth config from MCP server", "url", mcpServerURL)

	prm, err := discoverProtectedResourceMetadata(ctx, mcpServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("protected resource metadata discovery: %w", err)
	}

	if len(prm.AuthorizationServers) == 0 {
		return nil, "", fmt.Errorf("no authorization servers in protected resource metadata")
	}

	authServerURL := prm.AuthorizationServers[0]
	if err = requireHTTPS(authServerURL, "authorization server URL"); err != nil {
		return nil, "", err
	}
	log.Debug("discovered authorization server", "url", authServerURL)

	asm, err := discoverAuthServerMetadata(ctx, authServerURL)
	if err != nil {
		return nil, "", fmt.Errorf("auth server metadata discovery: %w", err)
	}

	// Validate discovered endpoints use HTTPS.
	if err = requireHTTPS(asm.AuthorizationEndpoint, "authorization endpoint"); err != nil {
		return nil, "", err
	}
	if err = requireHTTPS(asm.TokenEndpoint, "token endpoint"); err != nil {
		return nil, "", err
	}

	cfg := &Config{
		AuthURL:  asm.AuthorizationEndpoint,
		TokenURL: asm.TokenEndpoint,
	}

	if asm.RegistrationEndpoint != "" {
		if err = requireHTTPS(asm.RegistrationEndpoint, "registration endpoint"); err != nil {
			return nil, "", err
		}
		cfg.RegistrationEndpoint = asm.RegistrationEndpoint
		log.Debug("DCR endpoint available (deferred until redirect URI known)", "endpoint", asm.RegistrationEndpoint)
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
// The well-known component precedes the issuer path. OIDC discovery tries
// the RFC 8414 location before the legacy OIDC suffix location.
func discoverAuthServerMetadata(ctx context.Context, authServerURL string) (*AuthServerMetadata, error) {
	issuer, err := url.Parse(authServerURL)
	if err != nil {
		return nil, fmt.Errorf("parsing authorization server URL: %w", err)
	}
	if issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.ForceQuery || issuer.Fragment != "" {
		return nil, fmt.Errorf("authorization server URL must have a host and no userinfo, query, or fragment")
	}
	// Public MCP discovery enforces HTTPS before this helper. Keep HTTP
	// available only for loopback fixtures, never for remote issuer discovery.
	loopback := strings.EqualFold(issuer.Hostname(), "localhost")
	if ip := net.ParseIP(issuer.Hostname()); ip != nil {
		loopback = ip.IsLoopback()
	}
	if issuer.Scheme != "https" && (issuer.Scheme != "http" || !loopback) {
		return nil, fmt.Errorf("authorization server URL must use HTTPS (HTTP is only allowed on loopback)")
	}
	origin := issuer.Scheme + "://" + issuer.Host
	path := strings.TrimSuffix(issuer.EscapedPath(), "/")
	candidates := []string{
		origin + "/.well-known/oauth-authorization-server" + path,
		origin + "/.well-known/openid-configuration" + path,
	}
	if path != "" {
		candidates = append(candidates,
			origin+path+"/.well-known/openid-configuration",
			// Compatibility with servers that worked with Moat's former suffix lookup.
			origin+path+"/.well-known/oauth-authorization-server",
		)
	}
	var failures []error
	for _, candidate := range candidates {
		log.Debug("trying authorization server metadata", "url", candidate)
		metadata, fetchErr := fetchJSON[AuthServerMetadata](ctx, candidate)
		if fetchErr != nil {
			failures = append(failures, fetchErr)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		if err := validateAuthServerMetadata(metadata, authServerURL); err != nil {
			failures = append(failures, fmt.Errorf("invalid metadata from %s: %w", candidate, err))
			continue
		}
		return metadata, nil
	}
	return nil, fmt.Errorf("fetching auth server metadata: %w", errors.Join(failures...))
}

// validateAuthServerMetadata binds metadata to the requested issuer (RFC 8414
// section 3.3) and checks the endpoints required for authorization-code grants.
func validateAuthServerMetadata(asm *AuthServerMetadata, expectedIssuer string) error {
	if asm.Issuer == "" {
		return fmt.Errorf("auth server metadata missing issuer")
	}
	if asm.Issuer != expectedIssuer {
		return fmt.Errorf("auth server metadata issuer %q does not match requested issuer %q", asm.Issuer, expectedIssuer)
	}
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

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading registration response: %w", err)
	}
	var reg ClientRegistration
	if err := json.Unmarshal(respBody, &reg); err != nil {
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, fmt.Errorf("reading response from %s: %w", url, err)
	}
	var result T
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("decoding response from %s: %w", url, err)
	}
	return &result, nil
}
