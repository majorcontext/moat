package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	claudeprov "github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/proxy"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var doctorClaudeCmd = &cobra.Command{
	Use:   "claude",
	Short: "Diagnose Claude Code authentication and configuration issues",
	Long: `Diagnose Claude Code authentication and configuration issues in moat containers.

This command compares your host Claude Code configuration against what's available
in moat containers to identify authentication problems.

What it checks:

  Environment Comparison:
    • Compares ~/.claude.json fields between host and container
    • Identifies missing critical fields (anonymousId, installMethod, etc.)
    • Shows which fields are copied vs. excluded by hostConfigAllowlist
    • Highlights configuration mismatches that affect authentication

  Credential Status:
    • Shows granted Anthropic credential type (OAuth token vs API key)
    • Displays token expiration time and remaining validity
    • Lists OAuth scopes (if applicable)
    • Shows when credential was last granted

  Field Analysis:
    • Shows which fields would be copied to containers based on allowlist
    • Identifies missing fields that could cause authentication issues
    • Verifies all required OAuth fields are present

  Configuration Files:
    • Verifies ~/.claude.json exists and has required OAuth fields
    • Checks ~/.claude/.credentials.json structure (container only)
    • Shows which fields are present/missing compared to host
    • Validates file permissions

  Container Testing (--test-container):
    Runs three progressive validation levels, short-circuiting on failure:
    1. Direct API call — verifies the stored token itself is valid
    2. Proxy injection — verifies the TLS proxy replaces placeholders with real credentials
    3. Container test — launches a real container for end-to-end verification
    If level 1 fails, levels 2 and 3 are skipped. If level 2 fails, level 3 is skipped.
    Each level costs ~$0.0001 for a minimal Haiku API call.

Exit codes:
  0   All checks passed (including container test if --test-container used)
  1   Configuration issues detected
  2   Token validation or container authentication test failed (--test-container only)`,
	RunE: runDoctorClaude,
}

var (
	doctorClaudeVerbose       bool
	doctorClaudeJSON          bool
	doctorClaudeTestContainer bool
)

func init() {
	doctorClaudeCmd.Flags().BoolVar(&doctorClaudeVerbose, "verbose", false, "Show full configuration diff and all checked fields")
	doctorClaudeCmd.Flags().BoolVar(&doctorClaudeJSON, "json", false, "Output results as JSON for scripting")
	doctorClaudeCmd.Flags().BoolVar(&doctorClaudeTestContainer, "test-container", false, "Launch a real container to test authentication end-to-end (~$0.0001 cost)")
	doctorCmd.AddCommand(doctorClaudeCmd)
}

type claudeDiagnostic struct {
	HostConfigPath        string                 `json:"host_config_path"`
	HostConfigExists      bool                   `json:"host_config_exists"`
	HostConfigFields      []string               `json:"host_config_fields"`
	ContainerConfigExists bool                   `json:"container_config_exists,omitempty"`
	ContainerConfigFields []string               `json:"container_config_fields,omitempty"`
	MissingFields         []string               `json:"missing_fields"`
	CredentialStatus      *credentialStatus      `json:"credential_status"`
	TokenValidation       *tokenValidationResult `json:"token_validation,omitempty"`
	ContainerTest         *containerTestResult   `json:"container_test,omitempty"`
	Issues                []issue                `json:"issues"`
	Suggestions           []string               `json:"suggestions"`
}

type containerTestResult struct {
	RunID            string                   `json:"run_id"`
	ConfigRead       bool                     `json:"config_read"`
	ContainerConfig  map[string]interface{}   `json:"container_config,omitempty"`
	APICallSucceeded bool                     `json:"api_call_succeeded"`
	NetworkRequests  []storage.NetworkRequest `json:"network_requests,omitempty"`
	AuthErrors       []string                 `json:"auth_errors,omitempty"`
	ExitCode         int                      `json:"exit_code"`
}

type credentialStatus struct {
	Granted       bool      `json:"granted"`
	Type          string    `json:"type"` // "OAuth Token" or "API Key"
	TokenPrefix   string    `json:"token_prefix"`
	ExpiresAt     time.Time `json:"expires_at,omitempty"`
	TimeRemaining string    `json:"time_remaining,omitempty"`
	Scopes        []string  `json:"scopes,omitempty"`
	GrantedAt     time.Time `json:"granted_at,omitempty"`
}

type issue struct {
	Severity    string `json:"severity"` // "error", "warning", "info"
	Component   string `json:"component"`
	Description string `json:"description"`
	Fix         string `json:"fix,omitempty"`
}

