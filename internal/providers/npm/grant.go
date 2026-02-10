package npm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
)

// ctxKeyHost is the context key for the --host flag.
type ctxKeyHost struct{}

// WithGrantOptions returns a context with npm grant options.
func WithGrantOptions(ctx context.Context, host string) context.Context {
	return context.WithValue(ctx, ctxKeyHost{}, host)
}

// Grant acquires npm credentials by discovering registries or prompting for a specific host.
//
// When called without --host: discovers registries from .npmrc and NPM_TOKEN env var.
// When called with --host: prompts for token and scopes for a specific registry.
func (p *Provider) Grant(ctx context.Context) (*provider.Credential, error) {
	host, _ := ctx.Value(ctxKeyHost{}).(string)

	if host != "" {
		return p.grantSpecificHost(ctx, host)
	}
	return p.grantDiscover(ctx)
}

// grantDiscover auto-discovers registries from .npmrc and environment.
func (p *Provider) grantDiscover(ctx context.Context) (*provider.Credential, error) {
	var entries []RegistryEntry

	// Check .npmrc
	npmrcEntries, _ := discoverFromNpmrc()
	entries = append(entries, npmrcEntries...)

	// Check NPM_TOKEN env var — fill in empty tokens from env var references
	// (e.g., .npmrc has //registry.npmjs.org/:_authToken=${NPM_TOKEN})
	if envToken := os.Getenv("NPM_TOKEN"); envToken != "" {
		filled := false
		for i, e := range entries {
			if e.Host == DefaultRegistry && e.Token == "" {
				entries[i].Token = envToken
				entries[i].TokenSource = SourceEnv
				filled = true
				break
			}
		}
		if !filled {
			// No default registry entry at all — add one from env
			hasDefault := false
			for _, e := range entries {
				if e.Host == DefaultRegistry {
					hasDefault = true
					break
				}
			}
			if !hasDefault {
				entries = append(entries, RegistryEntry{
					Host:        DefaultRegistry,
					Token:       envToken,
					TokenSource: SourceEnv,
				})
			}
		}
	}

	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Println("Choose authentication method:")
		fmt.Println()

		optNum := 1
		importOpt := 0
		if len(entries) > 0 {
			importOpt = optNum
			fmt.Printf("  %d. Import from .npmrc / environment\n", optNum)
			fmt.Print("     Found registries: ")
			descs := make([]string, 0, len(entries))
			for _, entry := range entries {
				desc := entry.Host
				if entry.Host == DefaultRegistry {
					desc += " (default)"
				}
				if len(entry.Scopes) > 0 {
					desc += " (" + strings.Join(entry.Scopes, ", ") + ")"
				}
				descs = append(descs, desc)
			}
			fmt.Println(strings.Join(descs, ", "))
			if len(entries) > 1 {
				fmt.Println("     To import a single registry, use: moat grant npm --host=<host>")
			}
			fmt.Println()
			optNum++
		}

		manualOpt := optNum
		fmt.Printf("  %d. Enter token manually\n", optNum)
		fmt.Println("     Paste an npm access token for a registry.")
		fmt.Println()

		maxOpt := optNum
		fmt.Printf("Enter choice [1-%d]: ", maxOpt)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(response)

		if response == "" {
			response = "1"
		}

		switch response {
		case fmt.Sprint(importOpt):
			if importOpt == 0 {
				fmt.Printf("Invalid choice: %s\n\n", response)
				continue
			}
			return p.grantImportEntries(ctx, entries)

		case fmt.Sprint(manualOpt):
			fmt.Println()
			return p.grantManualToken(ctx, DefaultRegistry)

		default:
			fmt.Printf("Invalid choice: %s\n\n", response)
			continue
		}
	}
}

