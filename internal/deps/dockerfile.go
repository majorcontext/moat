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

	// Layer 1: Base apt packages (curl, ca-certificates, etc.)
	b.WriteString("# Base packages\n")
	b.WriteString("RUN apt-get update && apt-get install -y \\\n")
	b.WriteString("    curl \\\n")
	b.WriteString("    ca-certificates \\\n")
	b.WriteString("    gnupg \\\n")
	b.WriteString("    unzip \\\n")
	b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")

	// Layer 2: User apt packages
	if len(aptPkgs) > 0 {
		sort.Strings(aptPkgs)
		b.WriteString("# Apt packages\n")
		b.WriteString("RUN apt-get update && apt-get install -y \\\n")
		for _, pkg := range aptPkgs {
			b.WriteString("    " + pkg + " \\\n")
		}
		b.WriteString("    && rm -rf /var/lib/apt/lists/*\n\n")
	}

	// Layer 3: Runtimes
	for _, dep := range runtimes {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s runtime\n", dep.Name))
		b.WriteString(generateRuntimeInstall(dep.Name, version))
		b.WriteString("\n")
	}

	// Layer 4: GitHub binary downloads
	for _, dep := range githubBins {
		spec := Registry[dep.Name]
		version := dep.Version
		if version == "" {
			version = spec.Default
		}
		b.WriteString(fmt.Sprintf("# %s\n", dep.Name))
		b.WriteString(generateGithubBinaryInstall(dep.Name, version, spec))
		b.WriteString("\n")
	}

	// Layer 5: npm globals
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
		b.WriteString("RUN npm install -g " + strings.Join(pkgNames, " ") + "\n\n")
	}

	// Layer 6: Custom installs
	for _, dep := range customDeps {
		version := dep.Version
		if version == "" {
			version = Registry[dep.Name].Default
		}
		b.WriteString(fmt.Sprintf("# %s (custom)\n", dep.Name))
		b.WriteString(generateCustomInstall(dep.Name, version))
		b.WriteString("\n")
	}

	return b.String(), nil
}

func generateRuntimeInstall(name, version string) string {
	switch name {
	case "node":
		return fmt.Sprintf(`RUN curl -fsSL https://deb.nodesource.com/setup_%s.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*
`, version)
	case "go":
		return fmt.Sprintf(`RUN curl -fsSL https://go.dev/dl/go%s.linux-amd64.tar.gz | tar -C /usr/local -xz
ENV PATH="/usr/local/go/bin:$PATH"
`, version)
	case "python":
		return fmt.Sprintf(`RUN apt-get update && apt-get install -y software-properties-common \
    && add-apt-repository -y ppa:deadsnakes/ppa \
    && apt-get update \
    && apt-get install -y python%s python%s-venv python%s-distutils \
    && curl -sS https://bootstrap.pypa.io/get-pip.py | python%s - --root-user-action=ignore \
    && rm -rf /var/lib/apt/lists/*
`, version, version, version, version)
	default:
		return ""
	}
}

func generateGithubBinaryInstall(name, version string, spec DepSpec) string {
	asset := strings.ReplaceAll(spec.Asset, "{version}", version)
	binPath := strings.ReplaceAll(spec.Bin, "{version}", version)

	url := fmt.Sprintf("https://github.com/%s/releases/download/v%s/%s", spec.Repo, version, asset)

	if strings.HasSuffix(asset, ".zip") {
		return fmt.Sprintf(`RUN curl -fsSL %s -o /tmp/%s.zip \
    && unzip /tmp/%s.zip -d /tmp/%s \
    && mv /tmp/%s/%s /usr/local/bin/%s \
    && chmod +x /usr/local/bin/%s \
    && rm -rf /tmp/%s*
`, url, name, name, name, name, binPath, name, name, name)
	}
	// tar.gz
	return fmt.Sprintf(`RUN curl -fsSL %s | tar -xz -C /tmp \
    && mv /tmp/%s /usr/local/bin/%s \
    && chmod +x /usr/local/bin/%s
`, url, binPath, name, name)
}

func generateCustomInstall(name, version string) string {
	switch name {
	case "playwright":
		return `RUN npm install -g playwright \
    && npx playwright install --with-deps chromium
`
	case "aws":
		return `RUN curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o /tmp/awscliv2.zip \
    && unzip /tmp/awscliv2.zip -d /tmp \
    && /tmp/aws/install \
    && rm -rf /tmp/aws*
`
	case "gcloud":
		return `RUN curl -fsSL https://dl.google.com/dl/cloudsdk/channels/rapid/downloads/google-cloud-cli-linux-x86_64.tar.gz | tar -xz -C /opt \
    && /opt/google-cloud-sdk/install.sh --quiet --path-update=true
ENV PATH="/opt/google-cloud-sdk/bin:$PATH"
`
	default:
		return ""
	}
}