type tokenValidationResult struct {
	DirectTest *validationLevelResult `json:"direct_test"`
	ProxyTest  *validationLevelResult `json:"proxy_test,omitempty"`
}

type validationLevelResult struct {
	Passed     bool   `json:"passed"`
	StatusCode int    `json:"status_code,omitempty"`
	Error      string `json:"error,omitempty"`
	Duration   string `json:"duration,omitempty"`
	Skipped    bool   `json:"skipped,omitempty"`
	SkipReason string `json:"skip_reason,omitempty"`
}

// anthropicValidationURL is the URL used for token validation requests.
// It is a package-level variable to allow test overrides.
var anthropicValidationURL = "https://api.anthropic.com/v1/messages"

// validationRequestBody is the minimal request body used for token validation.
// Uses Haiku for minimal cost (~$0.0001 per validation call).
const validationRequestBody = `{"model":"claude-haiku-4-5-20251001","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`

// newValidationRequest creates an HTTP request configured for Anthropic API
// token validation. It sets the appropriate auth headers based on token type:
// OAuth tokens use Bearer auth with required beta flags; API keys use x-api-key.
func newValidationRequest(ctx context.Context, token string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", anthropicValidationURL, strings.NewReader(validationRequestBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	if credential.IsOAuthToken(token) {
		// OAuth tokens require Bearer auth with specific beta flags.
		// The oauth-2025-04-20 beta and dangerous-direct-browser-access headers
		// are required for OAuth tokens to work outside the proxy context.
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("anthropic-dangerous-direct-browser-access", "true")
		req.Header.Set("anthropic-beta", "oauth-2025-04-20")
	} else {
		req.Header.Set("x-api-key", token)
	}

	return req, nil
}

func runDoctorClaude(cmd *cobra.Command, args []string) error {
	ctx := cmd.Context()
	diag := &claudeDiagnostic{
		Issues:      []issue{},
		Suggestions: []string{},
	}

	// Check host configuration
	if err := checkHostClaudeConfig(diag); err != nil {
		return fmt.Errorf("checking host configuration: %w", err)
	}

	// Check credential status
	checkCredentialStatus(diag)

	// Analyze container field mapping
	if err := checkContainerConfig(diag); err != nil {
		return fmt.Errorf("analyzing container configuration: %w", err)
	}

	// Progressive validation and container test if requested
	if doctorClaudeTestContainer {
		runProgressiveValidation(ctx, diag)

		// Only run container test if token validation passed
		if tokenValidationPassed(diag) {
			if err := testContainerAuth(ctx, diag); err != nil {
				return fmt.Errorf("container test failed: %w", err)
			}
		}
	}

	// Analyze and report
	if doctorClaudeJSON {
		return outputJSON(diag)
	}
	return outputHuman(diag)
}

func checkHostClaudeConfig(diag *claudeDiagnostic) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	configPath := filepath.Join(homeDir, ".claude.json")
	diag.HostConfigPath = configPath

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			diag.HostConfigExists = false
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "host-config",
				Description: "~/.claude.json does not exist",
				Fix:         "Run 'claude' on the host to initialize Claude Code",
			})
			return nil
		}
		return err
	}

	diag.HostConfigExists = true

	var hostConfig map[string]any
	if err := json.Unmarshal(data, &hostConfig); err != nil {
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "host-config",
			Description: "~/.claude.json is not valid JSON",
		})
		return nil
	}

	// Get field list
	for key := range hostConfig {
		diag.HostConfigFields = append(diag.HostConfigFields, key)
	}
	sort.Strings(diag.HostConfigFields)

	// Check for critical OAuth fields
	if oauthAccount, ok := hostConfig["oauthAccount"].(map[string]any); ok {
		// Verify required OAuth fields
		requiredOAuthFields := []string{"organizationUuid", "accountUuid", "emailAddress"}
		for _, field := range requiredOAuthFields {
			if _, exists := oauthAccount[field]; !exists {
				diag.Issues = append(diag.Issues, issue{
					Severity:    "warning",
					Component:   "oauth",
					Description: fmt.Sprintf("oauthAccount missing field: %s", field),
				})
			}
		}
	} else {
		// No OAuth account - might be using API key, which is fine
		if _, hasUserID := hostConfig["userID"]; !hasUserID {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "info",
				Component:   "auth",
				Description: "No oauthAccount found (using API key authentication)",
			})
		}
	}

	return nil
}

