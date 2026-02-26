package daemon

import (
	"context"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// StartTokenRefresh begins a background goroutine that periodically
// refreshes credentials for the given run context.
func StartTokenRefresh(ctx context.Context, rc *RunContext, grants []string) {
	// Find refreshable providers
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		log.Debug("token refresh: cannot get encryption key", "error", err)
		return
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
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
		credName := credential.Provider(provider.ResolveName(grantName))
		prov := provider.Get(grantName)
		if prov == nil {
			continue
		}
		if rp, ok := prov.(provider.RefreshableProvider); ok {
			cred, err := store.Get(credName)
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

func refreshTokensForRun(ctx context.Context, rc *RunContext, grants []string, store credential.Store) {
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]
		if grantName == "ssh" {
			continue
		}
		credName := credential.Provider(provider.ResolveName(grantName))
		prov := provider.Get(grantName)
		if prov == nil {
			continue
		}
		rp, ok := prov.(provider.RefreshableProvider)
		if !ok {
			continue
		}
		cred, err := store.Get(credName)
		if err != nil {
			continue
		}
		provCred := provider.FromLegacy(cred)
		if !rp.CanRefresh(provCred) {
			continue
		}

		refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_, err = rp.Refresh(refreshCtx, rc, provCred)
		cancel()
		if err != nil {
			log.Debug("token refresh failed", "provider", credName, "error", err)
		}
	}
}
