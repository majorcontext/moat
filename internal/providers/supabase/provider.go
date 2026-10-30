// Package supabase provides a Supabase Management API grant with optional
// import of a host Supabase CLI login.
package supabase

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/majorcontext/gatekeeper/proxy"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
	"github.com/majorcontext/moat/internal/providers/configprovider"
	"github.com/zalando/go-keyring"
	"golang.org/x/term"
)

const (
	name           = "supabase"
	keyringService = "Supabase CLI"
	legacyAccount  = "access-token"
)

// Provider reuses the config provider's proxy and container behavior while
// adding an explicit host-login import choice to Grant.
type Provider struct{ *configprovider.ConfigProvider }

var _ provider.CredentialProvider = (*Provider)(nil)

func init() {
	provider.Register(New())
	proxy.RegisterGrantHosts(name, []string{"api.supabase.com"})
	credential.RegisterDynamicProvider(credential.Provider(name))
}

func New() *Provider {
	return &Provider{ConfigProvider: configprovider.NewConfigProvider(configprovider.ProviderDef{
		Name:         name,
		Description:  "Supabase Management API personal access token",
		Hosts:        []string{"api.supabase.com"},
		Inject:       configprovider.InjectConfig{Header: "Authorization", Prefix: "Bearer "},
		SourceEnv:    []string{"SUPABASE_ACCESS_TOKEN"},
		ContainerEnv: "SUPABASE_ACCESS_TOKEN",
		Prompt:       "Create a scoped personal access token at https://supabase.com/dashboard/account/tokens\n",
	}, "builtin")}
}

// ContainerEnv supplies the Supabase CLI with a fresh token-shaped placeholder.
// Header injection does not need this value again, so it can be random per run.
func (p *Provider) ContainerEnv(cred *provider.Credential) []string {
	if cred == nil {
		// Without a grant, no proxy credential can back the placeholder.
		return nil
	}
	var placeholder [20]byte
	if _, err := rand.Read(placeholder[:]); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return []string{"SUPABASE_ACCESS_TOKEN=sbp_" + hex.EncodeToString(placeholder[:])}
}

// ImpliedDependencies installs the Supabase CLI for runs using this grant.
func (p *Provider) ImpliedDependencies() []string { return []string{"supabase-cli"} }

func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	// Keep this environment check in sync with SourceEnv in New; the generic
	// provider owns environment and paste acquisition.
	if os.Getenv("SUPABASE_ACCESS_TOKEN") != "" {
		return p.ConfigProvider.Grant(ctx)
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return p.ConfigProvider.Grant(ctx)
	}

	confirmed, err := util.Confirm("Import your host Supabase CLI login?")
	if err != nil {
		return nil, fmt.Errorf("reading import choice: %w", err)
	}
	return grantAfterChoice(ctx, confirmed, hostToken, p.ConfigProvider.Grant)
}

func grantAfterChoice(ctx context.Context, confirmed bool, load func() (string, error), prompt func(context.Context) (*provider.Credential, error)) (*provider.Credential, error) {
	if confirmed {
		token, err := load()
		if err == nil && token != "" {
			fmt.Println("Using token from host Supabase CLI login")
			return &provider.Credential{
				Provider: name, Token: token, CreatedAt: time.Now(),
				Metadata: map[string]string{provider.MetaKeyTokenSource: "cli"},
			}, nil
		}
		if err != nil {
			fmt.Printf("Host Supabase CLI login unavailable: %v\n", err)
		} else {
			fmt.Println("No host Supabase CLI login found. Run 'supabase login' on the host, or paste a token below.")
		}
	}
	return prompt(ctx)
}

func hostToken() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	cliHome := os.Getenv("SUPABASE_HOME")
	if cliHome == "" {
		cliHome = filepath.Join(home, ".supabase")
	}
	return readHostToken(cliHome, keyring.Get)
}

// readHostToken follows the Supabase CLI's default-profile, legacy-key and
// fallback-file lookup. Keyring access only occurs after the user opts in.
func readHostToken(cliHome string, get func(string, string) (string, error)) (string, error) {
	profile := "supabase"
	if data, err := os.ReadFile(filepath.Join(cliHome, "profile")); err == nil {
		if active := strings.TrimSpace(string(data)); active != "" {
			profile = active
		}
	}
	var keyringErr error
	if os.Getenv("SUPABASE_NO_KEYRING") != "1" {
		for _, account := range []string{profile, legacyAccount} {
			token, err := get(keyringService, account)
			if err == nil && strings.TrimSpace(token) != "" {
				return strings.TrimSpace(token), nil
			}
			if err != nil && !errors.Is(err, keyring.ErrNotFound) && !errors.Is(err, keyring.ErrUnsupportedPlatform) && keyringErr == nil {
				keyringErr = err
			}
		}
	}
	data, err := os.ReadFile(filepath.Join(cliHome, legacyAccount))
	if err == nil {
		return strings.TrimSpace(string(data)), nil
	}
	if os.IsNotExist(err) {
		return "", keyringErr
	}
	return "", err
}
