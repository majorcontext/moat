package cli

import (
	"context"
	"fmt"

	intcli "github.com/majorcontext/moat/internal/cli"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/spf13/cobra"
)

var (
	resumeFlags  ExecFlags
	resumeNoYolo bool
)

var resumeCmd = &cobra.Command{
	Use:   "resume [workspace]",
	Short: "Resume the most recent Claude Code conversation",
	Long: `Resume the most recent Claude Code conversation in a new container.

This is a shortcut for 'moat claude --continue'. It starts a new container
with the same workspace and passes --continue to Claude Code, which picks up
the most recent conversation from the synced session logs.

Session history is preserved across runs because moat mounts the Claude
projects directory (~/.claude/projects/) between host and container. Claude
Code stores conversation logs as .jsonl files in this directory, so previous
sessions are always available for resumption.

Without a workspace argument, uses the current directory.

Examples:
  # Resume the most recent conversation
  moat resume

  # Resume in a specific project
  moat resume ./my-project

  # Resume with additional grants
  moat resume --grant github`,
	Args: cobra.MaximumNArgs(1),
	RunE: runResume,
}

func init() {
	rootCmd.AddCommand(resumeCmd)
	AddExecFlags(resumeCmd, &resumeFlags)
	resumeCmd.Flags().BoolVar(&resumeNoYolo, "noyolo", false, "disable --dangerously-skip-permissions")
}

func runResume(cmd *cobra.Command, args []string) error {
	workspace := "."
	if len(args) > 0 {
		workspace = args[0]
	}

	absPath, err := intcli.ResolveWorkspacePath(workspace)
	if err != nil {
		return err
	}

	// Load agent.yaml if present
	cfg, err := config.Load(absPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build grants list
	grantSet := make(map[string]bool)
	var grants []string
	addGrant := func(g string) {
		if !grantSet[g] {
			grantSet[g] = true
			grants = append(grants, g)
		}
	}

	if credName := getResumeCredentialName(); credName != "" {
		addGrant(credName)
	}
	if cfg != nil {
		for _, g := range cfg.Grants {
			addGrant(g)
		}
	}
	for _, g := range resumeFlags.Grants {
		addGrant(g)
	}
	resumeFlags.Grants = grants

	// Build container command with --continue
	containerCmd := []string{"claude"}
	if !resumeNoYolo {
		containerCmd = append(containerCmd, "--dangerously-skip-permissions")
	}
	containerCmd = append(containerCmd, "--continue")

	// Use name from flag, or config
	if resumeFlags.Name == "" && cfg != nil && cfg.Name != "" {
		resumeFlags.Name = cfg.Name
	}

	// Ensure dependencies
	if cfg == nil {
		cfg = &config.Config{}
	}
	if !intcli.HasDependency(cfg.Dependencies, "node") {
		cfg.Dependencies = append(cfg.Dependencies, "node@20")
	}
	if !intcli.HasDependency(cfg.Dependencies, "git") {
		cfg.Dependencies = append(cfg.Dependencies, "git")
	}
	if !intcli.HasDependency(cfg.Dependencies, "claude-code") {
		cfg.Dependencies = append(cfg.Dependencies, "claude-code")
	}

	// Always sync Claude logs (required for resume to work)
	syncLogs := true
	cfg.Claude.SyncLogs = &syncLogs

	// Allow network access to claude.ai
	cfg.Network.Allow = append(cfg.Network.Allow, "claude.ai", "*.claude.ai")

	// Add environment variables from flags
	if envErr := intcli.ParseEnvFlags(resumeFlags.Env, cfg); envErr != nil {
		return envErr
	}

	log.Debug("resuming claude code session",
		"workspace", absPath,
		"grants", grants,
	)

	if intcli.DryRun {
		fmt.Println("Dry run - would resume Claude Code session")
		fmt.Printf("Workspace: %s\n", absPath)
		fmt.Printf("Grants: %v\n", grants)
		return nil
	}

	ctx := context.Background()

	opts := intcli.ExecOptions{
		Flags:       resumeFlags,
		Workspace:   absPath,
		Command:     containerCmd,
		Config:      cfg,
		Interactive: true,
		TTY:         true,
	}

	result, err := intcli.ExecuteRun(ctx, opts)
	if err != nil {
		return err
	}

	if result != nil && !resumeFlags.Detach {
		fmt.Printf("Resuming Claude Code in %s\n", absPath)
		fmt.Printf("Run: %s (%s)\n", result.Name, result.ID)
	}

	return nil
}

// getResumeCredentialName returns the credential name for Claude, same as in the claude provider.
func getResumeCredentialName() string {
	for _, name := range []string{"claude", "anthropic"} {
		key, err := credential.DefaultEncryptionKey()
		if err != nil {
			continue
		}
		store, err := credential.NewFileStore(credential.DefaultStoreDir(), key)
		if err != nil {
			continue
		}
		if _, err := store.Get(credential.Provider(name)); err == nil {
			return name
		}
	}
	return ""
}
