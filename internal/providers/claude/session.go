package claude

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// uuidPattern matches a lowercase UUID (e.g., "b281f735-7d2b-4979-95de-0e2a7a9c2315").
// Used by both session extraction (JSONL filenames) and --resume argument validation.
var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// OnRunStopped extracts the Claude session ID from the projects directory
// after the container exits. It implements provider.RunStoppedHook.
func (p *OAuthProvider) OnRunStopped(ctx provider.RunStoppedContext) map[string]string {
	if ctx.Workspace == "" {
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return nil
	}

	claudeDir := WorkspaceToClaudeDir(ctx.Workspace)
	projectsDir := filepath.Join(homeDir, ".claude", "projects", claudeDir)

	sessionID := findLatestSessionID(projectsDir, ctx.StartedAt)
	if sessionID == "" {
		return nil
	}

	log.Debug("extracted claude session ID from projects dir",
		"sessionID", sessionID, "dir", projectsDir)

	return map[string]string{"claude_session_id": sessionID}
}

// findLatestSessionID scans a Claude projects directory for the most recently
// modified <uuid>.jsonl file that was modified at or after startedAt.
// Returns empty string if none found.
func findLatestSessionID(projectsDir string, startedAt time.Time) string {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return ""
	}

	var bestID string
	var bestTime time.Time

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		stem := strings.TrimSuffix(name, ".jsonl")
		if !uuidPattern.MatchString(stem) {
			continue
		}

		info, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}

		modTime := info.ModTime()
		// Only consider files modified during or after the run started.
		if !startedAt.IsZero() && modTime.Before(startedAt) {
			continue
		}
		if modTime.After(bestTime) {
			bestTime = modTime
			bestID = stem
		}
	}

	return bestID
}

// Ensure OAuthProvider implements RunStoppedHook.
var _ provider.RunStoppedHook = (*OAuthProvider)(nil)