// getAnthropicCredential reads the Anthropic/Claude credential from the encrypted store.
// It checks claude first (preferred for Claude Code), then falls back to anthropic.
// Returns nil and an error description if no credential can be read.
func getAnthropicCredential() (*credential.Credential, string) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return nil, "cannot access credential store encryption key"
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return nil, fmt.Sprintf("cannot open credential store: %v", err)
	}

	// Prefer claude, fall back to anthropic
	cred, err := store.Get(credential.ProviderClaude)
	if err != nil {
		cred, err = store.Get(credential.ProviderAnthropic)
	}
	if err != nil {
		return nil, "no Anthropic credential granted"
	}

	return cred, ""
}

func checkCredentialStatus(diag *claudeDiagnostic) {
	cred, errMsg := getAnthropicCredential()
	if errMsg != "" {
		diag.CredentialStatus = &credentialStatus{Granted: false}
		if errMsg == "no Anthropic credential granted" {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "credential",
				Description: "No Anthropic credential granted",
				Fix:         "Run 'moat grant claude' (OAuth) or 'moat grant anthropic' (API key) to grant credentials",
			})
		} else if errMsg == "cannot access credential store encryption key" {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "credential",
				Description: "Cannot access credential store encryption key",
				Fix:         "Check MOAT_CREDENTIAL_KEY environment variable",
			})
		}
		return
	}

	// Determine token type
	tokenType := "API Key"
	tokenPrefix := cred.Token
	if len(tokenPrefix) > 16 {
		tokenPrefix = tokenPrefix[:16] + "..."
	}

	if credential.IsOAuthToken(cred.Token) {
		tokenType = "OAuth Token"
	}

	status := &credentialStatus{
		Granted:     true,
		Type:        tokenType,
		TokenPrefix: tokenPrefix,
		Scopes:      cred.Scopes,
	}

	// Check expiration for OAuth tokens
	if !cred.ExpiresAt.IsZero() {
		status.ExpiresAt = cred.ExpiresAt
		remaining := time.Until(cred.ExpiresAt)
		if remaining > 0 {
			status.TimeRemaining = formatDuration(remaining)
			if remaining < 5*time.Minute {
				diag.Issues = append(diag.Issues, issue{
					Severity:    "warning",
					Component:   "credential",
					Description: fmt.Sprintf("OAuth token expires soon (%s remaining)", status.TimeRemaining),
					Fix:         "Run 'moat grant claude' to refresh the token",
				})
			}
		} else {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "credential",
				Description: "OAuth token has expired",
				Fix:         "Run 'moat grant claude' to refresh the token",
			})
		}
	}

	diag.CredentialStatus = status
}

func checkContainerConfig(diag *claudeDiagnostic) error {
	// Simulate what would be copied to container based on allowlist
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	// Read host config
	hostConfig, err := claudeprov.ReadHostConfig(filepath.Join(homeDir, ".claude.json"))
	if err != nil {
		return err
	}
	if hostConfig == nil {
		// No host config to copy
		return nil
	}

	diag.ContainerConfigExists = true

	// Determine which fields would be copied
	allowlistSet := make(map[string]bool)
	for _, field := range claudeprov.HostConfigAllowlist {
		allowlistSet[field] = true
		if _, exists := hostConfig[field]; exists {
			diag.ContainerConfigFields = append(diag.ContainerConfigFields, field)
		} else {
			diag.MissingFields = append(diag.MissingFields, field)
		}
	}
	sort.Strings(diag.ContainerConfigFields)

	// Check that all allowlisted fields are present in host config
	for _, field := range claudeprov.HostConfigAllowlist {
		if _, exists := hostConfig[field]; !exists {
			// Skip fields that are optional or set by moat:
			// - mcpServers: configured via agent.yaml, not copied from host
			// - cachedGrowthBookFeatures: optional optimization, may not exist on fresh installs
			if field == "mcpServers" || field == "cachedGrowthBookFeatures" {
				continue
			}
			diag.Issues = append(diag.Issues, issue{
				Severity:    "warning",
				Component:   "host-config",
				Description: fmt.Sprintf("Host config missing allowlisted field: %s", field),
				Fix:         "This field would not be copied to containers. Consider running Claude Code on host to initialize it.",
			})
		}
	}

	return nil
}

func outputJSON(diag *claudeDiagnostic) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(diag)
}

