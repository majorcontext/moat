// internal/deps/install.go
package deps

import (
	"fmt"
	"regexp"
	"strings"
)

// validPackageName matches safe package names for shell commands.
// Allows alphanumeric, dash, underscore, dot, @, /, =, and limited special chars.
// This prevents shell injection while allowing:
// - Scoped npm packages: @org/pkg, @org/pkg@1.0.0
// - Python packages with version: pkg==1.0.0, pkg>=1.0.0
// - Go packages: golang.org/x/tools/gopls@latest
// - Cargo packages: pkg@1.0.0
//
// The version separator can be @ (npm/go/cargo) or comparison operators (pip: ==, >=, <=, ~=).
// Single = is intentionally not allowed for pip as it's not valid pip syntax.
var validPackageName = regexp.MustCompile(`^[@a-zA-Z0-9._/-]+([@~<>=][=]?[a-zA-Z0-9._/-]+)?$`)

// shellQuote returns a shell-safe quoted string.
// For package names that pass validation, returns as-is.
// For others, wraps in single quotes with proper escaping.
func shellQuote(s string) string {
	if validPackageName.MatchString(s) {
		return s
	}
	// Escape single quotes by ending quote, adding escaped quote, starting new quote
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// InstallCommands holds the commands needed to install a dependency.
type InstallCommands struct {
	Commands []string          // Shell commands to run
	EnvVars  map[string]string // Environment variables to set
}

// getRuntimeCommands returns install commands for runtime dependencies.
func getRuntimeCommands(name, version string) InstallCommands {
	switch name {
	case "node":
		// NodeSource setup scripts use major versions only (e.g., setup_20.x, not setup_20.11.0.x)
		majorVersion := version
		if idx := strings.Index(version, "."); idx > 0 {
			majorVersion = version[:idx]
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL https://deb.nodesource.com/setup_%s.x | bash -", majorVersion),
				"apt-get install -y nodejs",
			},
		}
	case "go":
		// Detect architecture at build time: x86_64 -> amd64, aarch64 -> arm64
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf(`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && curl -fsSL "https://go.dev/dl/go%s.linux-${ARCH}.tar.gz" | tar -C /usr/local -xz`, version),
			},
			EnvVars: map[string]string{
				"PATH": "/usr/local/go/bin:$PATH",
			},
		}
	case "python":
		// Python version handling strategy:
		// - When a base image provides the runtime (python:X.Y-slim), this code is not used
		// - When installing on Ubuntu, we use the system python3 (3.10 on Ubuntu 22.04)
		//
		// For specific Python versions, prefer using the official Docker base image
		// by specifying python as a dependency in agent.yaml. The dockerfile generator
		// will select python:X.Y-slim as the base image.
		//
		// This fallback installs Ubuntu's system Python for cases where Python is
		// needed alongside other runtimes (e.g., node + python).
		return InstallCommands{
			Commands: []string{
				"apt-get update && apt-get install -y python3 python3-pip python3-venv",
				"update-alternatives --install /usr/bin/python python /usr/bin/python3 1",
			},
		}
	default:
		return InstallCommands{}
	}
}

// getGithubBinaryCommands returns install commands for GitHub binary dependencies.
// Supports multi-arch via {target}/{arch} placeholder with Targets map, or legacy AssetARM64 field.
func getGithubBinaryCommands(name, version string, spec DepSpec) InstallCommands {
	// New style: use Targets map with {target} or {arch} placeholder
	if len(spec.Targets) > 0 {
		return getGithubBinaryCommandsWithTargets(name, version, spec)
	}

	// Legacy style: separate ARM64 asset/bin fields
	if spec.AssetARM64 != "" {
		return getGithubBinaryCommandsLegacy(name, version, spec)
	}

	// Single architecture only
	// Use Command field if specified, otherwise fall back to name
	cmdName := orDefault(spec.Command, name)

	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)
	if binPath == "" {
		binPath = cmdName
	}

	url := githubReleaseURL(spec.Repo, version, asset, spec.TagPrefix)

	if strings.HasSuffix(asset, ".zip") {
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL %s -o /tmp/%s.zip", url, cmdName),
				fmt.Sprintf("unzip -q /tmp/%s.zip -d /tmp/%s", cmdName, cmdName),
				fmt.Sprintf("mv /tmp/%s/%s /usr/local/bin/%s", cmdName, binPath, cmdName),
				fmt.Sprintf("chmod +x /usr/local/bin/%s", cmdName),
				fmt.Sprintf("rm -rf /tmp/%s*", cmdName),
			},
		}
	}

	if strings.HasSuffix(asset, ".tar.gz") || strings.HasSuffix(asset, ".tgz") {
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL %s | tar -xz -C /tmp", url),
				fmt.Sprintf("mv /tmp/%s /usr/local/bin/%s", binPath, cmdName),
				fmt.Sprintf("chmod +x /usr/local/bin/%s", cmdName),
			},
		}
	}

	// Raw binary (no archive extension)
	return InstallCommands{
		Commands: []string{
			fmt.Sprintf("curl -fsSL %s -o /usr/local/bin/%s", url, cmdName),
			fmt.Sprintf("chmod +x /usr/local/bin/%s", cmdName),
		},
	}
}

