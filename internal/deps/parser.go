package deps

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
)

// Dynamic dependency prefixes and their corresponding types
var dynamicPrefixes = map[string]InstallType{
	"npm:":   TypeDynamicNpm,
	"pip:":   TypeDynamicPip,
	"uv:":    TypeDynamicUv,
	"cargo:": TypeDynamicCargo,
	"go:":    TypeDynamicGo,
}

// Parse parses a dependency string like "node", "node@20", or "npm:eslint".
func Parse(s string) (Dependency, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Dependency{}, fmt.Errorf("empty dependency")
	}

	// Check for dynamic package manager prefixes
	for prefix, depType := range dynamicPrefixes {
		if strings.HasPrefix(s, prefix) {
			return parseDynamicDep(s, prefix, depType)
		}
	}

	// Standard registry-based dependency
	parts := strings.SplitN(s, "@", 2)
	name := parts[0]
	if name == "" {
		return Dependency{}, fmt.Errorf("invalid dependency %q: missing name", s)
	}

	var version string
	if len(parts) == 2 {
		version = parts[1]
		if err := validateVersion(version); err != nil {
			return Dependency{}, fmt.Errorf("invalid dependency %q: %w", s, err)
		}
	}

	return Dependency{Name: name, Version: version}, nil
}

// parseDynamicDep parses a dynamic dependency like "npm:eslint@8.0.0".
func parseDynamicDep(s, prefix string, depType InstallType) (Dependency, error) {
	remainder := strings.TrimPrefix(s, prefix)
	if remainder == "" {
		return Dependency{}, fmt.Errorf("invalid dependency %q: missing package name after %s", s, prefix)
	}

	// Split on @ to separate package from version
	// But be careful with scoped npm packages like @anthropic-ai/claude-code
	var pkg, version string

	if depType == TypeDynamicNpm && strings.HasPrefix(remainder, "@") {
		// Scoped npm package: @scope/name or @scope/name@version
		// Find the second @ if it exists
		rest := remainder[1:] // skip first @
		if idx := strings.Index(rest, "@"); idx >= 0 {
			pkg = "@" + rest[:idx]
			version = rest[idx+1:]
		} else {
			pkg = remainder
		}
	} else {
		// Regular package: name or name@version
		parts := strings.SplitN(remainder, "@", 2)
		pkg = parts[0]
		if len(parts) == 2 {
			version = parts[1]
		}
	}

	// Validate package name
	if err := validatePackageName(pkg, depType); err != nil {
		return Dependency{}, fmt.Errorf("invalid dependency %q: %w", s, err)
	}

	// Validate version if present
	if version != "" {
		if err := validateVersion(version); err != nil {
			return Dependency{}, fmt.Errorf("invalid dependency %q: %w", s, err)
		}
	}

	return Dependency{
		Name:    packageDisplayName(pkg, depType),
		Version: version,
		Type:    depType,
		Package: pkg,
	}, nil
}

// packageDisplayName returns a display name for a dynamic package.
func packageDisplayName(pkg string, depType InstallType) string {
	// Use the package name directly, but strip scope for display if desired
	// For now, just return the full package name
	switch depType {
	case TypeDynamicNpm:
		// For npm, show full package name including scope
		return pkg
	case TypeDynamicGo:
		// For Go, show just the binary name (last path component)
		parts := strings.Split(pkg, "/")
		return parts[len(parts)-1]
	default:
		return pkg
	}
}

// validatePackageName validates a package name for a given type.
func validatePackageName(pkg string, depType InstallType) error {
	if pkg == "" {
		return fmt.Errorf("empty package name")
	}

	switch depType {
	case TypeDynamicNpm:
		return validateNpmPackage(pkg)
	case TypeDynamicPip, TypeDynamicUv:
		return validatePipPackage(pkg)
	case TypeDynamicCargo:
		return validateCargoPackage(pkg)
	case TypeDynamicGo:
		return validateGoPackage(pkg)
	default:
		return nil
	}
}

// validateNpmPackage validates an npm package name.
func validateNpmPackage(pkg string) error {
	// npm packages: alphanumeric, hyphens, underscores, dots
	// Scoped packages: @scope/name
	if strings.HasPrefix(pkg, "@") {
		parts := strings.SplitN(pkg[1:], "/", 2)
		if len(parts) != 2 {
			return fmt.Errorf("invalid scoped npm package %q: expected @scope/name", pkg)
		}
		// Validate scope and name parts
		if err := validateNpmName(parts[0]); err != nil {
			return fmt.Errorf("invalid npm scope %q: %w", parts[0], err)
		}
		if err := validateNpmName(parts[1]); err != nil {
			return fmt.Errorf("invalid npm package name %q: %w", parts[1], err)
		}
		return nil
	}
	return validateNpmName(pkg)
}

func validateNpmName(name string) error {
	if name == "" {
		return fmt.Errorf("empty name")
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("invalid character %q", r)
		}
	}
	return nil
}

// validatePipPackage validates a pip/uv package name.
func validatePipPackage(pkg string) error {
	// pip packages: alphanumeric, hyphens, underscores, dots
	// Also allow extras like package[extra]
	name := pkg
	if idx := strings.Index(pkg, "["); idx >= 0 {
		name = pkg[:idx]
	}
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' {
			return fmt.Errorf("invalid character %q in package name", r)
		}
	}
	return nil
}

// validateCargoPackage validates a cargo package name.
func validateCargoPackage(pkg string) error {
	// cargo packages: alphanumeric, hyphens, underscores
	for _, r := range pkg {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
			return fmt.Errorf("invalid character %q in crate name", r)
		}
	}
	return nil
}

