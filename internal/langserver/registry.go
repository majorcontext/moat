// Package langserver provides a registry of prepackaged language servers
// that can be enabled with a single line in agent.yaml.
//
// Language servers run inside the container as MCP (stdio) processes,
// giving AI agents access to code intelligence features like
// go-to-definition, find-references, and diagnostics.
package langserver

import "fmt"

// ServerSpec defines a prepackaged language server.
type ServerSpec struct {
	// Name is the language server identifier used in agent.yaml.
	Name string

	// Description is a short human-readable description.
	Description string

	// Command is the executable to run inside the container.
	Command string

	// Args are command-line arguments for the server process.
	Args []string

	// Dependencies are additional entries to add to the dependency list.
	// These are installed during image build (e.g., "go" for gopls).
	Dependencies []string

	// InstallDep is the dependency registry name that installs the server binary.
	// If empty, the server is assumed to already be available via Dependencies.
	// Example: "gopls" installs via go-install in the deps registry.
	InstallDep string
}

// registry holds all known language server specs keyed by name.
var registry = map[string]ServerSpec{
	"gopls": {
		Name:         "gopls",
		Description:  "Go language server with MCP support (code intelligence, refactoring, diagnostics)",
		Command:      "gopls",
		Args:         []string{"mcp"},
		Dependencies: []string{"go"},
		InstallDep:   "gopls",
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
// given language servers, including the install dependency for each server.
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
		if spec.InstallDep != "" && !seen[spec.InstallDep] {
			seen[spec.InstallDep] = true
			deps = append(deps, spec.InstallDep)
		}
	}
	return deps
}

// MCPConfigs returns MCP server configurations for the given language servers.
// Each entry maps the server name to its command/args for use as a
// stdio-based MCP server inside the container.
func MCPConfigs(names []string) map[string]MCPConfig {
	configs := make(map[string]MCPConfig)
	for _, name := range names {
		spec, ok := registry[name]
		if !ok {
			continue
		}
		configs[name] = MCPConfig{
			Command: spec.Command,
			Args:    spec.Args,
		}
	}
	return configs
}

// MCPConfig holds the MCP server configuration for a language server.
type MCPConfig struct {
	Command string
	Args    []string
}

// List returns all available language server names sorted.
func List() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// listNames returns a comma-separated list of available names.
func listNames() string {
	names := List()
	if len(names) == 0 {
		return "(none)"
	}
	result := names[0]
	for _, n := range names[1:] {
		result += ", " + n
	}
	return result
}
