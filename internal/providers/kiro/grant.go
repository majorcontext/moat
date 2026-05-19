package kiro

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// Grant handles the Kiro token grant. It reads KIRO_API_KEY from the
// environment, falling back to an interactive prompt. The token is stored
// as-is and validated only by the upstream API at run time (no local
// validation endpoint — see spec).
type Grant struct {
	// readToken reads a token interactively. Overridable in tests.
	readToken func() (string, error)
}

// NewGrant creates a Grant with the default interactive reader. The default
// uses the shared util.PromptForToken helper, which hides typed input on a
// TTY and falls back to a line read for piped input.
func NewGrant() *Grant {
	return &Grant{
		readToken: func() (string, error) {
			return util.PromptForToken("Enter your Kiro token")
		},
	}
}

// Execute performs the grant. Returns a provider.Credential; the CLI wrapper
// persists it to the credential store.
func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	token := os.Getenv("KIRO_API_KEY")
	if token != "" {
		fmt.Println("Using token from KIRO_API_KEY environment variable")
	} else {
		var err error
		token, err = g.readToken()
		if err != nil {
			return nil, fmt.Errorf("reading Kiro token: %w", err)
		}
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return nil, fmt.Errorf("kiro token is empty")
	}

	return &provider.Credential{
		Provider:  "kiro",
		Token:     token,
		CreatedAt: time.Now(),
	}, nil
}

// HasCredential reports whether a Kiro credential exists in the store.
func HasCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	cred, err := store.Get(credential.ProviderKiro)
	return err == nil && cred != nil
}
