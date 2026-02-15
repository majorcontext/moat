// Package config handles agent.yaml manifest parsing.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// volumeNameRe matches valid volume names: lowercase alphanumeric, hyphens, underscores.
// Must start with a letter or digit.
var volumeNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Config represents an agent.yaml manifest.
type Config struct {
	Name         string            `yaml:"name,omitempty"`
	Agent        string            `yaml:"agent"`
	Version      string            `yaml:"version,omitempty"`
	Dependencies []string          `yaml:"dependencies,omitempty"`
	Grants       []string          `yaml:"grants,omitempty"`
	Env          map[string]string `yaml:"env,omitempty"`
	Secrets      map[string]string `yaml:"secrets,omitempty"`
	Mounts       []string          `yaml:"mounts,omitempty"`
	Ports        map[string]int    `yaml:"ports,omitempty"`
	Network      NetworkConfig     `yaml:"network,omitempty"`
	Command      []string          `yaml:"command,omitempty"`
	Claude       ClaudeConfig      `yaml:"claude,omitempty"`
	Codex        CodexConfig       `yaml:"codex,omitempty"`
	Gemini       GeminiConfig      `yaml:"gemini,omitempty"`
	Interactive  bool              `yaml:"interactive,omitempty"`
	Snapshots    SnapshotConfig    `yaml:"snapshots,omitempty"`
	Tracing      TracingConfig     `yaml:"tracing,omitempty"`
	Hooks        HooksConfig       `yaml:"hooks,omitempty"`

	// Sandbox configures container sandboxing.
	// "none" disables gVisor sandbox (Docker only).
	// Empty string or omitted uses default (gVisor enabled).
	Sandbox string `yaml:"sandbox,omitempty"`

	// Runtime forces a specific container runtime ("docker" or "apple").
	// If not set, moat auto-detects the best available runtime.
	// Useful when agent needs docker:dind on macOS (Apple containers can't run dind).
	Runtime string `yaml:"runtime,omitempty"`

	Volumes   []VolumeConfig         `yaml:"volumes,omitempty"`
	Container ContainerConfig        `yaml:"container,omitempty"`
	MCP       []MCPServerConfig      `yaml:"mcp,omitempty"`
	Services  map[string]ServiceSpec `yaml:"services,omitempty"`

	// Deprecated: old runtime field for language versions
	DeprecatedRuntime *deprecatedRuntime `yaml:"-"`
}

// ContainerConfig configures container resource limits and settings.
// These settings apply to both Docker and Apple container runtimes.
type ContainerConfig struct {
	// Memory specifies the memory limit in megabytes.
	// Applies to both Docker and Apple containers.
	// If not set, Apple containers default to 4096 MB (4 GB).
	// Docker containers have no default limit.
	//
	// Example:
	//   container:
	//     memory: 8192  # 8 GB
	Memory int `yaml:"memory,omitempty"`

	// CPUs specifies the number of CPUs.
	// Applies to both Docker and Apple containers.
	// If not set, uses runtime defaults.
	//
	// Example:
	//   container:
	//     cpus: 8
	CPUs int `yaml:"cpus,omitempty"`

	// DNS specifies DNS servers for both runtime containers and builders.
	// Applies to both Docker and Apple containers.
	// If not set, defaults to ["8.8.8.8", "8.8.4.4"] (Google DNS).
	//
	// Example:
	//   container:
	//     dns: ["192.168.1.1", "1.1.1.1"]
	//
	// Note: Using public DNS will send queries to that provider,
	// potentially leaking information about your dependencies and internal services.
	DNS []string `yaml:"dns,omitempty"`
}

// VolumeConfig defines a named volume to mount inside the container.
// Volumes are managed by moat and persist across runs for the same agent name.
type VolumeConfig struct {
	Name     string `yaml:"name"`
	Target   string `yaml:"target"`
	ReadOnly bool   `yaml:"readonly,omitempty"`
}

// MCPServerConfig defines a remote MCP server configuration for top-level
// MCP servers in agent.yaml. It specifies the server name, URL endpoint,
// and optional authentication settings for credential injection.
type MCPServerConfig struct {
	Name string         `yaml:"name"`
	URL  string         `yaml:"url"`
	Auth *MCPAuthConfig `yaml:"auth,omitempty"`
}

