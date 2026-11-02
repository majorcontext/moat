package githubscope

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	// maxDepth bounds how far below the workspace root discovery looks for
	// nested clones. Depth 0 is the workspace itself.
	maxDepth = 4
	// maxEntries bounds the number of directory entries discovery visits so a
	// huge workspace cannot stall run startup. Stopping early only means fewer
	// repositories are suggested — never that more are granted.
	maxEntries = 200_000
	// maxConfigBytes caps how much of a git config file is read.
	maxConfigBytes = 1 << 20
)

// skipDirs are directory names discovery never descends into. They hold
// dependencies, build output, and tool caches — places package managers leave
// git checkouts of third-party code (Composer --prefer-source in vendor/,
// SwiftPM in .build/checkouts, Terraform in .terraform/modules, CMake
// FetchContent in _deps). Skipping them also keeps discovery fast. A clone
// hidden in one of them is simply never suggested, so this list can only
// narrow access.
var skipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true,
	"node_modules": true, "bower_components": true, "jspm_packages": true,
	".npm": true, ".yarn": true, ".pnpm-store": true,
	".next": true, ".nuxt": true, ".svelte-kit": true, ".turbo": true,
	"vendor": true, ".bundle": true,
	".venv": true, "venv": true, "site-packages": true, "__pycache__": true,
	".tox": true, ".nox": true, ".mypy_cache": true, ".pytest_cache": true,
	"target": true, ".cargo": true, ".gradle": true, ".m2": true,
	".build": true, ".swiftpm": true, "Pods": true, "Carthage": true,
	".terraform": true, ".terragrunt-cache": true,
	"_deps": true, "deps": true, "_build": true,
	".dart_tool": true, ".pub-cache": true, ".stack-work": true, "elm-stuff": true,
	".cache": true, ".direnv": true,
}

// Found is a GitHub repository discovered in a workspace.
type Found struct {
	// Repo is the lowercased "owner/repo".
	Repo string
	// Sources says where it was found, e.g. "./keep (origin)".
	Sources []string
}

// DiscoverResult is the outcome of scanning a workspace.
type DiscoverResult struct {
	Repos []Found
	// Truncated reports that the scan stopped at maxEntries.
	Truncated bool
}

// Discover finds the github.com repositories referenced by the remotes of git
// checkouts in workspace: the workspace itself, nested clones, linked
// worktrees, and checked-out submodules, up to maxDepth levels deep.
//
// It reads git config files directly and never executes git: the workspace is
// writable by the agent, and running git there could execute hooks or
// fsmonitor commands on the host.
func Discover(workspace string) (DiscoverResult, error) {
	root, err := filepath.Abs(workspace)
	if err != nil {
		return DiscoverResult{}, fmt.Errorf("resolving workspace %s: %w", workspace, err)
	}
	if fi, statErr := os.Stat(root); statErr != nil {
		return DiscoverResult{}, fmt.Errorf("reading workspace %s: %w", workspace, statErr)
	} else if !fi.IsDir() {
		return DiscoverResult{}, fmt.Errorf("workspace %s is not a directory", workspace)
	}

	sources := map[string]map[string]bool{}
	add := func(repo, source string) {
		if sources[repo] == nil {
			sources[repo] = map[string]bool{}
		}
		sources[repo][source] = true
	}

	var res DiscoverResult
	visited := 0
	walkErr := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable subdirectories are skipped, not fatal: discovery
			// that misses a repository only grants less.
			if path == root {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		visited++
		if visited > maxEntries {
			res.Truncated = true
			return fs.SkipAll
		}
		if !d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		depth := 0
		if rel != "." {
			depth = strings.Count(rel, string(filepath.Separator)) + 1
			if skipDirs[d.Name()] {
				return fs.SkipDir
			}
		}

		configPath, ok := gitConfigPath(path)
		if ok {
			for repo, remotes := range remotesIn(configPath) {
				add(repo, formatSource(rel, remotes))
			}
		}

		if depth >= maxDepth {
			return fs.SkipDir
		}
		return nil
	})
	if walkErr != nil {
		return DiscoverResult{}, fmt.Errorf("scanning workspace %s: %w", workspace, walkErr)
	}

	for repo, srcs := range sources {
		f := Found{Repo: repo}
		for s := range srcs {
			f.Sources = append(f.Sources, s)
		}
		sort.Strings(f.Sources)
		res.Repos = append(res.Repos, f)
	}
	sort.Slice(res.Repos, func(i, j int) bool { return res.Repos[i].Repo < res.Repos[j].Repo })
	return res, nil
}

// gitConfigPath returns the config file for the checkout rooted at dir, if
// dir has a .git directory or a .git file (worktree or submodule).
func gitConfigPath(dir string) (string, bool) {
	dotGit := filepath.Join(dir, ".git")
	fi, err := os.Lstat(dotGit)
	if err != nil {
		return "", false
	}
	switch {
	case fi.IsDir():
		return filepath.Join(dotGit, "config"), true
	case fi.Mode().IsRegular():
		gitdir, ok := readGitdirFile(dotGit)
		if !ok {
			return "", false
		}
		if !filepath.IsAbs(gitdir) {
			gitdir = filepath.Join(dir, gitdir)
		}
		// A linked worktree keeps its config in the main repository, named by
		// commondir. A submodule's gitdir holds its own config.
		if data, err := readSmallFile(filepath.Join(gitdir, "commondir"), 4096); err == nil {
			common := strings.TrimSpace(string(data))
			if !filepath.IsAbs(common) {
				common = filepath.Join(gitdir, common)
			}
			return filepath.Join(filepath.Clean(common), "config"), true
		}
		return filepath.Join(filepath.Clean(gitdir), "config"), true
	default:
		// Symlinks and other file types are ignored.
		return "", false
	}
}

func readGitdirFile(path string) (string, bool) {
	data, err := readSmallFile(path, 4096)
	if err != nil {
		return "", false
	}
	line := strings.TrimSpace(string(data))
	gitdir, ok := strings.CutPrefix(line, "gitdir:")
	if !ok {
		return "", false
	}
	gitdir = strings.TrimSpace(gitdir)
	return gitdir, gitdir != ""
}

// remotesIn returns repo → remote names for the github.com remotes in the
// config file at path. Unreadable or oversized files yield nothing.
func remotesIn(path string) map[string][]string {
	data, err := readSmallFile(path, maxConfigBytes)
	if err != nil {
		return nil
	}
	urls := parseRemoteURLs(data)
	out := map[string][]string{}
	for _, name := range sortedKeys(urls) {
		seen := map[string]bool{}
		for _, u := range urls[name] {
			repo, ok := ParseRemoteURL(u)
			if !ok || seen[repo] {
				continue
			}
			seen[repo] = true
			out[repo] = append(out[repo], name)
		}
	}
	return out
}

// readSmallFile reads a regular file, refusing anything larger than limit.
func readSmallFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	if fi.Size() > limit {
		return nil, fmt.Errorf("%s is larger than %d bytes", path, limit)
	}
	return io.ReadAll(io.LimitReader(f, limit))
}

func formatSource(rel string, remotes []string) string {
	dir := "./"
	if rel != "." {
		dir = "./" + filepath.ToSlash(rel)
	}
	return fmt.Sprintf("%s (%s)", dir, strings.Join(remotes, ", "))
}
