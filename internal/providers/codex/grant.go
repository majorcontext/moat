package codex

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

// Grant handles the OpenAI API key grant process.
// It checks for an existing key in the environment, validates it,
// and stores it in the credential store.
type Grant struct {
	auth *credential.OpenAIAuth
}

// NewGrant creates a new Grant instance.
func NewGrant() *Grant {
	return &Grant{
		auth: &credential.OpenAIAuth{},
	}
}

// Execute performs the grant process.
// It checks for OPENAI_API_KEY in the environment first,
// then prompts the user if not found.
// Returns a provider.Credential that can be used for proxy configuration.
func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	var apiKey string

	// Check environment variable first
	if envKey := os.Getenv("OPENAI_API_KEY"); envKey != "" {
		apiKey = envKey
		fmt.Println("Using API key from OPENAI_API_KEY environment variable")
	} else {
		// Prompt for API key
		var err error
		apiKey, err = g.auth.PromptForAPIKey()
		if err != nil {
			return nil, fmt.Errorf("reading API key: %w", err)
		}
	}

	// Validate the key
	fmt.Println("\nValidating API key...")
	validateCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := g.auth.ValidateKey(validateCtx, apiKey); err != nil {
		return nil, fmt.Errorf("validating API key: %w", err)
	}
	fmt.Println("API key is valid.")

	// Return as provider.Credential for the caller
	// The CLI wrapper handles saving to the credential store
	return &provider.Credential{
		Provider:  "openai",
		Token:     apiKey,
		CreatedAt: time.Now(),
	}, nil
}

// HasCredential checks if an OpenAI credential exists in the store.
func HasCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}

	cred, err := store.Get(credential.ProviderOpenAI)
	return err == nil && cred != nil
}
