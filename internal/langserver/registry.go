// Package langserver provides a registry of prepackaged language servers
// that can be enabled with a single line in agent.yaml.
//
// Language servers run inside the container via Claude Code plugins,
// giving AI agents access to code intelligence features like
// go-to-definition, find-references, and diagnostics.
package langserver

import (
	"fmt"
	"sort"
	"strings"
)

// ServerSpec defines a prepackaged language server.
type ServerSpec struct {
	// Name is the language server identifier used in agent.yaml.
	Name string

	// Description is a short human-readable description.
	Description string

	// Plugin is the Claude Code plugin ID (e.g., "gopls-lsp@claude-plugins-official").
	Plugin string

	// Dependencies are runtime dependencies added to the dependency list
	// (e.g., "go@1.25" for the Go language server).
	Dependencies []string

	// InstallDeps are binary install dependencies added to the dependency list
	// (e.g., "gopls" or "npm:pyright").
	InstallDeps []string
}

// registry holds all known language server specs keyed by language name.
var registry = map[string]ServerSpec{
	"go": {
		Name:         "go",
		Description:  "Go language server (code intelligence, refactoring, diagnostics)",
		Plugin:       "gopls-lsp@claude-plugins-official",
		Dependencies: []string{"go@1.25"},
		InstallDeps:  []string{"gopls"},
	},
	"typescript": {
		Name:         "typescript",
		Description:  "TypeScript/JavaScript language server (code intelligence, diagnostics)",
		Plugin:       "typescript-lsp@claude-plugins-official",
		Dependencies: []string{"node@20"},
		InstallDeps:  []string{"npm:typescript", "npm:typescript-language-server"},
	},
	"python": {
		Name:         "python",
		Description:  "Python language server (code intelligence, type checking, diagnostics)",
		Plugin:       "pyright-lsp@claude-plugins-official",
		Dependencies: []string{"python"},
		InstallDeps:  []string{"npm:pyright"},
	},
}

// Get returns the ServerSpec for a language server name, or ok=false.
func Get(name string) (ServerSpec, bool) {
	spec, ok := registry[name]
	return spec, ok
}

// Validate checks that all language server names are recognized.
func Validate(names []string) error {
	for _, name := range names {
		if _, ok := registry[name]; !ok {
			return fmt.Errorf("unknown language server %q\n\n  Available language servers: %s", name, listNames())
		}
	}
	return nil
}

// AllDependencies returns the union of all dependencies required by the
// given language servers, including the install dependencies for each server.
func AllDependencies(names []string) []string {
	seen := make(map[string]bool)
	var deps []string
	for _, name := range names {
		spec, ok := registry[name]
		if !ok {
			continue
		}
		for _, d := range spec.Dependencies {
			if !seen[d] {
				seen[d] = true
				deps = append(deps, d)
			}
		}
		for _, d := range spec.InstallDeps {
			if !seen[d] {
				seen[d] = true
				deps = append(deps, d)
			}
		}
	}
	return deps
}

// Plugins returns the Claude Code plugin IDs for the given language servers.
// Unknown names are silently skipped.
func Plugins(names []string) []string {
	var plugins []string
	seen := make(map[string]bool)
	for _, name := range names {
		spec, ok := registry[name]
		if !ok {
			continue
		}
		if spec.Plugin != "" && !seen[spec.Plugin] {
			seen[spec.Plugin] = true
			plugins = append(plugins, spec.Plugin)
		}
	}
	return plugins
}

// List returns all available language server names sorted.
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// listNames returns a comma-separated list of available names.
func listNames() string {
	names := List()
	if len(names) == 0 {
		return "(none)"
	}
	return strings.Join(names, ", ")
}
