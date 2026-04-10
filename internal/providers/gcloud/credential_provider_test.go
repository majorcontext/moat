package gcloud

import (
	"context"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type fakeTokenSource struct {
	tok  *oauth2.Token
	err  error
	hits int
}

func (f *fakeTokenSource) Token() (*oauth2.Token, error) {
	f.hits++
	return f.tok, f.err
}

func TestCredentialProviderReturnsToken(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour)
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "ya29.fake", Expiry: exp}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	tok, err := p.GetToken(context.Background())
	if err != nil {
		t.Fatalf("GetToken: %v", err)
	}
	if tok.AccessToken != "ya29.fake" {
		t.Errorf("AccessToken = %q", tok.AccessToken)
	}
}

func TestCredentialProviderCaches(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour)
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: exp}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	for i := 0; i < 5; i++ {
		_, _ = p.GetToken(context.Background())
	}
	if fts.hits > 1 {
		t.Errorf("expected caching, token source hit %d times", fts.hits)
	}
}

func TestCredentialProviderRefreshesOnExpiry(t *testing.T) {
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(1 * time.Minute)}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	_, _ = p.GetToken(context.Background())
	_, _ = p.GetToken(context.Background())
	if fts.hits < 2 {
		t.Errorf("expected refresh within buffer window, hits=%d", fts.hits)
	}
}

func TestCredentialProviderContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(1 * time.Hour)}}
	p := NewCredentialProviderFromTokenSource(fts, &Config{ProjectID: "p"})
	_, err := p.GetToken(ctx)
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestCredentialProviderGetters(t *testing.T) {
	cfg := &Config{
		ProjectID: "test-project",
		Scopes:    []string{"scope1", "scope2"},
		Email:     "test@example.com",
	}
	fts := &fakeTokenSource{tok: &oauth2.Token{AccessToken: "a", Expiry: time.Now().Add(1 * time.Hour)}}
	p := NewCredentialProviderFromTokenSource(fts, cfg)

	if p.ProjectID() != "test-project" {
		t.Errorf("ProjectID() = %q", p.ProjectID())
	}
	if len(p.Scopes()) != 2 {
		t.Errorf("Scopes() = %v", p.Scopes())
	}
	if p.Email() != "test@example.com" {
		t.Errorf("Email() = %q", p.Email())
	}
}
