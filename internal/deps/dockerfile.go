// internal/deps/dockerfile.go
package deps

import (
	"fmt"
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/providers/claude"
)

// DockerfileOptions configures Dockerfile generation.
type DockerfileOptions struct {
	// NeedsSSH indicates SSH grants are present and the image needs
	// openssh-client, socat, and the moat-init entrypoint for agent forwarding.
	NeedsSSH bool

	// SSHHosts lists the hosts for which SSH access is granted (e.g., "github.com").
	// Known host keys will be added to /etc/ssh/ssh_known_hosts for these hosts.
	SSHHosts []string

	// NeedsClaudeInit indicates Claude Code configuration files need to be
	// copied from a staging directory at container startup. This requires
	// the moat-init entrypoint script.
	NeedsClaudeInit bool

	// NeedsCodexInit indicates Codex CLI configuration files need to be
	// copied from a staging directory at container startup. This requires
	// the moat-init entrypoint script.
	NeedsCodexInit bool

	// NeedsGeminiInit indicates Gemini CLI configuration files need to be
	// copied from a staging directory at container startup. This requires
	// the moat-init entrypoint script.
	NeedsGeminiInit bool

	// UseBuildKit enables BuildKit-specific features like cache mounts.
	// When false, generates Dockerfiles compatible with the legacy builder.
	// Defaults to true if not explicitly set (checked via useBuildKit method).
	UseBuildKit *bool

	// ClaudeMarketplaces are plugin marketplaces to register during image build.
	ClaudeMarketplaces []claude.MarketplaceConfig

	// ClaudePlugins are plugins to install during image build.
	// Format: "plugin-name@marketplace-name"
	ClaudePlugins []string
}

// useBuildKit returns whether to use BuildKit features.
// Defaults to true if UseBuildKit is nil.
func (o *DockerfileOptions) useBuildKit() bool {
	if o == nil || o.UseBuildKit == nil {
		return true
	}
	return *o.UseBuildKit
}

// DockerfileResult contains the generated Dockerfile and any additional context files
// that should be placed alongside the Dockerfile in the build context directory.
type DockerfileResult struct {
	// Dockerfile is the generated Dockerfile content.
	Dockerfile string

	// ContextFiles maps relative file paths to their contents.
	// These files should be written to the build context directory
	// alongside the Dockerfile (e.g., "moat-init.sh" → script content).
	ContextFiles map[string][]byte
}

const defaultBaseImage = "debian:bookworm-slim"

