// internal/deps/dockerfile.go
package deps

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
)

// DockerfileOptions configures Dockerfile generation.
type DockerfileOptions struct {
	// NeedsSSH indicates SSH grants are present and the image needs
	// openssh-client, socat, and the moat-init entrypoint for agent forwarding.
	NeedsSSH bool
}

const defaultBaseImage = "ubuntu:22.04"

// runtimeBaseImage returns the official Docker image for a runtime, or empty string
// if we should fall back to installing on Ubuntu.
func runtimeBaseImage(name, version string) string {
	switch name {
	case "python":
		// Use slim variant - Debian-based, has apt, much smaller than full image
		return fmt.Sprintf("python:%s-slim", version)
	case "node":
		// Use slim variant - Debian-based, has apt
		return fmt.Sprintf("node:%s-slim", version)
	case "go":
		// Official golang image is Debian-based
		return fmt.Sprintf("golang:%s", version)
	default:
		return ""
	}
}

// GenerateDockerfile creates a Dockerfile for the given dependencies.
func GenerateDockerfile(deps []Dependency, opts *DockerfileOptions) (string, error) {
	if opts == nil {
		opts = &DockerfileOptions{}
	}
	var b strings.Builder

	// Sort dependencies into categories for optimal layer caching
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
			// They should not appear here
		}
	}

	// Determine base image: use official runtime image if we have exactly one runtime
	// This is much faster than installing runtimes via apt on Ubuntu
	baseImage := defaultBaseImage
	var baseRuntime *Dependency // The runtime provided by the base image (skip installing it)

	if len(runtimes) == 1 {
		rt := runtimes[0]
		spec, _ := GetSpec(rt.Name)
		version := rt.Version
		if version == "" {
			version = spec.Default
		}
		if img := runtimeBaseImage(rt.Name, version); img != "" {
			baseImage = img
			baseRuntime = &rt
		}
	}

	b.WriteString("FROM " + baseImage + "\n\n")
	b.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n\n")

	// Add SSH packages if SSH grants are present
	if opts.NeedsSSH {
		aptPkgs = append(aptPkgs, "openssh-client", "socat")
	}

	// Base packages (curl, ca-certificates for HTTPS, gnupg for apt keys, unzip for archives, iptables for firewall)
	// Note: Official runtime images are Debian-based and support apt
	b.WriteString("# Base packages\n")
	b.WriteString("RUN apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip \\\n")
	b.WriteString("    iptables \\\n")
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

	// Runtimes (node, python, go) - skip if already provided by base image
	for _, dep := range runtimes {
		// Skip if this runtime is provided by the base image
		if baseRuntime != nil && dep.Name == baseRuntime.Name {
			b.WriteString(fmt.Sprintf("# %s runtime (provided by base image)\n\n", dep.Name))
			continue
		}

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

	// go install packages (requires Go runtime)
	if len(goInstallPkgs) > 0 {
		b.WriteString("# go install packages\n")
		for _, dep := range goInstallPkgs {
			spec, _ := GetSpec(dep.Name)
			b.WriteString(getGoInstallCommands(spec).FormatForDockerfile())
		}
		b.WriteString("\n")
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

	// If SSH is needed, install the moat-init entrypoint script
	// This script sets up the SSH agent bridge when MOAT_SSH_TCP_ADDR is set
	if opts.NeedsSSH {
		// Base64 encode the embedded script to avoid shell escaping issues
		encoded := base64.StdEncoding.EncodeToString([]byte(MoatInitScript))

		b.WriteString("# Moat initialization script (SSH agent forwarding)\n")
		b.WriteString(fmt.Sprintf("RUN echo '%s' | base64 -d > /usr/local/bin/moat-init && chmod +x /usr/local/bin/moat-init\n", encoded))
		b.WriteString("ENTRYPOINT [\"/usr/local/bin/moat-init\"]\n")
	}

	return b.String(), nil
}
