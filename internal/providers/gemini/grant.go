package gemini

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Grant acquires Gemini credentials interactively or from environment.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Check for GEMINI_API_KEY in environment
	if envKey := os.Getenv("GEMINI_API_KEY"); envKey != "" {
		fmt.Println("Using API key from GEMINI_API_KEY environment variable")
		return grantViaAPIKey(ctx, envKey)
	}

	// Check for existing Gemini CLI credentials
	cliCreds := &CLICredentials{}
	hasOAuth := cliCreds.HasCredentials()

	if hasOAuth {
		// Offer choice between OAuth import and API key
		fmt.Println("Choose authentication method:")
		fmt.Println()
		fmt.Println("  1. Import Gemini CLI credentials (recommended)")
		fmt.Println("     Use OAuth tokens from your local Gemini CLI installation.")
		fmt.Println("     Refresh tokens are stored for automatic access token renewal.")
		fmt.Println()
		fmt.Println("  2. Gemini API key")
		fmt.Println("     Use an API key from aistudio.google.com/apikey")
		fmt.Println()

		reader := bufio.NewReader(os.Stdin)
		fmt.Print("Enter choice [1 or 2]: ")
		choice, err := reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading choice: %w", err)
		}
		choice = strings.TrimSpace(choice)

		switch choice {
		case "1":
			return grantViaExistingCreds(ctx, cliCreds)
		case "2":
			return grantViaPromptedAPIKey(ctx)
		default:
			return nil, fmt.Errorf("invalid choice %q: enter 1 or 2", choice)
		}
	}

	// No OAuth credentials found — go straight to API key
	fmt.Println("Tip: Install Gemini CLI for OAuth authentication:")
	fmt.Println("  npm install -g @google/gemini-cli && gemini")
	fmt.Println()
	return grantViaPromptedAPIKey(ctx)
}

// grantViaAPIKey validates and creates a credential from an API key.
func grantViaAPIKey(ctx context.Context, apiKey string) (*provider.Credential, error) {
	auth := &Auth{}

	fmt.Println("\nValidating API key...")
	validateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := auth.ValidateKey(validateCtx, apiKey); err != nil {
		return nil, fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")

	return auth.CreateAPIKeyCredential(apiKey), nil
}

// grantViaPromptedAPIKey prompts for an API key and validates it.
func grantViaPromptedAPIKey(ctx context.Context) (*provider.Credential, error) {
	auth := &Auth{}
	apiKey, err := auth.PromptForAPIKey()
	if err != nil {
		return nil, err
	}
	return grantViaAPIKey(ctx, apiKey)
}

// grantViaExistingCreds imports OAuth credentials from Gemini CLI.
func grantViaExistingCreds(ctx context.Context, cliCreds *CLICredentials) (*provider.Credential, error) {
	token, err := cliCreds.GetCredentials()
	if err != nil {
		return nil, fmt.Errorf("reading Gemini CLI credentials: %w", err)
	}

	fmt.Println()
	fmt.Println("Found Gemini CLI credentials.")
	fmt.Printf("  Expires: %s\n", token.ExpiresAtTime().Format(time.RFC3339))

	// Validate the refresh token by performing a real refresh
	fmt.Println("\nValidating refresh token...")
	refresher := &TokenRefresher{}
	refreshCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	result, err := refresher.Refresh(refreshCtx, token.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("validating refresh token: %w\n\nTry re-authenticating: gemini", err)
	}
	fmt.Println("Refresh token is valid.")

	// Use the freshly refreshed access token
	auth := &Auth{}
	return auth.CreateOAuthCredential(result.AccessToken, token.RefreshToken, result.ExpiresAt), nil
}

// HasCredential returns true if a Gemini credential exists in the store.
func HasCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	cred, err := store.Get(credential.ProviderGemini)
	return err == nil && cred != nil
}