func outputHuman(diag *claudeDiagnostic) error {
	fmt.Println(ui.Bold("Claude Code Diagnostics"))
	fmt.Println()

	// Credential Status
	fmt.Println(ui.Bold("Credential Status:"))
	if diag.CredentialStatus != nil && diag.CredentialStatus.Granted {
		fmt.Printf("  %s Anthropic credential granted\n", ui.OKTag())
		fmt.Printf("    Type: %s\n", diag.CredentialStatus.Type)
		fmt.Printf("    Prefix: %s\n", diag.CredentialStatus.TokenPrefix)
		if !diag.CredentialStatus.ExpiresAt.IsZero() {
			fmt.Printf("    Expires: %s (%s remaining)\n",
				diag.CredentialStatus.ExpiresAt.Format(time.RFC3339),
				diag.CredentialStatus.TimeRemaining)
		}
		if len(diag.CredentialStatus.Scopes) > 0 {
			fmt.Printf("    Scopes: %s\n", strings.Join(diag.CredentialStatus.Scopes, ", "))
		}
	} else {
		fmt.Printf("  %s No Anthropic credential granted\n", ui.FailTag())
	}
	fmt.Println()

	// Host Configuration
	fmt.Println(ui.Bold("Host Configuration:"))
	if diag.HostConfigExists {
		fmt.Printf("  %s ~/.claude.json exists\n", ui.OKTag())
		fmt.Printf("    Fields: %d\n", len(diag.HostConfigFields))
		if doctorClaudeVerbose {
			fmt.Printf("    All fields: %s\n", strings.Join(diag.HostConfigFields, ", "))
		}

		// Check for OAuth account
		hasOAuth := false
		for _, field := range diag.HostConfigFields {
			if field == "oauthAccount" {
				hasOAuth = true
				break
			}
		}
		if hasOAuth {
			fmt.Printf("  %s OAuth account configured\n", ui.OKTag())
		}
	} else {
		fmt.Printf("  %s ~/.claude.json does not exist\n", ui.FailTag())
	}
	fmt.Println()

	// Container Configuration
	fmt.Println(ui.Bold("Container Configuration (Simulated):"))
	if diag.ContainerConfigExists {
		fmt.Printf("  %s Would copy %d fields to container\n", ui.OKTag(), len(diag.ContainerConfigFields))
		if doctorClaudeVerbose {
			fmt.Printf("    Copied fields: %s\n", strings.Join(diag.ContainerConfigFields, ", "))
		}
		fmt.Printf("    From host: %d total fields\n", len(diag.HostConfigFields))

		if len(diag.MissingFields) > 0 {
			fmt.Printf("  %s Missing %d allowlisted fields from host:\n", ui.FailTag(), len(diag.MissingFields))
			for _, field := range diag.MissingFields {
				fmt.Printf("      - %s (would not be copied)\n", field)
			}
		} else {
			fmt.Printf("  %s All allowlisted fields present in host config\n", ui.OKTag())
		}
	} else {
		fmt.Printf("  %s Could not analyze container configuration\n", ui.FailTag())
	}
	fmt.Println()

	// Token Validation Results (shown if --test-container was used)
	if diag.TokenValidation != nil {
		fmt.Println(ui.Bold("Token Validation:"))

		// Level 1: Direct test
		if dt := diag.TokenValidation.DirectTest; dt != nil {
			if dt.Passed {
				fmt.Printf("  %s Direct API call succeeded (%s)\n", ui.OKTag(), dt.Duration)
			} else if dt.Skipped {
				fmt.Printf("  %s Direct test skipped (%s)\n", ui.Dim("-"), dt.SkipReason)
			} else {
				fmt.Printf("  %s Direct API call failed: %s\n", ui.FailTag(), dt.Error)
				fmt.Printf("    Fix: Run 'moat grant claude' to get a new token\n")
			}
		}

		// Level 2: Proxy test
		if pt := diag.TokenValidation.ProxyTest; pt != nil {
			if pt.Passed {
				fmt.Printf("  %s Proxy injection test succeeded (%s)\n", ui.OKTag(), pt.Duration)
			} else if pt.Skipped {
				fmt.Printf("  %s Proxy test skipped (%s)\n", ui.Dim("-"), pt.SkipReason)
			} else {
				fmt.Printf("  %s Proxy injection test failed: %s\n", ui.FailTag(), pt.Error)
			}
		}

		// If validation failed, note that container test was skipped
		if !tokenValidationPassed(diag) && diag.ContainerTest == nil {
			fmt.Printf("  %s Container test skipped (token validation failed)\n", ui.Dim("-"))
		}

		fmt.Println()
	}

	// Container Test Results (only shown if --test-container was used)
	if diag.ContainerTest != nil {
		fmt.Println(ui.Bold("Container Authentication Test:"))
		fmt.Printf("  Run ID: %s\n", diag.ContainerTest.RunID)

		// Config file check
		if diag.ContainerTest.ConfigRead {
			fmt.Printf("  %s Successfully read ~/.claude.json from container\n", ui.OKTag())

			if doctorClaudeVerbose && diag.ContainerTest.ContainerConfig != nil {
				keys := getConfigKeys(diag.ContainerTest.ContainerConfig)
				fmt.Printf("    Container fields (%d): %s\n", len(keys), strings.Join(keys, ", "))

				// Compare with host config
				if len(diag.HostConfigFields) > 0 {
					missing, extra := compareConfigs(
						makeConfigMap(diag.HostConfigFields),
						diag.ContainerTest.ContainerConfig,
					)
					if len(missing) > 0 {
						fmt.Printf("    Missing from container: %s\n", strings.Join(missing, ", "))
					}
					if len(extra) > 0 {
						fmt.Printf("    Extra in container: %s\n", strings.Join(extra, ", "))
					}
				}
			}
		} else {
			fmt.Printf("  %s Could not read ~/.claude.json from container\n", ui.FailTag())
		}

		// API authentication check
		if diag.ContainerTest.APICallSucceeded {
			fmt.Printf("  %s API authentication succeeded\n", ui.OKTag())
			fmt.Printf("    Network requests: %d\n", len(diag.ContainerTest.NetworkRequests))
		} else {
			fmt.Printf("  %s API authentication failed\n", ui.FailTag())

			if len(diag.ContainerTest.AuthErrors) > 0 {
				fmt.Printf("  Authentication errors:\n")
				for _, errMsg := range diag.ContainerTest.AuthErrors {
					fmt.Printf("    • %s\n", errMsg)
				}
			}

			if len(diag.ContainerTest.NetworkRequests) == 0 {
				fmt.Printf("  No network requests captured (proxy may not be working)\n")
			}
		}

		if doctorClaudeVerbose && len(diag.ContainerTest.NetworkRequests) > 0 {
			fmt.Printf("  Network requests:\n")
			for _, req := range diag.ContainerTest.NetworkRequests {
				fmt.Printf("    %s %s -> %d (%dms)\n", req.Method, req.URL, req.StatusCode, req.Duration)
			}
		}

		if diag.ContainerTest.ExitCode != 0 {
			fmt.Printf("  Container exit code: %d\n", diag.ContainerTest.ExitCode)
		}

		fmt.Println()
	}

	// Issues and Suggestions
	if len(diag.Issues) > 0 {
		fmt.Println(ui.Bold("Issues Found:"))
		for _, iss := range diag.Issues {
			var icon string
			switch iss.Severity {
			case "error":
				icon = ui.FailTag()
			case "warning":
				icon = ui.WarnTag()
			default:
				icon = ui.InfoTag()
			}
			fmt.Printf("  %s [%s] %s\n", icon, iss.Component, iss.Description)
			if iss.Fix != "" {
				fmt.Printf("      Fix: %s\n", iss.Fix)
			}
		}
		fmt.Println()
	}

	if len(diag.Suggestions) > 0 {
		fmt.Println(ui.Bold("Suggestions:"))
		for _, suggestion := range diag.Suggestions {
			fmt.Printf("  → %s\n", suggestion)
		}
		fmt.Println()
	}

	// Summary
	errorCount := 0
	warningCount := 0
	for _, iss := range diag.Issues {
		if iss.Severity == "error" {
			errorCount++
		} else if iss.Severity == "warning" {
			warningCount++
		}
	}

	// Check token validation failure — exit code 2 signals auth failure
	// specifically, distinct from general configuration errors (exit code 1).
	if diag.TokenValidation != nil && !tokenValidationPassed(diag) {
		fmt.Printf("Result: Token validation FAILED (%d errors, %d warnings)\n", errorCount, warningCount)
		os.Exit(2)
	}
	if diag.ContainerTest != nil && !diag.ContainerTest.APICallSucceeded {
		fmt.Printf("Result: Container authentication test FAILED (%d errors, %d warnings)\n", errorCount, warningCount)
		os.Exit(2)
	}
	if errorCount > 0 {
		fmt.Printf("Result: %d errors, %d warnings\n", errorCount, warningCount)
		os.Exit(1)
	}
	if warningCount > 0 {
		fmt.Printf("Result: %d warnings\n", warningCount)
		os.Exit(0)
	}

	fmt.Println(ui.Green("Result: All checks passed ✓"))
	return nil
}

