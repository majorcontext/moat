package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/majorcontext/moat/internal/codex"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/doctor"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Diagnostic information about the Moat environment",
	Long: `Displays diagnostic information about the Moat environment for debugging.

This command shows:
- Moat version and environment
- Container runtime status
- Credential status (scrubbed for safety)
- Claude Code configuration
- Recent runs
- Network connectivity

All sensitive information (tokens, keys, secrets) is automatically redacted.`,
	RunE: runDoctor,
}

var doctorVerbose bool

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().BoolVarP(&doctorVerbose, "verbose", "v", false, "show verbose output including JWT claims")
}

func runDoctor(cmd *cobra.Command, args []string) error {
	fmt.Println(ui.Bold("Moat Doctor"))
	fmt.Println()

	// Create registry and register all sections
	reg := doctor.NewRegistry()
	reg.Register(&versionSection{})
	reg.Register(&containerSection{})
	reg.Register(&credentialSection{})
	reg.Register(&sshSection{})
	reg.Register(&claudeSection{})
	reg.Register(&codex.DoctorSection{})
	reg.Register(&storageSection{})
	reg.Register(&runsSection{})

	// Run all sections
	for _, section := range reg.Sections() {
		ui.Section(section.Name())
		if err := section.Print(os.Stdout); err != nil {
			fmt.Printf("%s Error: %v\n", ui.FailTag(), err)
		}
		fmt.Println()
	}

	return nil
}

// versionSection shows platform and version info
type versionSection struct{}

func (s *versionSection) Name() string { return "Version" }

func (s *versionSection) Print(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Platform:\t%s/%s\n", runtime.GOOS, runtime.GOARCH)
	// TODO: Add actual version from build-time variable
	return tw.Flush()
}

// containerSection shows container runtime status
type containerSection struct{}

func (s *containerSection) Name() string { return "Container Runtime" }

