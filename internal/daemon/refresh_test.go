package daemon

import (
	"context"
	"testing"

	"github.com/majorcontext/moat/internal/credential"
)

func TestRefreshTokensForRun_NoRefreshableProviders(t *testing.T) {
	// With no providers registered (test environment), refreshTokensForRun
	// should be a no-op and not panic.
	rc := NewRunContext("test-run")
	store := &nullStore{}

	// Should complete without error or panic.
	refreshTokensForRun(context.Background(), rc, []string{"github", "claude"}, store)
}

func TestStartTokenRefresh_NoRefreshable(t *testing.T) {
	// With no refreshable providers, StartTokenRefresh should return
	// immediately without spawning a goroutine.
	rc := NewRunContext("test-run")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should return immediately — no goroutine spawned since no providers
	// are registered in the test environment.
	StartTokenRefresh(ctx, rc, []string{"github", "claude"})
}

func TestStartTokenRefresh_EmptyGrants(t *testing.T) {
	rc := NewRunContext("test-run")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Empty grants should be a no-op.
	StartTokenRefresh(ctx, rc, nil)
}

func TestRefreshTokensForRun_SkipsSSH(t *testing.T) {
	// SSH grants should be skipped entirely.
	rc := NewRunContext("test-run")
	store := &nullStore{}

	// Should complete without error — "ssh" is skipped before provider lookup.
	refreshTokensForRun(context.Background(), rc, []string{"ssh"}, store)
}

// nullStore is a minimal credential.Store that returns errors for all operations.
// Used in tests where no actual credential access is expected.
type nullStore struct{}

func (s *nullStore) Save(_ credential.Credential) error                        { return nil }
func (s *nullStore) Get(_ credential.Provider) (*credential.Credential, error) { return nil, nil }
func (s *nullStore) Delete(_ credential.Provider) error                        { return nil }
func (s *nullStore) List() ([]credential.Credential, error)                    { return nil, nil }
