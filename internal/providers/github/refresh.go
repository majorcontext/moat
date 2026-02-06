package github

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/majorcontext/moat/internal/provider"
)

// Refresh re-acquires a fresh token from the original source and updates the proxy.
// Returns ErrRefreshNotSupported if the credential cannot be refreshed.
func (p *Provider) Refresh(ctx context.Context, proxy provider.ProxyConfigurer, cred *provider.Credential) (*provider.Credential, error) {
	if cred.Metadata == nil {
		return nil, provider.ErrRefreshNotSupported
	}

	source := cred.Metadata[provider.MetaKeyTokenSource]
	var newToken string

	switch source {
	case SourceCLI:
		out, err := exec.CommandContext(ctx, "gh", "auth", "token").Output()
		if err != nil {
			return nil, fmt.Errorf("gh auth token: %w", err)
		}
		newToken = strings.TrimSpace(string(out))
	case SourceEnv:
		newToken = os.Getenv("GITHUB_TOKEN")
		if newToken == "" {
			newToken = os.Getenv("GH_TOKEN")
		}
		if newToken == "" {
			return nil, fmt.Errorf("GITHUB_TOKEN and GH_TOKEN are both empty")
		}
	default:
		return nil, provider.ErrRefreshNotSupported
	}

	// Update proxy
	proxy.SetCredentialWithGrant("api.github.com", "Authorization", "Bearer "+newToken, "github")
	proxy.SetCredentialWithGrant("github.com", "Authorization", "Bearer "+newToken, "github")

	// Return updated credential (copy to avoid mutating original)
	updated := *cred
	updated.Token = newToken
	return &updated, nil
}