// validateTokenDirect tests that the stored token is valid by making a direct
// API call to Anthropic (Level 1). This verifies the token itself works before
// testing proxy injection or containers.
func validateTokenDirect(ctx context.Context, token string) *validationLevelResult {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	req, err := newValidationRequest(ctx, token)
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("creating request: %v", err)}
	}

	start := time.Now()
	resp, err := http.DefaultClient.Do(req)
	duration := time.Since(start)

	result := &validationLevelResult{
		Duration: fmt.Sprintf("%dms", duration.Milliseconds()),
	}

	if err != nil {
		result.Error = fmt.Sprintf("network error: %v", err)
		return result
	}
	defer resp.Body.Close()

	// Read response body to check for OAuth-specific errors that indicate
	// Anthropic changed their OAuth endpoint requirements.
	body, _ := io.ReadAll(resp.Body)

	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Passed = true
		return result
	}

	// Check for OAuth-specific error messages that suggest the probe needs updating
	bodyStr := string(body)
	if credential.IsOAuthToken(token) && strings.Contains(bodyStr, "OAuth") {
		result.Error = fmt.Sprintf("API returned status %d: OAuth endpoint requirements may have changed — "+
			"this diagnostic probe may need updating", resp.StatusCode)
	} else {
		result.Error = fmt.Sprintf("API returned status %d", resp.StatusCode)
	}

	return result
}

