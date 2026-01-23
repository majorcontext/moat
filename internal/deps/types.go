package deps

// InstallType defines how a dependency is installed.
type InstallType string

const (
	// Registry-based types (defined in registry.yaml)
	TypeRuntime      InstallType = "runtime"
	TypeGithubBinary InstallType = "github-binary"
	TypeApt          InstallType = "apt"
	TypeNpm          InstallType = "npm"
	TypeGoInstall    InstallType = "go-install"
	TypeCustom       InstallType = "custom"
	TypeMeta         InstallType = "meta"
	TypeUvTool       InstallType = "uv-tool" // Tools installed via uv tool install

	// Dynamic types (parsed from prefixes like npm:eslint)
	TypeDynamicNpm   InstallType = "dynamic-npm"   // npm:package
	TypeDynamicPip   InstallType = "dynamic-pip"   // pip:package
	TypeDynamicUv    InstallType = "dynamic-uv"    // uv:package
	TypeDynamicCargo InstallType = "dynamic-cargo" // cargo:package
	TypeDynamicGo    InstallType = "dynamic-go"    // go:package
)

// IsDynamic returns true if this is a dynamic (prefixed) dependency type.
func (t InstallType) IsDynamic() bool {
	switch t {
	case TypeDynamicNpm, TypeDynamicPip, TypeDynamicUv, TypeDynamicCargo, TypeDynamicGo:
		return true
	default:
		return false
	}
}

// DepSpec defines a dependency in the registry.
type DepSpec struct {
	Description string      `yaml:"description,omitempty"`
	Type        InstallType `yaml:"type"`
	Default     string      `yaml:"default,omitempty"`
	Versions    []string    `yaml:"versions,omitempty"`
	Requires    []string    `yaml:"requires,omitempty"`

	// For github-binary type
	Repo  string `yaml:"repo,omitempty"`
	Asset string `yaml:"asset,omitempty"` // Supports {version}, {arch}, {target} placeholders
	Bin   string `yaml:"bin,omitempty"`   // Supports {version}, {arch}, {target} placeholders

	// Targets maps Go architecture names to project-specific target strings.
	// Used for {target} placeholder substitution in Asset/Bin fields.
	// Example: {"amd64": "x86_64-unknown-linux-musl", "arm64": "aarch64-unknown-linux-gnu"}
	// If empty and {arch} is used, standard arch name mapping is applied.
	Targets map[string]string `yaml:"targets,omitempty"`

	// TagPrefix is the prefix used for release tags. Defaults to "v" if empty.
	// Set to "none" for repos that don't use a prefix (e.g., ripgrep uses "14.1.1" not "v14.1.1").
	// Set to a custom value for repos with non-standard prefixes (e.g., "bun-v" for bun).
	TagPrefix string `yaml:"tag-prefix,omitempty"`

	// Command is the name of the installed command if different from the dependency name.
	// For example, ripgrep installs as "rg", not "ripgrep".
	Command string `yaml:"command,omitempty"`

	// Legacy ARM64 support (deprecated, use Targets instead)
	AssetARM64 string `yaml:"asset-arm64,omitempty"`
	BinARM64   string `yaml:"bin-arm64,omitempty"`

	// For apt type
	Package string `yaml:"package,omitempty"`

	// For npm type
	// Package field is reused

	// For go-install type
	GoPackage string `yaml:"go-package,omitempty"`
}

// Dependency represents a parsed dependency from agent.yaml.
type Dependency struct {
	Name    string      // e.g., "node", "eslint"
	Version string      // e.g., "20" or "" for default
	Type    InstallType // Set for dynamic deps (npm:, pip:, etc.)
	Package string      // For dynamic deps: the package name/path
}

// IsDynamic returns true if this dependency was parsed from a prefixed spec.
func (d Dependency) IsDynamic() bool {
	return d.Type.IsDynamic()
}

// ImplicitRequires returns dependencies that are implicitly required.
// For example, npm:eslint requires node.
func (d Dependency) ImplicitRequires() []string {
	switch d.Type {
	case TypeDynamicNpm:
		return []string{"node"}
	case TypeDynamicPip:
		return []string{"python"}
	case TypeDynamicUv:
		return []string{"python", "uv"}
	case TypeDynamicCargo:
		return []string{"rust"}
	case TypeDynamicGo:
		return []string{"go"}
	default:
		return nil
	}
}
