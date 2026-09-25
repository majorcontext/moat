package lunaroute

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Token source values stored in Credential.Metadata[provider.MetaKeyTokenSource].
const (
	SourceEnv    = "env"    // From LUNAROUTE_API_KEY
	SourceManual = "manual" // Interactive prompt entry
)

// modelsURL is the endpoint a key is validated against. A var so tests can
// point it at a local server.
var modelsURL = "https://" + GatewayHost + "/v1/models"

const keyHint = "Get a key from the LunaRoute dashboard (https://app.lunaroute.com) or with 'npx @lunaroute/cli login', then run 'moat grant lunaroute'"

// Grant acquires a LunaRoute API key from LUNAROUTE_API_KEY or an interactive
// prompt, and validates it against the gateway.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	if key := util.CheckEnvVars("LUNAROUTE_API_KEY"); key != "" {
		fmt.Println("Using key from LUNAROUTE_API_KEY environment variable")
		return p.createCredential(ctx, key, SourceEnv)
	}

	fmt.Println(`Enter a LunaRoute API key (starts with lr_).

To get a key:
  1. Visit https://app.lunaroute.com, or run: npx @lunaroute/cli login
  2. Copy the key
  3. Paste it below`)

	key, err := util.PromptForToken("API key")
	if err != nil {
		return nil, fmt.Errorf("reading key: %w", err)
	}
	if strings.TrimSpace(key) == "" {
		return nil, &provider.GrantError{
			Provider: "lunaroute",
			Cause:    fmt.Errorf("no key provided"),
			Hint:     keyHint,
		}
	}
	return p.createCredential(ctx, key, SourceManual)
}

// createCredential checks the key's shape, validates it against the gateway,
// and builds the credential.
func (p *Provider) createCredential(ctx context.Context, key, source string) (*provider.Credential, error) {
	key = strings.TrimSpace(key)
	if err := util.ValidateTokenPrefix(key, "lr_", "LunaRoute API key"); err != nil {
		return nil, &provider.GrantError{Provider: "lunaroute", Cause: err, Hint: keyHint}
	}

	fmt.Println("Validating key...")
	if err := validateKey(ctx, modelsURL, key); err != nil {
		return nil, &provider.GrantError{Provider: "lunaroute", Cause: err, Hint: keyHint}
	}
	fmt.Println("Key validated successfully")

	return &provider.Credential{
		Provider:  "lunaroute",
		Token:     key,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{provider.MetaKeyTokenSource: source},
	}, nil
}

// validateKey lists models with the key. A rejected key, an unexpected status,
// and a network failure are reported differently: only the first means the
// key itself is wrong.
func validateKey(ctx context.Context, url, key string) error {
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("User-Agent", "moat")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach LunaRoute to validate the key: %w", err)
	}
	defer func() {
		io.Copy(io.Discard, resp.Body) //nolint:errcheck // drain for connection reuse
		resp.Body.Close()
	}()

	switch resp.StatusCode {
	case http.StatusOK:
		return nil
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("LunaRoute rejected the key (HTTP %d) — it may be revoked or mistyped", resp.StatusCode)
	default:
		return fmt.Errorf("unexpected status %d validating the key against %s", resp.StatusCode, url)
	}
}