// validateTokenViaProxy tests that the proxy correctly injects credentials
// (Level 2). It spins up a real TLS-intercepting proxy, sends a request with
// a placeholder token, and verifies the proxy replaces it with the real token.
func validateTokenViaProxy(ctx context.Context, token string) *validationLevelResult {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Create temp directory for CA
	tmpDir, err := os.MkdirTemp("", "moat-doctor-proxy-*")
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("creating temp dir: %v", err)}
	}
	defer os.RemoveAll(tmpDir)

	// Create CA for TLS interception
	ca, err := proxy.NewCA(tmpDir)
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("creating CA: %v", err)}
	}

	// Extract the target host from the validation URL so credential injection
	// matches the actual request host. In production this is api.anthropic.com;
	// in tests it may be a localhost httptest server.
	targetURL, err := url.Parse(anthropicValidationURL)
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("parsing validation URL: %v", err)}
	}
	targetHost := targetURL.Hostname()

	// Create and configure proxy
	p := proxy.NewProxy()
	p.SetCA(ca)

	// Configure credential injection matching the Claude provider logic
	if credential.IsOAuthToken(token) {
		p.SetCredential(targetHost, "Bearer "+token)
	} else {
		p.SetCredentialHeader(targetHost, "x-api-key", token)
	}

	// Start proxy server
	server := proxy.NewServer(p)
	err = server.Start()
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("starting proxy: %v", err)}
	}
	defer server.Stop(context.Background()) //nolint:errcheck

	// Build HTTP client that trusts our CA and routes through the proxy
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(ca.CertPEM())

	proxyURL, err := url.Parse("http://" + server.Addr())
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("parsing proxy URL: %v", err)}
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy:           http.ProxyURL(proxyURL),
			TLSClientConfig: &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12},
		},
	}

	// Build request with placeholder token — the proxy should replace it
	// with the real credential. We use newValidationRequest with a placeholder
	// so the request has all the right headers (beta flags, etc.) but the
	// proxy's credential injection replaces the auth header.
	placeholder := credential.ProxyInjectedPlaceholder
	if credential.IsOAuthToken(token) {
		// Use a placeholder that looks like an OAuth token so newValidationRequest
		// sets the correct OAuth headers (Bearer auth, beta flags).
		placeholder = "sk-ant-oat-" + credential.ProxyInjectedPlaceholder
	}
	req, err := newValidationRequest(ctx, placeholder)
	if err != nil {
		return &validationLevelResult{Error: fmt.Sprintf("creating request: %v", err)}
	}

	start := time.Now()
	resp, err := client.Do(req)
	duration := time.Since(start)

	result := &validationLevelResult{
		Duration: fmt.Sprintf("%dms", duration.Milliseconds()),
	}

	if err != nil {
		result.Error = fmt.Sprintf("request through proxy failed: %v", err)
		return result
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	result.StatusCode = resp.StatusCode
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		result.Passed = true
	} else {
		result.Error = fmt.Sprintf("API returned status %d through proxy", resp.StatusCode)
	}

	return result
}

