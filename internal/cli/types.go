package cli

import (
	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

// ExecFlags holds the common flags for container execution commands.
// These are shared between `moat run`, `moat claude`, and future tool commands.
type ExecFlags struct {
	Grants        []string
	Env           []string
	Name          string
	Runtime       string
	Rebuild       bool
	KeepContainer bool
	Detach        bool
	Interactive   bool
	NoSandbox     bool
	TTYTrace      string // Path to save terminal I/O trace for debugging
}

// AddExecFlags adds the common execution flags to a command.
func AddExecFlags(cmd *cobra.Command, flags *ExecFlags) {
	cmd.Flags().StringSliceVarP(&flags.Grants, "grant", "g", nil, "capabilities to grant (e.g., github, aws:s3.read)")
	cmd.Flags().StringArrayVarP(&flags.Env, "env", "e", nil, "environment variables (KEY=VALUE)")
	cmd.Flags().StringVarP(&flags.Name, "name", "n", "", "name for this run (default: from agent.yaml or random)")
	cmd.Flags().BoolVar(&flags.Rebuild, "rebuild", false, "force rebuild of container image")
	cmd.Flags().BoolVar(&flags.KeepContainer, "keep", false, "keep container after run completes (for debugging)")
	cmd.Flags().BoolVarP(&flags.Detach, "detach", "d", false, "run in background and return immediately")
	cmd.Flags().StringVar(&flags.Runtime, "runtime", "", "container runtime to use (apple, docker)")
	cmd.Flags().BoolVar(&flags.NoSandbox, "no-sandbox", false, "disable gVisor sandbox (reduced isolation, Docker only)")
	cmd.Flags().StringVar(&flags.TTYTrace, "tty-trace", "", "capture terminal I/O to file for debugging (e.g., session.json)")
}

// RunInfo contains minimal information about a run, extracted to avoid import cycles.
// This is passed to callbacks instead of the full *run.Run type.
type RunInfo struct {
	ID   string
	Name string
}

// ExecOptions contains all the options needed to execute a containerized command.
type ExecOptions struct {
	// From flags
	Flags ExecFlags

	// Command-specific
	Workspace   string
	Command     []string
	Config      *config.Config
	Interactive bool // Can be set by flags or command logic
	TTY         bool

	// Callbacks for command-specific behavior
	// OnRunCreated is called after run is created, before start.
	// The RunInfo parameter contains the run's ID and Name.
	OnRunCreated func(info RunInfo)
}

// ExecResult contains the result of executing a run.
// This is returned instead of *run.Run to avoid import cycles.
type ExecResult struct {
	ID   string
	Name string
}
