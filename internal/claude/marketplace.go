package claude

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MarketplaceManager handles cloning and updating marketplace repositories.
type MarketplaceManager struct {
	// CacheDir is the base directory for marketplace cache (typically ~/.moat/claude/plugins)
	CacheDir string
}

// NewMarketplaceManager creates a new marketplace manager.
func NewMarketplaceManager(cacheDir string) *MarketplaceManager {
	return &MarketplaceManager{CacheDir: cacheDir}
}

// DefaultCacheDir returns the default plugin cache directory.
func DefaultCacheDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "claude", "plugins"), nil
}

// MarketplacesDir returns the path to the marketplaces directory.
func (m *MarketplaceManager) MarketplacesDir() string {
	return filepath.Join(m.CacheDir, "marketplaces")
}

// MarketplacePath returns the path to a specific marketplace.
// Returns empty string if name contains path traversal characters.
func (m *MarketplaceManager) MarketplacePath(name string) string {
	// Prevent path traversal attacks
	if strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return ""
	}
	// Verify the cleaned name is the same (catches edge cases)
	cleaned := filepath.Clean(name)
	if cleaned != name || cleaned == "." {
		return ""
	}
	return filepath.Join(m.MarketplacesDir(), name)
}

// EnsureMarketplace ensures a marketplace is cloned and up to date.
// For SSH URLs, it validates that SSH grants are available.
func (m *MarketplaceManager) EnsureMarketplace(name string, entry MarketplaceEntry, sshHosts []string) error {
	switch entry.Source.Source {
	case "directory":
		// Directory sources are used directly, no cloning needed
		if _, err := os.Stat(entry.Source.Path); err != nil {
			return fmt.Errorf("marketplace %q: directory not found: %s", name, entry.Source.Path)
		}
		return nil

	case "git":
		return m.ensureGitMarketplace(name, entry.Source.URL, "", sshHosts)

	default:
		return fmt.Errorf("marketplace %q: unsupported source type %q", name, entry.Source.Source)
	}
}

// ensureGitMarketplace clones or updates a git repository.
func (m *MarketplaceManager) ensureGitMarketplace(name, url, ref string, sshHosts []string) error {
	// Validate SSH access if needed
	if IsSSHURL(url) {
		host := ExtractHost(url)
		if !hasSSHGrant(sshHosts, host) {
			return &MarketplaceAccessError{
				Name:   name,
				URL:    url,
				Host:   host,
				Reason: "no SSH grant configured",
			}
		}
	}

	marketplacePath := m.MarketplacePath(name)

	// Check if already cloned
	if _, err := os.Stat(filepath.Join(marketplacePath, ".git")); err == nil {
		// Already cloned, pull latest
		return m.updateMarketplace(marketplacePath, ref)
	}

	// Clone the repository
	if err := os.MkdirAll(m.MarketplacesDir(), 0755); err != nil {
		return fmt.Errorf("creating marketplaces directory: %w", err)
	}

	args := []string{"clone", "--depth=1"}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, url, marketplacePath)

	cmd := exec.Command("git", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("cloning marketplace %q: %w", name, err)
	}

	return nil
}

// updateMarketplace pulls the latest changes from a marketplace repository.
func (m *MarketplaceManager) updateMarketplace(path, ref string) error {
	// If a specific ref is requested, check it out
	if ref != "" {
		cmd := exec.Command("git", "fetch", "origin", ref)
		cmd.Dir = path
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("fetching ref %q: %w", ref, err)
		}

		cmd = exec.Command("git", "checkout", "FETCH_HEAD")
		cmd.Dir = path
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("checking out ref %q: %w", ref, err)
		}
		return nil
	}

	// Otherwise, pull latest from default branch
	cmd := exec.Command("git", "pull", "--ff-only")
	cmd.Dir = path
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Non-fatal - might be on a detached HEAD or have local changes
		// Just log and continue
		return nil
	}
	return nil
}

// EnsureAllMarketplaces ensures all marketplaces in settings are available.
func (m *MarketplaceManager) EnsureAllMarketplaces(settings *Settings, sshHosts []string) error {
	if settings == nil {
		return nil
	}

	for name, entry := range settings.ExtraKnownMarketplaces {
		if err := m.EnsureMarketplace(name, entry, sshHosts); err != nil {
			return err
		}
	}

	return nil
}

// ValidateSSHAccess checks if all SSH-based marketplaces have grants configured.
// This is a fast-fail check that should be called early in the run setup.
func ValidateSSHAccess(settings *Settings, sshHosts []string) error {
	if settings == nil {
		return nil
	}

	for name, entry := range settings.ExtraKnownMarketplaces {
		if entry.Source.Source != "git" {
			continue
		}
		url := entry.Source.URL
		if !IsSSHURL(url) {
			continue
		}

		host := ExtractHost(url)
		if !hasSSHGrant(sshHosts, host) {
			return &MarketplaceAccessError{
				Name:   name,
				URL:    url,
				Host:   host,
				Reason: "no SSH grant configured",
			}
		}
	}

	return nil
}

