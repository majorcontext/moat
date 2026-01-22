// internal/deps/install.go
package deps

import (
	"fmt"
	"strings"
)

// InstallCommands holds the commands needed to install a dependency.
type InstallCommands struct {
	Commands []string          // Shell commands to run
	EnvVars  map[string]string // Environment variables to set
}

// getRuntimeCommands returns install commands for runtime dependencies.
func getRuntimeCommands(name, version string) InstallCommands {
	switch name {
	case "node":
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL https://deb.nodesource.com/setup_%s.x | bash -", version),
				"apt-get install -y nodejs",
			},
		}
	case "go":
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xz", version),
			},
			EnvVars: map[string]string{
				"PATH": "/usr/local/go/bin:$PATH",
			},
		}
	case "python":
		// Use Ubuntu's default python3 packages - more reliable than deadsnakes PPA
		// For 3.10 (Ubuntu 22.04 default), use python3 packages directly
		// For other versions, we'd need a different approach (e.g., pyenv or official docker images)
		if version == "3.10" || version == "" {
			return InstallCommands{
				Commands: []string{
					"apt-get update && apt-get install -y python3 python3-pip python3-venv",
					"update-alternatives --install /usr/bin/python python /usr/bin/python3 1",
				},
			}
		}
		// For specific versions, use the official Python docker image approach via multi-stage
		// or fall back to trying the system package manager
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
// Supports ARM64 via architecture detection at build time when AssetARM64 is specified.
func getGithubBinaryCommands(name, version string, spec DepSpec) InstallCommands {
	// If ARM64 asset is specified, use architecture detection
	if spec.AssetARM64 != "" {
		return getGithubBinaryCommandsMultiArch(name, version, spec)
	}

	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)
	if binPath == "" {
		binPath = name
	}

	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.Repo, version, asset)

	if strings.HasSuffix(asset, ".zip") {
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL %s -o /tmp/%s.zip", url, name),
				fmt.Sprintf("unzip -q /tmp/%s.zip -d /tmp/%s", name, name),
				fmt.Sprintf("mv /tmp/%s/%s /usr/local/bin/%s", name, binPath, name),
				fmt.Sprintf("chmod +x /usr/local/bin/%s", name),
				fmt.Sprintf("rm -rf /tmp/%s*", name),
			},
		}
	}

	if strings.HasSuffix(asset, ".tar.gz") || strings.HasSuffix(asset, ".tgz") {
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("curl -fsSL %s | tar -xz -C /tmp", url),
				fmt.Sprintf("mv /tmp/%s /usr/local/bin/%s", binPath, name),
				fmt.Sprintf("chmod +x /usr/local/bin/%s", name),
			},
		}
	}

	// Raw binary (no archive extension)
	return InstallCommands{
		Commands: []string{
			fmt.Sprintf("curl -fsSL %s -o /usr/local/bin/%s", url, name),
			fmt.Sprintf("chmod +x /usr/local/bin/%s", name),
		},
	}
}

// archBinarySpec holds architecture-specific binary details.
type archBinarySpec struct {
	url string
	bin string
}

// getGithubBinaryCommandsMultiArch generates install commands with architecture detection.
func getGithubBinaryCommandsMultiArch(name, version string, spec DepSpec) InstallCommands {
	amd64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, replaceVersion(spec.Asset, version)),
		bin: orDefault(replaceVersion(spec.Bin, version), name),
	}
	arm64 := archBinarySpec{
		url: githubReleaseURL(spec.Repo, version, replaceVersion(spec.AssetARM64, version)),
		bin: orDefault(replaceVersion(spec.BinARM64, version), amd64.bin),
	}

	isZip := strings.HasSuffix(spec.Asset, ".zip")
	downloadCmd := buildArchDetectCommand(name, amd64, arm64, isZip)

	return InstallCommands{
		Commands: []string{
			downloadCmd,
			fmt.Sprintf("chmod +x /usr/local/bin/%s", name),
			fmt.Sprintf("rm -rf /tmp/%s*", name),
		},
	}
}

// githubReleaseURL constructs a GitHub release download URL.
func githubReleaseURL(repo, version, asset string) string {
	return fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", repo, version, asset)
}

// replaceVersion replaces {version} placeholder in a string.
func replaceVersion(s, version string) string {
	return strings.ReplaceAll(s, "{version}", version)
}

// orDefault returns s if non-empty, otherwise def.
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// buildArchDetectCommand generates a shell command that downloads the correct binary for the architecture.
func buildArchDetectCommand(name string, amd64, arm64 archBinarySpec, isZip bool) string {
	if isZip {
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
	}
	// tar.gz
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
func getCustomCommands(name, _ string) InstallCommands {
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
		return InstallCommands{
			Commands: []string{
				"curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xz -C /opt",
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
	case "kubectl":
		// Install kubectl - detects architecture
		return InstallCommands{
			Commands: []string{
				`ARCH=$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') && curl -fsSL "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/${ARCH}/kubectl" -o /usr/local/bin/kubectl`,
				"chmod +x /usr/local/bin/kubectl",
			},
		}
	default:
		return InstallCommands{}
	}
}

// getDynamicPackageCommands returns install commands for dynamic dependencies.
func getDynamicPackageCommands(dep Dependency) InstallCommands {
	switch dep.Type {
	case TypeDynamicNpm:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "@" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("npm install -g %s", pkg),
			},
		}
	case TypeDynamicPip:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "==" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("pip install %s", pkg),
			},
		}
	case TypeDynamicUv:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "==" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("uv tool install %s", pkg),
			},
		}
	case TypeDynamicCargo:
		pkg := dep.Package
		if dep.Version != "" {
			pkg = pkg + "@" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("cargo install %s", pkg),
			},
		}
	case TypeDynamicGo:
		pkg := dep.Package
		version := "latest"
		if dep.Version != "" {
			version = "v" + dep.Version
		}
		return InstallCommands{
			Commands: []string{
				fmt.Sprintf("GOBIN=/usr/local/bin go install %s@%s", pkg, version),
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
