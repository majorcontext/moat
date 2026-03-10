package meta

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

const (
	graphAPIBase    = "https://graph.facebook.com"
	graphAPIVersion = "v21.0"
)

// Grant acquires Meta credentials interactively or from environment.
//
// Token acquisition:
//  1. META_ACCESS_TOKEN environment variable
//  2. Interactive prompt
//
// Optionally collects META_APP_ID and META_APP_SECRET for token refresh.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	var token string

	// Priority 1: Environment variable
	if envToken, name := util.CheckEnvVarWithName("META_ACCESS_TOKEN"); envToken != "" {
		fmt.Printf("Using token from %s environment variable\n", name)
		token = envToken
	} else {
		// Priority 2: Interactive prompt
		fmt.Println(`Enter a Meta access token.

To create one:
  1. Go to https://developers.facebook.com/tools/explorer/
  2. Select your app and generate a token with the required permissions
  3. For long-lived server use, create a System User token in Business Settings`)

		var err error
		token, err = util.PromptForToken("Access token")
		if err != nil {
			return nil, fmt.Errorf("reading token: %w", err)
		}
		if token == "" {
			return nil, &provider.GrantError{
				Provider: "meta",
				Cause:    fmt.Errorf("no token provided"),
				Hint:     "Run 'moat grant meta' and enter a valid Meta access token",
			}
		}
	}

	// Validate token
	name, err := validateMetaToken(ctx, token, graphAPIBase)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "meta",
			Cause:    err,
			Hint:     "Ensure your token is valid and has not expired",
		}
	}
	fmt.Printf("Authenticated as: %s\n", name)

	// Collect optional app credentials for refresh
	metadata := map[string]string{}

	appID := util.CheckEnvVars("META_APP_ID")
	appSecret := util.CheckEnvVars("META_APP_SECRET")

	if appID != "" && appSecret != "" {
		fmt.Println("Found META_APP_ID and META_APP_SECRET for token refresh")
		metadata[MetaKeyAppID] = appID
		metadata[MetaKeyAppSecret] = appSecret
	} else {
		fmt.Println("\nOptional: provide app ID and app secret to enable automatic token refresh.")
		fmt.Println("Press Enter to skip.")

		if appID == "" {
			var promptErr error
			appID, promptErr = util.PromptForToken("App ID (or Enter to skip)")
			if promptErr != nil {
				return nil, fmt.Errorf("reading app ID: %w", promptErr)
			}
		}

		if appID != "" && appSecret == "" {
			var promptErr error
			appSecret, promptErr = util.PromptForToken("App secret")
			if promptErr != nil {
				return nil, fmt.Errorf("reading app secret: %w", promptErr)
			}
		}

		if appID != "" && appSecret != "" {
			metadata[MetaKeyAppID] = appID
			metadata[MetaKeyAppSecret] = appSecret
			fmt.Println("Token refresh enabled")
		} else {
			fmt.Println("Token refresh disabled (no app credentials)")
		}
	}

	return &provider.Credential{
		Provider:  "meta",
		Token:     token,
		CreatedAt: time.Now(),
		Metadata:  metadata,
	}, nil
}

// validateMetaToken validates the token by calling the Graph API /me endpoint.
// baseURL allows overriding for tests.
func validateMetaToken(ctx context.Context, token, baseURL string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, "GET", baseURL+"/"+graphAPIVersion+"/me?fields=id,name", nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("validating token: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var errResp struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&errResp); decodeErr == nil && errResp.Error.Message != "" {
			return "", fmt.Errorf("token validation failed (%d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return "", fmt.Errorf("token validation failed with status %d", resp.StatusCode)
	}

	var user struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if decodeErr := json.NewDecoder(resp.Body).Decode(&user); decodeErr != nil {
		return "", fmt.Errorf("parsing response: %w", decodeErr)
	}
	if user.Name == "" {
		user.Name = user.ID
	}
	return user.Name, nil
}