// grantImportEntries validates and saves discovered registry entries.
func (p *Provider) grantImportEntries(ctx context.Context, entries []RegistryEntry) (*provider.Credential, error) {
	fmt.Println("Validating...")
	for i, entry := range entries {
		if entry.Token == "" {
			fmt.Printf("  ✗ %s — no token (skipped)\n", entry.Host)
			continue
		}
		username, validateErr := validateNpmToken(ctx, entry.Host, entry.Token)
		if validateErr != nil {
			return nil, &provider.GrantError{
				Provider: "npm",
				Cause:    fmt.Errorf("validation failed for %s: %w", entry.Host, validateErr),
				Hint:     "Check that your npm token is valid and not expired",
			}
		}
		fmt.Printf("  ✓ %s — authenticated as %q\n", entries[i].Host, username)
	}

	// Filter out entries with no token
	var validEntries []RegistryEntry
	for _, entry := range entries {
		if entry.Token != "" {
			validEntries = append(validEntries, entry)
		}
	}

	if len(validEntries) == 0 {
		return nil, &provider.GrantError{
			Provider: "npm",
			Cause:    fmt.Errorf("no valid tokens found"),
			Hint:     "Run 'npm login' to authenticate, then retry 'moat grant npm'",
		}
	}

	return createCredential(validEntries)
}

// grantSpecificHost adds a credential for a specific registry host.
func (p *Provider) grantSpecificHost(ctx context.Context, host string) (*provider.Credential, error) {
	// Try to find token from .npmrc or environment
	var discoveredToken string
	var discoveredSource string
	npmrcEntries, _ := discoverFromNpmrc()
	for _, entry := range npmrcEntries {
		if entry.Host == host {
			discoveredToken = entry.Token
			discoveredSource = SourceNpmrc
			break
		}
	}
	if discoveredToken == "" && host == DefaultRegistry {
		if envToken := os.Getenv("NPM_TOKEN"); envToken != "" {
			discoveredToken = envToken
			discoveredSource = SourceEnv
		}
	}

	reader := bufio.NewReader(os.Stdin)
	var token, source string

	if discoveredToken != "" {
		sourceName := ".npmrc"
		if discoveredSource == SourceEnv {
			sourceName = "NPM_TOKEN"
		}

		for {
			fmt.Printf("Choose token source for %s:\n\n", host)
			fmt.Printf("  1. Use token from %s\n", sourceName)
			fmt.Println("  2. Enter token manually")
			fmt.Println()
			fmt.Print("Enter choice [1-2]: ")
			response, _ := reader.ReadString('\n')
			response = strings.TrimSpace(response)
			if response == "" {
				response = "1"
			}

			switch response {
			case "1":
				token = discoveredToken
				source = discoveredSource
			case "2":
				fmt.Println()
				fmt.Printf("Enter npm token for %s\n", host)
				var err error
				token, err = util.PromptForToken("Token")
				if err != nil {
					return nil, fmt.Errorf("reading token: %w", err)
				}
				if token == "" {
					return nil, &provider.GrantError{
						Provider: "npm",
						Cause:    fmt.Errorf("no token provided"),
						Hint:     "Run 'moat grant npm --host=" + host + "' and enter a valid npm token",
					}
				}
				source = SourceManual
			default:
				fmt.Printf("Invalid choice: %s\n\n", response)
				continue
			}
			break
		}
	} else {
		fmt.Printf("Enter npm token for %s\n", host)
		var err error
		token, err = util.PromptForToken("Token")
		if err != nil {
			return nil, fmt.Errorf("reading token: %w", err)
		}
		if token == "" {
			return nil, &provider.GrantError{
				Provider: "npm",
				Cause:    fmt.Errorf("no token provided"),
				Hint:     "Run 'moat grant npm --host=" + host + "' and enter a valid npm token",
			}
		}
		source = SourceManual
	}

	// Ask for scopes
	fmt.Print("What scopes use this registry? (comma-separated, e.g. @myorg,@other; leave empty for none): ")
	scopeInput, _ := reader.ReadString('\n')
	scopeInput = strings.TrimSpace(scopeInput)

	var scopes []string
	if scopeInput != "" {
		for _, s := range strings.Split(scopeInput, ",") {
			s = strings.TrimSpace(s)
			if s != "" {
				if !strings.HasPrefix(s, "@") {
					s = "@" + s
				}
				scopes = append(scopes, s)
			}
		}
	}

	// Validate
	fmt.Print("Validating... ")
	username, err := validateNpmToken(ctx, host, token)
	if err != nil {
		fmt.Println()
		return nil, &provider.GrantError{
			Provider: "npm",
			Cause:    fmt.Errorf("validation failed for %s: %w", host, err),
			Hint:     "Check that your npm token is valid and not expired",
		}
	}
	fmt.Printf("✓ authenticated as %q\n", username)

	newEntry := RegistryEntry{
		Host:        host,
		Token:       token,
		Scopes:      scopes,
		TokenSource: source,
	}

	// Load existing entries and merge
	entries, _ := loadExistingEntries()
	entries = MergeEntry(entries, newEntry)

	return createCredential(entries)
}

