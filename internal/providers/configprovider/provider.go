package configprovider

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// ConfigProvider implements provider.CredentialProvider using a YAML-defined ProviderDef.
type ConfigProvider struct {
	def    ProviderDef
	source string // "builtin" or "custom"
}

// Verify interface compliance at compile time.
var (
	_ provider.CredentialProvider  = (*ConfigProvider)(nil)
	_ provider.DescribableProvider = (*ConfigProvider)(nil)
)

// NewConfigProvider creates a new ConfigProvider from a definition.
func NewConfigProvider(def ProviderDef, source string) *ConfigProvider {
	return &ConfigProvider{def: def, source: source}
}

// Name returns the provider identifier.
func (p *ConfigProvider) Name() string {
	return p.def.Name
}

// Grant acquires credentials from environment variables or interactive prompt.
func (p *ConfigProvider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Check source environment variables in order
	if len(p.def.SourceEnv) > 0 {
		if token, name := util.CheckEnvVarWithName(p.def.SourceEnv...); token != "" {
			fmt.Printf("Using token from %s environment variable\n", name)
			return p.validateAndCreate(ctx, token, "env")
		}
	}

	// Interactive prompt
	if p.def.Prompt != "" {
		fmt.Print(p.def.Prompt)
	} else {
		fmt.Printf("Enter a %s token.\n", p.def.Description)
	}

	token, err := util.PromptForToken("Token")
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	if token == "" {
		return nil, &provider.GrantError{
			Provider: p.def.Name,
			Cause:    fmt.Errorf("no token provided"),
			Hint:     fmt.Sprintf("Run 'moat grant %s' and enter a valid token", p.def.Name),
		}
	}

	return p.validateAndCreate(ctx, token, "prompt")
}

// validateAndCreate optionally validates the token and creates a credential.
func (p *ConfigProvider) validateAndCreate(ctx context.Context, token, source string) (*provider.Credential, error) {
	if p.def.Validate != nil {
		fmt.Println("Validating token...")
		if err := p.validateToken(ctx, token); err != nil {
			return nil, &provider.GrantError{
				Provider: p.def.Name,
				Cause:    err,
				Hint:     "Ensure your token is valid and has appropriate permissions",
			}
		}
		fmt.Println("Token validated successfully")
	}

	return &provider.Credential{
		Provider:  p.def.Name,
		Token:     token,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{provider.MetaKeyTokenSource: source},
	}, nil
}

// validateToken validates a token against the configured endpoint.
func (p *ConfigProvider) validateToken(ctx context.Context, token string) error {
	v := p.def.Validate

	method := v.Method
	if method == "" {
		method = "GET"
	}

	header := v.Header
	if header == "" {
		header = p.def.Inject.Header
	}

	prefix := v.Prefix
	if prefix == "" && v.Header == "" {
		// Only inherit inject prefix when header is also inherited
		prefix = p.def.Inject.Prefix
	}

	client := &http.Client{}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, v.URL, nil)
	if err != nil {
		return fmt.Errorf("creating validation request: %w", err)
	}
	req.Header.Set(header, prefix+token)
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validating token: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	switch resp.StatusCode {
	case 401:
		return fmt.Errorf("invalid token (401 Unauthorized)")
	case 403:
		return fmt.Errorf("token rejected (403 Forbidden)")
	default:
		return fmt.Errorf("unexpected status validating token: %d", resp.StatusCode)
	}
}

// ConfigureProxy sets up proxy headers for this credential.
func (p *ConfigProvider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	headerValue := p.def.Inject.Prefix + cred.Token
	for _, host := range p.def.Hosts {
		proxy.SetCredentialWithGrant(host, p.def.Inject.Header, headerValue, p.def.Name)
	}
}

// ContainerEnv returns environment variables to set in the container.
func (p *ConfigProvider) ContainerEnv(cred *provider.Credential) []string {
	if p.def.ContainerEnv != "" {
		return []string{p.def.ContainerEnv + "=" + credential.ProxyInjectedPlaceholder}
	}
	return nil
}

// ContainerMounts returns mounts needed for this credential (none for config providers).
func (p *ConfigProvider) ContainerMounts(cred *provider.Credential, containerHome string) ([]provider.MountConfig, string, error) {
	return nil, "", nil
}

// Cleanup is a no-op for config providers.
func (p *ConfigProvider) Cleanup(cleanupPath string) {}

// ImpliedDependencies returns nil (no implied dependencies for config providers).
func (p *ConfigProvider) ImpliedDependencies() []string {
	return nil
}

// Description returns the provider description.
func (p *ConfigProvider) Description() string {
	return p.def.Description
}

// Source returns "builtin" or "custom" depending on origin.
func (p *ConfigProvider) Source() string {
	return p.source
}