// MCPAuthConfig defines authentication for an MCP server. It specifies which
// grant credential to use and which HTTP header to inject it into when
// proxying requests to the MCP server.
type MCPAuthConfig struct {
	Grant  string `yaml:"grant"`
	Header string `yaml:"header"`
}

// ServiceSpec allows customizing service behavior.
type ServiceSpec struct {
	Env   map[string]string `yaml:"env,omitempty"`
	Image string            `yaml:"image,omitempty"`
	Wait  *bool             `yaml:"wait,omitempty"`
}

// ServiceWait returns whether to wait for this service to be ready (default: true).
func (s ServiceSpec) ServiceWait() bool {
	if s.Wait == nil {
		return true
	}
	return *s.Wait
}

// ValidateServices checks that services: keys correspond to declared service dependencies.
func (c *Config) ValidateServices(serviceNames []string) error {
	nameSet := make(map[string]bool, len(serviceNames))
	for _, n := range serviceNames {
		nameSet[n] = true
	}
	for name := range c.Services {
		if !nameSet[name] {
			return fmt.Errorf("services.%s configured but %s not declared in dependencies\n\nAdd to dependencies:\n  dependencies:\n    - %s", name, name, name)
		}
	}
	return nil
}

// NetworkConfig configures network access policies for the agent.
type NetworkConfig struct {
	Policy string   `yaml:"policy,omitempty"` // "permissive" or "strict", default "permissive"
	Allow  []string `yaml:"allow,omitempty"`  // allowed host patterns
}

// ClaudeConfig configures Claude Code integration options.
type ClaudeConfig struct {
	// SyncLogs enables mounting Claude's session logs directory so logs from
	// inside the container appear on the host at the correct project location.
	// Default: false, unless the "anthropic" grant is configured (then true).
	SyncLogs *bool `yaml:"sync_logs,omitempty"`

	// Plugins enables or disables specific plugins for this run.
	// Keys are in format "plugin-name@marketplace", values are true/false.
	Plugins map[string]bool `yaml:"plugins,omitempty"`

	// Marketplaces defines additional plugin marketplaces for this run.
	Marketplaces map[string]MarketplaceSpec `yaml:"marketplaces,omitempty"`

	// MCP defines MCP (Model Context Protocol) server configurations.
	MCP map[string]MCPServerSpec `yaml:"mcp,omitempty"`
}

// CodexConfig configures OpenAI Codex CLI integration options.
type CodexConfig struct {
	// SyncLogs enables mounting Codex's session logs directory so logs from
	// inside the container appear on the host at the correct project location.
	// Default: false, unless the "openai" grant is configured (then true).
	SyncLogs *bool `yaml:"sync_logs,omitempty"`

	// MCP defines MCP (Model Context Protocol) server configurations.
	MCP map[string]MCPServerSpec `yaml:"mcp,omitempty"`
}

// GeminiConfig configures Google Gemini CLI integration options.
type GeminiConfig struct {
	// SyncLogs enables mounting Gemini's session logs directory so logs from
	// inside the container appear on the host at the correct project location.
	// Default: false, unless the "gemini" grant is configured (then true).
	SyncLogs *bool `yaml:"sync_logs,omitempty"`

	// MCP defines MCP (Model Context Protocol) server configurations.
	MCP map[string]MCPServerSpec `yaml:"mcp,omitempty"`
}

// MarketplaceSpec defines a plugin marketplace source.
type MarketplaceSpec struct {
	// Source is the type of marketplace: "github", "git", or "directory"
	Source string `yaml:"source"`

	// Repo is the GitHub repository in "owner/repo" format (for source: github)
	Repo string `yaml:"repo,omitempty"`

	// URL is the git URL (for source: git)
	// Supports both HTTPS (https://github.com/org/repo.git) and
	// SSH (git@github.com:org/repo.git) URLs
	URL string `yaml:"url,omitempty"`

	// Path is the local directory path (for source: directory)
	Path string `yaml:"path,omitempty"`

	// Ref is the git branch, tag, or commit to use (optional)
	Ref string `yaml:"ref,omitempty"`
}

// MCPServerSpec defines an MCP server configuration.
type MCPServerSpec struct {
	// Command is the executable to run
	Command string `yaml:"command"`

	// Args are command-line arguments
	Args []string `yaml:"args,omitempty"`

	// Env are environment variables for the server
	// Supports ${secrets.NAME} syntax for secret references
	Env map[string]string `yaml:"env,omitempty"`

	// Grant specifies a credential grant to inject (e.g., "github", "anthropic")
	Grant string `yaml:"grant,omitempty"`

	// Cwd is the working directory for the server
	Cwd string `yaml:"cwd,omitempty"`
}