// grantManualToken prompts for a token for a specific host, validates it, and saves.
func (p *Provider) grantManualToken(ctx context.Context, host string) (*provider.Credential, error) {
	fmt.Printf("Enter npm token for %s\n", host)
	token, err := util.PromptForToken("Token")
	if err != nil {
		return nil, fmt.Errorf("reading token: %w", err)
	}
	if token == "" {
		return nil, &provider.GrantError{
			Provider: "npm",
			Cause:    fmt.Errorf("no token provided"),
			Hint:     "Run 'moat grant npm --host=" + host + "' and enter a valid npm token",
		}
	}

	fmt.Print("Validating... ")
	username, err := validateNpmToken(ctx, host, token)
	if err != nil {
		fmt.Println()
		return nil, &provider.GrantError{
			Provider: "npm",
			Cause:    fmt.Errorf("validation failed for %s: %w", host, err),
			Hint:     "Check that your npm token is valid and not expired",
		}
	}
	fmt.Printf("✓ authenticated as %q\n", username)

	newEntry := RegistryEntry{
		Host:        host,
		Token:       token,
		TokenSource: SourceManual,
	}

	// Load existing entries and merge
	entries, _ := loadExistingEntries()
	entries = MergeEntry(entries, newEntry)

	return createCredential(entries)
}

// discoverFromNpmrc reads the user's .npmrc file.
func discoverFromNpmrc() ([]RegistryEntry, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}

	npmrcPath := filepath.Join(homeDir, ".npmrc")
	f, err := os.Open(npmrcPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return ParseNpmrc(f)
}

// validateNpmToken validates an npm token by calling the registry's /-/whoami endpoint.
func validateNpmToken(ctx context.Context, host, token string) (string, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	whoamiURL := fmt.Sprintf("https://%s/-/whoami", host)
	req, err := http.NewRequestWithContext(reqCtx, "GET", whoamiURL, nil)
	if err != nil {
		return "", fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "moat")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("contacting registry: %w", err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case 200:
		var result struct {
			Username string `json:"username"`
		}
		if decodeErr := json.NewDecoder(resp.Body).Decode(&result); decodeErr != nil {
			return "", fmt.Errorf("parsing whoami response: %w", decodeErr)
		}
		return result.Username, nil
	case 401:
		return "", fmt.Errorf("invalid token (401 Unauthorized)")
	case 403:
		return "", fmt.Errorf("token rejected (403 Forbidden)")
	default:
		return "", fmt.Errorf("unexpected status from registry: %d", resp.StatusCode)
	}
}

// loadExistingEntries loads existing npm credential entries from the credential store.
func loadExistingEntries() ([]RegistryEntry, error) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return nil, err
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return nil, err
	}
	cred, err := store.Get(credential.ProviderNpm)
	if err != nil {
		return nil, err
	}
	return UnmarshalEntries(cred.Token)
}

// MergeEntry merges a new entry into the existing entries, replacing any with the same host.
func MergeEntry(entries []RegistryEntry, newEntry RegistryEntry) []RegistryEntry {
	for i, e := range entries {
		if e.Host == newEntry.Host {
			entries[i] = newEntry
			return entries
		}
	}
	return append(entries, newEntry)
}

// createCredential creates a provider.Credential from registry entries.
func createCredential(entries []RegistryEntry) (*provider.Credential, error) {
	token, err := MarshalEntries(entries)
	if err != nil {
		return nil, err
	}

	return &provider.Credential{
		Provider:  "npm",
		Token:     token,
		CreatedAt: time.Now(),
	}, nil
}
