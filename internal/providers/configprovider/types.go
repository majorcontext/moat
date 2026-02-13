package configprovider

// ProviderDef defines a credential provider via YAML configuration.
type ProviderDef struct {
	Name         string          `yaml:"name"`
	Description  string          `yaml:"description"`
	Aliases      []string        `yaml:"aliases,omitempty"`
	Hosts        []string        `yaml:"hosts"`
	Inject       InjectConfig    `yaml:"inject"`
	SourceEnv    []string        `yaml:"source_env,omitempty"`
	ContainerEnv string          `yaml:"container_env,omitempty"`
	Validate     *ValidateConfig `yaml:"validate,omitempty"`
	Prompt       string          `yaml:"prompt,omitempty"`
}

// InjectConfig defines how credentials are injected into HTTP requests.
type InjectConfig struct {
	Header string `yaml:"header"`
	Prefix string `yaml:"prefix,omitempty"`
}

// ValidateConfig defines an optional endpoint for token validation.
type ValidateConfig struct {
	URL    string `yaml:"url"`
	Method string `yaml:"method,omitempty"` // default: GET
	Header string `yaml:"header,omitempty"` // default: inject.header
	Prefix string `yaml:"prefix,omitempty"` // default: inject.prefix
}