// MarketplaceAccessError is returned when a marketplace cannot be accessed
// due to missing SSH credentials.
type MarketplaceAccessError struct {
	Name   string
	URL    string
	Host   string
	Reason string
}

func (e *MarketplaceAccessError) Error() string {
	return fmt.Sprintf(`cannot access marketplace %q

The marketplace at %s requires SSH access to %s,
but %s.

To fix this:

1. Grant SSH access:
   moat grant ssh --host %s

2. Add the grant to your agent.yaml:
   grants:
     - ssh:%s

3. Ensure your SSH key is loaded:
   ssh-add -l   # Should show your key`,
		e.Name, e.URL, e.Host, e.Reason, e.Host, e.Host)
}

// IsSSHURL returns true if the URL is an SSH URL.
// SSH URLs have these formats:
// - git@github.com:org/repo.git
// - ssh://git@github.com/org/repo.git
func IsSSHURL(url string) bool {
	// Check for ssh:// scheme
	if strings.HasPrefix(url, "ssh://") {
		return true
	}
	// Check for git@host:path format (SCP-like syntax)
	if strings.Contains(url, "@") && strings.Contains(url, ":") && !strings.Contains(url, "://") {
		return true
	}
	return false
}

// ExtractHost extracts the hostname from a git URL.
// Handles both HTTPS and SSH URL formats.
func ExtractHost(url string) string {
	// ssh://git@github.com/org/repo.git or ssh://git@gitlab.com:22/org/repo.git
	if strings.HasPrefix(url, "ssh://") {
		url = strings.TrimPrefix(url, "ssh://")
		// Remove user@ if present
		if idx := strings.Index(url, "@"); idx >= 0 {
			url = url[idx+1:]
		}
		// Get host (before / or :port)
		// Handle both / (path) and : (port) - take whichever comes first
		slashIdx := strings.Index(url, "/")
		colonIdx := strings.Index(url, ":")

		if slashIdx >= 0 && colonIdx >= 0 {
			if slashIdx < colonIdx {
				return url[:slashIdx]
			}
			return url[:colonIdx]
		}
		if slashIdx >= 0 {
			return url[:slashIdx]
		}
		if colonIdx >= 0 {
			return url[:colonIdx]
		}
		return url
	}

	// git@github.com:org/repo.git (SCP-like syntax)
	if strings.Contains(url, "@") && strings.Contains(url, ":") && !strings.Contains(url, "://") {
		// Find @ and :
		atIdx := strings.Index(url, "@")
		colonIdx := strings.Index(url, ":")
		if atIdx >= 0 && colonIdx > atIdx {
			return url[atIdx+1 : colonIdx]
		}
	}

	// https://github.com/org/repo.git
	if strings.HasPrefix(url, "https://") {
		url = strings.TrimPrefix(url, "https://")
		if idx := strings.Index(url, "/"); idx >= 0 {
			return url[:idx]
		}
		return url
	}

	// http://github.com/org/repo.git
	if strings.HasPrefix(url, "http://") {
		url = strings.TrimPrefix(url, "http://")
		if idx := strings.Index(url, "/"); idx >= 0 {
			return url[:idx]
		}
		return url
	}

	return ""
}

// FilterSSHGrants extracts SSH grants from a grants list.
// SSH grants have the format "ssh:hostname" or just "ssh" (for all configured hosts).
func FilterSSHGrants(grants []string) []string {
	var sshHosts []string
	for _, grant := range grants {
		if grant == "ssh" {
			// "ssh" alone means all configured SSH hosts
			continue
		}
		if strings.HasPrefix(grant, "ssh:") {
			host := strings.TrimPrefix(grant, "ssh:")
			sshHosts = append(sshHosts, host)
		}
	}
	return sshHosts
}

// hasSSHGrant checks if a host is covered by the SSH grants.
func hasSSHGrant(sshHosts []string, host string) bool {
	for _, h := range sshHosts {
		if h == host {
			return true
		}
		// Support wildcard patterns like *.github.com
		if strings.HasPrefix(h, "*.") {
			suffix := h[1:] // Remove the *
			if strings.HasSuffix(host, suffix) {
				return true
			}
		}
	}
	return false
}

// ConvertToDirectorySource converts a marketplace entry to use directory source.
// This is used when generating container settings to point to the mounted cache.
func ConvertToDirectorySource(name string, entry MarketplaceEntry, mountPath string) MarketplaceEntry {
	if entry.Source.Source == "directory" {
		return entry
	}

	return MarketplaceEntry{
		Source: MarketplaceSource{
			Source: "directory",
			Path:   filepath.Join(mountPath, "marketplaces", name),
		},
	}
}
