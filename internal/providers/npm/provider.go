package npm

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Provider implements provider.CredentialProvider for npm registries.
type Provider struct{}

// Verify interface compliance at compile time.
var _ provider.CredentialProvider = (*Provider)(nil)

func init() {
	provider.Register(&Provider{})
	credential.RegisterImpliedDeps(credential.ProviderNpm, func() []string {
		return []string{"node", "npm"}
	})
}

// Name returns the provider identifier.
func (p *Provider) Name() string {
	return "npm"
}

// ConfigureProxy sets up proxy headers for npm registries.
// Parses the Token JSON and sets per-host Bearer credentials.
func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	entries, err := UnmarshalEntries(cred.Token)
	if err != nil {
		return
	}

	for _, entry := range entries {
		proxy.SetCredentialWithGrant(entry.Host, "Authorization", "Bearer "+entry.Token, "npm")
	}
}

// ContainerEnv returns environment variables for npm.
// No env vars needed — npm reads ~/.npmrc by default, and ContainerMounts
// places the generated .npmrc at containerHome/.npmrc.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	return nil
}

// ContainerMounts generates a stub .npmrc in a temp directory and returns mount configs.
// The .npmrc contains real scope-to-registry routing but placeholder tokens.
func (p *Provider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	entries, err := UnmarshalEntries(cred.Token)
	if err != nil {
		return nil, "", fmt.Errorf("parsing npm credential: %w", err)
	}

	if len(entries) == 0 {
		return nil, "", nil
	}

	tmpDir, err := os.MkdirTemp("", "moat-npm-*")
	if err != nil {
		return nil, "", fmt.Errorf("creating npm config dir: %w", err)
	}
	if err := os.Chmod(tmpDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, "", fmt.Errorf("setting permissions on npm config dir: %w", err)
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	// Generate .npmrc with placeholder tokens
	npmrcContent := GenerateNpmrc(entries, NpmTokenPlaceholder)
	npmrcPath := filepath.Join(tmpDir, ".npmrc")
	if err := os.WriteFile(npmrcPath, []byte(npmrcContent), 0600); err != nil {
		return nil, "", fmt.Errorf("writing .npmrc: %w", err)
	}

	mounts := []provider.MountConfig{
		{
			Source:   npmrcPath,
			Target:   filepath.Join(containerHome, ".npmrc"),
			ReadOnly: true,
		},
	}

	success = true
	return mounts, tmpDir, nil
}

// Cleanup cleans up npm resources.
func (p *Provider) Cleanup(cleanupPath string) {
	if cleanupPath != "" {
		os.RemoveAll(cleanupPath)
	}
}

// ImpliedDependencies returns dependencies implied by this provider.
// node provides the runtime; npm upgrades to version 11 (node bundles an older npm).
func (p *Provider) ImpliedDependencies() []string {
	return []string{"node", "npm"}
}
