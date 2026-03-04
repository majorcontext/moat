package quickstart

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// extensionToLanguage maps file extensions to language/tool names.
var extensionToLanguage = map[string]string{
	".go":    "Go",
	".py":    "Python",
	".js":    "JavaScript",
	".ts":    "TypeScript",
	".tsx":   "TypeScript (React)",
	".jsx":   "JavaScript (React)",
	".rb":    "Ruby",
	".rs":    "Rust",
	".java":  "Java",
	".kt":    "Kotlin",
	".swift": "Swift",
	".c":     "C",
	".cpp":   "C++",
	".h":     "C/C++ Header",
	".cs":    "C#",
	".php":   "PHP",
	".sh":    "Shell",
	".bash":  "Shell",
	".zsh":   "Shell",
	".bats":  "Bats (Bash tests)",
	".lua":   "Lua",
	".ex":    "Elixir",
	".exs":   "Elixir",
	".erl":   "Erlang",
	".zig":   "Zig",
	".sql":   "SQL",
	".proto": "Protocol Buffers",
}

// knownManifests maps manifest filenames to what they indicate.
var knownManifests = map[string]string{
	"package.json":        "Node.js (package.json)",
	"go.mod":              "Go (go.mod)",
	"pyproject.toml":      "Python (pyproject.toml)",
	"requirements.txt":    "Python (requirements.txt)",
	"Pipfile":             "Python (Pipfile)",
	"setup.py":            "Python (setup.py)",
	"Gemfile":             "Ruby (Gemfile)",
	"Cargo.toml":          "Rust (Cargo.toml)",
	"pom.xml":             "Java (Maven)",
	"build.gradle":        "Java (Gradle)",
	"composer.json":       "PHP (Composer)",
	"mix.exs":             "Elixir (Mix)",
	"Makefile":            "Makefile",
	"Taskfile.yml":        "Taskfile",
	"Taskfile.yaml":       "Taskfile",
	"Dockerfile":          "Dockerfile",
	"docker-compose.yml":  "Docker Compose",
	"docker-compose.yaml": "Docker Compose",
	".python-version":     "Python version file",
	".node-version":       "Node version file",
	".nvmrc":              "Node version file (nvm)",
	".ruby-version":       "Ruby version file",
	".tool-versions":      "asdf version manager",
	"go.sum":              "Go (go.sum)",
	"yarn.lock":           "Yarn lockfile",
	"pnpm-lock.yaml":      "pnpm lockfile",
	"package-lock.json":   "npm lockfile",
	"bun.lockb":           "Bun lockfile",
	"uv.lock":             "uv lockfile",
	"Cargo.lock":          "Rust (Cargo.lock)",
	"Gemfile.lock":        "Ruby (Gemfile.lock)",
}

// skipDirs are directories to skip when scanning.
var skipDirs = map[string]bool{
	"node_modules": true,
	".git":         true,
	"vendor":       true,
	".venv":        true,
	"venv":         true,
	"__pycache__":  true,
	".tox":         true,
	"dist":         true,
	"build":        true,
	"target":       true,
	".worktrees":   true,
}

// maxScanDepth limits directory traversal to prevent excessive scan times
// in very deep or pathological directory trees.
const maxScanDepth = 15

// ScanWorkspace walks the workspace and produces a summary of detected
// file types and manifest files. The output is a markdown section suitable
// for inclusion in a quickstart prompt.
func ScanWorkspace(root string) string {
	extCounts := make(map[string]int)
	var manifests []string
	var ciFiles []string

	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip errors
		}

		// Skip symlinks to avoid walking outside the workspace.
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		name := d.Name()
		rel, _ := filepath.Rel(root, path)

		if d.IsDir() {
			if skipDirs[name] {
				return filepath.SkipDir
			}
			// Enforce depth limit.
			depth := strings.Count(rel, string(os.PathSeparator))
			if depth >= maxScanDepth {
				return filepath.SkipDir
			}
			return nil
		}

		// Check manifests (only in root or one level deep).
		depth := strings.Count(rel, string(os.PathSeparator))
		if depth <= 1 {
			if desc, ok := knownManifests[name]; ok {
				manifests = append(manifests, desc)
			}
		}

		// Check CI configs.
		if strings.HasPrefix(rel, ".github/workflows/") && (strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")) {
			ciFiles = append(ciFiles, rel)
		}
		if strings.HasPrefix(rel, ".circleci/") || name == ".travis.yml" || name == "Jenkinsfile" {
			ciFiles = append(ciFiles, rel)
		}

		// Count file extensions.
		ext := strings.ToLower(filepath.Ext(name))
		if ext != "" {
			if _, known := extensionToLanguage[ext]; known {
				extCounts[ext]++
			}
		}

		// Special: Makefile has no extension.
		if name == "Makefile" || name == "GNUmakefile" {
			extCounts["Makefile"]++
		}

		return nil
	})

	var b strings.Builder
	b.WriteString("## Project Scan Results\n\n")
	b.WriteString("The following was detected automatically by scanning the workspace.\n")
	b.WriteString("Use this to inform your moat.yaml — every language/tool detected here\n")
	b.WriteString("likely needs a corresponding dependency.\n\n")

	// File types.
	if len(extCounts) > 0 {
		b.WriteString("### Detected File Types\n\n")

		type extEntry struct {
			ext   string
			count int
			lang  string
		}

		entries := make([]extEntry, 0, len(extCounts))
		for ext, count := range extCounts {
			lang := ext
			if ext == "Makefile" {
				lang = "Makefile (make)"
			} else if l, ok := extensionToLanguage[ext]; ok {
				lang = l
			}
			entries = append(entries, extEntry{ext, count, lang})
		}

		// Sort by count descending.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].count > entries[j].count
		})

		for _, e := range entries {
			fmt.Fprintf(&b, "- %s: %d files\n", e.lang, e.count)
		}
		b.WriteString("\n")
	}

	// Manifests.
	if len(manifests) > 0 {
		b.WriteString("### Detected Manifest Files\n\n")
		// Deduplicate.
		seen := make(map[string]bool)
		for _, m := range manifests {
			if !seen[m] {
				seen[m] = true
				fmt.Fprintf(&b, "- %s\n", m)
			}
		}
		b.WriteString("\n")
	}

	// CI.
	if len(ciFiles) > 0 {
		b.WriteString("### CI/CD Configs\n\n")
		for _, f := range ciFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	return b.String()
}
