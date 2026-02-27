// Package daemon implements the proxy daemon for multi-run credential injection.
package daemon

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/proxy"
)

// CredentialEntry holds a credential header for proxy injection.
type CredentialEntry struct {
	Name  string `json:"name"`  // Header name (e.g., "Authorization", "x-api-key")
	Value string `json:"value"` // Header value
	Grant string `json:"grant"` // Grant name for logging
}

// ExtraHeaderEntry holds an additional header to inject.
type ExtraHeaderEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// TokenSubstitutionEntry holds a placeholder-to-real-token mapping.
type TokenSubstitutionEntry struct {
	Placeholder string `json:"placeholder"`
	RealToken   string `json:"real_token"`
}

// AWSConfig holds AWS credential provider configuration.
type AWSConfig struct {
	RoleARN         string        `json:"role_arn"`
	Region          string        `json:"region"`
	SessionDuration time.Duration `json:"session_duration"`
	ExternalID      string        `json:"external_id,omitempty"`
}

// RunContext holds per-run proxy state. It implements credential.ProxyConfigurer
// so providers can configure it identically to how they configure proxy.Proxy.
type RunContext struct {
	RunID       string `json:"run_id"`
	ContainerID string `json:"container_id,omitempty"`
	AuthToken   string `json:"auth_token"`

	Credentials          map[string]CredentialEntry                  `json:"credentials"`
	ExtraHeaders         map[string][]ExtraHeaderEntry               `json:"extra_headers"`
	RemoveHeaders        map[string][]string                         `json:"remove_headers"`
	TokenSubstitutions   map[string]TokenSubstitutionEntry           `json:"token_substitutions"`
	ResponseTransformers map[string][]credential.ResponseTransformer `json:"-"` // not serialized

	MCPServers    []config.MCPServerConfig `json:"mcp_servers,omitempty"`
	NetworkPolicy string                   `json:"network_policy,omitempty"`
	NetworkAllow  []string                 `json:"network_allow,omitempty"`

	AWSConfig *AWSConfig `json:"aws_config,omitempty"`

	RegisteredAt time.Time `json:"registered_at"`

	refreshCancel context.CancelFunc `json:"-"` // cancels token refresh goroutine
	mu            sync.RWMutex
}

// NewRunContext creates a new RunContext for a run.
func NewRunContext(runID string) *RunContext {
	return &RunContext{
		RunID:                runID,
		Credentials:          make(map[string]CredentialEntry),
		ExtraHeaders:         make(map[string][]ExtraHeaderEntry),
		RemoveHeaders:        make(map[string][]string),
		TokenSubstitutions:   make(map[string]TokenSubstitutionEntry),
		ResponseTransformers: make(map[string][]credential.ResponseTransformer),
		RegisteredAt:         time.Now(),
	}
}

// CancelRefresh cancels the token refresh goroutine, if any.
// Safe to call concurrently and multiple times.
func (rc *RunContext) CancelRefresh() {
	rc.mu.Lock()
	cancel := rc.refreshCancel
	rc.refreshCancel = nil
	rc.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// SetRefreshCancel stores the cancel function for the token refresh goroutine.
func (rc *RunContext) SetRefreshCancel(cancel context.CancelFunc) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.refreshCancel = cancel
}

// GetContainerID returns the container ID safely.
func (rc *RunContext) GetContainerID() string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	return rc.ContainerID
}

// SetCredential implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredential(host, value string) {
	rc.SetCredentialHeader(host, "Authorization", value)
}

// SetCredentialHeader implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredentialHeader(host, headerName, headerValue string) {
	rc.SetCredentialWithGrant(host, headerName, headerValue, "")
}

// SetCredentialWithGrant implements credential.ProxyConfigurer.
func (rc *RunContext) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.Credentials[host] = CredentialEntry{Name: headerName, Value: headerValue, Grant: grant}
}

// AddExtraHeader implements credential.ProxyConfigurer.
func (rc *RunContext) AddExtraHeader(host, headerName, headerValue string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.ExtraHeaders[host] = append(rc.ExtraHeaders[host], ExtraHeaderEntry{Name: headerName, Value: headerValue})
}

// AddResponseTransformer implements credential.ProxyConfigurer.
func (rc *RunContext) AddResponseTransformer(host string, transformer credential.ResponseTransformer) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.ResponseTransformers[host] = append(rc.ResponseTransformers[host], transformer)
}

