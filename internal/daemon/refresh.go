package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
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

// loadCredentialForGrant mirrors run.loadCredentialForGrant: the logical codex
// grant prefers subscription auth and falls back to a separately stored OpenAI
// API key only when no subscription is stored at all. A decrypt or read failure
// is surfaced rather than quietly downgrading the run to API-key billing.
func loadCredentialForGrant(store credential.Store, grantName, grant string) (*credential.Credential, credential.Provider, error) {
	key := resolveCredName(grantName, grant)
	cred, err := store.Get(key)
	if err == nil {
		return cred, key, nil
	}
	if errors.Is(err, credential.ErrNotFound) && provider.ResolveName(grantName) == "codex" {
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

// codexRefreshMu serializes Codex refresh-token redemption across runs. The
// refresh token is single-use and rotates, so two runs exchanging the same one
// concurrently would leave one of them holding a dead credential.
var codexRefreshMu sync.Mutex

// codexRefreshSkew is how long before expiry a subscription is refreshed.
const codexRefreshSkew = 10 * time.Minute

// codexNeedsRefresh reports whether a subscription credential is due.
//
// A zero ExpiresAt means the expiry could not be determined — only possible for
// credentials written before the provider started recording a fallback TTL — so
// it is treated as due. That self-heals: a successful refresh always records an
// expiry, so a credential can take this branch at most once.
func codexNeedsRefresh(cred *provider.Credential) bool {
	return cred.ExpiresAt.IsZero() || time.Until(cred.ExpiresAt) < codexRefreshSkew
}

// refreshCodexSubscription returns the credential a run should use, refreshing
// and persisting it first when it is at or near expiry.
//
// The lock is taken only when a refresh is actually needed, and the store is
// re-read under it: a caller that finds a valid token never blocks behind
// another run's in-flight exchange, and a caller that waited picks up the token
// that exchange produced instead of redeeming a spent one.
//
// Persistence happens before the value is returned for publication, so a daemon
// restart never resurrects a refresh token the OAuth server has already rotated.
func refreshCodexSubscription(ctx context.Context, store credential.Store, cred *provider.Credential) (*provider.Credential, error) {
	if !codexNeedsRefresh(cred) {
		return cred, nil
	}
	codexRefreshMu.Lock()
	defer codexRefreshMu.Unlock()

	if latest, err := store.Get(credential.ProviderCodexSubscription); err == nil {
		reloaded := provider.FromLegacy(latest)
		if !codexNeedsRefresh(reloaded) {
			return reloaded, nil
		}
		cred = reloaded
	}

	rp, ok := provider.Get("codex").(provider.RefreshableProvider)
	if !ok || !rp.CanRefresh(cred) {
		return cred, nil
	}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	updated, err := rp.Refresh(refreshCtx, nil, cred)
	cancel()
	if err != nil {
		return nil, err
	}
	if updated == nil || updated.Token == cred.Token {
		return cred, nil
	}
	if saveErr := store.Save(credential.Credential{
		Provider: credential.ProviderCodexSubscription, Token: updated.Token, Scopes: updated.Scopes,
		ExpiresAt: updated.ExpiresAt, CreatedAt: updated.CreatedAt, Metadata: updated.Metadata,
	}); saveErr != nil {
		return nil, fmt.Errorf("persisting refreshed Codex subscription: %w", saveErr)
	}
	return updated, nil
}

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
	cred, credName, err := loadCredentialForGrant(store, grantName, grant)
	if err != nil {
		return
	}
	provCred := provider.FromLegacy(cred)
	if !rp.CanRefresh(provCred) {
		return
	}

	// Codex subscriptions are refreshed centrally so concurrent runs cannot
	// each redeem the same single-use refresh token. Every other provider
	// refreshes per run against its own credential.
	if credName == credential.ProviderCodexSubscription {
		updated, refreshErr := refreshCodexSubscription(ctx, store, provCred)
		if refreshErr != nil {
			log.Warn("Codex subscription refresh failed", "error", refreshErr)
			return
		}
		if updated.Token != provCred.Token {
			// The one positive signal that refresh is working. Without it a
			// successful rotation is indistinguishable from one that never
			// ran, since only failures were recorded. Never log token values;
			// the new expiry is what makes the event checkable.
			log.Info("Codex subscription refreshed",
				"run_id", rc.RunID, "expires_at", updated.ExpiresAt.Format(time.RFC3339))
		}
		// Publish unconditionally: this run may be newly registered, or another
		// run may have rotated the token while this one waited.
		prov.ConfigureProxy(rc, updated)
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
		// Keep publishing the new token to this run's MCP servers and Copilot
		// below. Refresh() has already rewritten the run's direct credentials
		// in memory, so returning here would leave the relay injecting a stale
		// token while the direct path used the fresh one. The cost of
		// continuing is that a daemon restart re-reads the unsaved older token.
		log.Warn("failed to persist refreshed credential", "provider", credName, "error", saveErr)
	}
	// Update MCP server credentials that use this grant.
	//
	// prov.ConfigureProxy is deliberately not called here: Refresh() already
	// updates the proxy's host credentials directly (e.g. GitHub sets
	// api.github.com and github.com in Refresh). Calling it again would be
	// redundant and would duplicate any AddExtraHeader/AddResponseTransformer.
	//
	// rc.MCPServers is safe to read without locking — it's written once during
	// run registration and never modified after.
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

// credentialRefProviders are the grants a client may ask the daemon to resolve
// from the encrypted store. Only credentials whose injection mechanism cannot
// travel in a registration request belong here — everything else is sent as an
// ordinary CredentialSpec by a caller that already holds the value.
var credentialRefProviders = map[string]bool{"codex": true}

// validateCredentialRefs rejects refs that the run did not declare as grants or
// that are not served by the reference mechanism at all.
func validateCredentialRefs(refs, grants []string) error {
	for _, ref := range refs {
		name := strings.Split(ref, ":")[0]
		if !credentialRefProviders[provider.ResolveName(name)] {
			return fmt.Errorf("credential ref %q is not resolvable by reference", ref)
		}
		if !slices.ContainsFunc(grants, func(g string) bool {
			return provider.ResolveName(strings.Split(g, ":")[0]) == provider.ResolveName(name)
		}) {
			return fmt.Errorf("credential ref %q was not declared as a grant for this run", ref)
		}
	}
	return nil
}
