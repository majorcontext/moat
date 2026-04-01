// Package daemon implements the proxy daemon for multi-run credential injection.
package daemon

import (
	"context"
	"net"
	"net/http"
	"sync"
	"time"

	keeplib "github.com/majorcontext/keep"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/netrules"
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

	Credentials          map[string][]CredentialEntry                `json:"credentials"`
	ExtraHeaders         map[string][]ExtraHeaderEntry               `json:"extra_headers"`
	RemoveHeaders        map[string][]string                         `json:"remove_headers"`
	TokenSubstitutions   map[string]TokenSubstitutionEntry           `json:"token_substitutions"`
	ResponseTransformers map[string][]credential.ResponseTransformer `json:"-"` // not serialized

	MCPServers    []config.MCPServerConfig `json:"mcp_servers,omitempty"`
	NetworkPolicy string                   `json:"network_policy,omitempty"`
	NetworkAllow  []string                 `json:"network_allow,omitempty"`
	NetworkRules  []netrules.HostRules     `json:"network_rules,omitempty"`

	AWSConfig        *AWSConfig        `json:"aws_config,omitempty"`
	TransformerSpecs []TransformerSpec `json:"transformer_specs,omitempty"`
	Grants           []string          `json:"grants,omitempty"`

	RegisteredAt time.Time `json:"registered_at"`

	KeepEngines   map[string]*keeplib.Engine `json:"-"` // compiled Keep policy engines per scope
	refreshCancel context.CancelFunc         `json:"-"` // cancels token refresh goroutine
	awsHandler    http.Handler               `json:"-"` // AWS credential endpoint handler
	mu            sync.RWMutex
}

// NewRunContext creates a new RunContext for a run.
func NewRunContext(runID string) *RunContext {
	return &RunContext{
		RunID:                runID,
		Credentials:          make(map[string][]CredentialEntry),
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

// Close releases resources held by this RunContext, including all Keep engines.
// Safe to call concurrently and multiple times.
func (rc *RunContext) Close() {
	rc.mu.Lock()
	engines := rc.KeepEngines
	rc.KeepEngines = nil
	rc.mu.Unlock()
	if len(engines) > 0 {
		// Close engines after a short delay to let any in-flight proxy
		// evaluations complete. ToProxyContextData copies the engine map
		// pointer, so concurrent Evaluate calls may still hold references.
		go func() {
			time.Sleep(2 * time.Second)
			for _, eng := range engines {
				eng.Close()
			}
		}()
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

// SetAWSHandler stores the AWS credential endpoint handler for this run.
func (rc *RunContext) SetAWSHandler(h http.Handler) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.awsHandler = h
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
// When multiple grants target the same host (e.g., "claude" and "anthropic"
// on api.anthropic.com), each is stored separately. If an entry with the same
// grant and header name already exists for the host (e.g., during token
// refresh), it is updated in place rather than duplicated.
func (rc *RunContext) SetCredentialWithGrant(host, headerName, headerValue, grant string) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	entry := CredentialEntry{Name: headerName, Value: headerValue, Grant: grant}
	for i, existing := range rc.Credentials[host] {
		if existing.Grant == grant && existing.Name == headerName {
			rc.Credentials[host][i] = entry
			return
		}
	}
	rc.Credentials[host] = append(rc.Credentials[host], entry)
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

// GetCredential returns the first credential for a host, checking host:port fallback.
// Use GetCredentials to retrieve all credentials when multiple grants target the same host.
func (rc *RunContext) GetCredential(host string) (CredentialEntry, bool) {
	creds := rc.GetCredentials(host)
	if len(creds) > 0 {
		return creds[0], true
	}
	return CredentialEntry{}, false
}

// GetCredentials returns all credentials for a host, checking host:port fallback.
func (rc *RunContext) GetCredentials(host string) []CredentialEntry {
	rc.mu.RLock()
	defer rc.mu.RUnlock()
	if creds := rc.Credentials[host]; len(creds) > 0 {
		return creds
	}
	h, _, _ := net.SplitHostPort(host)
	if h != "" {
		return rc.Credentials[h]
	}
	return nil
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
	d.Credentials = make(map[string][]proxy.CredentialHeader, len(rc.Credentials))
	for host, creds := range rc.Credentials {
		for _, c := range creds {
			d.Credentials[host] = append(d.Credentials[host], proxy.CredentialHeader{Name: c.Name, Value: c.Value, Grant: c.Grant})
		}
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

	// Reconstruct response transformers from serializable specs.
	// In daemon mode, providers can't pass Go functions across the process boundary,
	// so they register TransformerSpecs (kind + host) that the daemon reconstructs.
	// The function-based ResponseTransformers map is also copied for non-daemon use.
	d.ResponseTransformers = make(map[string][]credential.ResponseTransformer, len(rc.ResponseTransformers)+len(rc.TransformerSpecs))
	for host, transformers := range rc.ResponseTransformers {
		d.ResponseTransformers[host] = append(d.ResponseTransformers[host], transformers...)
	}
	for _, spec := range rc.TransformerSpecs {
		var tf credential.ResponseTransformer
		switch spec.Kind {
		case "oauth-endpoint-workaround":
			tf = newOAuthEndpointTransformer()
		case "response-scrub":
			if ts, ok := rc.TokenSubstitutions[spec.Host]; ok {
				tf = newResponseScrubber(ts.RealToken, ts.Placeholder)
			} else {
				log.Warn("response-scrub transformer has no matching token substitution",
					"host", spec.Host, "run_id", rc.RunID)
			}
		}
		if tf != nil {
			d.ResponseTransformers[spec.Host] = append(d.ResponseTransformers[spec.Host], tf)
		}
	}

	// Copy MCP servers (consistent with other fields being deep-copied).
	if len(rc.MCPServers) > 0 {
		d.MCPServers = make([]config.MCPServerConfig, len(rc.MCPServers))
		copy(d.MCPServers, rc.MCPServers)
	}

	// Convert network allow list to AllowedHosts.
	// If NetworkRules is populated (new CLI), use that as the primary source.
	// Fall back to NetworkAllow (old CLI) for backwards compatibility.
	if len(rc.NetworkRules) > 0 {
		// Shallow copy: inner Rules slices share backing arrays, which is safe
		// because these are never mutated after creation.
		d.HostRules = make([]netrules.HostRules, len(rc.NetworkRules))
		copy(d.HostRules, rc.NetworkRules)
		// Also add rule hosts to AllowedHosts for host-level matching.
		for _, hr := range rc.NetworkRules {
			d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(hr.Host))
		}
	} else {
		// Old CLI: NetworkAllow contains plain host strings.
		for _, host := range rc.NetworkAllow {
			d.AllowedHosts = append(d.AllowedHosts, proxy.ParseHostPattern(host))
		}
	}

	// Include AWS handler if configured.
	d.AWSHandler = rc.awsHandler

	// Propagate Keep policy engines.
	d.KeepEngines = rc.KeepEngines

	return d
}
