package deps

import (
	"fmt"
	"sort"
	"strings"
)

// Parse parses a dependency string like "node" or "node@20".
func Parse(s string) (Dependency, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Dependency{}, fmt.Errorf("empty dependency")
	}

	parts := strings.SplitN(s, "@", 2)
	name := parts[0]
	if name == "" {
		return Dependency{}, fmt.Errorf("invalid dependency %q: missing name", s)
	}

	var version string
	if len(parts) == 2 {
		version = parts[1]
	}

	return Dependency{Name: name, Version: version}, nil
}

// ParseAll parses multiple dependency strings and validates they exist in the registry.
func ParseAll(specs []string) ([]Dependency, error) {
	deps := make([]Dependency, 0, len(specs))
	for _, s := range specs {
		dep, err := Parse(s)
		if err != nil {
			return nil, err
		}
		if _, ok := Registry[dep.Name]; !ok {
			return nil, fmt.Errorf("unknown dependency %q%s", dep.Name, suggestDep(dep.Name))
		}
		deps = append(deps, dep)
	}
	return deps, nil
}

// Validate checks that all dependency requirements are satisfied.
func Validate(deps []Dependency) error {
	// Build set of dependency names
	depSet := make(map[string]bool)
	for _, d := range deps {
		depSet[d.Name] = true
	}

	// Check requirements
	for _, d := range deps {
		spec, ok := Registry[d.Name]
		if !ok {
			return fmt.Errorf("unknown dependency %q%s", d.Name, suggestDep(d.Name))
		}
		for _, req := range spec.Requires {
			if !depSet[req] {
				return fmt.Errorf("%s requires %s\n\n  Add '%s' to your dependencies:\n    dependencies:\n      - %s\n      - %s",
					d.Name, req, req, req, d.Name)
			}
		}
	}
	return nil
}

// suggestDep returns a suggestion message if a similar dependency exists.
func suggestDep(name string) string {
	suggestions := map[string]string{
		"nodejs":   "node",
		"node.js":  "node",
		"golang":   "go",
		"python3":  "python",
		"py":       "python",
		"postgres": "psql",
		"pg":       "psql",
		"awscli":   "aws",
		"aws-cli":  "aws",
		"gcp":      "gcloud",
	}
	if sugg, ok := suggestions[name]; ok {
		return fmt.Sprintf("\n  Did you mean '%s'?", sugg)
	}

	// Check for close matches in registry
	for regName := range Registry {
		if strings.Contains(regName, name) || strings.Contains(name, regName) {
			return fmt.Sprintf("\n  Did you mean '%s'?", regName)
		}
	}
	return ""
}

// List returns all available dependency names sorted alphabetically.
func List() []string {
	names := make([]string, 0, len(Registry))
	for name := range Registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
