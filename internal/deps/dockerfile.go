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

	// NeedsClaudeInit indicates Claude Code configuration files need to be
	// copied from a staging directory at container startup. This requires
	// the moat-init entrypoint script.
	NeedsClaudeInit bool

	// NeedsCodexInit indicates Codex CLI configuration files need to be
	// copied from a staging directory at container startup. This requires
	// the moat-init entrypoint script.
	NeedsCodexInit bool
}

const defaultBaseImage = "debian:bookworm-slim"

// runtimeBaseImage returns the official Docker image for a runtime, or empty string
// if we should fall back to installing on Debian.
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

// containerUser is the non-root user created in generated images.
// Using UID 5000 to avoid collision with existing users in base images.
// Many base images have a default user at UID 1000, and deleting that user
// doesn't change ownership of their files - the new user would inherit access.
// UID 5000 is safely above the typical user range (1000-4999).
const containerUser = "moatuser"
const containerUID = "5000"

// categorizedDeps holds dependencies sorted by type for Dockerfile generation.
type categorizedDeps struct {
	aptPkgs       []string
	runtimes      []Dependency
	githubBins    []Dependency
	npmPkgs       []Dependency
	goInstallPkgs []Dependency
	uvToolPkgs    []Dependency
	customDeps    []Dependency
	dynamicNpm    []Dependency
	dynamicPip    []Dependency
	dynamicUv     []Dependency
	dynamicCargo  []Dependency
	dynamicGo     []Dependency
}

// categorizeDeps sorts dependencies into categories for optimal Dockerfile layer caching.
func categorizeDeps(deps []Dependency) categorizedDeps {
	var c categorizedDeps
	for _, dep := range deps {
		if dep.IsDynamic() {
			switch dep.Type {
			case TypeDynamicNpm:
				c.dynamicNpm = append(c.dynamicNpm, dep)
			case TypeDynamicPip:
				c.dynamicPip = append(c.dynamicPip, dep)
			case TypeDynamicUv:
				c.dynamicUv = append(c.dynamicUv, dep)
			case TypeDynamicCargo:
				c.dynamicCargo = append(c.dynamicCargo, dep)
			case TypeDynamicGo:
				c.dynamicGo = append(c.dynamicGo, dep)
			}
			continue
		}

		spec, _ := GetSpec(dep.Name)
		switch spec.Type {
		case TypeApt:
			c.aptPkgs = append(c.aptPkgs, spec.Package)
		case TypeRuntime:
			c.runtimes = append(c.runtimes, dep)
		case TypeGithubBinary:
			c.githubBins = append(c.githubBins, dep)
		case TypeNpm:
			c.npmPkgs = append(c.npmPkgs, dep)
		case TypeGoInstall:
			c.goInstallPkgs = append(c.goInstallPkgs, dep)
		case TypeUvTool:
			c.uvToolPkgs = append(c.uvToolPkgs, dep)
		case TypeCustom:
			c.customDeps = append(c.customDeps, dep)
		case TypeMeta:
			// Meta dependencies are expanded during parsing/validation
		}
	}
	return c
}

// writeDynamicDeps writes install commands for a slice of dynamic dependencies.
func writeDynamicDeps(b *strings.Builder, comment string, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	b.WriteString("# ")
	b.WriteString(comment)
	b.WriteString("\n")
	for _, dep := range deps {
		b.WriteString(getDynamicPackageCommands(dep).FormatForDockerfile())
	}
	b.WriteString("\n")
}

// GenerateDockerfile creates a Dockerfile for the given dependencies.
func GenerateDockerfile(deps []Dependency, opts *DockerfileOptions) (string, error) {
	if opts == nil {
		opts = &DockerfileOptions{}
	}
	var b strings.Builder

	c := categorizeDeps(deps)

	// Add SSH packages if SSH grants are present
	if opts.NeedsSSH {
		c.aptPkgs = append(c.aptPkgs, "openssh-client", "socat")
	}

	// Determine base image and write header
	baseImage, baseRuntime := selectBaseImage(c.runtimes)
	b.WriteString("FROM " + baseImage + "\n\n")
	b.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n\n")

	// Write all sections
	writeBasePackages(&b)
	writeUserSetup(&b)
	writeAptPackages(&b, c.aptPkgs)
	writeRuntimes(&b, c.runtimes, baseRuntime)
	writeGithubBinaries(&b, c.githubBins)
	writeNpmPackages(&b, c.npmPkgs)
	writeGoInstallPackages(&b, c.goInstallPkgs)
	writeCustomDeps(&b, c.customDeps)
	writeUvToolPackages(&b, c.uvToolPkgs)

	// Dynamic package manager dependencies
	writeDynamicDeps(&b, "npm packages (dynamic)", c.dynamicNpm)
	writeDynamicDeps(&b, "pip packages (dynamic)", c.dynamicPip)
	writeDynamicDeps(&b, "uv packages (dynamic)", c.dynamicUv)
	writeDynamicDeps(&b, "cargo packages (dynamic)", c.dynamicCargo)
	writeDynamicDeps(&b, "go packages (dynamic)", c.dynamicGo)

	// Finalize with entrypoint and user setup
	writeEntrypoint(&b, opts)

	return b.String(), nil
}