func (s *containerSection) Print(w io.Writer) error {
	defaultRT, err := container.NewRuntime()
	if err != nil {
		fmt.Fprintf(w, "%s Error detecting runtime: %v\n", ui.FailTag(), err)
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Check which runtimes are available
	var runtimes []string
	var dockerRT *container.DockerRuntime

	// Check Docker
	if rt, err := container.NewDockerRuntime(false); err == nil {
		dockerRT = rt
		marker := ""
		if defaultRT.Type() == container.RuntimeDocker {
			marker = " (default)"
		}
		runtimes = append(runtimes, "docker"+marker)
	}

	// Check Apple Containers
	if appleRT, err := container.NewAppleRuntime(); err == nil {
		_ = appleRT // Suppress unused warning
		marker := ""
		if defaultRT.Type() == container.RuntimeApple {
			marker = " (default)"
		}
		runtimes = append(runtimes, "apple"+marker)
	}

	if len(runtimes) > 0 {
		fmt.Fprintf(tw, "Available:\t%s\n", strings.Join(runtimes, ", "))
	} else {
		fmt.Fprintln(tw, "Available:\tnone")
	}

	// Check for Docker-specific features
	if dockerRT != nil {
		// Check gVisor
		if hasGVisor() {
			fmt.Fprintf(tw, "gVisor:\t%s available\n", ui.OKTag())
		} else {
			fmt.Fprintf(tw, "gVisor:\t%s not available\n", ui.Dim("—"))
		}

		// Check BuildKit
		buildkit := os.Getenv("DOCKER_BUILDKIT")
		if buildkit == "1" {
			fmt.Fprintf(tw, "BuildKit:\t%s enabled (DOCKER_BUILDKIT=1)\n", ui.OKTag())
		} else {
			// Check if buildx is available
			if hasBuildx() {
				fmt.Fprintf(tw, "BuildKit:\t%s available (buildx installed)\n", ui.OKTag())
			} else {
				fmt.Fprintf(tw, "BuildKit:\t%s not available\n", ui.Dim("—"))
			}
		}
	}

	return tw.Flush()
}

// credentialSection shows stored credentials (redacted)
type credentialSection struct{}

func (s *credentialSection) Name() string { return "Credentials" }

func (s *credentialSection) Print(w io.Writer) error {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return fmt.Errorf("creating credential store: %w", err)
	}

	creds, err := store.List()
	if err != nil {
		return err
	}

	if len(creds) == 0 {
		fmt.Fprintln(w, "No credentials stored")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	for i, cred := range creds {
		if i > 0 {
			fmt.Fprintln(tw) // Blank line between credentials
		}

		fmt.Fprintf(tw, "Provider:\t%s\n", cred.Provider)

		// Show token prefix (safe to show)
		prefix := getTokenPrefix(cred.Token)
		if prefix != "" {
			fmt.Fprintf(tw, "Token prefix:\t%s...\n", prefix)
		}

		// Determine token type and extract JWT claims if available
		tokenType := "API Key"
		var jwtClaims map[string]interface{}

		// Try to decode as JWT (has 3 parts separated by dots)
		parts := strings.Split(cred.Token, ".")
		if len(parts) == 3 {
			payload, err := base64.RawURLEncoding.DecodeString(parts[1])
			if err == nil {
				if json.Unmarshal(payload, &jwtClaims) == nil {
					tokenType = "OAuth Token (JWT)"
				}
			}
		} else if credential.IsOAuthToken(cred.Token) {
			// OAuth token but not JWT (e.g., sk-ant-oat01-xxx bearer tokens)
			tokenType = "OAuth Token"
		}

		fmt.Fprintf(tw, "Type:\t%s\n", tokenType)

		// Show scopes (from credential or JWT)
		scopes := cred.Scopes
		if len(scopes) == 0 && jwtClaims != nil {
			// Try to extract from JWT "scope" claim
			if scope, ok := jwtClaims["scope"].(string); ok {
				scopes = strings.Split(scope, " ")
			}
		}
		if len(scopes) > 0 {
			fmt.Fprintf(tw, "Scopes:\t%s\n", strings.Join(scopes, ", "))
		}

		// Show expiration (from JWT or credential)
		if jwtClaims != nil {
			if exp, ok := jwtClaims["exp"].(float64); ok {
				expTime := time.Unix(int64(exp), 0)
				if time.Now().After(expTime) {
					fmt.Fprintf(tw, "Expires:\t%s EXPIRED (%s ago)\n", ui.FailTag(), formatAge(expTime))
				} else {
					fmt.Fprintf(tw, "Expires:\t%s\n", expTime.Format("2006-01-02"))
				}
			}

			// Always show JWT claims for OAuth tokens
			tw.Flush() // Flush before printing claims
			fmt.Fprintln(w, "JWT Claims:")
			printClaims(w, jwtClaims, "  ")
		} else if !cred.ExpiresAt.IsZero() {
			if time.Now().After(cred.ExpiresAt) {
				fmt.Fprintf(tw, "Expires:\t%s EXPIRED (%s ago)\n", ui.FailTag(), formatAge(cred.ExpiresAt))
			} else {
				fmt.Fprintf(tw, "Expires:\t%s\n", cred.ExpiresAt.Format("2006-01-02"))
			}
		}
	}

	return tw.Flush()
}

// getTokenPrefix returns a safe-to-display prefix of the token
func getTokenPrefix(token string) string {
	// For tokens with prefixes (sk-ant-, ghp_, etc), show the prefix + a few chars
	if len(token) > 12 {
		// Check for common prefixes
		if strings.HasPrefix(token, "sk-ant-") {
			// Anthropic tokens: sk-ant-api03-... or sk-ant-oat01-...
			parts := strings.SplitN(token, "-", 4)
			if len(parts) >= 3 {
				return strings.Join(parts[:3], "-") // e.g., "sk-ant-api03"
			}
		}
		if strings.HasPrefix(token, "ghp_") {
			return "ghp_" + token[4:8] // Show ghp_XXXX
		}
		if strings.HasPrefix(token, "gho_") {
			return "gho_" + token[4:8]
		}
		// For other tokens, show first 8 chars
		return token[:8]
	}
	return ""
}

// printClaims recursively prints JWT claims (used in verbose mode)
func printClaims(w io.Writer, claims map[string]interface{}, indent string) {
	for k, v := range claims {
		switch val := v.(type) {
		case map[string]interface{}:
			fmt.Fprintf(w, "%s%s:\n", indent, k)
			printClaims(w, val, indent+"  ")
		case float64:
			// Check if it's a timestamp
			if k == "exp" || k == "iat" || k == "nbf" {
				t := time.Unix(int64(val), 0)
				fmt.Fprintf(w, "%s%s: %s\n", indent, k, t.Format(time.RFC3339))
			} else {
				fmt.Fprintf(w, "%s%s: %v\n", indent, k, val)
			}
		case string:
			// Redact long IDs but show readable strings
			if len(val) > 32 && (k == "sub" || k == "jti" || strings.HasSuffix(k, "_id") || strings.HasSuffix(k, "Id")) {
				fmt.Fprintf(w, "%s%s: %s... (redacted)\n", indent, k, val[:8])
			} else {
				fmt.Fprintf(w, "%s%s: %s\n", indent, k, val)
			}
		case []interface{}:
			fmt.Fprintf(w, "%s%s: %v\n", indent, k, val)
		default:
			fmt.Fprintf(w, "%s%s: %v\n", indent, k, val)
		}
	}
}

// sshSection shows SSH grants
type sshSection struct{}

func (s *sshSection) Name() string { return "SSH Grants" }

func (s *sshSection) Print(w io.Writer) error {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return fmt.Errorf("getting encryption key: %w", err)
	}

	store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
	if err != nil {
		return fmt.Errorf("creating credential store: %w", err)
	}

	mappings, err := store.GetSSHMappings()
	if err != nil {
		return fmt.Errorf("getting SSH mappings: %w", err)
	}

	if len(mappings) == 0 {
		fmt.Fprintln(w, "No SSH grants configured")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "Total SSH grants:\t%d\n\n", len(mappings))

	for _, m := range mappings {
		fmt.Fprintf(tw, "Host:\t%s\n", m.Host)
		fmt.Fprintf(tw, "  Key fingerprint:\t%s\n", m.KeyFingerprint)
		if m.KeyPath != "" {
			fmt.Fprintf(tw, "  Key path:\t%s\n", m.KeyPath)
		}
		fmt.Fprintf(tw, "  Created:\t%s\n", m.CreatedAt.Format(time.RFC3339))
		fmt.Fprintln(tw)
	}

	return tw.Flush()
}

