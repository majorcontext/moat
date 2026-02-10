package cli

import (
	"fmt"
	"os"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/worktree"
)

// ResolveWorktreeWorkspace handles the --wt flag for provider commands.
// If wtBranch is empty, returns the original workspace unchanged.
// Otherwise, resolves the worktree and returns the updated workspace path,
// sets the run name on flags if not already set, and returns worktree metadata.
func ResolveWorktreeWorkspace(wtBranch, workspace string, flags *ExecFlags, cfg *config.Config) (string, *worktree.Result, error) {
	if wtBranch == "" {
		return workspace, nil, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", nil, fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return "", nil, fmt.Errorf("--wt requires a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return "", nil, fmt.Errorf("resolving repo identity: %w", err)
	}

	agentName := ""
	if cfg != nil {
		agentName = cfg.Name
	}

	result, err := worktree.Resolve(repoRoot, repoID, wtBranch, agentName)
	if err != nil {
		return "", nil, fmt.Errorf("resolving worktree: %w", err)
	}

	if flags.Name == "" {
		flags.Name = result.RunName
	}

	return result.WorkspacePath, result, nil
}
