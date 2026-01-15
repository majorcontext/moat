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
func getGithubBinaryCommands(name, version string, spec DepSpec) InstallCommands {
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

	// tar.gz
	return InstallCommands{
		Commands: []string{
			fmt.Sprintf("curl -fsSL %s | tar -xz -C /tmp", url),
			fmt.Sprintf("mv /tmp/%s /usr/local/bin/%s", binPath, name),
			fmt.Sprintf("chmod +x /usr/local/bin/%s", name),
		},
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
		return InstallCommands{
			Commands: []string{
				`curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip`,
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
