// Package credential provides secure credential storage and retrieval.
package credential

import (
	"time"

	"github.com/andybons/moat/internal/container"
)

// ProxyConfigurer is the interface for configuring proxy credentials.
// This avoids importing the proxy package directly.
type ProxyConfigurer interface {
	// SetCredential sets an Authorization header for a host.
	SetCredential(host, value string)
	// SetCredentialHeader sets a custom header for a host.
	SetCredentialHeader(host, headerName, headerValue string)
	// AddExtraHeader adds an additional header to inject for a host.
	AddExtraHeader(host, headerName, headerValue string)
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

// GitHubSetup implements ProviderSetup for GitHub credentials.
type GitHubSetup struct{}

// Provider returns the provider identifier.
func (g *GitHubSetup) Provider() Provider {
	return ProviderGitHub
}

// ConfigureProxy sets up proxy headers for GitHub.
func (g *GitHubSetup) ConfigureProxy(p ProxyConfigurer, cred *Credential) {
	p.SetCredential("api.github.com", "Bearer "+cred.Token)
	p.SetCredential("github.com", "Bearer "+cred.Token)
}

// ContainerEnv returns environment variables for GitHub.
func (g *GitHubSetup) ContainerEnv(cred *Credential) []string {
	return nil // GitHub doesn't need special env vars
}

// ContainerMounts returns mounts for GitHub.
func (g *GitHubSetup) ContainerMounts(cred *Credential, containerHome string) ([]container.MountConfig, string, error) {
	return nil, "", nil // GitHub doesn't need special mounts
}

// Cleanup cleans up GitHub resources.
func (g *GitHubSetup) Cleanup(cleanupPath string) {
	// Nothing to clean up for GitHub
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
