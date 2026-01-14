// internal/deps/dockerfile.go
package deps

import (
	"fmt"
	"sort"
	"strings"
)

const baseImage = "ubuntu:22.04"

// GenerateDockerfile creates a Dockerfile for the given dependencies.
func GenerateDockerfile(deps []Dependency) (string, error) {
	var b strings.Builder

	b.WriteString("FROM " + baseImage + "\n\n")
	b.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n\n")

	// Sort dependencies into categories for optimal layer caching
	var (
		aptPkgs    []string
		runtimes   []Dependency
		githubBins []Dependency
		npmPkgs    []Dependency
		customDeps []Dependency
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
		case TypeCustom:
			customDeps = append(customDeps, dep)
		}
	}

	// Base packages (curl, ca-certificates for HTTPS, gnupg for apt keys, unzip for archives)
	b.WriteString("# Base packages\n")
	b.WriteString("RUN apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip \\\n")
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")

	// User-specified apt packages
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		b.WriteString("# Apt packages\n")
		b.WriteString("RUN apt-get update && apt-get install -y \\\n")
		for _, pkg := range aptPkgs {
			b.WriteString("    " + pkg + " \\\n")
		}
		b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
	}

	// Runtimes (node, python, go)
	for _, dep := range runtimes {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s runtime\n", dep.Name))
		b.WriteString(getRuntimeCommands(dep.Name, version).FormatForDockerfile())
		b.WriteString("\n")
	}

	// GitHub binary downloads
	for _, dep := range githubBins {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(getGithubBinaryCommands(dep.Name, version, spec).FormatForDockerfile())
		b.WriteString("\n")
	}

	// npm global packages
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
		b.WriteString("RUN npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Custom installs (playwright, aws, gcloud)
	for _, dep := range customDeps {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(getCustomCommands(dep.Name, version).FormatForDockerfile())
		b.WriteString("\n")
	}

	return b.String(), nil
}