// claudeSection shows Claude Code configuration
type claudeSection struct{}

func (s *claudeSection) Name() string { return "Claude Code Configuration" }

func (s *claudeSection) Print(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	// Check ~/.claude.json (main config)
	claudeConfigPath := filepath.Join(home, ".claude.json")
	if data, err := os.ReadFile(claudeConfigPath); err == nil {
		var config map[string]interface{}
		if json.Unmarshal(data, &config) == nil {
			fmt.Fprintf(tw, "Main config:\t%s\n", claudeConfigPath)
			if onboarded, ok := config["hasCompletedOnboarding"].(bool); ok && onboarded {
				fmt.Fprintf(tw, "Onboarding:\t%s Complete\n", ui.OKTag())
			}
		}
	} else {
		fmt.Fprintln(tw, "Main config:\tnot found")
	}

	fmt.Fprintln(tw)
	fmt.Fprintln(tw, "Merged settings sources (for container):")

	// 1. Claude's known marketplaces
	knownMarketplacesPath := filepath.Join(home, ".claude", "plugins", "known_marketplaces.json")
	if _, err := os.Stat(knownMarketplacesPath); err == nil {
		fmt.Fprintf(tw, "  1. Known marketplaces:\t%s %s\n", knownMarketplacesPath, ui.OKTag())
	} else {
		fmt.Fprintf(tw, "  1. Known marketplaces:\t%s\n", knownMarketplacesPath)
	}

	// 2. Claude's native user settings
	claudeUserSettingsPath := filepath.Join(home, ".claude", "settings.json")
	if data, err := os.ReadFile(claudeUserSettingsPath); err == nil {
		var settings map[string]interface{}
		if json.Unmarshal(data, &settings) == nil {
			enabledCount := 0
			if plugins, ok := settings["enabledPlugins"].(map[string]interface{}); ok {
				for _, enabled := range plugins {
					if e, ok := enabled.(bool); ok && e {
						enabledCount++
					}
				}
			}
			fmt.Fprintf(tw, "  2. User settings:\t%s %s (%d plugins)\n", claudeUserSettingsPath, ui.OKTag(), enabledCount)
		}
	} else {
		fmt.Fprintf(tw, "  2. User settings:\t%s\n", claudeUserSettingsPath)
	}

	// 3. Moat-specific user defaults
	moatUserSettingsPath := filepath.Join(home, ".moat", "claude", "settings.json")
	if _, err := os.Stat(moatUserSettingsPath); err == nil {
		fmt.Fprintf(tw, "  3. Moat user defaults:\t%s %s\n", moatUserSettingsPath, ui.OKTag())
	} else {
		fmt.Fprintf(tw, "  3. Moat user defaults:\t%s\n", moatUserSettingsPath)
	}

	// 4. Project settings
	cwd, _ := os.Getwd()
	projectSettingsPath := filepath.Join(cwd, ".claude", "settings.json")
	if _, err := os.Stat(projectSettingsPath); err == nil {
		fmt.Fprintf(tw, "  4. Project settings:\t%s %s\n", projectSettingsPath, ui.OKTag())
	} else {
		fmt.Fprintf(tw, "  4. Project settings:\t%s\n", projectSettingsPath)
	}

	// 5. agent.yaml overrides
	agentYamlPath := filepath.Join(cwd, "agent.yaml")
	if data, err := os.ReadFile(agentYamlPath); err == nil {
		// Check if it has Claude-related configuration
		hasClaudeConfig := strings.Contains(string(data), "claude:")
		if hasClaudeConfig {
			fmt.Fprintf(tw, "  5. agent.yaml overrides:\t%s %s (has claude config)\n", agentYamlPath, ui.OKTag())
		} else {
			fmt.Fprintf(tw, "  5. agent.yaml overrides:\t%s %s\n", agentYamlPath, ui.OKTag())
		}
	} else {
		fmt.Fprintf(tw, "  5. agent.yaml overrides:\t%s\n", agentYamlPath)
	}

	return tw.Flush()
}

