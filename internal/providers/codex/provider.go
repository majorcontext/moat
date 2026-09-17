package codex

import (
	"context"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

const (
	subscriptionHost       = "chatgpt.com"
	subscriptionOrigin     = "https://chatgpt.com"
	subscriptionPathPrefix = "/backend-api/codex"
	syntheticAccountID     = "moat-proxy-codex-account"
)

type Provider struct{}

var (
	_ provider.CredentialProvider  = (*Provider)(nil)
	_ provider.AgentProvider       = (*Provider)(nil)
	_ provider.RefreshableProvider = (*Provider)(nil)
)

func init() { provider.Register(&Provider{}) }

func (p *Provider) Name() string { return "codex" }

func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	return NewGrant().Execute(ctx)
}

// ConfigureProxy installs the real access token and account identity as one
// atomic bundle. Older proxy implementations do not receive a fallback static
// credential: failing closed is safer than widening a subscription token.
func (p *Provider) ConfigureProxy(proxyConfig provider.ProxyConfigurer, cred *provider.Credential) {
	bundles, ok := proxyConfig.(credential.BundleConfigurer)
	if !ok {
		// Failing closed is right, but failing closed silently leaves the user
		// staring at 401s with nothing to explain them.
		log.Warn("Codex subscription not installed: proxy does not support credential bundles")
		return
	}
	auth, err := decodeSubscriptionAuth(cred)
	if err != nil {
		log.Warn("Codex subscription not installed: stored credential is unreadable", "error", err)
		return
	}
	bundles.SetCredentialBundle(subscriptionHost, credential.Bundle{
		ID:    string(credential.ProviderCodexSubscription),
		Grant: "codex",
		Scope: credential.Scope{
			RequireTLS:   true,
			Origins:      []string{subscriptionOrigin},
			Methods:      []string{"GET", "POST"},
			PathPrefixes: []string{subscriptionPathPrefix},
		},
		RequireAll: true,
		Replacements: []credential.HeaderReplacement{
			{Name: "Authorization", Placeholder: "Bearer " + credential.GenerateAccessTokenPlaceholder(syntheticAccountID), Value: "Bearer " + auth.AccessToken},
			{Name: "ChatGPT-Account-ID", Placeholder: syntheticAccountID, Value: auth.AccountID},
		},
	})
}

// Subscription auth is represented by synthetic auth.json, not an API-key env
// var. In particular OPENAI_API_KEY must not override Codex's selected mode.
func (p *Provider) ContainerEnv(*provider.Credential) []string { return nil }

func (p *Provider) ContainerMounts(*provider.Credential, string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

func (p *Provider) Cleanup(string) {}

func (p *Provider) ImpliedDependencies() []string { return nil }

func (p *Provider) CanRefresh(cred *provider.Credential) bool {
	_, err := decodeSubscriptionAuth(cred)
	return err == nil
}

func (p *Provider) RefreshInterval() time.Duration { return 5 * time.Minute }
