// Package credential provides secure credential storage and retrieval.
package credential

import (
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/majorcontext/moat/internal/container"
)

// ProxyInjectedPlaceholder is a placeholder value for credentials that will be
// injected by the Moat proxy at runtime. The actual credential never reaches
// the container; instead, the proxy intercepts requests and adds the real
// Authorization header. This placeholder signals to tools that a credential
// is expected without exposing the actual value.
const ProxyInjectedPlaceholder = "moat-proxy-injected"

// OpenAIAPIKeyPlaceholder is a placeholder that looks like a valid OpenAI API key.
// Some tools validate the API key format locally before making requests.
// Using a valid-looking placeholder bypasses these checks while still allowing
// the proxy to inject the real key at the network layer.
const OpenAIAPIKeyPlaceholder = "sk-moat-proxy-injected-placeholder-0000000000000000000000000000000000000000"

// GitHubTokenPlaceholder is a placeholder that looks like a valid GitHub personal access token.
//
// The gh CLI validates token format locally before making requests. Real GitHub PATs use
// the format ghp_XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX (ghp_ prefix + 36 base62 characters, 40 total).
//
// This format-valid placeholder is necessary because:
// - gh CLI checks GH_TOKEN format before making API calls
// - Tools like Claude Code's footer use gh CLI to fetch PR status
// - Without a valid-looking token, gh CLI rejects it and may prompt for auth
//
// The proxy intercepts all GitHub HTTPS traffic and injects the real token via
// Authorization headers, so this placeholder never reaches GitHub's servers.
const GitHubTokenPlaceholder = "ghp_moatProxyInjectedPlaceholder000000000000"

// JWTPlaceholder is a basic placeholder JWT for format validation only.
// Use GenerateIDTokenPlaceholder for Codex CLI which needs account_id in claims.
const JWTPlaceholder = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtb2F0LXByb3h5LXBsYWNlaG9sZGVyIiwiZXhwIjo5OTk5OTk5OTk5fQ.placeholder-signature-not-valid"

// GenerateIDTokenPlaceholder creates a JWT-formatted placeholder with the account_id
// embedded in the claims. This is required for Codex CLI which extracts the
// chatgpt_account_id from the id_token's JWT claims locally before making API calls.
//
// The generated JWT has:
// - Header: {"alg":"RS256","typ":"JWT"}
// - Payload: {"sub":"moat-placeholder","exp":<future>,"https://api.openai.com/auth":{"chatgpt_account_id":"<account_id>"}}
// - Signature: placeholder (not cryptographically valid)
//
// The signature is not valid, but Codex CLI only decodes the claims - it doesn't
// verify the signature locally (that's done server-side).
func GenerateIDTokenPlaceholder(accountID string) string {
	// JWT header: {"alg":"RS256","typ":"JWT"}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))

	// JWT payload with account_id in the OpenAI auth claims structure
	payload := map[string]interface{}{
		"sub": "moat-proxy-placeholder",
		"exp": 9999999999, // Far future expiration
		"https://api.openai.com/auth": map[string]string{
			"chatgpt_account_id": accountID,
		},
	}
	// json.Marshal cannot fail for static map[string]interface{} with primitive types
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Signature placeholder (not cryptographically valid, but not checked locally)
	signature := base64.RawURLEncoding.EncodeToString([]byte("moat-placeholder-signature"))

	return header + "." + payloadB64 + "." + signature
}

// GenerateAccessTokenPlaceholder creates a JWT-formatted access token placeholder.
// The Codex CLI also validates the access_token as a JWT and extracts claims from it.
// This placeholder mirrors the structure of a real OpenAI access token.
func GenerateAccessTokenPlaceholder(accountID string) string {
	// JWT header: {"alg":"RS256","typ":"JWT"}
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))

	// JWT payload matching OpenAI access token structure
	// Based on real token structure: includes aud, client_id, exp, and auth claims
	payload := map[string]interface{}{
		"aud":       []string{"https://api.openai.com/v1"},
		"client_id": codexCLIClientID,
		"exp":       9999999999, // Far future expiration
		"iat":       time.Now().Unix(),
		"iss":       "https://auth.openai.com",
		"sub":       "moat-proxy-placeholder",
		"https://api.openai.com/auth": map[string]interface{}{
			"chatgpt_account_id":      accountID,
			"chatgpt_account_user_id": "user-moat-placeholder__" + accountID,
			"chatgpt_plan_type":       "plus",
			"chatgpt_user_id":         "user-moat-placeholder",
			"user_id":                 "user-moat-placeholder",
		},
		"https://api.openai.com/profile": map[string]interface{}{
			"email":          "moat-placeholder@example.com",
			"email_verified": true,
		},
		"scp": []string{"openid", "profile", "email", "offline_access"},
	}
	// json.Marshal cannot fail for static map[string]interface{} with primitive types
	payloadJSON, _ := json.Marshal(payload)
	payloadB64 := base64.RawURLEncoding.EncodeToString(payloadJSON)

	// Signature placeholder
	signature := base64.RawURLEncoding.EncodeToString([]byte("moat-placeholder-signature"))

	return header + "." + payloadB64 + "." + signature
}

