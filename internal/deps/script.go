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
		aptPkgs    []string
		runtimes   []Dependency
		githubBins []Dependency
		npmPkgs    []Dependency
		customDeps []Dependency
	)

	for _, dep := range deps {
		spec := Registry[dep.Name]
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

	// Step 1: Base apt packages
	b.WriteString("# Base packages\n")
	b.WriteString("apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip\n\n")

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
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s runtime\n", dep.Name))
		b.WriteString(generateRuntimeInstallScript(dep.Name, version))
		b.WriteString("\n")
	}

	// Step 4: GitHub binary downloads
	for _, dep := range githubBins {
		spec := Registry[dep.Name]
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(generateGithubBinaryInstallScript(dep.Name, version, spec))
		b.WriteString("\n")
	}

	// Step 5: npm globals (grouped into single command)
	if len(npmPkgs) > 0 {
		var pkgNames []string
		for _, dep := range npmPkgs {
			spec := Registry[dep.Name]
			pkg := spec.Package
			if pkg == "" {
				pkg = dep.Name
			}
			pkgNames = append(pkgNames, pkg)
		}
		b.WriteString("# npm packages\n")
		b.WriteString("npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Step 6: Custom installs
	for _, dep := range customDeps {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(generateCustomInstallScript(dep.Name, version))
		b.WriteString("\n")
	}

	return b.String(), nil
}

func generateRuntimeInstallScript(name, version string) string {
	switch name {
	case "node":
		return fmt.Sprintf(`curl -fsSL https://deb.nodesource.com/setup_%s.x | bash -
apt-get install -y nodejs
`, version)
	case "go":
		return fmt.Sprintf(`curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"
`, version)
	case "python":
		return fmt.Sprintf(`apt-get update && apt-get install -y software-properties-common
add-apt-repository -y ppa:deadsnakes/ppa
apt-get update
apt-get install -y python%s python%s-venv python%s-distutils
curl -sS https://bootstrap.pypa.io/get-pip.py | python%s - --root-user-action=ignore
`, version, version, version, version)
	default:
		return ""
	}
}

func generateGithubBinaryInstallScript(name, version string, spec DepSpec) string {
	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)

	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.Repo, version, asset)

	if strings.HasSuffix(asset, ".zip") {
		// For zip files, extract and move binary
		if binPath == "" {
			binPath = name
		}
		return fmt.Sprintf(`curl -fsSL %s -o /tmp/%s.zip
unzip -q /tmp/%s.zip -d /tmp/%s
mv /tmp/%s/%s /usr/local/bin/%s
chmod +x /usr/local/bin/%s
rm -rf /tmp/%s*
`, url, name, name, name, name, binPath, name, name, name)
	}

	// For tar.gz files
	if binPath == "" {
		binPath = name
	}
	return fmt.Sprintf(`curl -fsSL %s | tar -xz -C /tmp
mv /tmp/%s /usr/local/bin/%s
chmod +x /usr/local/bin/%s
`, url, binPath, name, name)
}

func generateCustomInstallScript(name, version string) string {
	switch name {
	case "playwright":
		return `npm install -g playwright
npx playwright install --with-deps chromium
`
	case "aws":
		return `curl -fsSL "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip
unzip -q /tmp/awscliv2.zip -d /tmp
/tmp/aws/install
rm -rf /tmp/aws*
`
	case "gcloud":
		return `curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xz -C /opt
/opt/google-cloud-sdk/install.sh --quiet --path-update=true
export PATH="/opt/google-cloud-sdk/bin:$PATH"
`
	default:
		return ""
	}
}
