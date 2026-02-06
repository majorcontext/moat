package cli

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
)

// Re-export helper functions from internal/cli for backward compatibility.
var (
	resolveWorkspacePath = intcli.ResolveWorkspacePath
	hasDependency        = intcli.HasDependency
)

// parseEnvFlags validates and parses environment variable flags into the config.
func parseEnvFlags(envFlags []string, cfg *config.Config) error {
	return intcli.ParseEnvFlags(envFlags, cfg)
}

// shortenPath shortens a path for display, using ~ for home directory.
func shortenPath(path string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return path
	}

	if strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}

	// If still too long, truncate from the start
	const maxLen = 40
	if len(path) > maxLen {
		return "..." + path[len(path)-maxLen+3:]
	}

	return path
}

// validateHost checks if a host string is valid for network allow rules.
// Accepts hostnames, IP addresses, or wildcard patterns (e.g., "*.example.com").
func validateHost(host string) error {
	if host == "" {
		return fmt.Errorf("host cannot be empty")
	}

	// Trim and validate length
	host = strings.TrimSpace(host)
	if len(host) > 253 {
		return fmt.Errorf("host too long (max 253 characters)")
	}

	// Check for obvious invalid characters
	if strings.ContainsAny(host, " \t\n\r/\\:@#?") {
		return fmt.Errorf("contains invalid characters")
	}

	// Handle wildcard patterns (e.g., "*.example.com")
	if strings.HasPrefix(host, "*.") {
		// Validate the rest as a domain
		domain := host[2:]
		if domain == "" {
			return fmt.Errorf("wildcard pattern missing domain")
		}
		return validateDomainPart(domain)
	}

	// Try parsing as IP address
	if ip := net.ParseIP(host); ip != nil {
		return nil // Valid IP address
	}

	// Validate as hostname/domain
	return validateDomainPart(host)
}

// validateDomainPart validates a domain name or hostname.
func validateDomainPart(domain string) error {
	if len(domain) == 0 {
		return fmt.Errorf("domain cannot be empty")
	}

	// Split into labels and validate each
	labels := strings.Split(domain, ".")
	for _, label := range labels {
		if len(label) == 0 {
			return fmt.Errorf("empty label in domain")
		}
		if len(label) > 63 {
			return fmt.Errorf("label %q too long (max 63 characters)", label)
		}
		// Labels must start with alphanumeric
		if !isAlphanumeric(label[0]) {
			return fmt.Errorf("label %q must start with letter or digit", label)
		}
		// Labels can contain alphanumeric and hyphens, but not start/end with hyphen
		for _, c := range label {
			if !isAlphanumeric(byte(c)) && c != '-' {
				return fmt.Errorf("label %q contains invalid character %q", label, c)
			}
		}
		if label[len(label)-1] == '-' {
			return fmt.Errorf("label %q cannot end with hyphen", label)
		}
	}

	return nil
}

// isAlphanumeric returns true if the byte is a letter or digit.
func isAlphanumeric(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// formatTimeAgo formats a time as a human-readable "X ago" string.
func formatTimeAgo(t time.Time) string {
	d := time.Since(t)

	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		mins := int(d.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	case d < 24*time.Hour:
		hours := int(d.Hours())
		if hours == 1 {
			return "1 hour ago"
		}
		return fmt.Sprintf("%d hours ago", hours)
	case d < 7*24*time.Hour:
		days := int(d.Hours() / 24)
		if days == 1 {
			return "1 day ago"
		}
		return fmt.Sprintf("%d days ago", days)
	default:
		return t.Format("Jan 2, 2006")
	}
}