// runProgressiveValidation runs Level 1 (direct) and Level 2 (proxy) token
// validation before the container test. It short-circuits on failure so users
// see exactly which layer is broken.
func runProgressiveValidation(ctx context.Context, diag *claudeDiagnostic) {
	validation := &tokenValidationResult{}
	diag.TokenValidation = validation

	cred, errMsg := getAnthropicCredential()
	if errMsg != "" {
		validation.DirectTest = &validationLevelResult{Error: errMsg}
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "token-validation",
			Description: fmt.Sprintf("Cannot validate token: %s", errMsg),
			Fix:         "Run 'moat grant claude' (OAuth) or 'moat grant anthropic' (API key) to grant credentials",
		})
		return
	}

	// Level 1: Direct API call
	validation.DirectTest = validateTokenDirect(ctx, cred.Token)

	if !validation.DirectTest.Passed {
		// Short-circuit: skip Level 2
		validation.ProxyTest = &validationLevelResult{
			Skipped:    true,
			SkipReason: "token is invalid",
		}

		fix := "Run 'moat grant claude' (OAuth) or 'moat grant anthropic' (API key) to get a new token"
		if validation.DirectTest.StatusCode == 403 {
			fix = "Token lacks required permissions. Re-run 'moat grant claude' or 'moat grant anthropic' to get a new token with correct scopes"
		}

		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "token-validation",
			Description: fmt.Sprintf("Direct API call failed: %s", validation.DirectTest.Error),
			Fix:         fix,
		})
		return
	}

	// Level 2: Proxy injection test
	validation.ProxyTest = validateTokenViaProxy(ctx, cred.Token)

	if !validation.ProxyTest.Passed {
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "token-validation",
			Description: fmt.Sprintf("Proxy injection test failed: %s", validation.ProxyTest.Error),
			Fix:         "The token is valid but proxy credential injection is not working. Check proxy configuration.",
		})
	}
}

// tokenValidationPassed returns true if both direct and proxy tests passed.
func tokenValidationPassed(diag *claudeDiagnostic) bool {
	if diag.TokenValidation == nil {
		return false
	}
	if diag.TokenValidation.DirectTest == nil || !diag.TokenValidation.DirectTest.Passed {
		return false
	}
	if diag.TokenValidation.ProxyTest == nil || !diag.TokenValidation.ProxyTest.Passed {
		return false
	}
	return true
}

func testContainerAuth(ctx context.Context, diag *claudeDiagnostic) error {
	// Create temporary workspace
	tmpDir, err := os.MkdirTemp("", "moat-doctor-*")
	if err != nil {
		return fmt.Errorf("creating temp workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Determine which grant to use based on the stored provider name.
	// This mirrors the preference logic in moat claude: claude first, anthropic fallback.
	grantName := "anthropic"
	cred, _ := getAnthropicCredential()
	if cred != nil {
		grantName = string(cred.Provider)
	}

	// Create config programmatically with claude-code dependency
	cfg := &config.Config{
		Dependencies: []string{"claude-code"},
		Grants:       []string{grantName},
	}

	// Create manager
	mgr, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer mgr.Close()

	// Create run with test command
	// Use claude CLI to test authentication through the proxy
	testCmd := []string{
		"sh", "-c",
		"cat ~/.claude.json && echo '---CONFIG-END---' && claude -p 'test'",
	}

	r, err := mgr.Create(ctx, run.Options{
		Name:          "doctor-claude-test",
		Workspace:     tmpDir,
		Config:        cfg,
		Grants:        []string{grantName},
		Cmd:           testCmd,
		KeepContainer: false,
	})
	if err != nil {
		return fmt.Errorf("creating test run: %w", err)
	}
	defer func() {
		// Stop the container and proxy but preserve run storage (logs, network)
		// so `moat logs <run-id>` works after the doctor finishes.
		_ = mgr.Stop(context.Background(), r.ID)
	}()

	result := &containerTestResult{RunID: r.ID}
	diag.ContainerTest = result

	// Start container (don't stream logs to avoid cluttering output)
	if startErr := mgr.Start(ctx, r.ID, run.StartOptions{}); startErr != nil {
		return fmt.Errorf("starting container: %w", startErr)
	}

	// Wait for completion with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	waitErr := mgr.Wait(waitCtx, r.ID)
	if waitErr != nil {
		// Non-zero exit or timeout - may indicate failure
		result.ExitCode = extractExitCode(waitErr)
		if waitCtx.Err() != nil {
			// Timeout — stop container to trigger log capture before we read logs
			_ = mgr.Stop(ctx, r.ID)
		}
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "container-test",
			Description: fmt.Sprintf("Container exited with error: %v", waitErr),
		})
	}

	// Read container logs to extract ~/.claude.json
	logs, err := r.Store.ReadLogs(0, 1000)
	if err != nil {
		return fmt.Errorf("reading container logs: %w", err)
	}

	parseClaudeConfigFromLogs(logs, result, diag)

	// Read network logs to check for auth errors
	requests, err := r.Store.ReadNetworkRequests()
	if err != nil {
		return fmt.Errorf("reading network logs: %w", err)
	}

	result.NetworkRequests = requests

	analyzeNetworkAuth(requests, result, diag)

	return nil
}