// archBinarySpec holds architecture-specific binary details.
type archBinarySpec struct {
	url string
	bin string
}

// getGithubBinaryCommandsWithTargets uses the Targets map for {target} and {arch} substitution.
// Both placeholders are replaced with the architecture-specific target value from the map.
func getGithubBinaryCommandsWithTargets(name, version string, spec DepSpec) InstallCommands {
	amd64Target := spec.Targets["amd64"]
	arm64Target := spec.Targets["arm64"]

	// Use Command field if specified, otherwise fall back to name
	cmdName := orDefault(spec.Command, name)

	amd64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, substituteAllPlaceholders(spec.Asset, version, amd64Target), spec.TagPrefix),
		bin: orDefault(substituteAllPlaceholders(spec.Bin, version, amd64Target), cmdName),
	}
	arm64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, substituteAllPlaceholders(spec.Asset, version, arm64Target), spec.TagPrefix),
		bin: orDefault(substituteAllPlaceholders(spec.Bin, version, arm64Target), cmdName),
	}

	archiveType := detectArchiveType(spec.Asset)
	downloadCmd := buildArchDetectCommand(cmdName, amd64, arm64, archiveType)

	return InstallCommands{
		Commands: []string{
			downloadCmd,
			fmt.Sprintf("chmod +x /usr/local/bin/%s", cmdName),
			fmt.Sprintf("rm -rf /tmp/%s*", cmdName),
		},
	}
}

// getGithubBinaryCommandsLegacy handles the deprecated AssetARM64/BinARM64 fields.
func getGithubBinaryCommandsLegacy(name, version string, spec DepSpec) InstallCommands {
	// Use Command field if specified, otherwise fall back to name
	cmdName := orDefault(spec.Command, name)

	amd64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, replaceVersion(spec.Asset, version), spec.TagPrefix),
		bin: orDefault(replaceVersion(spec.Bin, version), cmdName),
	}
	arm64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, replaceVersion(spec.AssetARM64, version), spec.TagPrefix),
		bin: orDefault(replaceVersion(spec.BinARM64, version), amd64.bin),
	}

	archiveType := detectArchiveType(spec.Asset)
	downloadCmd := buildArchDetectCommand(cmdName, amd64, arm64, archiveType)

	return InstallCommands{
		Commands: []string{
			downloadCmd,
			fmt.Sprintf("chmod +x /usr/local/bin/%s", cmdName),
			fmt.Sprintf("rm -rf /tmp/%s*", cmdName),
		},
	}
}

// githubReleaseURL constructs a GitHub release download URL.
// The tagPrefix parameter controls the version prefix in the URL:
// - "" or "v" results in "v{version}" (default for most projects)
// - "none" results in "{version}" (for repos like ripgrep that don't use v prefix)
// - any other value is used as-is (e.g., "bun-v" for bun releases)
func githubReleaseURL(repo, version, asset, tagPrefix string) string {
	var tag string
	switch tagPrefix {
	case "", "v":
		tag = "v" + version
	case "none":
		tag = version
	default:
		tag = tagPrefix + version
	}
	return fmt.Sprintf("https://github.com/%s/releases/download/%s/%s", repo, tag, asset)
}

// replaceVersion replaces {version} placeholder in a string.
func replaceVersion(s, version string) string {
	return strings.ReplaceAll(s, "{version}", version)
}

