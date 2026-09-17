package codex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/provider"
)

type grantOptions struct {
	deviceAuth bool
}
type grantOptionsKey struct{}

func WithGrantOptions(ctx context.Context, deviceAuth bool) context.Context {
	return context.WithValue(ctx, grantOptionsKey{}, grantOptions{deviceAuth: deviceAuth})
}

// SubscriptionAuth is the encrypted payload stored for a Codex subscription.
// It is never staged into the container or sent over the daemon API.
type SubscriptionAuth struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	AccountID    string `json:"account_id"`
}

type codexLoginTokens struct {
	IDToken string `json:"id_token"`
	SubscriptionAuth
}

type codexAuthFile struct {
	AuthMode string            `json:"auth_mode"`
	Tokens   *codexLoginTokens `json:"tokens"`
}

type Grant struct{}

func NewGrant() *Grant { return &Grant{} }

// codexVersionPattern matches a bare semver triple. checkCodexVersion anchors
// it to the codex-cli line of `codex --version` rather than scanning the whole
// output, so an unrelated version number cannot be mistaken for Codex's own.
var codexVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

// Execute starts a fresh Codex login in a private one-use CODEX_HOME. It does
// not import or overwrite the user's ordinary ~/.codex/auth.json.
func (g *Grant) Execute(ctx context.Context) (*provider.Credential, error) {
	codexPath, lookupErr := exec.LookPath("codex")
	if lookupErr != nil {
		return nil, fmt.Errorf("Codex CLI is required for subscription login; install a supported codex-cli first: %w", lookupErr)
	}
	if err := checkCodexVersion(ctx, codexPath); err != nil {
		return nil, err
	}
	tmpDir, tempErr := os.MkdirTemp("", "moat-codex-login-*")
	if tempErr != nil {
		return nil, fmt.Errorf("creating private Codex login directory: %w", tempErr)
	}
	defer os.RemoveAll(tmpDir)
	if err := os.Chmod(tmpDir, 0o700); err != nil {
		return nil, fmt.Errorf("securing private Codex login directory: %w", err)
	}
	loginConfig := []byte("cli_auth_credentials_store = \"file\"\nchatgpt_base_url = \"" + subscriptionOrigin + "\"\n")
	if err := os.WriteFile(filepath.Join(tmpDir, "config.toml"), loginConfig, 0o600); err != nil {
		return nil, fmt.Errorf("writing private Codex login config: %w", err)
	}

	args := []string{"login"}
	if opts, _ := ctx.Value(grantOptionsKey{}).(grantOptions); opts.deviceAuth {
		args = append(args, "--device-auth")
	}
	cmd := exec.CommandContext(ctx, codexPath, args...)
	cmd.Env = isolatedCodexEnv(os.Environ(), tmpDir)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("Codex subscription login failed: %w", err)
	}

	authPath := filepath.Join(tmpDir, "auth.json")
	info, statErr := os.Stat(authPath)
	if statErr != nil {
		return nil, fmt.Errorf("Codex login did not produce auth.json: %w", statErr)
	}
	if info.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("Codex login produced auth.json with unsafe permissions %o", info.Mode().Perm())
	}
	authBytes, readErr := os.ReadFile(authPath)
	if readErr != nil {
		return nil, fmt.Errorf("Codex login did not produce auth.json: %w", readErr)
	}
	var authFile codexAuthFile
	if err := json.Unmarshal(authBytes, &authFile); err != nil {
		return nil, fmt.Errorf("parsing Codex login result: %w", err)
	}
	if authFile.AuthMode != "chatgpt" || authFile.Tokens == nil {
		return nil, fmt.Errorf("Codex login returned auth mode %q; Moat requires a ChatGPT subscription login", authFile.AuthMode)
	}
	if err := validateSubscriptionAuth(&authFile.Tokens.SubscriptionAuth); err != nil {
		return nil, err
	}
	for _, token := range []string{authFile.Tokens.IDToken, authFile.Tokens.AccessToken} {
		if accountID := jwtAccountID(token); accountID != "" && accountID != authFile.Tokens.AccountID {
			return nil, fmt.Errorf("Codex login returned conflicting account identities")
		}
	}
	stored, marshalErr := json.Marshal(&authFile.Tokens.SubscriptionAuth)
	if marshalErr != nil {
		return nil, fmt.Errorf("encoding Codex subscription credential: %w", marshalErr)
	}
	return &provider.Credential{
		Provider:  string(credential.ProviderCodexSubscription),
		Token:     string(stored),
		ExpiresAt: accessTokenExpiry(authFile.Tokens.AccessToken, time.Now()),
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			provider.MetaKeyTokenSource: "codex-subscription",
			"auth_mode":                 authFile.AuthMode,
		},
	}, nil
}

