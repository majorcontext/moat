package graphite

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Token source values stored in Credential.Metadata[provider.MetaKeyTokenSource].
const (
	SourceEnv    = "env"    // From GRAPHITE_TOKEN or GT_TOKEN env var
	SourceManual = "manual" // Interactive prompt entry
)

// Grant acquires Graphite credentials interactively or from environment.
//
// Token acquisition order:
//  1. GRAPHITE_TOKEN or GT_TOKEN environment variable
//  2. Interactive token prompt
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Priority 1: Environment variable
	if token, name := util.CheckEnvVarWithName("GRAPHITE_TOKEN", "GT_TOKEN"); token != "" {
		fmt.Printf("Using token from %s environment variable\n", name)
		return p.validateAndCreateCredential(ctx, token, SourceEnv)
	}

	// Priority 2: Interactive prompt
	fmt.Println(`Enter a Graphite auth token.

To get your token:
  1. Visit https://app.graphite.com/activate
  2. Sign in and copy the token
  3. Paste it below`)

	token, err := util.PromptForToken("Token")
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	if token == "" {
		return nil, &provider.GrantError{
			Provider: "graphite",
			Cause:    fmt.Errorf("no token provided"),
			Hint:     "Run 'moat grant graphite' and enter a valid Graphite token",
		}
	}

	return p.validateAndCreateCredential(ctx, token, SourceManual)
}

// validateAndCreateCredential validates the token and creates a credential.
func (p *Provider) validateAndCreateCredential(ctx context.Context, token, source string) (*provider.Credential, error) {
	fmt.Println("Validating token...")

	if err := validateGraphiteToken(ctx, token); err != nil {
		return nil, &provider.GrantError{
			Provider: "graphite",
			Cause:    err,
			Hint:     "Get a valid token at https://app.graphite.com/activate",
		}
	}

	fmt.Println("Token validated successfully")

	return &provider.Credential{
		Provider:  "graphite",
		Token:     token,
		CreatedAt: time.Now(),
		Metadata:  map[string]string{provider.MetaKeyTokenSource: source},
	}, nil
}

// validateGraphiteToken validates the token by calling the Graphite check-auth endpoint.
func validateGraphiteToken(ctx context.Context, token string) error {
	client := &http.Client{}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "POST", "https://api.graphite.com/v1/graphite/check-auth", strings.NewReader("{}"))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		return nil
	case 401:
		return fmt.Errorf("invalid token (401 Unauthorized)")
	case 403:
		return fmt.Errorf("token rejected (403 Forbidden)")
	default:
		return fmt.Errorf("unexpected status validating token: %d", resp.StatusCode)
	}
}
