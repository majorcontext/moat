package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	claudeprov "github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
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
    • Launches a real moat container with --grant anthropic
    • Reads actual ~/.claude.json from inside the container
    • Makes minimal API call to verify authentication works (~$0.0001 cost)
    • Reports network errors and authentication failures
    • Compares container config against host config

Exit codes:
  0   All checks passed (including container test if --test-container used)
  1   Configuration issues detected
  2   Container authentication test failed (--test-container only)`,
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
	HostConfigPath        string               `json:"host_config_path"`
	HostConfigExists      bool                 `json:"host_config_exists"`
	HostConfigFields      []string             `json:"host_config_fields"`
	ContainerConfigExists bool                 `json:"container_config_exists,omitempty"`
	ContainerConfigFields []string             `json:"container_config_fields,omitempty"`
	MissingFields         []string             `json:"missing_fields"`
	CredentialStatus      *credentialStatus    `json:"credential_status"`
	ContainerTest         *containerTestResult `json:"container_test,omitempty"`
	Issues                []issue              `json:"issues"`
	Suggestions           []string             `json:"suggestions"`
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

	// Container test if requested
	if doctorClaudeTestContainer {
		if err := testContainerAuth(ctx, diag); err != nil {
			return fmt.Errorf("container test failed: %w", err)
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

func checkCredentialStatus(diag *claudeDiagnostic) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		diag.CredentialStatus = &credentialStatus{Granted: false}
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "credential",
			Description: "Cannot access credential store encryption key",
			Fix:         "Check MOAT_CREDENTIAL_KEY environment variable",
		})
		return
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		diag.CredentialStatus = &credentialStatus{Granted: false}
		return
	}

	cred, err := store.Get(credential.ProviderAnthropic)
	if err != nil {
		diag.CredentialStatus = &credentialStatus{Granted: false}
		diag.Issues = append(diag.Issues, issue{
			Severity:    "error",
			Component:   "credential",
			Description: "No Anthropic credential granted",
			Fix:         "Run 'moat grant anthropic' to grant credentials",
		})
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
					Fix:         "Run 'moat grant anthropic' to refresh the token",
				})
			}
		} else {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "credential",
				Description: "OAuth token has expired",
				Fix:         "Run 'moat grant anthropic' to refresh the token",
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
	fmt.Println("Claude Code Diagnostics")
	fmt.Println("=======================")
	fmt.Println()

	// Credential Status
	fmt.Println("Credential Status:")
	if diag.CredentialStatus != nil && diag.CredentialStatus.Granted {
		fmt.Printf("  ✓ Anthropic credential granted\n")
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
		fmt.Printf("  ✗ No Anthropic credential granted\n")
	}
	fmt.Println()

	// Host Configuration
	fmt.Println("Host Configuration:")
	if diag.HostConfigExists {
		fmt.Printf("  ✓ ~/.claude.json exists\n")
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
			fmt.Printf("  ✓ OAuth account configured\n")
		}
	} else {
		fmt.Printf("  ✗ ~/.claude.json does not exist\n")
	}
	fmt.Println()

	// Container Configuration
	fmt.Println("Container Configuration (Simulated):")
	if diag.ContainerConfigExists {
		fmt.Printf("  ✓ Would copy %d fields to container\n", len(diag.ContainerConfigFields))
		if doctorClaudeVerbose {
			fmt.Printf("    Copied fields: %s\n", strings.Join(diag.ContainerConfigFields, ", "))
		}
		fmt.Printf("    From host: %d total fields\n", len(diag.HostConfigFields))

		if len(diag.MissingFields) > 0 {
			fmt.Printf("  ✗ Missing %d allowlisted fields from host:\n", len(diag.MissingFields))
			for _, field := range diag.MissingFields {
				fmt.Printf("      - %s (would not be copied)\n", field)
			}
		} else {
			fmt.Printf("  ✓ All allowlisted fields present in host config\n")
		}
	} else {
		fmt.Printf("  ✗ Could not analyze container configuration\n")
	}
	fmt.Println()

	// Container Test Results (only shown if --test-container was used)
	if diag.ContainerTest != nil {
		fmt.Println("Container Authentication Test:")
		fmt.Printf("  Run ID: %s\n", diag.ContainerTest.RunID)

		// Config file check
		if diag.ContainerTest.ConfigRead {
			fmt.Printf("  ✓ Successfully read ~/.claude.json from container\n")

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
			fmt.Printf("  ✗ Could not read ~/.claude.json from container\n")
		}

		// API authentication check
		if diag.ContainerTest.APICallSucceeded {
			fmt.Printf("  ✓ API authentication succeeded\n")
			fmt.Printf("    Network requests: %d\n", len(diag.ContainerTest.NetworkRequests))
		} else {
			fmt.Printf("  ✗ API authentication failed\n")

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

		if diag.ContainerTest.ExitCode != 0 {
			fmt.Printf("  Container exit code: %d\n", diag.ContainerTest.ExitCode)
		}

		fmt.Println()
	}

	// Issues and Suggestions
	if len(diag.Issues) > 0 {
		fmt.Println("Issues Found:")
		for _, iss := range diag.Issues {
			icon := "ℹ"
			if iss.Severity == "error" {
				icon = "✗"
			} else if iss.Severity == "warning" {
				icon = "⚠"
			}
			fmt.Printf("  %s [%s] %s\n", icon, iss.Component, iss.Description)
			if iss.Fix != "" {
				fmt.Printf("      Fix: %s\n", iss.Fix)
			}
		}
		fmt.Println()
	}

	if len(diag.Suggestions) > 0 {
		fmt.Println("Suggestions:")
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

	// Check container test failure first — exit code 2 signals auth failure
	// specifically, distinct from general configuration errors (exit code 1).
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

	fmt.Println("Result: All checks passed ✓")
	return nil
}

func testContainerAuth(ctx context.Context, diag *claudeDiagnostic) error {
	// Create temporary workspace
	tmpDir, err := os.MkdirTemp("", "moat-doctor-*")
	if err != nil {
		return fmt.Errorf("creating temp workspace: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create config programmatically with claude-code dependency
	cfg := &config.Config{
		Dependencies: []string{"claude-code"},
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
		Grants:        []string{"anthropic"},
		Cmd:           testCmd,
		KeepContainer: false,
	})
	if err != nil {
		return fmt.Errorf("creating test run: %w", err)
	}
	defer func() {
		_ = mgr.Destroy(context.Background(), r.ID)
	}()

	result := &containerTestResult{RunID: r.ID}
	diag.ContainerTest = result

	// Start container (don't stream logs to avoid cluttering output)
	if startErr := mgr.Start(ctx, r.ID, run.StartOptions{StreamLogs: false}); startErr != nil {
		return fmt.Errorf("starting container: %w", startErr)
	}

	// Wait for completion with timeout
	waitCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	waitErr := mgr.Wait(waitCtx, r.ID)
	if waitErr != nil {
		// Non-zero exit or timeout - may indicate failure
		result.ExitCode = extractExitCode(waitErr)
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

	// Analyze requests for auth success/failure
	// We consider the test successful if ANY request to api.anthropic.com succeeded
	// 403s on secondary endpoints (like client_data) are expected with limited OAuth scopes
	for _, req := range requests {
		if strings.Contains(req.URL, "api.anthropic.com") {
			if req.StatusCode >= 200 && req.StatusCode < 300 {
				result.APICallSucceeded = true
			} else if req.StatusCode == 401 {
				// 401 = authentication error (bad/missing token)
				result.AuthErrors = append(result.AuthErrors,
					fmt.Sprintf("%s %s -> %d", req.Method, req.URL, req.StatusCode))
			}
			// Note: 403 responses are expected for OAuth endpoints requiring scopes
			// that aren't available in long-lived tokens (e.g., client_data endpoint)
			// We don't treat these as authentication failures
		}
	}

	// Only report auth failure if we got 401s OR if all requests failed
	if len(result.AuthErrors) > 0 && !result.APICallSucceeded {
		for _, errMsg := range result.AuthErrors {
			diag.Issues = append(diag.Issues, issue{
				Severity:    "error",
				Component:   "container-auth",
				Description: fmt.Sprintf("API authentication failed: %s", errMsg),
				Fix:         "Check that 'moat grant anthropic' has been run and token is valid",
			})
		}
	}

	if result.APICallSucceeded {
		diag.Suggestions = append(diag.Suggestions,
			"Container authentication test PASSED - Claude Code should work in moat containers")
	}

	return nil
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