// substituteAllPlaceholders replaces {version}, {target}, and {arch} placeholders.
// Both {target} and {arch} are replaced with the same target value, allowing
// registry entries to use whichever is more semantically appropriate.
func substituteAllPlaceholders(s, version, target string) string {
	s = strings.ReplaceAll(s, "{version}", version)
	s = strings.ReplaceAll(s, "{target}", target)
	s = strings.ReplaceAll(s, "{arch}", target)
	return s
}

// orDefault returns s if non-empty, otherwise def.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// archiveType represents the type of archive for a GitHub binary release.
type archiveType int

const (
	archiveZip archiveType = iota
	archiveTarGz
	archiveRaw // raw binary, no archive
)

// detectArchiveType determines the archive type from an asset filename.
func detectArchiveType(asset string) archiveType {
	if strings.HasSuffix(asset, ".zip") {
		return archiveZip
	}
	if strings.HasSuffix(asset, ".tar.gz") || strings.HasSuffix(asset, ".tgz") {
		return archiveTarGz
	}
	return archiveRaw
}

// buildArchDetectCommand generates a shell command that downloads the correct binary for the architecture.
func buildArchDetectCommand(name string, amd64, arm64 archBinarySpec, atype archiveType) string {
	switch atype {
	case archiveZip:
		return fmt.Sprintf(`ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then \
        curl -fsSL "%s" -o /tmp/%s.zip && \
        unzip -q /tmp/%s.zip -d /tmp/%s && \
        mv /tmp/%s/%s /usr/local/bin/%s; \
    else \
        curl -fsSL "%s" -o /tmp/%s.zip && \
        unzip -q /tmp/%s.zip -d /tmp/%s && \
        mv /tmp/%s/%s /usr/local/bin/%s; \
    fi`,
			amd64.url, name, name, name, name, amd64.bin, name,
			arm64.url, name, name, name, name, arm64.bin, name)
	case archiveTarGz:
		return fmt.Sprintf(`ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then \
        curl -fsSL "%s" | tar -xz -C /tmp && \
        mv /tmp/%s /usr/local/bin/%s; \
    else \
        curl -fsSL "%s" | tar -xz -C /tmp && \
        mv /tmp/%s /usr/local/bin/%s; \
    fi`,
			amd64.url, amd64.bin, name,
			arm64.url, arm64.bin, name)
	default: // archiveRaw - direct binary download
		return fmt.Sprintf(`ARCH=$(uname -m) && \
    if [ "$ARCH" = "x86_64" ]; then \
        curl -fsSL "%s" -o /usr/local/bin/%s; \
    else \
        curl -fsSL "%s" -o /usr/local/bin/%s; \
    fi`,
			amd64.url, name,
			arm64.url, name)
	}
}

// getGoInstallCommands returns install commands for go-install dependencies.
// Uses GOBIN=/usr/local/bin to ensure binaries are in PATH.
func getGoInstallCommands(spec DepSpec) InstallCommands {
	return InstallCommands{
		Commands: []string{
			fmt.Sprintf("GOBIN=/usr/local/bin go install %s@latest", spec.GoPackage),
		},
	}
}

