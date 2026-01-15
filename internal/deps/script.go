// internal/deps/script.go
package deps

import (
	"fmt"
	"sort"
	"strings"
)

// GenerateInstallScript creates a bash install script for the given dependencies.
// The script is designed for Apple containers where Docker's layer caching is not available.
// Commands are idempotent and safe to re-run.
func GenerateInstallScript(deps []Dependency) (string, error) {
	var b strings.Builder

	// Shebang and error handling
	b.WriteString("#!/bin/bash\n")
	b.WriteString("set -e\n")

	// If no dependencies, return minimal script
	if len(deps) == 0 {
		return b.String(), nil
	}

	b.WriteString("\n")

	// Sort dependencies into categories for optimal install order
	var (
		aptPkgs       []string
		runtimes      []Dependency
		githubBins    []Dependency
		npmPkgs       []Dependency
		goInstallPkgs []Dependency
		customDeps    []Dependency
	)

	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		switch spec.Type {
		case TypeApt:
			aptPkgs = append(aptPkgs, spec.Package)
		case TypeRuntime:
			runtimes = append(runtimes, dep)
		case TypeGithubBinary:
			githubBins = append(githubBins, dep)
		case TypeNpm:
			npmPkgs = append(npmPkgs, dep)
		case TypeGoInstall:
			goInstallPkgs = append(goInstallPkgs, dep)
		case TypeCustom:
			customDeps = append(customDeps, dep)
		case TypeMeta:
			// Meta dependencies are expanded during parsing/validation
		}
	}

	// Step 1: Base apt packages
	b.WriteString("# Base packages\n")
	b.WriteString("apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip \\\n")
	b.WriteString("    iptables\n\n")

	// Step 2: User apt packages
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		b.WriteString("# Apt packages\n")
		b.WriteString("apt-get update && apt-get install -y")
		for _, pkg := range aptPkgs {
			b.WriteString(" \\\n    " + pkg)
		}
		b.WriteString("\n\n")
	}

	// Step 3: Runtimes
	for _, dep := range runtimes {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s runtime\n", dep.Name))
		b.WriteString(getRuntimeCommands(dep.Name, version).FormatForScript())
		b.WriteString("\n")
	}

	// Step 4: GitHub binary downloads
	for _, dep := range githubBins {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(getGithubBinaryCommands(dep.Name, version, spec).FormatForScript())
		b.WriteString("\n")
	}

	// Step 5: npm globals (grouped into single command)
	if len(npmPkgs) > 0 {
		var pkgNames []string
		for _, dep := range npmPkgs {
			spec, _ := GetSpec(dep.Name)
			pkg := spec.Package
			if pkg == "" {
				pkg = dep.Name
			}
			pkgNames = append(pkgNames, pkg)
		}
		b.WriteString("# npm packages\n")
		b.WriteString("npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Step 6: go install packages (requires Go runtime)
	if len(goInstallPkgs) > 0 {
		b.WriteString("# go install packages\n")
		for _, dep := range goInstallPkgs {
			spec, _ := GetSpec(dep.Name)
			b.WriteString(getGoInstallCommands(spec).FormatForScript())
		}
		b.WriteString("\n")
	}

	// Step 7: Custom installs
	for _, dep := range customDeps {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(getCustomCommands(dep.Name, version).FormatForScript())
		b.WriteString("\n")
	}

	return b.String(), nil
}
