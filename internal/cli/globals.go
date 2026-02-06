package cli

import (
	"context"

	"github.com/spf13/cobra"
)

// Global state that providers need access to.
// These are set by cmd/moat/cli/root.go during initialization.
var (
	// DryRun is set by the --dry-run flag
	DryRun bool

	// RootCmd is the root cobra command, needed for providers to add subcommands
	RootCmd *cobra.Command

	// ExecuteRun is the function that executes a containerized command.
	// This is set by cmd/moat/cli to avoid import cycles.
	// It accepts ExecOptions and returns (*ExecResult, error).
	ExecuteRun func(ctx context.Context, opts ExecOptions) (*ExecResult, error)
)
