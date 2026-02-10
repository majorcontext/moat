package cli

import (
	"fmt"
	"os"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/majorcontext/moat/internal/worktree"
)

// WorktreeResult holds the outcome of ResolveWorktreeWorkspace.
// It bundles the resolved workspace path and updated config together
// so callers don't need to handle feedback, config reloading, or
// active run detection themselves.
type WorktreeResult struct {
	// Workspace is the absolute path to use (worktree path if --wt was set,
	// or the original workspace if not).
	Workspace string

	// Config is the config to use (reloaded from worktree if it has its own
	// agent.yaml, or the original config if not).
	Config *config.Config

	// Result is the worktree resolution metadata (nil if --wt was not set).
	Result *worktree.Result
}

// ResolveWorktreeWorkspace handles the --wt flag for provider commands.
// If wtBranch is empty, returns the original workspace and config unchanged.
// Otherwise, resolves the worktree, checks for active runs, prints user
// feedback, reloads config from the worktree if available, and sets the
// run name on flags.
func ResolveWorktreeWorkspace(wtBranch, workspace string, flags *ExecFlags, cfg *config.Config) (*WorktreeResult, error) {
	if wtBranch == "" {
		return &WorktreeResult{Workspace: workspace, Config: cfg}, nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getting current directory: %w", err)
	}

	repoRoot, err := worktree.FindRepoRoot(cwd)
	if err != nil {
		return nil, fmt.Errorf("--wt requires a git repository: %w", err)
	}

	repoID, err := worktree.ResolveRepoID(repoRoot)
	if err != nil {
		return nil, fmt.Errorf("resolving repo identity: %w", err)
	}

	agentName := ""
	if cfg != nil {
		agentName = cfg.Name
	}

	result, err := worktree.Resolve(repoRoot, repoID, wtBranch, agentName)
	if err != nil {
		return nil, fmt.Errorf("resolving worktree: %w", err)
	}

	// Check for active run in this worktree (only possible if reused)
	if result.Reused && CheckWorktreeActive != nil {
		if name, id := CheckWorktreeActive(result.WorkspacePath); name != "" {
			return nil, fmt.Errorf("a run is already active in worktree for branch %q: %s (%s)\nStop it first with 'moat stop %s' or attach with 'moat attach %s'", wtBranch, name, id, id, id)
		}
	}

	// User feedback
	if result.Reused {
		ui.Infof("Using existing worktree at %s", result.WorkspacePath)
	} else {
		ui.Infof("Created worktree at %s", result.WorkspacePath)
	}

	// Reload config from worktree path if it has its own agent.yaml
	outCfg := cfg
	if wtCfg, loadErr := config.Load(result.WorkspacePath); loadErr == nil && wtCfg != nil {
		outCfg = wtCfg
	}

	if flags.Name == "" {
		flags.Name = result.RunName
	}

	return &WorktreeResult{
		Workspace: result.WorkspacePath,
		Config:    outCfg,
		Result:    result,
	}, nil
}

// SetWorktreeFields copies worktree metadata from a Result into ExecOptions.
func SetWorktreeFields(opts *ExecOptions, wt *worktree.Result) {
	if wt == nil {
		return
	}
	opts.WorktreeBranch = wt.Branch
	opts.WorktreePath = wt.WorkspacePath
	opts.WorktreeRepoID = wt.RepoID
}