// SnapshotConfig configures workspace snapshots.
type SnapshotConfig struct {
	Disabled  bool                    `yaml:"disabled,omitempty"`
	Triggers  SnapshotTriggerConfig   `yaml:"triggers,omitempty"`
	Exclude   SnapshotExcludeConfig   `yaml:"exclude,omitempty"`
	Retention SnapshotRetentionConfig `yaml:"retention,omitempty"`
}

// SnapshotTriggerConfig configures when snapshots are created.
type SnapshotTriggerConfig struct {
	DisablePreRun        bool `yaml:"disable_pre_run,omitempty"`
	DisableGitCommits    bool `yaml:"disable_git_commits,omitempty"`
	DisableBuilds        bool `yaml:"disable_builds,omitempty"`
	DisableIdle          bool `yaml:"disable_idle,omitempty"`
	IdleThresholdSeconds int  `yaml:"idle_threshold_seconds,omitempty"`
}

// SnapshotExcludeConfig configures what to exclude from snapshots.
type SnapshotExcludeConfig struct {
	IgnoreGitignore bool     `yaml:"ignore_gitignore,omitempty"`
	Additional      []string `yaml:"additional,omitempty"`
}

// SnapshotRetentionConfig configures snapshot retention.
type SnapshotRetentionConfig struct {
	MaxCount      int  `yaml:"max_count,omitempty"`
	DeleteInitial bool `yaml:"delete_initial,omitempty"`
}

// TracingConfig configures execution tracing.
type TracingConfig struct {
	DisableExec bool `yaml:"disable_exec,omitempty"`
}

// HooksConfig configures lifecycle hooks that run at different stages.
type HooksConfig struct {
	// PostBuild runs as the container user (moatuser) during image build,
	// after all dependencies are installed. Baked into image layers and cached.
	// Use for user-level image setup like configuring git defaults.
	PostBuild string `yaml:"post_build,omitempty"`

	// PostBuildRoot runs as root during image build, after all dependencies
	// are installed. Baked into image layers and cached.
	// Use for system-level setup like installing packages or kernel tuning.
	PostBuildRoot string `yaml:"post_build_root,omitempty"`

	// PreRun runs as the container user (moatuser) in /workspace on every
	// container start, before the main command. Use for workspace-level
	// setup that needs project files (e.g., "npm install").
	PreRun string `yaml:"pre_run,omitempty"`
}

// ShouldSyncClaudeLogs returns true if Claude session logs should be synced.
// The logic is:
// - If claude.sync_logs is explicitly set, use that value
// - Otherwise, enable sync_logs if "anthropic" is in grants (Claude Code integration)
func (c *Config) ShouldSyncClaudeLogs() bool {
	if c.Claude.SyncLogs != nil {
		return *c.Claude.SyncLogs
	}
	// Default: enable if anthropic grant is configured
	for _, grant := range c.Grants {
		if grant == "anthropic" || strings.HasPrefix(grant, "anthropic:") {
			return true
		}
	}
	return false
}

// ShouldSyncCodexLogs returns true if Codex session logs should be synced.
// The logic is:
// - If codex.sync_logs is explicitly set, use that value
// - Otherwise, enable sync_logs if "openai" is in grants (Codex integration)
func (c *Config) ShouldSyncCodexLogs() bool {
	if c.Codex.SyncLogs != nil {
		return *c.Codex.SyncLogs
	}
	// Default: enable if openai grant is configured
	for _, grant := range c.Grants {
		if grant == "openai" || strings.HasPrefix(grant, "openai:") {
			return true
		}
	}
	return false
}

// ShouldSyncGeminiLogs returns true if Gemini session logs should be synced.
// The logic is:
// - If gemini.sync_logs is explicitly set, use that value
// - Otherwise, enable sync_logs if "gemini" is in grants (Gemini integration)
func (c *Config) ShouldSyncGeminiLogs() bool {
	if c.Gemini.SyncLogs != nil {
		return *c.Gemini.SyncLogs
	}
	// Default: enable if gemini grant is configured
	for _, grant := range c.Grants {
		if grant == "gemini" || strings.HasPrefix(grant, "gemini:") {
			return true
		}
	}
	return false
}

