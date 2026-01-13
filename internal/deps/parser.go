package deps

import (
	"fmt"
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
			return nil, fmt.Errorf("unknown dependency %q", dep.Name)
		}
		deps = append(deps, dep)
	}
	return deps, nil
}