// knownSSHHostKeys maps hostnames to their SSH public keys.
// These are embedded to avoid network calls during image build and to ensure
// security (no TOFU - Trust On First Use vulnerability).
// Keys sourced from official documentation:
// - GitHub: https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/githubs-ssh-key-fingerprints
// - GitLab: https://docs.gitlab.com/ee/user/gitlab_com/#ssh-host-keys-fingerprints
// - Bitbucket: https://support.atlassian.com/bitbucket-cloud/docs/configure-ssh-and-two-step-verification/
var knownSSHHostKeys = map[string][]string{
	"github.com": {
		"github.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOMqqnkVzrm0SdG6UOoqKLsabgH5C9okWi0dh2l9GKJl",
		"github.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBEmKSENjQEezOmxkZMy7opKgwFB9nkt5YRrYMjNuG5N87uRgg6CLrbo5wAdT/y6v0mKV0U2w0WZ2YB/++Tpockg=",
		"github.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCj7ndNxQowgcQnjshcLrqPEiiphnt+VTTvDP6mHBL9j1aNUkY4Ue1gvwnGLVlOhGeYrnZaMgRK6+PKCUXaDbC7qtbW8gIkhL7aGCsOr/C56SJMy/BCZfxd1nWzAOxSDPgVsmerOBYfNqltV9/hWCqBywINIR+5dIg6JTJ72pcEpEjcYgXkE2YEFXV1JHnsKgbLWNlhScqb2UmyRkQyytRLtL+38TGxkxCflmO+5Z8CSSNY7GidjMIZ7Q4zMjA2n1nGrlTDkzwDCsw+wqFPGQA179cnfGWOWRVruj16z6XyvxvjJwbz0wQZ75XK5tKSb7FNyeIEs4TT4jk+S4dhPeAUC5y+bDYirYgM4GC7uEnztnZyaVWQ7B381AK4Qdrwt51ZqExKbQpTUNn+EjqoTwvqNj4kqx5QUCI0ThS/YkOxJCXmPUWZbhjpCg56i+2aB6CmK2JGhn57K5mj0MNdBXA4/WnwH6XoPWJzK5Nyu2zB3nAZp+S5hpQs+p1vN1/wsjk=",
	},
	"gitlab.com": {
		"gitlab.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIAfuCHKVTjquxvt6CM6tdG4SLp1Btn/nOeHHE5UOzRdf",
		"gitlab.com ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBFSMqzJeV9rUzU4kWitGjeR4PWSa29SPqJ1fVkhtj3Hw9xjLVXVYrU9QlYWrOLXBpQ6KWjbjTDTdDkoohFzgbEY=",
		"gitlab.com ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABAQCsj2bNKTBSpIYDEGk9KxsGh3mySTRgMtXL583qmBpzeQ+jqCMRgBqB98u3z++J1sKlXHWfM9dyhSevkMwSbhoR8XIq/U0tCNyokEi/ueaBMCvbcTHhO7FcwzY92WK4Yt0aGROY5qX2UKSeOvuP4D6TPqKF1onrSzH9bx9XUf2lEdWT/ia1NEKjunUqu1xOB/StKDHMoX4/OKyIzuS0q/T1zOATthvasJFoPrAjkohTyaDUz2LN5JoH839hViyEG82yB+MjcFV5MU3N1l1QL3cVUCh93xSaua1N85qivl+siMkPGbO5xR/En4iEY6K2XPASUEMaieWVNTRCtJ4S8H+9",
	},
	"bitbucket.org": {
		"bitbucket.org ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIIazEu89wgQZ4bqs3d63QSMzYVa0MuJ2e2gKTKqu+UUO",
		"bitbucket.org ecdsa-sha2-nistp256 AAAAE2VjZHNhLXNoYTItbmlzdHAyNTYAAAAIbmlzdHAyNTYAAABBBPIQmuzMBuKdWeF4+a2sjSSpBK0iqitSQ+5BM9KhpexuGt20JpTVM7u5BDZngncgrqDMbWdxMWWOGtZ9UgbqgZE=",
		"bitbucket.org ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQDQeJzhupRu0u0cdegZIa8e86EG2qOCsIsD1Xw0xSeiPDlCr7kq97NLmMbpKTX6Esc30NuoqEEHCuc7yWtwp8dI76EEEB1VqY9QJq6vk+aySyboD5QF61I/1WeTwu+deCbgKMGbUijeXhtfbxSxm6JwGrXrhBdofTsbKRUsrN1WoNgUa8uqN1Vx6WAJw1JHPhglEGGHea6QICwJOAr/6mrui/oB7pkaWKHj3z7d1IC4KWLtY47elvjbaTlkN04Kc/5LFEirorGYVbt15kAUlqGM65pk6ZBxtaO3+30LVlORZkxOh+LKL/BvbZ/iRNhItLqNyieoQj/uh/7Iv4uyH/cV/0b4WDSd3DptigWq84lJubb9t/DnZlrJazxyDCulTmKdOR7vs9gMTo+uoIrPSb8ScTtvw65+odKAlBj59dhnVp9zd7QUojOpXlL62Aw56U4oO+FALuevvMjiWeavKhJqlR7i5n9srYcrNV7ttmDw7kf/97P5zauIhxcjX+xHv4M=",
	},
}

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
	aptPkgs        []string
	runtimes       []Dependency
	githubBins     []Dependency
	npmPkgs        []Dependency
	goInstallPkgs  []Dependency
	uvToolPkgs     []Dependency
	customDeps     []Dependency
	userCustomDeps []Dependency
	dynamicNpm     []Dependency
	dynamicPip     []Dependency
	dynamicUv      []Dependency
	dynamicCargo   []Dependency
	dynamicGo      []Dependency
	dockerMode     DockerMode // empty string means no docker, "host" or "dind" otherwise
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
			if spec.UserInstall {
				c.userCustomDeps = append(c.userCustomDeps, dep)
			} else {
				c.customDeps = append(c.customDeps, dep)
			}
		case TypeDocker:
			c.dockerMode = dep.DockerMode
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
func GenerateDockerfile(deps []Dependency, opts *DockerfileOptions) (*DockerfileResult, error) {
	if opts == nil {
		opts = &DockerfileOptions{}
	}
	var b strings.Builder
	contextFiles := make(map[string][]byte)

	c := categorizeDeps(deps)

	// Add SSH packages if SSH grants are present
	if opts.NeedsSSH {
		c.aptPkgs = append(c.aptPkgs, "openssh-client", "socat")
	}

	// Note: Docker CLI is installed separately from Docker's official repo,
	// not via apt, to ensure a recent version compatible with modern daemons.

	// Determine base image and write header
	baseImage, baseRuntime := selectBaseImage(c.runtimes)
	b.WriteString("FROM " + baseImage + "\n\n")
	b.WriteString("ENV DEBIAN_FRONTEND=noninteractive\n\n")

	// Write all sections
	writeBasePackages(&b, opts.useBuildKit())
	writeUserSetup(&b)
	writeAptPackages(&b, c.aptPkgs, opts.useBuildKit())
	writeDockerCLI(&b, c.dockerMode)
	writeRuntimes(&b, c.runtimes, baseRuntime)
	writeGithubBinaries(&b, c.githubBins)
	writeNpmPackages(&b, c.npmPkgs)
	writeGoInstallPackages(&b, c.goInstallPkgs)
	writeCustomDeps(&b, c.customDeps)
	writeUvToolPackages(&b, c.uvToolPkgs)

	// User-space custom deps (install-as: user) run as moatuser
	writeUserCustomDeps(&b, c.userCustomDeps)
	pluginResult := claude.GenerateDockerfileSnippet(opts.ClaudeMarketplaces, opts.ClaudePlugins, containerUser)
	b.WriteString(pluginResult.DockerfileSnippet)
	if pluginResult.ScriptName != "" {
		contextFiles[pluginResult.ScriptName] = pluginResult.ScriptContent
	}

	// Dynamic package manager dependencies
	writeDynamicDeps(&b, "npm packages (dynamic)", c.dynamicNpm)
	writeDynamicDeps(&b, "pip packages (dynamic)", c.dynamicPip)
	writeDynamicDeps(&b, "uv packages (dynamic)", c.dynamicUv)
	writeDynamicDeps(&b, "cargo packages (dynamic)", c.dynamicCargo)
	writeDynamicDeps(&b, "go packages (dynamic)", c.dynamicGo)

	// SSH known hosts for granted hosts
	writeSSHKnownHosts(&b, opts.SSHHosts)

	// Finalize with entrypoint and user setup
	writeEntrypoint(&b, opts, c.dockerMode, contextFiles)

	return &DockerfileResult{
		Dockerfile:   b.String(),
		ContextFiles: contextFiles,
	}, nil
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
// Uses BuildKit cache mounts for apt to speed up rebuilds when useBuildKit is true.
func writeBasePackages(b *strings.Builder, useBuildKit bool) {
	b.WriteString("# Base packages\n")
	if useBuildKit {
		b.WriteString("RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,target=/var/lib/apt,sharing=locked \\\n")
		b.WriteString("    apt-get update \\\n")
	} else {
		b.WriteString("RUN apt-get update \\\n")
	}
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
// Uses BuildKit cache mounts for apt to speed up rebuilds when useBuildKit is true.
func writeAptPackages(b *strings.Builder, pkgs []string, useBuildKit bool) {
	if len(pkgs) == 0 {
		return
	}
	sort.Strings(pkgs)
	b.WriteString("# Apt packages\n")
	if useBuildKit {
		b.WriteString("RUN --mount=type=cache,target=/var/cache/apt,sharing=locked \\\n")
		b.WriteString("    --mount=type=cache,target=/var/lib/apt,sharing=locked \\\n")
		b.WriteString("    apt-get update \\\n")
	} else {
		b.WriteString("RUN apt-get update \\\n")
	}
	b.WriteString("    && apt-get install -y --no-install-recommends \\\n")
	for _, pkg := range pkgs {
		b.WriteString("       " + pkg + " \\\n")
	}
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
}

// writeDockerCLI installs Docker CLI from Docker's official repository.
// We use the official repo instead of the docker.io apt package because
// the apt package is often too old and incompatible with modern Docker daemons.
//
// For host mode: Installs docker-ce-cli only (talks to host daemon via socket).
// For dind mode: Installs docker-ce-cli + docker-ce (daemon) + containerd.io.
func writeDockerCLI(b *strings.Builder, mode DockerMode) {
	if mode == "" {
		return
	}

	// Determine which packages to install based on mode
	packages := "docker-ce-cli"
	comment := "Docker CLI (from official Docker repo for up-to-date version)"
	if mode == DockerModeDind {
		packages = "docker-ce docker-ce-cli containerd.io docker-buildx-plugin"
		comment = "Docker daemon + CLI + buildx (from official Docker repo for dind mode)"
	}

	b.WriteString("# " + comment + "\n")
	b.WriteString("RUN install -m 0755 -d /etc/apt/keyrings \\\n")
	b.WriteString("    && curl -fsSL https://download.docker.com/linux/debian/gpg -o /etc/apt/keyrings/docker.asc \\\n")
	b.WriteString("    && chmod a+r /etc/apt/keyrings/docker.asc \\\n")
	b.WriteString("    && echo \"deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/debian bookworm stable\" > /etc/apt/sources.list.d/docker.list \\\n")
	b.WriteString("    && apt-get update \\\n")
	b.WriteString("    && apt-get install -y --no-install-recommends " + packages + " \\\n")
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

// writeUserCustomDeps writes custom dependencies that require user-space installation.
// These are deps with user-install: true in the registry, meaning their installers
// write to $HOME and must run as the container user (moatuser) instead of root.
func writeUserCustomDeps(b *strings.Builder, deps []Dependency) {
	if len(deps) == 0 {
		return
	}
	b.WriteString(fmt.Sprintf("USER %s\n", containerUser))
	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))
	for _, dep := range deps {
		spec, _ := GetSpec(dep.Name)
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s (user-space)\n", dep.Name))
		cmds := getCustomCommands(dep.Name, version)
		for _, cmd := range cmds.Commands {
			b.WriteString("RUN " + cmd + "\n")
		}
		for k, v := range cmds.EnvVars {
			b.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", k, v))
		}
	}
	b.WriteString("USER root\n\n")
}

// writeSSHKnownHosts writes known SSH host keys to /etc/ssh/ssh_known_hosts.
// Only hosts with known keys are written; unknown hosts are skipped.
func writeSSHKnownHosts(b *strings.Builder, hosts []string) {
	if len(hosts) == 0 {
		return
	}

	// Collect keys for granted hosts
	var keys []string
	for _, host := range hosts {
		if hostKeys, ok := knownSSHHostKeys[host]; ok {
			keys = append(keys, hostKeys...)
		}
	}

	if len(keys) == 0 {
		return
	}

	// Write keys to /etc/ssh/ssh_known_hosts
	b.WriteString("# SSH known hosts for granted SSH hosts\n")
	b.WriteString("RUN mkdir -p /etc/ssh && \\\n")
	for i, key := range keys {
		escaped := strings.ReplaceAll(key, "'", "'\"'\"'")
		if i < len(keys)-1 {
			b.WriteString(fmt.Sprintf("    echo '%s' >> /etc/ssh/ssh_known_hosts && \\\n", escaped))
		} else {
			b.WriteString(fmt.Sprintf("    echo '%s' >> /etc/ssh/ssh_known_hosts\n", escaped))
		}
	}
	b.WriteString("\n")
}

// writeEntrypoint writes the entrypoint configuration and working directory.
// When the init script is needed, it is added as a context file and COPYed
// into the image. This avoids embedding a large base64 blob inline in a RUN
// command, which triggers gRPC transport errors in Apple's container builder.
func writeEntrypoint(b *strings.Builder, opts *DockerfileOptions, dockerMode DockerMode, contextFiles map[string][]byte) {
	// Features that require moat-init entrypoint:
	// - SSH agent forwarding
	// - Claude Code file setup
	// - Codex file setup
	// - Docker socket group setup (host mode)
	// - Docker daemon startup (dind mode)
	needsInit := opts.NeedsSSH || opts.NeedsClaudeInit || opts.NeedsCodexInit || opts.NeedsGeminiInit || dockerMode != ""
	if needsInit {
		contextFiles["moat-init.sh"] = []byte(MoatInitScript)
		b.WriteString("# Moat initialization script (privilege drop + feature setup)\n")
		b.WriteString("COPY moat-init.sh /usr/local/bin/moat-init\n")
		b.WriteString("RUN chmod +x /usr/local/bin/moat-init\n")
		b.WriteString("ENTRYPOINT [\"/usr/local/bin/moat-init\"]\n")
	} else {
		b.WriteString(fmt.Sprintf("# Run as non-root user\nUSER %s\n", containerUser))
	}
	b.WriteString(fmt.Sprintf("WORKDIR /home/%s\n", containerUser))
}
