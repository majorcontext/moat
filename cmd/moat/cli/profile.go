package cli

import (
	"fmt"

	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

func newProfileCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "profile", Short: "Manage credential profiles"}
	var parent string
	var inheritDefault bool
	create := &cobra.Command{
		Use:     "create <name>",
		Short:   "Create a credential profile, optionally inheriting another profile",
		Example: "  moat profile create client --inherit-default\n  moat profile create client --inherit work\n  moat grant oauth sentry --profile client",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("inherit") && parent == "" {
				return fmt.Errorf("--inherit requires a profile name")
			}
			if err := config.CreateProfile(args[0], config.ProfileConfig{Inherit: parent, InheritDefault: inheritDefault}); err != nil {
				return err
			}
			cmd.Printf("Created profile %s", args[0])
			if inheritDefault {
				cmd.Print(" (inherits default store)")
			} else if parent != "" {
				cmd.Printf(" (inherits %s)", parent)
			}
			cmd.Println()
			return nil
		},
	}
	create.Flags().StringVar(&parent, "inherit", "", "inherit grants and environment variables from a named profile")
	create.Flags().BoolVar(&inheritDefault, "inherit-default", false, "inherit grants from the default credential store")
	create.MarkFlagsMutuallyExclusive("inherit", "inherit-default")
	cmd.AddCommand(create)
	return cmd
}

func init() { rootCmd.AddCommand(newProfileCommand()) }
