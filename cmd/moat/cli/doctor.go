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

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/doctor"
	"github.com/majorcontext/moat/internal/providers/codex"
	"github.com/majorcontext/moat/internal/storage"
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
	fmt.Fprintf(tw, "Version:\t%s\n", Version())
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
	identity := engineUnknown

	// Check Docker
	if rt, err := container.NewDockerRuntime(false); err == nil {
		dockerRT = rt
		marker := ""
		if defaultRT.Type() == container.RuntimeDocker {
			marker = " (default)"
		}

		// NewDockerRuntime succeeds even with no reachable daemon (client
		// creation doesn't dial), so ping before trusting IsPodmanEngine. If
		// the ping times out or IsPodmanEngine errors, identity stays
		// engineUnknown — callers must not fail open and assume real Docker.
		pingCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		if rt.Ping(pingCtx) == nil {
			if isPodman, err := rt.IsPodmanEngine(pingCtx); err == nil {
				if isPodman {
					identity = enginePodman
				} else {
					identity = engineDocker
				}
			}
		}
		cancel()

		runtimes = append(runtimes, dockerRuntimeEntry(marker, identity))
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

	// Surface a podman socket sitting on disk whenever the connected engine
	// isn't already confirmed to be podman — without dialing the socket or
	// setting DOCKER_HOST, both of which doctor must avoid. This covers both
	// engineDocker (a real Docker daemon is connected, but a podman machine
	// may also be running alongside it) and engineUnknown (identity couldn't
	// be confirmed). It's suppressed for enginePodman since the "Available:"
	// line already labels that engine "docker (podman)".
	if line := podmanSocketLine(identity, container.PodmanSocketPaths()); line != "" {
		fmt.Fprintf(tw, "Podman:\t%s\n", line)
	}

	// Check for Docker-specific features
	if dockerRT != nil {
		// Check gVisor
		fmt.Fprintf(tw, "gVisor:\t%s\n", gvisorLine(identity, hasGVisor()))

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
	moatUserSettingsPath := filepath.Join(config.GlobalConfigDir(), "claude", "settings.json")
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

	// 5. moat.yaml overrides (falls back to agent.yaml)
	configPath := filepath.Join(cwd, config.ConfigFilename)
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configPath = filepath.Join(cwd, config.LegacyConfigFilename)
	}
	if data, err := os.ReadFile(configPath); err == nil {
		// Check if it has Claude-related configuration
		hasClaudeConfig := strings.Contains(string(data), "claude:")
		if hasClaudeConfig {
			fmt.Fprintf(tw, "  5. moat.yaml overrides:\t%s %s (has claude config)\n", configPath, ui.OKTag())
		} else {
			fmt.Fprintf(tw, "  5. moat.yaml overrides:\t%s %s\n", configPath, ui.OKTag())
		}
	} else {
		fmt.Fprintf(tw, "  5. moat.yaml overrides:\t%s\n", filepath.Join(cwd, config.ConfigFilename))
	}

	return tw.Flush()
}

// storageSection shows storage location and status
type storageSection struct{}

func (s *storageSection) Name() string { return "Storage" }

func (s *storageSection) Print(w io.Writer) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	moatDir := config.GlobalConfigDir()
	fmt.Fprintf(tw, "Moat directory:\t%s\n", moatDir)

	if info, err := os.Stat(moatDir); err == nil {
		fmt.Fprintf(tw, "Exists:\t%s\n", ui.OKTag())
		fmt.Fprintf(tw, "Permissions:\t%v\n", info.Mode())
	} else {
		fmt.Fprintf(tw, "Exists:\t%s (%v)\n", ui.FailTag(), err)
	}

	return tw.Flush()
}

// engineIdentity captures what doctor could confirm about the engine behind
// the Docker-API-compatible client. A failed or skipped ping (or an
// IsPodmanEngine error) leaves this at engineUnknown — that state must be
// treated as untrusted, not silently coerced into "real Docker". This is the
// three-state model dockerRuntimeEntry and gvisorLine key off of.
type engineIdentity int

const (
	engineUnknown engineIdentity = iota
	engineDocker
	enginePodman
)

// dockerRuntimeEntry formats the "Available:" list entry for the Docker
// runtime, labeling it when the connected engine is confirmed to be podman
// speaking Docker's compat API (see container.DockerRuntime.IsPodmanEngine).
// marker is appended as-is (e.g. " (default)"). identity must only be
// enginePodman or engineDocker when a successful ping actually confirmed the
// engine; engineUnknown (ping failed/timed out, or IsPodmanEngine errored)
// intentionally renders the same as engineDocker — the label must not
// speculate about an identity doctor never confirmed.
func dockerRuntimeEntry(marker string, identity engineIdentity) string {
	label := "docker"
	if identity == enginePodman {
		label = "docker (podman)"
	}
	return label + marker
}

// gvisorLine formats the doctor "gVisor:" status line. Podman's compat /info
// endpoint lists every OCI runtime configured in containers.conf — including
// gVisor's runsc — regardless of whether it's actually installed, so a
// "reported" runsc entry from a podman engine can't be trusted the way it can
// for real Docker. When the engine identity itself is unknown (ping failed or
// IsPodmanEngine errored), a reported runsc is equally untrustworthy — worse,
// even — since doctor doesn't even know it's talking to podman, so this must
// not fall through to the confirmed-Docker "available" line. identity must
// only be enginePodman/engineDocker when a successful ping confirmed it (see
// dockerRuntimeEntry); reported is the raw hasGVisor() result.
func gvisorLine(identity engineIdentity, reported bool) string {
	switch {
	case !reported:
		return ui.Dim("—") + " not available"
	case identity == enginePodman:
		return ui.WarnTag() + " reported by engine — unverified (podman lists configured OCI runtimes even when not installed)"
	case identity == engineUnknown:
		return ui.WarnTag() + " reported — engine identity unverified (daemon did not respond to ping)"
	default:
		return ui.OKTag() + " available"
	}
}

// podmanSocketLine formats the doctor "Podman:" status line, or returns ""
// when nothing should be shown. sockets is the stat-only result of
// container.PodmanSocketPaths() (never dialed). The line is suppressed for
// enginePodman — the "Available:" line already labels that engine "docker
// (podman)", so repeating it would be redundant — and shown for
// engineDocker and engineUnknown, since in both cases a live podman socket
// is real signal the user doesn't otherwise see.
func podmanSocketLine(identity engineIdentity, sockets []string) string {
	if identity == enginePodman || len(sockets) == 0 {
		return ""
	}
	return fmt.Sprintf("socket found at %s — use --runtime podman", strings.Join(sockets, ", "))
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
	runsDir := storage.DefaultBaseDir()
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