func isolatedCodexEnv(env []string, codexHome string) []string {
	blocked := map[string]bool{
		"CODEX_HOME": true, "OPENAI_API_KEY": true, "OPENAI_BASE_URL": true,
		"CODEX_API_KEY": true, "CODEX_BASE_URL": true,
	}
	out := make([]string, 0, len(env)+1)
	for _, item := range env {
		name, _, _ := strings.Cut(item, "=")
		if !blocked[name] && !strings.HasPrefix(name, "OPENAI_") && !strings.HasPrefix(name, "CODEX_") && !strings.HasPrefix(name, "CHATGPT_") {
			out = append(out, item)
		}
	}
	return append(out, "CODEX_HOME="+codexHome)
}

func checkCodexVersion(ctx context.Context, codexPath string) error {
	versionCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(versionCtx, codexPath, "--version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("checking Codex CLI version: %w", err)
	}
	version := parseCodexVersionOutput(string(out))
	if version == "" {
		return fmt.Errorf("could not read a Codex CLI version from %q; supported versions are 0.146.x through 0.154.x", strings.TrimSpace(string(out)))
	}
	return ValidateVersion(version)
}

// parseCodexVersionOutput extracts Codex's own version from `codex --version`,
// which prints "codex-cli <semver>".
//
// It anchors on the line Codex names itself on. Scanning the whole output for
// the first version-shaped token would let a warning line, or a bundled
// component's version, be validated as if it were Codex's — rejecting a
// supported install, or worse, accepting an unsupported one.
func parseCodexVersionOutput(out string) string {
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.Contains(strings.ToLower(line), "codex") {
			if version := versionField(line); version != "" {
				return version
			}
		}
	}
	// Fall back to a line that is nothing but a version, in case the "codex-cli"
	// prefix ever changes. Anything more ambiguous is reported as unreadable.
	for _, line := range lines {
		if fields := strings.Fields(line); len(fields) == 1 {
			if version := versionField(line); version != "" {
				return version
			}
		}
	}
	return ""
}

// versionField returns the first whitespace-separated field on the line that
// begins with a semver triple.
func versionField(line string) string {
	for _, field := range strings.Fields(line) {
		if version := codexVersionPattern.FindString(field); version != "" {
			return version
		}
	}
	return ""
}

// ValidateVersion checks the Codex auth adapter's explicitly supported range.
func ValidateVersion(version string) error {
	match := codexVersionPattern.FindStringSubmatch(version)
	if len(match) != 4 {
		return fmt.Errorf("unsupported Codex CLI version %q; supported versions are 0.146.x through 0.154.x", version)
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	if major != 0 || minor < 146 || minor > 154 {
		return fmt.Errorf("unsupported Codex CLI version %s; supported versions are 0.146.x through 0.154.x", match[0])
	}
	return nil
}

func validateSubscriptionAuth(auth *SubscriptionAuth) error {
	if auth.AccessToken == "" || auth.RefreshToken == "" || auth.AccountID == "" {
		return fmt.Errorf("Codex subscription login returned incomplete OAuth tokens")
	}
	return nil
}

func decodeSubscriptionAuth(cred *provider.Credential) (*SubscriptionAuth, error) {
	if cred == nil || cred.Token == "" {
		return nil, fmt.Errorf("missing Codex subscription credential")
	}
	var auth SubscriptionAuth
	if err := json.Unmarshal([]byte(cred.Token), &auth); err != nil {
		return nil, fmt.Errorf("invalid Codex subscription credential: %w", err)
	}
	if err := validateSubscriptionAuth(&auth); err != nil {
		return nil, err
	}
	return &auth, nil
}

func jwtExpiry(token string) time.Time {
	payload, ok := jwtPayload(token)
	if !ok {
		return time.Time{}
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || claims.Exp == 0 {
		return time.Time{}
	}
	return time.Unix(claims.Exp, 0)
}

func jwtAccountID(token string) string {
	payload, ok := jwtPayload(token)
	if !ok {
		return ""
	}
	var claims struct {
		Auth struct {
			AccountID string `json:"chatgpt_account_id"`
		} `json:"https://api.openai.com/auth"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return ""
	}
	return claims.Auth.AccountID
}

func jwtPayload(token string) ([]byte, bool) {
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, false
	}
	return payload, true
}

func HasCredential() bool {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return false
	}
	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return false
	}
	cred, err := store.Get(credential.ProviderCodexSubscription)
	return err == nil && cred != nil
}
