package gemini

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

// PrepareContainer sets up staging directories and config files for Gemini CLI.
// It creates a staging directory with settings.json and optionally oauth_creds.json
// that will be copied to ~/.gemini at container startup by moat-init.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	tmpDir, err := os.MkdirTemp("", "moat-gemini-staging-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp dir: %w", err)
	}

	cleanupFn := func() {
		os.RemoveAll(tmpDir)
	}

	// Populate staging directory based on credential type
	if opts.Credential != nil {
		if err := populateStagingDir(opts.Credential, tmpDir); err != nil {
			cleanupFn()
			return nil, fmt.Errorf("populating staging dir: %w", err)
		}
	} else {
		// No credential — write default settings
		if err := writeSettings(tmpDir, "oauth-personal"); err != nil {
			cleanupFn()
			return nil, fmt.Errorf("writing default settings: %w", err)
		}
	}

	// Build container environment
	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_GEMINI_INIT="+GeminiInitMountPath)

	// Build mounts - staging directory for init
	mounts := []provider.MountConfig{
		{
			Source:   tmpDir,
			Target:   GeminiInitMountPath,
			ReadOnly: true,
		},
	}

	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup:    cleanupFn,
	}, nil
}

// populateStagingDir populates the Gemini staging directory with auth configuration.
func populateStagingDir(cred *provider.Credential, stagingDir string) error {
	if IsOAuthCredential(cred) {
		return populateOAuthStagingDir(stagingDir)
	}
	return populateAPIKeyStagingDir(stagingDir)
}

func populateOAuthStagingDir(stagingDir string) error {
	if err := writeSettings(stagingDir, "oauth-personal"); err != nil {
		return err
	}

	// Write oauth_creds.json with placeholder token.
	// The real token is NEVER placed in the container — the proxy substitutes
	// the placeholder with the real token at the network layer.
	oauthCreds := OAuthCreds{
		AccessToken:  ProxyInjectedPlaceholder,
		Scope:        "https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/cloud-platform openid",
		TokenType:    "Bearer",
		ExpiryDate:   time.Now().Add(365 * 24 * time.Hour).UnixMilli(), // Far future — proxy handles real expiry
		RefreshToken: ProxyInjectedPlaceholder,                         // Placeholder — proxy handles refresh
	}
	credsData, err := json.MarshalIndent(oauthCreds, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling oauth_creds.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "oauth_creds.json"), credsData, 0600); err != nil {
		return fmt.Errorf("writing oauth_creds.json: %w", err)
	}

	return nil
}

func populateAPIKeyStagingDir(stagingDir string) error {
	return writeSettings(stagingDir, "gemini-api-key")
}

// writeSettings writes a settings.json file with the given auth type.
func writeSettings(stagingDir, authType string) error {
	settings := Settings{
		Security: SecuritySettings{
			Auth: AuthSettings{
				SelectedType: authType,
			},
		},
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshaling settings.json: %w", err)
	}
	if err := os.WriteFile(filepath.Join(stagingDir, "settings.json"), data, 0600); err != nil {
		return fmt.Errorf("writing settings.json: %w", err)
	}
	return nil
}
