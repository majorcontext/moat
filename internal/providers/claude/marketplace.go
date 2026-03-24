package claude

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/majorcontext/moat/internal/log"
)

// PreClonedMarketplace describes a marketplace that was cloned on the host
// and will be copied into the Docker build context.
type PreClonedMarketplace struct {
	Name        string // Marketplace name (e.g., "claude-plugins-official")
	Source      string // "github" or "git"
	Repo        string // Repository path (e.g., "anthropics/claude-plugins-official")
	LastUpdated string // ISO 8601 timestamp of the last commit in the repo
}

// maxMarketplaceFileSize is the maximum size of a single file to collect
// from a marketplace repo. Files larger than this are skipped to prevent
// loading large binaries into memory.
const maxMarketplaceFileSize = 1 << 20 // 1 MB

// CollectMarketplaceTar walks a cloned marketplace directory and returns
// a tar archive containing all files. The .git directory is excluded.
// Files larger than 1MB are skipped with a warning.
//
// The tar is returned as a single flat file for the build context, keyed as
// "marketplace-{name}.tar". This works around an Apple container builder bug
// where nested directory contents in the build context are not transferred
// to the builder VM (only ~18KB of metadata is sent instead of the full tree).
// Single flat files transfer correctly on all builders.
func CollectMarketplaceTar(clonedDir, name string) (contextKey string, data []byte, err error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	err = filepath.WalkDir(clonedDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}

		// Skip .git directory entirely.
		if d.IsDir() && d.Name() == ".git" {
			return filepath.SkipDir
		}

		rel, relErr := filepath.Rel(clonedDir, path)
		if relErr != nil {
			return fmt.Errorf("computing relative path: %w", relErr)
		}

		info, infoErr := d.Info()
		if infoErr != nil {
			return fmt.Errorf("stat %s: %w", d.Name(), infoErr)
		}

		// Add directory entries to the tar for correct extraction.
		if d.IsDir() {
			if hdrErr := tw.WriteHeader(&tar.Header{
				Name:     filepath.ToSlash(rel) + "/",
				Mode:     0755,
				Typeflag: tar.TypeDir,
			}); hdrErr != nil {
				return fmt.Errorf("writing dir header for %s: %w", rel, hdrErr)
			}
			return nil
		}

		// Skip files that are too large (e.g., binaries checked into the repo).
		if info.Size() > maxMarketplaceFileSize {
			log.Warn("skipping large file in marketplace",
				"file", filepath.ToSlash(rel),
				"size", info.Size(),
				"limit", maxMarketplaceFileSize)
			return nil
		}

		fileData, readErr := os.ReadFile(path) //nolint:gosec // G304: path is from our own temp clone dir, not user-controlled
		if readErr != nil {
			return fmt.Errorf("reading %s: %w", rel, readErr)
		}

		if hdrErr := tw.WriteHeader(&tar.Header{
			Name: filepath.ToSlash(rel),
			Mode: 0644,
			Size: int64(len(fileData)),
		}); hdrErr != nil {
			return fmt.Errorf("writing tar header for %s: %w", rel, hdrErr)
		}
		if _, wErr := tw.Write(fileData); wErr != nil {
			return fmt.Errorf("writing tar data for %s: %w", rel, wErr)
		}
		return nil
	})
	if err != nil {
		return "", nil, fmt.Errorf("walking marketplace directory: %w", err)
	}

	if closeErr := tw.Close(); closeErr != nil {
		return "", nil, fmt.Errorf("closing tar writer: %w", closeErr)
	}

	contextKey = "marketplace-" + name + ".tar"
	return contextKey, buf.Bytes(), nil
}

// CloneMarketplace clones a marketplace repo to a temporary directory.
// If repo doesn't contain "://" or start with "git@", it is treated as a
// GitHub shorthand and https://github.com/<repo>.git is used.
// Returns the cloned directory path and the ISO 8601 timestamp of the last
// commit (for use in known_marketplaces.json). The caller is responsible for
// removing the returned temp directory.
func CloneMarketplace(ctx context.Context, repo string) (dir string, commitTime string, err error) {
	if !validMarketplaceRepo.MatchString(repo) {
		return "", "", fmt.Errorf("invalid marketplace repo format: %q", repo)
	}

	// Build the list of URLs to try. For GitHub shorthand repos (org/repo),
	// try HTTPS first, then fall back to SSH. This handles hosts that have
	// SSH keys configured but no HTTPS credentials (gh auth / credential helper).
	isGitHubShorthand := !strings.Contains(repo, "://") && !strings.HasPrefix(repo, "git@")
	var urls []string
	if isGitHubShorthand {
		urls = []string{
			"https://github.com/" + repo + ".git",
			"git@github.com:" + repo + ".git",
		}
	} else {
		urls = []string{repo}
	}

	dir, err = os.MkdirTemp("", "moat-marketplace-*")
	if err != nil {
		return "", "", fmt.Errorf("creating temp dir: %w", err)
	}

	var cloneErrors []error
	var cloned bool
	for _, url := range urls {
		args := []string{"clone", "--depth", "1", "--no-recurse-submodules", url, dir}

		cmd := exec.CommandContext(ctx, "git", args...)
		// Prevent git from opening /dev/tty to prompt for credentials.
		// Without this, private repos cause an interactive username/password
		// prompt that blocks the build. Failing fast lets us try the next URL.
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		output, cloneErr := cmd.CombinedOutput()
		if cloneErr == nil {
			cloned = true
			break
		}
		cloneErrors = append(cloneErrors, fmt.Errorf("git clone %s: %w\n%s", url, cloneErr, output))
		// Clean dir contents for the next attempt (MkdirTemp created it,
		// git clone may have partially populated it).
		os.RemoveAll(dir)
		if mkErr := os.MkdirAll(dir, 0700); mkErr != nil {
			os.RemoveAll(dir)
			return "", "", fmt.Errorf("recreating temp dir: %w", mkErr)
		}
	}
	if !cloned {
		os.RemoveAll(dir)
		return "", "", errors.Join(cloneErrors...)
	}

	// Extract the last commit timestamp for deterministic known_marketplaces.json.
	logCmd := exec.CommandContext(ctx, "git", "-C", dir, "log", "-1", "--format=%aI")
	timeOutput, err := logCmd.Output()
	if err != nil {
		commitTime = "1970-01-01T00:00:00Z"
	} else {
		commitTime = strings.TrimSpace(string(timeOutput))
	}

	return dir, commitTime, nil
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
			LastUpdated:     m.LastUpdated,
		}
	}

	data, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshaling known_marketplaces.json: %w", err)
	}

	return data, nil
}
