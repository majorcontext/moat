package gemini

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
)

// Ensure CredentialRefresher implements io.Closer.
var _ interface{ Close() error } = (*CredentialRefresher)(nil)

const (
	// refreshBuffer is the time before expiration to refresh the token.
	// Google OAuth access tokens have a 1-hour lifetime. We refresh 5 minutes
	// early to ensure tokens don't expire during active API requests.
	refreshBuffer = 5 * time.Minute

	// retryMin is the minimum retry interval after a refresh failure.
	retryMin = 30 * time.Second

	// retryMax is the maximum retry interval after repeated failures.
	retryMax = 5 * time.Minute
)

// CredentialRefresher manages Gemini OAuth token refresh in the background,
// updating the proxy's credential map before the access token expires.
type CredentialRefresher struct {
	mu           sync.RWMutex
	accessToken  string
	expiresAt    time.Time
	refreshToken string
	refresher    *TokenRefresher
	proxy        credential.ProxyConfigurer
	stopCh       chan struct{}
	stopped      chan struct{}
}

// NewCredentialRefresher creates a new refresher that will proactively
// refresh the Gemini OAuth access token and update the proxy credential map.
func NewCredentialRefresher(accessToken, refreshToken string, expiresAt time.Time, proxy credential.ProxyConfigurer) *CredentialRefresher {
	return &CredentialRefresher{
		accessToken:  accessToken,
		expiresAt:    expiresAt,
		refreshToken: refreshToken,
		refresher:    &TokenRefresher{},
		proxy:        proxy,
		stopCh:       make(chan struct{}),
		stopped:      make(chan struct{}),
	}
}

// Start launches the background token refresh goroutine.
func (g *CredentialRefresher) Start() {
	go g.refreshLoop()
}

// Close signals the refresh goroutine to exit and waits for it to complete.
// Implements io.Closer.
func (g *CredentialRefresher) Close() error {
	close(g.stopCh)
	<-g.stopped
	return nil
}

func (g *CredentialRefresher) refreshLoop() {
	defer close(g.stopped)

	retryDelay := retryMin
	timer := time.NewTimer(0) // Initial timer fires immediately to check state
	defer timer.Stop()

	for {
		g.mu.RLock()
		expiresAt := g.expiresAt
		g.mu.RUnlock()

		sleepDuration := time.Until(expiresAt) - refreshBuffer
		if sleepDuration < 0 {
			sleepDuration = 0
		}

		// Reset timer for next sleep. When reusing a timer, we must stop it and
		// drain the channel before calling Reset. If Stop returns false, the timer
		// already fired and a value may be pending on timer.C.
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(sleepDuration)

		select {
		case <-timer.C:
			if err := g.refreshNow(); err != nil {
				var oauthErr *OAuthError
				if errors.As(err, &oauthErr) && oauthErr.IsRevoked() {
					log.Error("gemini refresh token revoked, stopping refresh", "error", err)
					return
				}
				log.Warn("gemini token refresh failed, will retry", "error", err, "retry_in", retryDelay)

				// Wait for retry delay or stop signal
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(retryDelay)

				select {
				case <-timer.C:
				case <-g.stopCh:
					return
				}

				// Exponential backoff
				retryDelay *= 2
				if retryDelay > retryMax {
					retryDelay = retryMax
				}
				continue
			}
			// Reset retry delay on success
			retryDelay = retryMin

		case <-g.stopCh:
			return
		}
	}
}

func (g *CredentialRefresher) refreshNow() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	result, err := g.refresher.Refresh(ctx, g.refreshToken)
	if err != nil {
		return err
	}

	g.mu.Lock()
	g.accessToken = result.AccessToken
	g.expiresAt = result.ExpiresAt
	g.mu.Unlock()

	// Update the proxy credential map for the OAuth API host
	g.proxy.SetCredential(GeminiAPIHost, "Bearer "+result.AccessToken)
	// Update token substitution so tokeninfo calls use the new token
	g.proxy.SetTokenSubstitution(GeminiOAuthHost, credential.ProxyInjectedPlaceholder, result.AccessToken)

	log.Debug("gemini token refreshed", "expires_at", result.ExpiresAt)
	return nil
}