// validateGoPackage validates a Go package path.
func validateGoPackage(pkg string) error {
	// Go packages: module paths like github.com/user/repo/cmd/tool
	// Allow alphanumeric, hyphens, underscores, dots, slashes
	for _, r := range pkg {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' && r != '.' && r != '/' {
			return fmt.Errorf("invalid character %q in Go package path", r)
		}
	}
	// Must have at least one slash for a valid module path
	if !strings.Contains(pkg, "/") {
		return fmt.Errorf("invalid Go package path %q: must be a full module path (e.g., github.com/user/repo)", pkg)
	}
	return nil
}

// validateVersion ensures version strings only contain safe characters.
// This prevents shell injection when versions are interpolated into install commands.
func validateVersion(version string) error {
	if version == "" {
		return nil
	}
	for _, r := range version {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '.' && r != '-' && r != '_' {
			return fmt.Errorf("invalid character %q in version %q: only letters, digits, dots, hyphens, and underscores are allowed", r, version)
		}
	}
	return nil
}

// ParseAll parses multiple dependency strings and validates them.
// Meta dependencies are expanded into their constituent dependencies.
// Dynamic dependencies (npm:, pip:, etc.) are handled directly.
func ParseAll(specs []string) ([]Dependency, error) {
	deps := make([]Dependency, 0, len(specs))
	seen := make(map[string]bool) // Track seen deps to avoid duplicates

	for _, s := range specs {
		dep, err := Parse(s)
		if err != nil {
			return nil, err
		}

		// Dynamic dependencies don't need registry lookup
		if dep.IsDynamic() {
			key := dep.Type.String() + ":" + dep.Package
			if !seen[key] {
				seen[key] = true
				deps = append(deps, dep)
			}
			continue
		}

		// Registry-based dependency
		spec, ok := GetSpec(dep.Name)
		if !ok {
			return nil, fmt.Errorf("unknown dependency %q%s", dep.Name, suggestDep(dep.Name))
		}

		// Meta dependencies don't support version specifiers
		if spec.Type == TypeMeta {
			if dep.Version != "" {
				return nil, fmt.Errorf("meta dependency %q does not support version specifications", dep.Name)
			}
			for _, reqName := range spec.Requires {
				if seen[reqName] {
					continue
				}
				seen[reqName] = true
				deps = append(deps, Dependency{Name: reqName})
			}
		} else {
			if !seen[dep.Name] {
				seen[dep.Name] = true
				deps = append(deps, dep)
			}
		}
	}
	return deps, nil
}

// Validate checks that all dependency requirements are satisfied and versions are valid.
func Validate(deps []Dependency) error {
	// Build set of dependency names (including dynamic deps' implicit requirements)
	depSet := make(map[string]bool)
	for _, d := range deps {
		depSet[d.Name] = true
	}

	// Check requirements and version constraints
	for _, d := range deps {
		// Dynamic dependencies: check implicit requirements
		if d.IsDynamic() {
			for _, req := range d.ImplicitRequires() {
				if !depSet[req] {
					return fmt.Errorf("%s requires %s\n\n  Add '%s' to your dependencies:\n    dependencies:\n      - %s\n      - %s",
						d.Package, req, req, req, d.Type.String()+":"+d.Package)
				}
			}
			continue
		}

		// Registry-based dependency
		spec, ok := registry[d.Name]
		if !ok {
			return fmt.Errorf("unknown dependency %q%s", d.Name, suggestDep(d.Name))
		}

		// Validate go-install dependencies require go runtime
		if spec.Type == TypeGoInstall && !depSet["go"] {
			return fmt.Errorf("%s requires Go runtime\n\n  Add 'go' to your dependencies:\n    dependencies:\n      - go\n      - %s",
				d.Name, d.Name)
		}

		// Validate go-install has required go-package field
		if spec.Type == TypeGoInstall && spec.GoPackage == "" {
			return fmt.Errorf("go-install dependency %q missing required 'go-package' field in registry", d.Name)
		}

		// Validate uv-tool dependencies require uv
		if spec.Type == TypeUvTool && !depSet["uv"] {
			return fmt.Errorf("%s requires uv\n\n  Add 'uv' to your dependencies:\n    dependencies:\n      - uv\n      - %s",
				d.Name, d.Name)
		}

		// Validate version against allowed versions if specified
		if d.Version != "" && len(spec.Versions) > 0 {
			found := false
			for _, allowed := range spec.Versions {
				if d.Version == allowed {
					found = true
					break
				}
			}
			if !found {
				return fmt.Errorf("invalid version %q for %s\n\n  Available versions: %s\n  Or omit the version to use the default (%s)",
					d.Version, d.Name, strings.Join(spec.Versions, ", "), spec.Default)
			}
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
	for regName := range AllSpecs() {
		if strings.Contains(regName, name) || strings.Contains(name, regName) {
			return fmt.Sprintf("\n  Did you mean '%s'?", regName)
		}
	}

	// Suggest dynamic prefixes if it looks like a package name
	if strings.Contains(name, "-") || strings.Contains(name, "_") {
		return "\n  For packages from npm, pip, or other managers, use prefixes:\n    npm:package-name, pip:package-name, uv:package-name"
	}

	return ""
}

// List returns all available dependency names sorted alphabetically.
func List() []string {
	specs := AllSpecs()
	names := make([]string, 0, len(specs))
	for name := range specs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// String returns the string representation of an InstallType.
func (t InstallType) String() string {
	return string(t)
}
