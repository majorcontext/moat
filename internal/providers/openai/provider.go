// Package openai provides OpenAI API-key credentials independently of the
// Codex subscription provider.
package openai

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

type Provider struct {
	auth *credential.OpenAIAuth
}

func init() { provider.Register(&Provider{auth: &credential.OpenAIAuth{}}) }

func (p *Provider) Name() string { return "openai" }

func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	var key string
	if key = os.Getenv("OPENAI_API_KEY"); key != "" {
		fmt.Println("Using API key from OPENAI_API_KEY environment variable")
	} else {
		var err error
		key, err = p.auth.PromptForAPIKey()
		if err != nil {
			return nil, fmt.Errorf("reading API key: %w", err)
		}
	}
	fmt.Println("\nValidating API key...")
	validateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := p.auth.ValidateKey(validateCtx, key); err != nil {
		return nil, fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")
	return &provider.Credential{Provider: "openai", Token: key, CreatedAt: time.Now()}, nil
}

func (p *Provider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	proxy.SetCredentialWithGrant("api.openai.com", "Authorization", "Bearer "+cred.Token, "openai")
}

func (p *Provider) ContainerEnv(*provider.Credential) []string {
	return []string{"OPENAI_API_KEY=" + credential.OpenAIAPIKeyPlaceholder}
}

func (p *Provider) ContainerMounts(*provider.Credential, string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

func (p *Provider) Cleanup(string) {}

func (p *Provider) ImpliedDependencies() []string { return nil }
