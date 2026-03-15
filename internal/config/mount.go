package config

import (
	"fmt"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// MountEntry represents a mount configuration. It supports two YAML forms:
// - String: "source:target[:mode]" (existing format)
// - Object: {source, target, mode, exclude} (new format with exclude support)
type MountEntry struct {
	Source   string   `yaml:"source"`
	Target   string   `yaml:"target"`
	Mode     string   `yaml:"mode,omitempty"`
	ReadOnly bool     `yaml:"-"`
	Exclude  []string `yaml:"exclude,omitempty"`
}

// UnmarshalYAML handles both string and object forms in a mixed-type YAML array.
func (m *MountEntry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		// String form: "source:target[:mode]"
		parsed, err := parseMount(value.Value)
		if err != nil {
			return err
		}
		*m = *parsed
		return nil

	case yaml.MappingNode:
		// Object form: {source, target, mode, exclude}
		// Use an alias type to avoid infinite recursion.
		type mountAlias struct {
			Source  string   `yaml:"source"`
			Target  string   `yaml:"target"`
			Mode    string   `yaml:"mode"`
			Exclude []string `yaml:"exclude"`
		}
		var raw mountAlias
		if err := value.Decode(&raw); err != nil {
			return fmt.Errorf("parsing mount object: %w", err)
		}
		if raw.Source == "" {
			return fmt.Errorf("mount object: 'source' is required")
		}
		if raw.Target == "" {
			return fmt.Errorf("mount object: 'target' is required")
		}
		if !filepath.IsAbs(raw.Target) {
			return fmt.Errorf("mount object: 'target' must be an absolute path, got %q", raw.Target)
		}
		switch raw.Mode {
		case "", "rw":
			// default read-write
		case "ro":
			m.ReadOnly = true
			m.Mode = "ro"
		default:
			return fmt.Errorf("mount object: invalid mode %q (must be 'ro' or 'rw')", raw.Mode)
		}
		m.Source = raw.Source
		m.Target = raw.Target
		m.Exclude = raw.Exclude
		return nil

	default:
		return fmt.Errorf("mount entry must be a string or object, got %v", value.Kind)
	}
}

// parseMount parses a mount string like "./data:/data:ro".
func parseMount(s string) (*MountEntry, error) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return nil, fmt.Errorf("invalid mount: %s (expected source:target[:mode])", s)
	}

	if parts[0] == "" {
		return nil, fmt.Errorf("invalid mount: %s (source must not be empty)", s)
	}

	if !filepath.IsAbs(parts[1]) {
		return nil, fmt.Errorf("invalid mount: %s (target must be an absolute path)", s)
	}

	m := &MountEntry{
		Source: parts[0],
		Target: parts[1],
	}

	if len(parts) == 3 {
		switch parts[2] {
		case "ro":
			m.ReadOnly = true
			m.Mode = "ro"
		case "rw", "":
			// default read-write
		default:
			return nil, fmt.Errorf("invalid mount: %s (mode must be 'ro' or 'rw')", s)
		}
	}

	return m, nil
}

// ParseMount parses a mount string like "./data:/data:ro".
// This is the public API used by the run manager for --mount CLI flags.
func ParseMount(s string) (*MountEntry, error) {
	return parseMount(s)
}

// ValidateExcludes validates exclude paths on a MountEntry.
// Paths are normalized with filepath.Clean before validation.
// Returns the cleaned exclude list or an error.
func ValidateExcludes(excludes []string, target string) ([]string, error) {
	if len(excludes) == 0 {
		return nil, nil
	}

	seen := make(map[string]bool, len(excludes))
	cleaned := make([]string, 0, len(excludes))

	for _, exc := range excludes {
		if exc == "" {
			return nil, fmt.Errorf("mount %s: exclude path must not be empty", target)
		}

		// Check for ".." in the raw path before cleaning (filepath.Clean
		// resolves "foo/../bar" to "bar", hiding the traversal).
		for _, part := range strings.Split(exc, "/") {
			if part == ".." {
				return nil, fmt.Errorf("mount %s: exclude path %q must not contain '..'", target, exc)
			}
		}

		c := filepath.Clean(exc)

		// After cleaning, reject "." (e.g., from "./")
		if c == "." {
			return nil, fmt.Errorf("mount %s: exclude path %q resolves to current directory", target, exc)
		}

		// Must be relative
		if filepath.IsAbs(c) {
			return nil, fmt.Errorf("mount %s: exclude path %q must be relative", target, exc)
		}

		// No duplicates
		if seen[c] {
			return nil, fmt.Errorf("mount %s: duplicate exclude path %q", target, c)
		}
		seen[c] = true
		cleaned = append(cleaned, c)
	}

	// Check for overlapping excludes (e.g., "foo" and "foo/bar" — the
	// deeper path is already shadowed by the parent tmpfs).
	for i, a := range cleaned {
		for _, b := range cleaned[i+1:] {
			if strings.HasPrefix(b+"/", a+"/") {
				return nil, fmt.Errorf("mount %s: exclude %q is redundant with %q", target, b, a)
			}
			if strings.HasPrefix(a+"/", b+"/") {
				return nil, fmt.Errorf("mount %s: exclude %q is redundant with %q", target, a, b)
			}
		}
	}

	return cleaned, nil
}