// deprecatedRuntime is kept only to detect and reject old configs.
type deprecatedRuntime struct {
	Node   string `yaml:"node,omitempty"`
	Python string `yaml:"python,omitempty"`
	Go     string `yaml:"go,omitempty"`
}

// Load reads agent.yaml from the given directory.
// Returns nil, nil if the file doesn't exist.
func Load(dir string) (*Config, error) {
	path := filepath.Join(dir, "agent.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("reading agent.yaml: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing agent.yaml: %w", err)
	}

	// Validate runtime field (only "docker" or "apple" allowed)
	if cfg.Runtime != "" && cfg.Runtime != "docker" && cfg.Runtime != "apple" {
		return nil, fmt.Errorf("invalid runtime %q: must be 'docker' or 'apple'", cfg.Runtime)
	}

	// Validate container resource limits
	if cfg.Container.Memory < 0 {
		return nil, fmt.Errorf("container.memory must be non-negative, got %d", cfg.Container.Memory)
	}
	if cfg.Container.Memory > 0 && cfg.Container.Memory < 128 {
		return nil, fmt.Errorf("container.memory must be at least 128 MB, got %d MB", cfg.Container.Memory)
	}
	if cfg.Container.CPUs < 0 {
		return nil, fmt.Errorf("container.cpus must be non-negative, got %d", cfg.Container.CPUs)
	}

	// Set default network policy if not specified
	if cfg.Network.Policy == "" {
		cfg.Network.Policy = "permissive"
	}

	// Validate network policy
	if cfg.Network.Policy != "permissive" && cfg.Network.Policy != "strict" {
		return nil, fmt.Errorf("invalid network policy %q: must be 'permissive' or 'strict'", cfg.Network.Policy)
	}

	// Validate sandbox setting
	if cfg.Sandbox != "" && cfg.Sandbox != "none" {
		return nil, fmt.Errorf("invalid sandbox value %q: must be empty (default) or 'none'", cfg.Sandbox)
	}

	// Check for overlapping env and secrets keys
	for key := range cfg.Secrets {
		if _, exists := cfg.Env[key]; exists {
			return nil, fmt.Errorf("key %q defined in both 'env' and 'secrets' - use one or the other", key)
		}
	}

	// Validate secret references have valid URI format
	for key, ref := range cfg.Secrets {
		if !strings.Contains(ref, "://") {
			return nil, fmt.Errorf("secret %q has invalid reference %q: missing scheme (expected format: scheme://path, e.g., op://vault/item/field)", key, ref)
		}
	}

	// Validate command if specified
	if len(cfg.Command) > 0 && cfg.Command[0] == "" {
		return nil, fmt.Errorf("command[0] cannot be empty: the first element must be the executable")
	}

	// Validate Claude marketplace specs
	for name, spec := range cfg.Claude.Marketplaces {
		if err := validateMarketplaceSpec(name, spec); err != nil {
			return nil, err
		}
	}

	// Validate Claude MCP server specs
	for name, spec := range cfg.Claude.MCP {
		if err := validateMCPServerSpec("claude", name, spec); err != nil {
			return nil, err
		}
	}

	// Validate Codex MCP server specs
	for name, spec := range cfg.Codex.MCP {
		if err := validateMCPServerSpec("codex", name, spec); err != nil {
			return nil, err
		}
	}

	// Validate Gemini MCP server specs
	for name, spec := range cfg.Gemini.MCP {
		if err := validateMCPServerSpec("gemini", name, spec); err != nil {
			return nil, err
		}
	}

	// Validate top-level MCP server specs
	seenNames := make(map[string]bool)
	for i, spec := range cfg.MCP {
		if err := validateTopLevelMCPServerSpec(i, spec, seenNames); err != nil {
			return nil, err
		}
	}

	// Validate volumes
	if len(cfg.Volumes) > 0 {
		if cfg.Name == "" {
			return nil, fmt.Errorf("'name' is required when volumes are configured (volumes are scoped by agent name)")
		}
		seenVolNames := make(map[string]bool)
		seenVolTargets := make(map[string]bool)
		for i, vol := range cfg.Volumes {
			prefix := fmt.Sprintf("volumes[%d]", i)
			if vol.Name == "" {
				return nil, fmt.Errorf("%s: 'name' is required", prefix)
			}
			if !volumeNameRe.MatchString(vol.Name) {
				return nil, fmt.Errorf("%s: invalid name %q (must match [a-z0-9][a-z0-9_-]*)", prefix, vol.Name)
			}
			if vol.Target == "" {
				return nil, fmt.Errorf("%s: 'target' is required", prefix)
			}
			if !filepath.IsAbs(vol.Target) {
				return nil, fmt.Errorf("%s: 'target' must be an absolute path, got %q", prefix, vol.Target)
			}
			if seenVolNames[vol.Name] {
				return nil, fmt.Errorf("%s: duplicate volume name %q", prefix, vol.Name)
			}
			seenVolNames[vol.Name] = true
			if seenVolTargets[vol.Target] {
				return nil, fmt.Errorf("%s: duplicate volume target %q", prefix, vol.Target)
			}
			seenVolTargets[vol.Target] = true
		}
	}

	// Snapshot defaults
	if cfg.Snapshots.Triggers.IdleThresholdSeconds == 0 {
		cfg.Snapshots.Triggers.IdleThresholdSeconds = 30
	}
	if cfg.Snapshots.Retention.MaxCount == 0 {
		cfg.Snapshots.Retention.MaxCount = 10
	}

	return &cfg, nil
}

