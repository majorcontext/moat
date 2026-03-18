package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// PreClonedMarketplace describes a marketplace that was cloned on the host
// and will be copied into the Docker build context.
type PreClonedMarketplace struct {
	Name   string // Marketplace name (e.g., "claude-plugins-official")
	Source string // "github" or "git"
	Repo   string // Repository path (e.g., "anthropics/claude-plugins-official")
}

// CollectMarketplaceFiles walks a cloned marketplace directory and returns
// all files keyed by their build-context-relative path. The .git directory
// is excluded. Paths use forward slashes for Docker compatibility.
func CollectMarketplaceFiles(clonedDir, name string) (map[string][]byte, error) {
	files := make(map[string][]byte)

	err := filepath.WalkDir(clonedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip .git directory entirely.
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		// Skip directories — only collect files.
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(clonedDir, path)
		if err != nil {
			return fmt.Errorf("computing relative path: %w", err)
		}

		data, err := os.ReadFile(path) //nolint:gosec // G304: path is from our own temp clone dir, not user-controlled
		if err != nil {
			return fmt.Errorf("reading %s: %w", rel, err)
		}

		key := "marketplaces/" + name + "/" + filepath.ToSlash(rel)
		files[key] = data
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking marketplace directory: %w", err)
	}

	return files, nil
}

// CloneMarketplace clones a marketplace repo to a temporary directory.
// If repo doesn't contain "://" or start with "git@", it is treated as a
// GitHub shorthand and https://github.com/<repo>.git is used.
// The caller is responsible for removing the returned temp directory.
func CloneMarketplace(ctx context.Context, repo string) (string, error) {
	if !validMarketplaceRepo.MatchString(repo) {
		return "", fmt.Errorf("invalid marketplace repo format: %q", repo)
	}

	url := repo
	if !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@") {
		url = "https://github.com/" + repo + ".git"
	}

	dir, err := os.MkdirTemp("", "moat-marketplace-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}

	args := []string{"clone", "--depth", "1", "--no-recurse-submodules", url, dir}

	cmd := exec.CommandContext(ctx, "git", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Clean up on failure.
		os.RemoveAll(dir)
		return "", fmt.Errorf("git clone %s: %w\n%s", url, err, output)
	}

	return dir, nil
}

// knownMarketplaceEntry is the JSON structure for a single entry in
// Claude Code's known_marketplaces.json file.
type knownMarketplaceEntry struct {
	Source          knownMarketplaceSource `json:"source"`
	InstallLocation string                 `json:"installLocation"`
	LastUpdated     string                 `json:"lastUpdated"`
}

// knownMarketplaceSource describes the origin of a marketplace.
type knownMarketplaceSource struct {
	Source string `json:"source"`
	Repo   string `json:"repo,omitempty"`
	URL    string `json:"url,omitempty"`
}

// GenerateKnownMarketplaces generates Claude Code's known_marketplaces.json
// content for pre-cloned marketplaces. Each entry records the source, install
// location, and timestamp so Claude Code recognizes the marketplace without
// needing to clone it again.
//
// Returns "{}" when the input slice is nil or empty.
func GenerateKnownMarketplaces(marketplaces []PreClonedMarketplace, containerUser string) ([]byte, error) {
	if len(marketplaces) == 0 {
		return []byte("{}"), nil
	}

	now := time.Now().UTC().Format(time.RFC3339)

	entries := make(map[string]knownMarketplaceEntry, len(marketplaces))
	for _, m := range marketplaces {
		installLocation := fmt.Sprintf("/home/%s/.claude/plugins/marketplaces/%s", containerUser, m.Name)
		src := knownMarketplaceSource{Source: m.Source}
		if m.Source == "github" {
			src.Repo = m.Repo
		} else {
			src.URL = m.Repo
		}
		entries[m.Name] = knownMarketplaceEntry{
			Source:          src,
			InstallLocation: installLocation,
			LastUpdated:     now,
		}
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling known_marketplaces.json: %w", err)
	}

	return data, nil
}
