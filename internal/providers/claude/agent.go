package claude

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/session"
)

// PrepareContainer sets up staging directories and config files for Claude Code.
// It creates the necessary files that will be copied into the container at startup.
//
// If opts.HostConfig is nil, this method reads the host's ~/.claude.json automatically.
func (p *Provider) PrepareContainer(ctx context.Context, opts provider.PrepareOpts) (*provider.ContainerConfig, error) {
	// Create a temporary staging directory
	tmpDir, err := os.MkdirTemp("", "moat-claude-*")
	if err != nil {
		return nil, fmt.Errorf("creating temp directory: %w", err)
	}

	// Ensure proper permissions
	if err := os.Chmod(tmpDir, 0700); err != nil {
		os.RemoveAll(tmpDir)
		return nil, fmt.Errorf("setting permissions on staging dir: %w", err)
	}

	success := false
	defer func() {
		if !success {
			os.RemoveAll(tmpDir)
		}
	}()

	// Write credentials file for OAuth tokens
	if opts.Credential != nil {
		if err := WriteCredentialsFile(opts.Credential, tmpDir); err != nil {
			return nil, fmt.Errorf("writing credentials file: %w", err)
		}
	}

	// Convert MCP servers to Claude's format
	var mcpServers map[string]MCPServerForContainer
	if len(opts.MCPServers) > 0 {
		mcpServers = make(map[string]MCPServerForContainer)
		for name, cfg := range opts.MCPServers {
			mcpServers[name] = MCPServerForContainer{
				Type:    "http",
				URL:     cfg.URL,
				Headers: cfg.Headers,
			}
		}
	}

	// Get host config - use provided or read from host's ~/.claude.json
	var hostConfig map[string]any
	if opts.HostConfig != nil {
		hostConfig = opts.HostConfig
	} else {
		// Read host config automatically
		if hostHome, err := os.UserHomeDir(); err == nil {
			hostConfig, _ = ReadHostConfig(filepath.Join(hostHome, ".claude.json"))
			// Ignore errors - missing host config is OK
		}
	}

	// Write .claude.json config
	if err := WriteClaudeConfig(tmpDir, mcpServers, hostConfig); err != nil {
		return nil, fmt.Errorf("writing claude config: %w", err)
	}

	// Build mounts
	mounts := []provider.MountConfig{
		{
			Source:   tmpDir,
			Target:   ClaudeInitMountPath,
			ReadOnly: true,
		},
	}

	// Build environment variables
	// Include credential env vars plus the init mount path for moat-init script
	env := p.ContainerEnv(opts.Credential)
	env = append(env, "MOAT_CLAUDE_INIT="+ClaudeInitMountPath)

	success = true
	return &provider.ContainerConfig{
		Env:        env,
		Mounts:     mounts,
		StagingDir: tmpDir,
		Cleanup: func() {
			os.RemoveAll(tmpDir)
		},
	}, nil
}

// Sessions returns all Claude Code sessions.
func (p *Provider) Sessions() ([]provider.Session, error) {
	mgr, err := newSessionManager()
	if err != nil {
		return nil, err
	}

	sessions, err := mgr.List()
	if err != nil {
		return nil, err
	}

	result := make([]provider.Session, len(sessions))
	for i, s := range sessions {
		result[i] = provider.Session{
			ID:        s.ID,
			Name:      s.Name,
			CreatedAt: s.CreatedAt,
			UpdatedAt: s.LastAccessedAt,
		}
	}

	return result, nil
}

// ResumeSession resumes an existing session by ID.
func (p *Provider) ResumeSession(id string) error {
	mgr, err := newSessionManager()
	if err != nil {
		return err
	}

	sess, err := mgr.Get(id)
	if err != nil {
		return fmt.Errorf("session not found: %s", id)
	}

	// Touch the session to update last accessed time
	if err := mgr.Touch(sess.ID); err != nil {
		// Log but don't fail - this is non-critical
		fmt.Fprintf(os.Stderr, "warning: failed to update session timestamp: %v\n", err)
	}

	return nil
}

// sessionManager wraps session.Manager for Claude Code sessions.
type sessionManager struct {
	*session.Manager
}

// newSessionManager creates a session manager for Claude Code sessions.
func newSessionManager() (*sessionManager, error) {
	dir, err := defaultSessionDir()
	if err != nil {
		return nil, err
	}
	return &sessionManager{Manager: session.NewManager(dir)}, nil
}

// defaultSessionDir returns the default Claude session storage directory.
func defaultSessionDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(homeDir, ".moat", "claude", "sessions"), nil
}