// storageSection shows storage location and status
type storageSection struct{}

func (s *storageSection) Name() string { return "Storage" }

func (s *storageSection) Print(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	moatDir := filepath.Join(home, ".moat")
	fmt.Fprintf(tw, "Moat directory:\t%s\n", moatDir)

	if info, err := os.Stat(moatDir); err == nil {
		fmt.Fprintf(tw, "Exists:\t%s\n", ui.OKTag())
		fmt.Fprintf(tw, "Permissions:\t%v\n", info.Mode())
	} else {
		fmt.Fprintf(tw, "Exists:\t%s (%v)\n", ui.FailTag(), err)
	}

	return tw.Flush()
}

// hasBuildx checks if docker buildx is available
func hasBuildx() bool {
	cmd := exec.Command("docker", "buildx", "version")
	return cmd.Run() == nil
}

// hasGVisor checks if gVisor (runsc) is available for Docker
func hasGVisor() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Using deprecated GVisorAvailable is acceptable here:
	// - This is a diagnostic tool that runs infrequently
	// - DockerRuntime.gvisorAvailable() is private (can't be called externally)
	// - The performance impact of creating a Docker client is negligible for doctor command
	//nolint:staticcheck // SA1019: GVisorAvailable is deprecated but needed for diagnostics
	return container.GVisorAvailable(ctx)
}

// runsSection shows recent runs count
type runsSection struct{}

func (s *runsSection) Name() string { return "Recent Runs" }

func (s *runsSection) Print(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	runsDir := filepath.Join(home, ".moat", "runs")
	entries, err := os.ReadDir(runsDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintln(w, "No runs found")
			return nil
		}
		return err
	}

	// Count runs
	runCount := 0
	for _, entry := range entries {
		if entry.IsDir() {
			runCount++
		}
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if runCount == 0 {
		fmt.Fprintln(tw, "No runs found")
	} else {
		fmt.Fprintf(tw, "Total runs:\t%d\n", runCount)
		fmt.Fprintln(tw, "Use 'moat list' to see run details")
	}

	return tw.Flush()
}
