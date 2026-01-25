package claude

import (
	"os"
)

// GeneratedConfig holds the paths to generated configuration files.
type GeneratedConfig struct {
	// StagingDir is the directory containing all files to copy to ~/.claude at container startup.
	// This directory is mounted to /moat/claude-init and copied by moat-init script.
	StagingDir string

	// TempDir is the temporary directory holding generated files
	TempDir string
}

// Cleanup removes all generated temporary files.
func (g *GeneratedConfig) Cleanup() error {
	if g.TempDir != "" {
		return os.RemoveAll(g.TempDir)
	}
	return nil
}

// ClaudeInitMountPath is the path where the Claude staging directory is mounted.
// The moat-init script reads from this path and copies files to ~/.claude.
const ClaudeInitMountPath = "/moat/claude-init"

// ClaudePluginsPath is the base path for Claude plugins in the container.
// This matches Claude Code's expected location at ~/.claude/plugins.
// We use the absolute path for moatuser since that's our standard container user.
const ClaudePluginsPath = "/home/moatuser/.claude/plugins"

// ClaudeMarketplacesPath is the path where marketplaces are mounted in the container.
const ClaudeMarketplacesPath = ClaudePluginsPath + "/marketplaces"
