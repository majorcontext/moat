package oauth

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/majorcontext/moat/internal/config"
)

// Config holds the OAuth provider configuration for a named grant.
// Stored at ~/.moat/oauth/<name>.yaml.
type Config struct {
	AuthURL      string `yaml:"auth_url"`
	TokenURL     string `yaml:"token_url"`
	ClientID     string `yaml:"client_id"`
	ClientSecret string `yaml:"client_secret,omitempty"`
	Scopes       string `yaml:"scopes,omitempty"`

	// RegistrationEndpoint is set by discovery when Dynamic Client
	// Registration (RFC 7591) is available. It is not persisted to YAML;
	// once DCR succeeds the resulting ClientID is cached instead.
	RegistrationEndpoint string `yaml:"-"`
}

// Validate checks that required fields are present and URLs use HTTPS.
func (c *Config) Validate() error {
	if c.AuthURL == "" {
		return fmt.Errorf("auth_url is required")
	}
	if err := requireHTTPS(c.AuthURL, "auth_url"); err != nil {
		return err
	}
	if c.TokenURL == "" {
		return fmt.Errorf("token_url is required")
	}
	if err := requireHTTPS(c.TokenURL, "token_url"); err != nil {
		return err
	}
	if c.ClientID == "" {
		return fmt.Errorf("client_id is required")
	}
	return nil
}

// requireHTTPS validates that a URL uses the HTTPS scheme.
func requireHTTPS(raw, field string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s is not a valid URL: %w", field, err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%s must use HTTPS (got %q)", field, u.Scheme)
	}
	return nil
}

// DefaultConfigDir returns the default directory for OAuth configs
// (<moat-home>/oauth/). See config.GlobalConfigDir for how MOAT_HOME
// overrides the default ~/.moat location.
func DefaultConfigDir() string {
	return filepath.Join(config.GlobalConfigDir(), "oauth")
}

// LoadConfig loads an OAuth config from <dir>/<name>.yaml.
func LoadConfig(dir, name string) (*Config, error) {
	path := filepath.Join(dir, name+".yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading oauth config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing oauth config %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid oauth config %s: %w", path, err)
	}

	return &cfg, nil
}

// SaveConfig writes an OAuth config to <dir>/<name>.yaml.
// Creates the directory if it does not exist.
func SaveConfig(dir, name string, cfg *Config) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating oauth config dir: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling oauth config: %w", err)
	}

	path := filepath.Join(dir, name+".yaml")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("writing oauth config %s: %w", path, err)
	}

	return nil
}