// selectBaseImage determines the base image based on runtime dependencies.
// Returns the image name and the runtime dependency provided by it (if any).
func selectBaseImage(runtimes []Dependency) (string, *Dependency) {
	if len(runtimes) != 1 {
		return defaultBaseImage, nil
	}

	rt := runtimes[0]
	spec, _ := GetSpec(rt.Name)
	version := rt.Version
	if version == "" {
		version = spec.Default
	}
	if img := runtimeBaseImage(rt.Name, version); img != "" {
		return img, &rt
	}
	return defaultBaseImage, nil
}

// writeBasePackages writes the base package installation.
// Uses BuildKit cache mounts for apt to speed up rebuilds.
func writeBasePackages(b *strings.Builder) {
	b.WriteString("# Base packages\n")
	b.WriteString("RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \\\n")
	b.WriteString("    --mount=type=cache,target=/var/lib/apt,sharing=locked \\\n")
	b.WriteString("    apt-get update \\\n")
	b.WriteString("    && apt-get install -y --no-install-recommends \\\n")
	b.WriteString("       ca-certificates \\\n")
	b.WriteString("       curl \\\n")
	b.WriteString("       gnupg \\\n")
	b.WriteString("       gosu \\\n")
	b.WriteString("       iptables \\\n")
	b.WriteString("       unzip \\\n")
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
}

// writeUserSetup writes the non-root user creation commands.
func writeUserSetup(b *strings.Builder) {
	b.WriteString("# Create non-root user\n")
	b.WriteString(fmt.Sprintf("RUN existing_user=$(getent passwd %s | cut -d: -f1) && \\\n", containerUID))
	b.WriteString("    if [ -n \"$existing_user\" ]; then \\\n")
	b.WriteString("      echo \"Removing existing user $existing_user with UID " + containerUID + "\" && \\\n")
	b.WriteString("      userdel -r \"$existing_user\" || echo \"Warning: failed to remove user $existing_user\"; \\\n")
	b.WriteString("    fi && \\\n")
	b.WriteString(fmt.Sprintf("    useradd -m -u %s -s /bin/bash %s && \\\n", containerUID, containerUser))
	b.WriteString(fmt.Sprintf("    mkdir -p /home/%s/.claude/projects && \\\n", containerUser))
	b.WriteString(fmt.Sprintf("    chown -R %s:%s /home/%s/.claude\n\n", containerUser, containerUser, containerUser))
}

// writeAptPackages writes user-specified apt package installation.
// Uses BuildKit cache mounts for apt to speed up rebuilds.
func writeAptPackages(b *strings.Builder, pkgs []string) {
	if len(pkgs) == 0 {
		return
	}
	sort.Strings(pkgs)
	b.WriteString("# Apt packages\n")
	b.WriteString("RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \\\n")
	b.WriteString("    --mount=type=cache,target=/var/lib/apt,sharing=locked \\\n")
	b.WriteString("    apt-get update \\\n")
	b.WriteString("    && apt-get install -y --no-install-recommends \\\n")
	for _, pkg := range pkgs {
		b.WriteString("       " + pkg + " \\\n")
	}
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
}

// writeRuntimes writes runtime installation commands, skipping those provided by base image.
func writeRuntimes(b *strings.Builder, runtimes []Dependency, baseRuntime *Dependency) {
	for _, dep := range runtimes {
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
}

// writeGithubBinaries writes GitHub binary download commands.
func writeGithubBinaries(b *strings.Builder, deps []Dependency) {
	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(getGithubBinaryCommands(dep.Name, version, spec).FormatForDockerfile())
		b.WriteString("\n")
	}
}

// writeNpmPackages writes npm global package installation.
func writeNpmPackages(b *strings.Builder, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	pkgNames := make([]string, 0, len(deps))
	for _, dep := range deps {
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

// writeGoInstallPackages writes go install commands.
func writeGoInstallPackages(b *strings.Builder, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	b.WriteString("# go install packages\n")
	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		b.WriteString(getGoInstallCommands(spec).FormatForDockerfile())
	}
	b.WriteString("\n")
}

// writeCustomDeps writes custom dependency installation commands.
func writeCustomDeps(b *strings.Builder, deps []Dependency) {
	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(getCustomCommands(dep.Name, version).FormatForDockerfile())
		b.WriteString("\n")
	}
}

// writeUvToolPackages writes uv tool package installation.
func writeUvToolPackages(b *strings.Builder, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	b.WriteString("# uv tool packages\n")
	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		pkg := spec.Package
		if pkg == "" {
			pkg = dep.Name
		}
		b.WriteString(fmt.Sprintf("RUN uv tool install %s\n", pkg))
	}
	b.WriteString("\n")
}

// writeEntrypoint writes the entrypoint configuration and working directory.
func writeEntrypoint(b *strings.Builder, opts *DockerfileOptions) {
	// Features: SSH agent forwarding, Claude Code file setup, Codex file setup, privilege drop to moatuser
	needsInit := opts.NeedsSSH || opts.NeedsClaudeInit || opts.NeedsCodexInit
	if needsInit {
		encoded := base64.StdEncoding.EncodeToString([]byte(MoatInitScript))
		b.WriteString("# Moat initialization script (SSH agent forwarding + privilege drop)\n")
		b.WriteString(fmt.Sprintf("RUN echo '%s' | base64 -d > /usr/local/bin/moat-init && chmod +x /usr/local/bin/moat-init\n", encoded))
		b.WriteString("ENTRYPOINT [\"/usr/local/bin/moat-init\"]\n")
	} else {
		b.WriteString(fmt.Sprintf("# Run as non-root user\nUSER %s\n", containerUser))
	}
	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))
}
