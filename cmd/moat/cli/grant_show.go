package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var showToken bool

var grantShowCmd = &cobra.Command{
	Use:   "show <provider>",
	Short: "Show details of a stored credential",
	Long: `Show detailed information about a stored credential.

Displays the provider, type, source, scopes, expiration, and a redacted
token. Use --show-token to reveal the full credential value.

For SSH credentials, use the "ssh:<host>" format.

Examples:
  moat grant show github                    # Show GitHub credential details
  moat grant show github --show-token       # Reveal the full token
  moat grant show aws                       # Show AWS role configuration
  moat grant show ssh:github.com            # Show SSH key details
  moat grant show github --json             # Output as JSON
  moat grant show github --profile myproj   # Show profile credential`,
	Args: cobra.ExactArgs(1),
	RunE: runGrantShow,
}

func init() {
	grantCmd.AddCommand(grantShowCmd)
	grantShowCmd.Flags().BoolVar(&showToken, "show-token", false, "reveal the full credential value")
}

func runGrantShow(cmd *cobra.Command, args []string) error {
	providerName := args[0]

	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return fmt.Errorf("opening credential store: %w", err)
	}

	// Handle SSH credentials separately
	if strings.HasPrefix(providerName, "ssh:") {
		host := strings.TrimPrefix(providerName, "ssh:")
		if host == "" {
			return fmt.Errorf("SSH host cannot be empty\n\nUsage: moat grant show ssh:<hostname>")
		}
		return showSSHCredential(store, host)
	}

	cred, err := store.Get(credential.Provider(providerName))
	if err != nil {
		if strings.Contains(err.Error(), "credential not found") {
			return fmt.Errorf("no credential found for %s\n\nRun 'moat grant %s' to store a credential", providerName, providerName)
		}
		return err // surface decryption/permission errors directly
	}

	if jsonOut {
		return showCredentialJSON(cred)
	}

	return showCredentialTable(cred)
}

func showCredentialTable(cred *credential.Credential) error {
	fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Bold("Provider:"), cred.Provider)
	fmt.Fprintf(os.Stdout, "%s      %s\n", ui.Bold("Type:"), credType(*cred))

	if source := cred.Metadata[credential.MetaKeyTokenSource]; source != "" {
		fmt.Fprintf(os.Stdout, "%s    %s\n", ui.Bold("Source:"), source)
	}

	if len(cred.Scopes) > 0 {
		fmt.Fprintf(os.Stdout, "%s    %s\n", ui.Bold("Scopes:"), strings.Join(cred.Scopes, ", "))
	}

	// Provider-specific metadata
	showProviderMetadata(cred)

	fmt.Fprintf(os.Stdout, "%s   %s %s\n",
		ui.Bold("Granted:"),
		cred.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		ui.Dim("("+formatAge(cred.CreatedAt)+")"),
	)

	if !cred.ExpiresAt.IsZero() {
		fmt.Fprintf(os.Stdout, "%s   %s %s\n",
			ui.Bold("Expires:"),
			cred.ExpiresAt.Format("2006-01-02T15:04:05Z07:00"),
			ui.Dim("("+formatAge(cred.ExpiresAt)+")"),
		)
	} else {
		fmt.Fprintf(os.Stdout, "%s   %s\n", ui.Bold("Expires:"), ui.Dim("never"))
	}

	// AWS token is the role ARN, already shown as "Role" above
	if cred.Provider != credential.ProviderAWS {
		fmt.Fprintf(os.Stdout, "%s     %s\n", ui.Bold("Token:"), redactToken(cred.Token))
	}

	return nil
}

func showProviderMetadata(cred *credential.Credential) {
	if cred.Metadata == nil {
		return
	}

	switch cred.Provider {
	case credential.ProviderAWS:
		// Token is the role ARN for AWS — always safe to show
		fmt.Fprintf(os.Stdout, "%s      %s\n", ui.Bold("Role:"), cred.Token)
		if v := cred.Metadata["region"]; v != "" {
			fmt.Fprintf(os.Stdout, "%s    %s\n", ui.Bold("Region:"), v)
		}
		if v := cred.Metadata["session_duration"]; v != "" {
			fmt.Fprintf(os.Stdout, "%s   %s\n", ui.Bold("Session:"), v)
		}
		if v := cred.Metadata["profile"]; v != "" {
			fmt.Fprintf(os.Stdout, "%s   %s\n", ui.Bold("Profile:"), v)
		}
	case credential.ProviderNpm:
		showNpmRegistries(cred.Token)
	default:
		// Show auth_type if present (e.g., for openai/gemini OAuth)
		if v := cred.Metadata["auth_type"]; v != "" {
			fmt.Fprintf(os.Stdout, "%s      %s\n", ui.Bold("Auth:"), v)
		}
	}
}

