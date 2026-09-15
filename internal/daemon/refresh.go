package daemon

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/mcpcatalog"
	"github.com/majorcontext/moat/internal/provider"
)

// resolveCredName maps a grant (e.g. "oauth:notion", "github") to the
// credential store key. OAuth and MCP grants use the full grant name; all
// others use the resolved provider name.
func resolveCredName(grantName, grant string) credential.Provider {
	// MCP grants ("mcp:<name>" or deprecated "mcp-<name>") store under the full
	// grant name. This must precede provider.ResolveName because "mcp:context7"
	// splits to grantName "mcp", which is not a registered provider.
	if mcpcatalog.IsGrant(grant) {
		return credential.Provider(grant)
	}
	canonical := provider.ResolveName(grantName)
	if canonical == "codex" {
		return credential.ProviderCodexSubscription
	}
	if canonical == "oauth" {
		return credential.Provider(grant)
	}
	if canonical == "copilot" {
		return credential.ProviderGitHub
	}
	return credential.Provider(canonical)
}

func loadCredentialForGrant(store credential.Store, grantName, grant string) (*credential.Credential, credential.Provider, error) {
	key := resolveCredName(grantName, grant)
	cred, err := store.Get(key)
	if err == nil {
		return cred, key, nil
	}
	if provider.ResolveName(grantName) == "codex" {
		if fallback, fallbackErr := store.Get(credential.ProviderOpenAI); fallbackErr == nil {
			return fallback, credential.ProviderOpenAI, nil
		}
	}
	return nil, key, err
}

// storeDirForRun returns the credential store directory for the run's profile.
// The daemon is shared across profiles, so refresh must use the run's own
// profile rather than the daemon process's credential.ActiveProfile.
func storeDirForRun(rc *RunContext) string {
	return credential.StoreDirForProfile(rc.CredProfile)
}

// StartTokenRefresh begins a background goroutine that periodically
// refreshes credentials for the given run context.
func StartTokenRefresh(ctx context.Context, rc *RunContext, grants []string) {
	// Find refreshable providers
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Debug("token refresh: cannot get encryption key", "error", err)
		return
	}
	store, err := credential.NewFileStore(storeDirForRun(rc), key)
	if err != nil {
		log.Debug("token refresh: cannot open store", "error", err)
		return
	}

	var hasRefreshable bool
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]
		if grantName == "ssh" {
			continue
		}
		prov := provider.Get(grantName)
		if prov == nil {
			continue
		}
		if rp, ok := prov.(provider.RefreshableProvider); ok {
			cred, _, err := loadCredentialForGrant(store, grantName, grant)
			if err != nil {
				continue
			}
			provCred := provider.FromLegacy(cred)
			if rp.CanRefresh(provCred) {
				hasRefreshable = true
				break
			}
		}
	}

	if !hasRefreshable {
		return
	}

	go func() {
		// Do an initial refresh at startup
		refreshTokensForRun(ctx, rc, grants, store)

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshTokensForRun(ctx, rc, grants, store)
			}
		}
	}()
}

var codexRefreshMu sync.Mutex

func refreshTokensForRun(ctx context.Context, rc *RunContext, grants []string, store credential.Store) {
	for _, grant := range grants {
		refreshTokenForGrant(ctx, rc, grant, store)
	}
}

func refreshTokenForGrant(ctx context.Context, rc *RunContext, grant string, store credential.Store) {
	grantName := strings.Split(grant, ":")[0]
	if grantName == "ssh" {
		return
	}
	prov := provider.Get(grantName)
	if prov == nil {
		return
	}
	rp, ok := prov.(provider.RefreshableProvider)
	if !ok {
		return
	}
	isCodex := provider.ResolveName(grantName) == "codex"
	if isCodex {
		// Refresh tokens can rotate. Serialize across runs, then reload the
		// encrypted store after taking the lock so only one run uses an old token.
		codexRefreshMu.Lock()
		defer codexRefreshMu.Unlock()
	}
	cred, credName, err := loadCredentialForGrant(store, grantName, grant)
	if err != nil {
		return
	}
	provCred := provider.FromLegacy(cred)
	if !rp.CanRefresh(provCred) {
		return
	}
	if isCodex && !provCred.ExpiresAt.IsZero() && time.Until(provCred.ExpiresAt) >= 10*time.Minute {
		// Another run may just have refreshed while this one waited. Publish the
		// newly reloaded value into this run without another network exchange.
		prov.ConfigureProxy(rc, provCred)
		return
	}

	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	updated, err := rp.Refresh(refreshCtx, rc, provCred)
	cancel()
	if err != nil {
		log.Debug("token refresh failed", "provider", credName, "error", err)
		return
	}
	if updated == nil || updated.Token == provCred.Token {
		return
	}
	storeCred := credential.Credential{
		Provider: credName, Token: updated.Token, Scopes: updated.Scopes,
		ExpiresAt: updated.ExpiresAt, CreatedAt: updated.CreatedAt, Metadata: updated.Metadata,
	}
	if saveErr := store.Save(storeCred); saveErr != nil {
		log.Debug("failed to persist refreshed credential", "provider", credName, "error", saveErr)
		return
	}
	if isCodex {
		prov.ConfigureProxy(rc, updated)
	}
	for _, mcp := range rc.MCPServers {
		if mcp.Auth != nil && mcp.Auth.Grant == grant {
			serverHost := mcp.URL
			if u, parseErr := url.Parse(mcp.URL); parseErr == nil {
				serverHost = u.Host
			}
			rc.SetCredentialWithGrant(serverHost, mcp.Auth.Header, updated.Token, grant)
		}
	}
	if grantName == "github" && rc.CopilotGitHubAuth {
		if copilotProv := provider.Get("copilot"); copilotProv != nil {
			copilotProv.ConfigureProxy(rc, updated)
		}
	}
	log.Debug("token refreshed", "provider", credName)
}