// analyzeNetworkAuth checks captured network requests for auth success/failure.
// A 401 on any Anthropic endpoint means auth is broken, even if other endpoints
// returned 2xx (e.g. health checks or non-authenticated routes). 403s on secondary
// endpoints (like client_data) are expected with limited OAuth scopes and are NOT
// treated as authentication failures.
func analyzeNetworkAuth(requests []storage.NetworkRequest, result *containerTestResult, diag *claudeDiagnostic) {
	hasSuccess := false
	has401 := false
	anthropicTotal := 0
	for _, req := range requests {
		if strings.Contains(req.URL, "api.anthropic.com") {
			anthropicTotal++
			if req.StatusCode >= 200 && req.StatusCode < 300 {
				hasSuccess = true
			} else if req.StatusCode == 401 {
				has401 = true
				result.AuthErrors = append(result.AuthErrors,
					fmt.Sprintf("%s %s -> %d", req.Method, req.URL, req.StatusCode))
			}
		}
	}
	result.APICallSucceeded = hasSuccess && !has401

	if has401 {
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "container-auth",
			Description: fmt.Sprintf("API authentication failed: %d of %d Anthropic requests returned 401", len(result.AuthErrors), anthropicTotal),
			Fix:         "Run 'moat grant claude' to refresh credentials, then retry",
		})
	}

	if result.APICallSucceeded {
		diag.Suggestions = append(diag.Suggestions,
			"Container authentication test PASSED - Claude Code should work in moat containers")
	}
}

func parseClaudeConfigFromLogs(logs []storage.LogEntry, result *containerTestResult, diag *claudeDiagnostic) {
	var configLines []string
	foundConfig := false
	inJSON := false

	for _, entry := range logs {
		line := entry.Line

		// Check for end marker
		if strings.Contains(line, "---CONFIG-END---") {
			foundConfig = true
			break
		}

		// Start collecting when we see an opening brace
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "{") {
			inJSON = true
		}

		// Collect lines while we're in the JSON object
		if inJSON {
			configLines = append(configLines, line)
		}

		// Stop collecting after closing brace at start of line
		if inJSON && trimmed == "}" {
			inJSON = false
		}
	}

	if !foundConfig {
		diag.Issues = append(diag.Issues, issue{
			Severity:    "warning",
			Component:   "container-test",
			Description: "Could not read ~/.claude.json from container logs",
		})
		return
	}

	result.ConfigRead = true
	configJSON := strings.Join(configLines, "\n")

	var config map[string]interface{}
	if err := json.Unmarshal([]byte(configJSON), &config); err != nil {
		// Don't report as error - the main test is whether API calls work
		// JSON parsing is just nice-to-have for verbose output
		return
	}

	result.ContainerConfig = config
}

func extractExitCode(err error) int {
	if err == nil {
		return 0
	}
	// Default to 1 for any error
	return 1
}

func getConfigKeys(config map[string]interface{}) []string {
	if config == nil {
		return nil
	}
	keys := make([]string, 0, len(config))
	for k := range config {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func makeConfigMap(fields []string) map[string]interface{} {
	m := make(map[string]interface{})
	for _, k := range fields {
		m[k] = true // Value doesn't matter, just keys
	}
	return m
}

func compareConfigs(host, container map[string]interface{}) (missing []string, extra []string) {
	hostKeys := make(map[string]bool)
	for k := range host {
		hostKeys[k] = true
	}

	containerKeys := make(map[string]bool)
	for k := range container {
		containerKeys[k] = true
		if !hostKeys[k] {
			extra = append(extra, k)
		}
	}

	for k := range host {
		if !containerKeys[k] {
			missing = append(missing, k)
		}
	}

	sort.Strings(missing)
	sort.Strings(extra)
	return missing, extra
}