func showNpmRegistries(token string) {
	var entries []json.RawMessage
	if err := json.Unmarshal([]byte(token), &entries); err != nil {
		return // single registry, nothing extra to show
	}
	// Parse each entry to extract registry URL
	for _, entry := range entries {
		var reg struct {
			Registry string `json:"registry"`
		}
		if err := json.Unmarshal(entry, &reg); err == nil && reg.Registry != "" {
			fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Bold("Registry:"), reg.Registry)
		}
	}
}

func showCredentialJSON(cred *credential.Credential) error {
	type jsonOutput struct {
		Provider  string            `json:"provider"`
		Type      string            `json:"type"`
		Source    string            `json:"source,omitempty"`
		Scopes    []string          `json:"scopes,omitempty"`
		Metadata  map[string]string `json:"metadata,omitempty"`
		GrantedAt string            `json:"granted_at"`
		ExpiresAt string            `json:"expires_at,omitempty"`
		Token     string            `json:"token,omitempty"`
	}

	out := jsonOutput{
		Provider:  string(cred.Provider),
		Type:      credType(*cred),
		Source:    cred.Metadata[credential.MetaKeyTokenSource],
		Scopes:    cred.Scopes,
		GrantedAt: cred.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}

	if !cred.ExpiresAt.IsZero() {
		out.ExpiresAt = cred.ExpiresAt.Format("2006-01-02T15:04:05Z07:00")
	}

	// Include safe metadata (exclude secrets like refresh_token, client_secret)
	out.Metadata = filterMetadata(cred.Metadata)

	// AWS token is the role ARN (not a secret) — always include it
	if cred.Provider == credential.ProviderAWS {
		out.Token = cred.Token
	} else if showToken {
		out.Token = cred.Token
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// filterMetadata returns metadata with secret values removed.
// NOTE: This denylist must be updated when new providers store secret metadata keys.
func filterMetadata(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}

	secretKeys := map[string]bool{
		"refresh_token":               true,
		"client_secret":               true,
		"client_id":                   true,
		"token_url":                   true,
		"meta_app_id":                 true,
		"meta_app_secret":             true,
		credential.MetaKeyTokenSource: true, // already shown as top-level "source"
	}

	filtered := make(map[string]string)
	for k, v := range m {
		if !secretKeys[k] {
			filtered[k] = v
		}
	}
	if len(filtered) == 0 {
		return nil
	}
	return filtered
}

func showSSHCredential(store *credential.FileStore, host string) error {
	mappings, err := store.GetSSHMappings()
	if err != nil {
		return fmt.Errorf("reading SSH mappings: %w", err)
	}

	for _, m := range mappings {
		if m.Host == host {
			if jsonOut {
				return showSSHJSON(m)
			}
			fmt.Fprintf(os.Stdout, "%s     ssh:%s\n", ui.Bold("Provider:"), m.Host)
			fmt.Fprintf(os.Stdout, "%s         key\n", ui.Bold("Type:"))
			fmt.Fprintf(os.Stdout, "%s  %s\n", ui.Bold("Fingerprint:"), m.KeyFingerprint)
			if m.KeyPath != "" {
				fmt.Fprintf(os.Stdout, "%s     %s\n", ui.Bold("Key path:"), m.KeyPath)
			}
			fmt.Fprintf(os.Stdout, "%s      %s %s\n",
				ui.Bold("Granted:"),
				m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
				ui.Dim("("+formatAge(m.CreatedAt)+")"),
			)
			return nil
		}
	}

	return fmt.Errorf("no SSH credential found for host %s\n\nRun 'moat grant ssh --host %s' to add SSH access", host, host)
}

func showSSHJSON(m credential.SSHMapping) error {
	type jsonSSH struct {
		Provider    string `json:"provider"`
		Type        string `json:"type"`
		Host        string `json:"host"`
		Fingerprint string `json:"fingerprint"`
		KeyPath     string `json:"key_path,omitempty"`
		GrantedAt   string `json:"granted_at"`
	}
	out := jsonSSH{
		Provider:    "ssh:" + m.Host,
		Type:        "key",
		Host:        m.Host,
		Fingerprint: m.KeyFingerprint,
		KeyPath:     m.KeyPath,
		GrantedAt:   m.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// redactToken returns a redacted version of a token, or the full token if --show-token is set.
func redactToken(token string) string {
	if showToken {
		return token
	}
	if len(token) == 0 {
		return ui.Dim("(empty)")
	}
	// Show last 4 characters, following AWS CLI convention
	if len(token) <= 4 {
		return ui.Dim("****")
	}
	return ui.Dim(strings.Repeat("*", 16)) + token[len(token)-4:]
}