// codexCLIClientID is the OAuth client ID used by the Codex CLI.
const codexCLIClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

// ProxyConfigurer is the interface for configuring proxy credentials.
// This avoids importing the proxy package directly.
// ResponseTransformer modifies HTTP responses for a host.
// It receives the request and response as interface{} to avoid circular dependencies.
// Cast to *http.Request and *http.Response in the transformer implementation.
// Returns the modified response and true if transformed, or original response and false otherwise.
//
// Note on body handling: Transformers are called BEFORE body capture for logging.
// If you need to inspect the response body, read it within the transformer and return
// a new response with a fresh body reader. The original body is not rewound after reading.
type ResponseTransformer func(req, resp interface{}) (interface{}, bool)

type ProxyConfigurer interface {
	// SetCredential sets an Authorization header for a host.
	SetCredential(host, value string)
	// SetCredentialHeader sets a custom header for a host.
	SetCredentialHeader(host, headerName, headerValue string)
	// AddExtraHeader adds an additional header to inject for a host.
	AddExtraHeader(host, headerName, headerValue string)
	// AddResponseTransformer registers a response transformer for a host.
	// Transformers are called in registration order after the response is received.
	AddResponseTransformer(host string, transformer ResponseTransformer)
}

// ProviderSetup configures a credential provider for use in a container run.
// Each provider (GitHub, Anthropic, etc.) implements this interface to handle
// its specific proxy configuration, environment variables, and container mounts.
type ProviderSetup interface {
	// Provider returns the provider identifier.
	Provider() Provider

	// ConfigureProxy sets up proxy headers for this credential.
	ConfigureProxy(p ProxyConfigurer, cred *Credential)

	// ContainerEnv returns environment variables to set in the container.
	ContainerEnv(cred *Credential) []string

	// ContainerMounts returns mounts needed for this credential.
	// The containerHome parameter is the home directory inside the container.
	// Returns the mounts and an optional cleanup directory path.
	ContainerMounts(cred *Credential, containerHome string) ([]container.MountConfig, string, error)

	// Cleanup is called when the run ends to clean up any resources.
	// The cleanupPath is the path returned by ContainerMounts.
	Cleanup(cleanupPath string)
}

// ProviderResult holds the result of configuring a provider.
type ProviderResult struct {
	// Env contains environment variables to add to the container.
	Env []string
	// Mounts contains mount configurations for the container.
	Mounts []container.MountConfig
	// CleanupPath is a path to clean up when the run ends (optional).
	CleanupPath string
}

// providerSetups holds registered provider setups.
var providerSetups = make(map[Provider]ProviderSetup)

// RegisterProviderSetup registers a ProviderSetup for a provider.
// This is typically called from init() functions in provider packages.
func RegisterProviderSetup(provider Provider, setup ProviderSetup) {
	providerSetups[provider] = setup
}

// GetProviderSetup returns the ProviderSetup for a given provider.
// Returns nil if the provider doesn't have a registered setup.
func GetProviderSetup(provider Provider) ProviderSetup {
	if setup, ok := providerSetups[provider]; ok {
		return setup
	}
	// Fall back to built-in providers
	switch provider {
	case ProviderGitHub:
		return &GitHubSetup{}
	default:
		return nil
	}
}

// IsOAuthToken returns true if the token appears to be a Claude Code OAuth token.
//
// This uses a prefix-based heuristic: OAuth tokens from Claude Code start with
// "sk-ant-oat" (Anthropic OAuth Token). This prefix format is based on observed
// token structure as of 2025. If Anthropic changes their token format in the
// future, this function may need to be updated.
//
// Note: API keys typically start with "sk-ant-api" for comparison.
func IsOAuthToken(token string) bool {
	return len(token) > 10 && token[:10] == "sk-ant-oat"
}

// OAuthCredentialInfo holds information extracted from an OAuth credential
// for creating credential files.
type OAuthCredentialInfo struct {
	AccessToken string
	ExpiresAt   time.Time
	Scopes      []string
}
