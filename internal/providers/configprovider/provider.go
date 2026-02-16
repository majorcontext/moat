package configprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// tokenSubPlaceholder generates a per-credential placeholder for token substitution.
// The placeholder is derived from a hash of the real token, so it's deterministic
// (ContainerEnv and ConfigureProxy produce the same value) but unpredictable
// without knowing the token, preventing container code from crafting matching URLs.
func tokenSubPlaceholder(token string) string {
	h := sha256.Sum256([]byte("moat-token-sub:" + token))
	return "moat-" + hex.EncodeToString(h[:8])
}

// maxScrubBodySize is the maximum response body size for token scrubbing.
// Larger responses are passed through unscrubbed to avoid memory issues.
const maxScrubBodySize = 512 * 1024

// buildResponseScrubber creates a response transformer that replaces the real token
// with the placeholder in response bodies. This prevents APIs from leaking the token
// back to the container (e.g., Telegram's getWebhookInfo can return URLs containing
// the bot token).
func buildResponseScrubber(realToken, placeholder string) credential.ResponseTransformer {
	return func(reqInterface, respInterface interface{}) (interface{}, bool) {
		resp, ok := respInterface.(*http.Response)
		if !ok || resp.Body == nil {
			return respInterface, false
		}

		// Only scrub text-based responses
		ct := resp.Header.Get("Content-Type")
		if ct != "" && !strings.Contains(ct, "json") && !strings.Contains(ct, "text") {
			return resp, false
		}

		// Skip bodies that are known to exceed the scrub limit.
		// For unknown sizes (ContentLength == -1, e.g. chunked encoding),
		// we still read up to the limit so tokens aren't leaked via chunked responses.
		if resp.ContentLength > maxScrubBodySize {
			return resp, false
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, maxScrubBodySize))
		resp.Body.Close()
		if err != nil {
			resp.Body = io.NopCloser(bytes.NewReader(body))
			return resp, false
		}

		tokenBytes := []byte(realToken)
		scrubbed := bytes.ReplaceAll(body, tokenBytes, []byte(placeholder))
		if !bytes.Equal(body, scrubbed) {
			log.Debug("scrubbed credential from response body",
				"subsystem", "configprovider",
				"placeholder", placeholder,
				"bodyLen", len(body),
				"occurrences", bytes.Count(body, tokenBytes),
			)
			resp.Body = io.NopCloser(bytes.NewReader(scrubbed))
			resp.ContentLength = int64(len(scrubbed))
			return resp, true
		}

		resp.Body = io.NopCloser(bytes.NewReader(body))
		return resp, false
	}
}

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
// If the validation URL contains ${token}, the token is substituted into the URL
// and no credential header is set. This supports APIs like Telegram Bot API where
// the token is embedded in the URL path rather than sent as a header.
func (p *ConfigProvider) validateToken(ctx context.Context, token string) error {
	v := p.def.Validate

	method := v.Method
	if method == "" {
		method = "GET"
	}

	// Check if the URL contains a token placeholder
	url := v.URL
	tokenInURL := strings.Contains(url, "${token}")
	if tokenInURL {
		url = strings.ReplaceAll(url, "${token}", token)
	}

	client := &http.Client{}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, method, url, nil)
	if err != nil {
		return fmt.Errorf("creating validation request: %w", err)
	}

	// Only set credential header if the token is not in the URL
	if !tokenInURL {
		header := v.Header
		if header == "" {
			header = p.def.Inject.Header
		}

		prefix := v.Prefix
		if prefix == "" && v.Header == "" {
			// Only inherit inject prefix when header is also inherited
			prefix = p.def.Inject.Prefix
		}

		req.Header.Set(header, prefix+token)
	}
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
// When inject.header is configured, injects the credential as an HTTP header.
// Otherwise, sets up token substitution — the proxy replaces the placeholder
// token in URL paths, Authorization headers, and request bodies. This is used
// for APIs like Telegram Bot API where the token is embedded in the URL path.
func (p *ConfigProvider) ConfigureProxy(proxy provider.ProxyConfigurer, cred *provider.Credential) {
	if p.def.HasHeaderInjection() {
		headerValue := p.def.Inject.Prefix + cred.Token
		for _, host := range p.def.Hosts {
			proxy.SetCredentialWithGrant(host, p.def.Inject.Header, headerValue, p.def.Name)
		}
		return
	}
	// No header injection — set up token substitution so the proxy replaces
	// the placeholder value in URL paths, headers, and bodies.
	placeholder := tokenSubPlaceholder(cred.Token)
	for _, host := range p.def.Hosts {
		proxy.SetTokenSubstitution(host, placeholder, cred.Token)
	}

	// Scrub the real token from response bodies before they reach the container.
	// Some APIs may echo back URLs or tokens in responses (e.g., Telegram's
	// getWebhookInfo returns webhook URLs that may contain the bot token).
	// Without this, the credential could leak into the container via responses.
	scrubber := buildResponseScrubber(cred.Token, placeholder)
	for _, host := range p.def.Hosts {
		proxy.AddResponseTransformer(host, scrubber)
	}
}

// ContainerEnv returns environment variables to set in the container.
// The env var is always set to a placeholder — the real token is injected by
// the proxy at the network layer (either via header injection or token substitution).
func (p *ConfigProvider) ContainerEnv(cred *provider.Credential) []string {
	if p.def.ContainerEnv == "" {
		return nil
	}
	placeholder := credential.ProxyInjectedPlaceholder
	if !p.def.HasHeaderInjection() {
		// Token substitution mode: use a per-credential placeholder derived from
		// a hash of the token, so container code can't predict or craft matching URLs.
		placeholder = tokenSubPlaceholder(cred.Token)
	}
	return []string{p.def.ContainerEnv + "=" + placeholder}
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