// getCustomCommands returns install commands for custom dependencies.
func getCustomCommands(name, version string) InstallCommands {
	switch name {
	case "playwright":
		return InstallCommands{
			Commands: []string{
				"npm install -g playwright",
				"npx playwright install --with-deps chromium",
			},
		}
	case "aws":
		// Detect architecture at build time: x86_64 or aarch64
		return InstallCommands{
			Commands: []string{
				`ARCH=$(uname -m) && curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-${ARCH}.zip" -o /tmp/awscliv2.zip`,
				"unzip -q /tmp/awscliv2.zip -d /tmp",
				"/tmp/aws/install",
				"rm -rf /tmp/aws*",
			},
		}
	case "gcloud":
		// Detect architecture at build time: x86_64 or arm (gcloud uses "arm" not "aarch64")
		return InstallCommands{
			Commands: []string{
				`ARCH=$(uname -m | sed 's/aarch64/arm/') && curl -fsSL "https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-${ARCH}.tar.gz" | tar -xz -C /opt`,
				"/opt/google-cloud-sdk/install.sh --quiet --path-update=true",
			},
			EnvVars: map[string]string{
				"PATH": "/opt/google-cloud-sdk/bin:$PATH",
			},
		}
	case "rust":
		// Install Rust via rustup
		return InstallCommands{
			Commands: []string{
				"curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh -s -- -y --default-toolchain stable",
			},
			EnvVars: map[string]string{
				"PATH": "/root/.cargo/bin:$PATH",
			},
		}
	case "claude-code":
		// Native installer - avoids Claude startup warnings from npm-based installs.
		return InstallCommands{
			Commands: []string{
				`curl -fsSL https://claude.ai/install.sh | bash`,
			},
			EnvVars: map[string]string{
				"PATH": "/home/moatuser/.local/bin:$PATH",
			},
		}
	case "kubectl":
		// Install kubectl - detects architecture
		return InstallCommands{
			Commands: []string{
				`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && curl -fsSL "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${ARCH}/kubectl" -o /usr/local/bin/kubectl`,
				"chmod +x /usr/local/bin/kubectl",
			},
		}
	case "terraform":
		// Install terraform from releases.hashicorp.com (not GitHub releases)
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf(`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && curl -fsSL "https://releases.hashicorp.com/terraform/%s/terraform_%s_linux_${ARCH}.zip" -o /tmp/terraform.zip`, version, version),
				"unzip -q /tmp/terraform.zip -d /tmp",
				"mv /tmp/terraform /usr/local/bin/terraform",
				"chmod +x /usr/local/bin/terraform",
				"rm -f /tmp/terraform.zip",
			},
		}
	case "helm":
		// Install helm from get.helm.sh (not GitHub releases)
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf(`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && curl -fsSL "https://get.helm.sh/helm-v%s-linux-${ARCH}.tar.gz" | tar -xz -C /tmp`, version),
				`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && mv /tmp/linux-${ARCH}/helm /usr/local/bin/helm`,
				"chmod +x /usr/local/bin/helm",
				"rm -rf /tmp/linux-*",
			},
		}
	default:
		return InstallCommands{}
	}
}

// getDynamicPackageCommands returns install commands for dynamic dependencies.
// Package names are shell-quoted to prevent command injection.
func getDynamicPackageCommands(dep Dependency) InstallCommands {
	switch dep.Type {
	case TypeDynamicNpm:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "@" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("npm install -g %s", shellQuote(pkg)),
			},
		}
	case TypeDynamicPip:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "==" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("pip install %s", shellQuote(pkg)),
			},
		}
	case TypeDynamicUv:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "==" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("uv tool install %s", shellQuote(pkg)),
			},
		}
	case TypeDynamicCargo:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "@" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("cargo install %s", shellQuote(pkg)),
			},
		}
	case TypeDynamicGo:
		pkg := dep.Package
		version := "latest"
		if dep.Version != "" {
			// Don't prefix "latest" with "v" - it's a special Go module specifier
			if dep.Version == "latest" {
				version = "latest"
			} else {
				version = "v" + dep.Version
			}
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("GOBIN=/usr/local/bin go install %s@%s", shellQuote(pkg), shellQuote(version)),
			},
		}
	default:
		return InstallCommands{}
	}
}

// FormatForDockerfile formats install commands as Dockerfile RUN instructions.
func (ic InstallCommands) FormatForDockerfile() string {
	if len(ic.Commands) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("RUN ")
	b.WriteString(strings.Join(ic.Commands, " \\\n    && "))
	if len(ic.Commands) > 1 {
		b.WriteString(" \\\n    && rm -rf /var/lib/apt/lists/*")
	}
	b.WriteString("\n")

	for k, v := range ic.EnvVars {
		b.WriteString(fmt.Sprintf("ENV %s=\"%s\"\n", k, v))
	}

	return b.String()
}

// FormatForScript formats install commands as shell script lines.
func (ic InstallCommands) FormatForScript() string {
	if len(ic.Commands) == 0 {
		return ""
	}

	var b strings.Builder
	for _, cmd := range ic.Commands {
		b.WriteString(cmd)
		b.WriteString("\n")
	}

	for k, v := range ic.EnvVars {
		b.WriteString(fmt.Sprintf("export %s=\"%s\"\n", k, v))
	}

	return b.String()
}
