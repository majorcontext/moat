package gatekeeper

import (
	"os"

	"gopkg.in/yaml.v3"
)

// Config represents a Gate Keeper configuration file.
type Config struct {
	Proxy       ProxyConfig        `yaml:"proxy"`
	TLS         TLSConfig          `yaml:"tls"`
	Credentials []CredentialConfig `yaml:"credentials"`
	Network     NetworkConfig      `yaml:"network"`
	Policy      map[string]string  `yaml:"policy"` // Opaque policy settings (e.g., "scope": "tool-use")
	Log         LogConfig          `yaml:"log"`
}

// ProxyConfig configures the proxy listener.
type ProxyConfig struct {
	Port      int    `yaml:"port"`
	Host      string `yaml:"host"`
	AuthToken string `yaml:"auth_token,omitempty"` // Optional token clients must provide via Proxy-Authorization
}

// TLSConfig configures the CA certificate used for TLS interception.
type TLSConfig struct {
	CACert string `yaml:"ca_cert"`
	CAKey  string `yaml:"ca_key"`
}

// CredentialConfig describes a credential to resolve and inject.
// Host specifies which requests receive the credential. Header names the
// HTTP header to set (defaults to "Authorization"). Grant is an optional
// label used for logging.
//
// When the header is "Authorization", the proxy needs a full header value
// including the auth scheme (e.g., "Bearer token123"). If the source value
// is a bare token without a scheme prefix, the gatekeeper auto-detects the
// correct scheme from known token prefixes (GitHub ghp_/gho_/etc.) or
// defaults to "Bearer". Set Prefix to override the auto-detected scheme.
type CredentialConfig struct {
	Host   string       `yaml:"host"`             // Target host (e.g., "api.github.com")
	Header string       `yaml:"header,omitempty"` // Header name (default: "Authorization")
	Prefix string       `yaml:"prefix,omitempty"` // Auth scheme prefix (e.g., "Bearer", "token"); auto-detected if omitted
	Source SourceConfig `yaml:"source"`
	Grant  string       `yaml:"grant,omitempty"` // Optional label for logging
}

// SourceConfig describes where to read a credential value from.
type SourceConfig struct {
	Type   string `yaml:"type"`             // "env", "static", "aws-secretsmanager"
	Var    string `yaml:"var,omitempty"`    // for env source
	Value  string `yaml:"value,omitempty"`  // for static source
	Secret string `yaml:"secret,omitempty"` // for aws-secretsmanager
	Region string `yaml:"region,omitempty"` // for aws-secretsmanager
}

// NetworkConfig configures network policy.
type NetworkConfig struct {
	Policy string        `yaml:"policy"`
	Allow  []string      `yaml:"allow,omitempty"`
	Rules  []NetworkRule `yaml:"rules,omitempty"`
}

// NetworkRule describes per-host request restrictions.
type NetworkRule struct {
	Host    string   `yaml:"host"`
	Methods []string `yaml:"methods,omitempty"`
}

// LogConfig configures logging.
type LogConfig struct {
	Level    string `yaml:"level"`    // Log level (e.g., "debug", "info", "warn", "error")
	Format   string `yaml:"format"`   // Output format ("json" or "text")
	Output   string `yaml:"output"`   // Destination ("stderr", "stdout", or file path)
	Requests string `yaml:"requests"` // File path for proxied request logs (JSONL format)
}

// ParseConfig parses a Gate Keeper config from YAML bytes.
func ParseConfig(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// LoadConfig reads and parses a Gate Keeper config from a file path.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseConfig(data)
}