// validateMarketplaceSpec validates a marketplace specification.
func validateMarketplaceSpec(name string, spec MarketplaceSpec) error {
	switch spec.Source {
	case "github":
		if spec.Repo == "" {
			return fmt.Errorf("claude.marketplaces.%s: 'repo' is required for github source (format: owner/repo)", name)
		}
		if !strings.Contains(spec.Repo, "/") {
			return fmt.Errorf("claude.marketplaces.%s: 'repo' must be in owner/repo format, got %q", name, spec.Repo)
		}
	case "git":
		if spec.URL == "" {
			return fmt.Errorf("claude.marketplaces.%s: 'url' is required for git source", name)
		}
	case "directory":
		if spec.Path == "" {
			return fmt.Errorf("claude.marketplaces.%s: 'path' is required for directory source", name)
		}
	case "":
		return fmt.Errorf("claude.marketplaces.%s: 'source' is required (must be 'github', 'git', or 'directory')", name)
	default:
		return fmt.Errorf("claude.marketplaces.%s: invalid source %q (must be 'github', 'git', or 'directory')", name, spec.Source)
	}
	return nil
}

// validateMCPServerSpec validates an MCP server specification.
// The section parameter is "claude" or "codex" for error messages.
func validateMCPServerSpec(section, name string, spec MCPServerSpec) error {
	if spec.Command == "" {
		return fmt.Errorf("%s.mcp.%s: 'command' is required", section, name)
	}
	return nil
}

// validateTopLevelMCPServerSpec validates a top-level MCP server specification.
func validateTopLevelMCPServerSpec(index int, spec MCPServerConfig, seenNames map[string]bool) error {
	prefix := fmt.Sprintf("mcp[%d]", index)

	if spec.Name == "" {
		return fmt.Errorf("%s: 'name' is required", prefix)
	}

	if seenNames[spec.Name] {
		return fmt.Errorf("%s: duplicate name '%s'", prefix, spec.Name)
	}
	seenNames[spec.Name] = true

	if spec.URL == "" {
		return fmt.Errorf("%s: 'url' is required", prefix)
	}

	if !strings.HasPrefix(spec.URL, "https://") {
		return fmt.Errorf("%s: 'url' must use HTTPS", prefix)
	}

	if spec.Auth != nil {
		if spec.Auth.Grant == "" {
			return fmt.Errorf("%s: 'auth.grant' is required when auth is specified", prefix)
		}
		if spec.Auth.Header == "" {
			return fmt.Errorf("%s: 'auth.header' is required when auth is specified", prefix)
		}
	}

	return nil
}

// DefaultConfig returns a default configuration.
func DefaultConfig() *Config {
	return &Config{
		Env: make(map[string]string),
		Network: NetworkConfig{
			Policy: "permissive",
		},
		Snapshots: SnapshotConfig{
			Triggers: SnapshotTriggerConfig{
				IdleThresholdSeconds: 30,
			},
			Retention: SnapshotRetentionConfig{
				MaxCount: 10,
			},
		},
	}
}