// RemoveRequestHeader implements credential.ProxyConfigurer.
func (rc *RunContext) RemoveRequestHeader(host, headerName string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.RemoveHeaders[host] = append(rc.RemoveHeaders[host], headerName)
}

// SetTokenSubstitution implements credential.ProxyConfigurer.
func (rc *RunContext) SetTokenSubstitution(host, placeholder, realToken string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.TokenSubstitutions[host] = TokenSubstitutionEntry{Placeholder: placeholder, RealToken: realToken}
}

// GetCredential returns the credential for a host, checking host:port fallback.
func (rc *RunContext) GetCredential(host string) (CredentialEntry, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if cred, ok := rc.Credentials[host]; ok {
		return cred, true
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		cred, ok := rc.Credentials[h]
		return cred, ok
	}
	return CredentialEntry{}, false
}

// GetExtraHeaders returns extra headers for a host, checking host:port fallback.
func (rc *RunContext) GetExtraHeaders(host string) []ExtraHeaderEntry {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if headers, ok := rc.ExtraHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.ExtraHeaders[h]
	}
	return nil
}

// GetRemoveHeaders returns headers to remove for a host, checking host:port fallback.
func (rc *RunContext) GetRemoveHeaders(host string) []string {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if headers, ok := rc.RemoveHeaders[host]; ok {
		return headers
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.RemoveHeaders[h]
	}
	return nil
}

// GetTokenSubstitution returns the token substitution for a host, checking host:port fallback.
func (rc *RunContext) GetTokenSubstitution(host string) (TokenSubstitutionEntry, bool) {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if sub, ok := rc.TokenSubstitutions[host]; ok {
		return sub, true
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		sub, ok := rc.TokenSubstitutions[h]
		return sub, ok
	}
	return TokenSubstitutionEntry{}, false
}

// GetResponseTransformers returns response transformers for a host, checking host:port fallback.
func (rc *RunContext) GetResponseTransformers(host string) []credential.ResponseTransformer {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if t, ok := rc.ResponseTransformers[host]; ok {
		return t
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.ResponseTransformers[h]
	}
	return nil
}

// ToProxyContextData converts this RunContext into a proxy.RunContextData
// for use in per-request credential resolution.
func (rc *RunContext) ToProxyContextData() *proxy.RunContextData {
	rc.mu.RLock()
	defer rc.mu.RUnlock()

	d := &proxy.RunContextData{
		RunID:  rc.RunID,
		Policy: rc.NetworkPolicy,
	}

	// Convert credentials.
	d.Credentials = make(map[string]proxy.CredentialHeader, len(rc.Credentials))
	for host, c := range rc.Credentials {
		d.Credentials[host] = proxy.CredentialHeader{Name: c.Name, Value: c.Value, Grant: c.Grant}
	}

	// Convert extra headers.
	d.ExtraHeaders = make(map[string][]proxy.ExtraHeader, len(rc.ExtraHeaders))
	for host, headers := range rc.ExtraHeaders {
		for _, h := range headers {
			d.ExtraHeaders[host] = append(d.ExtraHeaders[host], proxy.ExtraHeader{Name: h.Name, Value: h.Value})
		}
	}

	// Convert remove headers.
	d.RemoveHeaders = make(map[string][]string, len(rc.RemoveHeaders))
	for host, headers := range rc.RemoveHeaders {
		d.RemoveHeaders[host] = append(d.RemoveHeaders[host], headers...)
	}

	// Convert token substitutions.
	d.TokenSubstitutions = make(map[string]*proxy.TokenSubstitution, len(rc.TokenSubstitutions))
	for host, ts := range rc.TokenSubstitutions {
		d.TokenSubstitutions[host] = proxy.NewTokenSubstitution(ts.Placeholder, ts.RealToken)
	}

	// Convert response transformers.
	d.ResponseTransformers = make(map[string][]credential.ResponseTransformer, len(rc.ResponseTransformers))
	for host, transformers := range rc.ResponseTransformers {
		d.ResponseTransformers[host] = append(d.ResponseTransformers[host], transformers...)
	}

	// Convert MCP servers.
	d.MCPServers = rc.MCPServers

	// Convert allowed hosts.
	for _, host := range rc.NetworkAllow {
		d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(host))
	}

	return d
}
