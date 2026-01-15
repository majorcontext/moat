package deps

// InstallType defines how a dependency is installed.
type InstallType string

const (
	TypeRuntime      InstallType = "runtime"
	TypeGithubBinary InstallType = "github-binary"
	TypeApt          InstallType = "apt"
	TypeNpm          InstallType = "npm"
	TypeGoInstall    InstallType = "go-install"
	TypeCustom       InstallType = "custom"
	TypeMeta         InstallType = "meta"
)

// DepSpec defines a dependency in the registry.
type DepSpec struct {
	Description string      `yaml:"description,omitempty"`
	Type        InstallType `yaml:"type"`
	Default     string      `yaml:"default,omitempty"`
	Versions    []string    `yaml:"versions,omitempty"`
	Requires    []string    `yaml:"requires,omitempty"`

	// For github-binary type
	Repo  string `yaml:"repo,omitempty"`
	Asset string `yaml:"asset,omitempty"`
	Bin   string `yaml:"bin,omitempty"`

	// For apt type
	Package string `yaml:"package,omitempty"`

	// For npm type
	// Package field is reused

	// For go-install type
	GoPackage string `yaml:"go-package,omitempty"`
}

// Dependency represents a parsed dependency from agent.yaml.
type Dependency struct {
	Name    string // e.g., "node"
	Version string // e.g., "20" or "" for default
}
