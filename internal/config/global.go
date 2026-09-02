package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/majorcontext/moat/internal/ui"
	"gopkg.in/yaml.v3"
)

// malformedConfigWarnOnce keeps the malformed-config warning to one line per
// process — LoadGlobal is called several times per command.
var malformedConfigWarnOnce sync.Once

// profileNameRe matches valid credential profile names. Kept in sync with
// credential.ValidateProfile — a profile name becomes a directory name under
// ~/.moat/credentials/profiles/, so it must stay free of path separators.
var profileNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]*$`)

// GlobalConfig holds global Moat settings from ~/.moat/config.yaml.
type GlobalConfig struct {
	Proxy  ProxyConfig  `yaml:"proxy"`
	Debug  DebugConfig  `yaml:"debug"`
	Mounts []MountEntry `yaml:"mounts,omitempty"`

	// Profiles holds per-credential-profile settings, keyed by profile name.
	// They apply to any run using that profile (--profile / MOAT_PROFILE).
	Profiles map[string]ProfileConfig `yaml:"profiles,omitempty"`
}

// ProfileConfig holds settings that apply to every run using a credential
// profile. A profile is an identity — a set of credentials plus whatever
// configuration goes with them — so settings that belong to the identity rather
// than to a project live here instead of in each moat.yaml.
type ProfileConfig struct {
	// Env are environment variables set in the container for any run using
	// this profile. moat.yaml env and -e flags override them; proxy variables
	// moat owns are filtered out, exactly as for the other two layers.
	Env map[string]string `yaml:"env,omitempty"`
}

// DebugConfig holds debug logging settings.
type DebugConfig struct {
	RetentionDays int `yaml:"retention_days"`
}

// ProxyConfig holds reverse proxy settings.
type ProxyConfig struct {
	Port int `yaml:"port"`
}

// DefaultGlobalConfig returns the default global configuration.
func DefaultGlobalConfig() *GlobalConfig {
	return &GlobalConfig{
		Proxy: ProxyConfig{
			Port: 8080,
		},
		Debug: DebugConfig{
			RetentionDays: 14,
		},
	}
}

// LoadGlobal reads the moat global config file and applies environment overrides.
// The config path is <GlobalConfigDir>/config.yaml — by default ~/.moat/config.yaml,
// or $MOAT_HOME/config.yaml when MOAT_HOME is set.
func LoadGlobal() (*GlobalConfig, error) {
	cfg := DefaultGlobalConfig()

	configPath := filepath.Join(GlobalConfigDir(), "config.yaml")
	if data, err := os.ReadFile(configPath); err == nil {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			// A malformed global config is almost always a user typo, so surface
			// it rather than silently falling back. Use ui (always on stderr) —
			// log.Warn only shows with --verbose — and warn once per process
			// despite the multiple LoadGlobal calls. Reset to clean defaults
			// since a partial unmarshal may have set some fields.
			malformedConfigWarnOnce.Do(func() {
				ui.Warnf("ignoring malformed global config %s; using defaults: %v", configPath, err)
			})
			cfg = DefaultGlobalConfig()
		}
	}

	// Tilde expansion in mount sources resolves against the real user home,
	// not MOAT_HOME — `~/foo` is a user-facing alias for the OS home dir.
	homeDir, _ := os.UserHomeDir()

	// Validate global mounts: require absolute source paths and read-only mode.
	var validMounts []MountEntry
	for i, m := range cfg.Mounts {
		// Expand ~ in source path
		if strings.HasPrefix(m.Source, "~/") && homeDir != "" {
			m.Source = filepath.Join(homeDir, m.Source[2:])
		}

		if !filepath.IsAbs(m.Source) {
			return nil, fmt.Errorf("global mount %d: source %q must be an absolute path (no workspace to resolve relative paths against)", i+1, m.Source)
		}

		// Enforce read-only
		m.ReadOnly = true
		m.Mode = "ro"

		// Excludes not supported on global mounts
		if len(m.Exclude) > 0 {
			return nil, fmt.Errorf("global mount %d: excludes are not supported on global mounts", i+1)
		}

		validMounts = append(validMounts, m)
	}
	cfg.Mounts = validMounts

	// Validate profile names and env keys. A typo here would otherwise surface
	// as a silently missing variable inside the container.
	for name, prof := range cfg.Profiles {
		if err := ValidateProfileName(name); err != nil {
			return nil, fmt.Errorf("profiles.%s: %w", name, err)
		}
		for key := range prof.Env {
			if err := validateEnvName(key); err != nil {
				return nil, fmt.Errorf("profiles.%s.env: %w", name, err)
			}
		}
	}

	// Apply environment overrides
	if portStr := os.Getenv("MOAT_PROXY_PORT"); portStr != "" {
		if port, err := strconv.Atoi(portStr); err == nil {
			cfg.Proxy.Port = port
		}
	}

	return cfg, nil
}

// GlobalConfigDir returns the path to the moat configuration directory.
//
// By default this is ~/.moat, but the MOAT_HOME environment variable may
// override it to an absolute path. MOAT_HOME is the complete moat directory,
// not a parent containing .moat — set it to e.g. /tmp/moat-test, not /tmp.
// Primarily used for hermetic test runs and rare multi-version setups where
// one daemon must not see another's state.
func GlobalConfigDir() string {
	if override := os.Getenv("MOAT_HOME"); override != "" {
		return override
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat")
	}
	return filepath.Join(homeDir, ".moat")
}

// ProfileEnv returns the environment variables configured for a credential
// profile, or nil when the profile has none. An empty name (no --profile) never
// matches: the default store is not a named profile, so there is nowhere to
// hang settings and a `profiles:` entry cannot accidentally apply to every run.
func (c *GlobalConfig) ProfileEnv(name string) map[string]string {
	if c == nil || name == "" {
		return nil
	}
	prof, ok := c.Profiles[name]
	if !ok {
		return nil
	}
	return prof.Env
}

// ValidateProfileName checks a profile name from the global config. It mirrors
// credential.ValidateProfile, which guards the --profile flag, so a name that
// is grantable is also configurable and vice versa. Duplicated rather than
// imported to keep config free of a dependency on credential.
func ValidateProfileName(name string) error {
	if name == "" {
		return fmt.Errorf("profile name cannot be empty")
	}
	if !profileNameRe.MatchString(name) {
		return fmt.Errorf("invalid profile name %q: must start with a letter or digit and contain only letters, digits, hyphens, and underscores", name)
	}
	return nil
}

// validateEnvName rejects environment variable names the container could not
// receive: an empty name, or one containing "=" or a NUL byte.
func validateEnvName(name string) error {
	if name == "" {
		return fmt.Errorf("environment variable name cannot be empty")
	}
	if strings.ContainsAny(name, "=\x00") {
		return fmt.Errorf("invalid environment variable name %q: must not contain %q or a NUL byte", name, "=")
	}
	return nil
}
