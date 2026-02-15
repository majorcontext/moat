package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var volumesCmd = &cobra.Command{
	Use:   "volumes",
	Short: "Manage persistent volumes",
	Long: `Manage persistent volumes for moat runs.

Volumes store data that persists across runs for the same agent name.
They are created automatically when agent.yaml specifies a volumes: section.`,
}

var volumesLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List managed volumes",
	RunE:  listVolumes,
}

var (
	volumesRmForce bool
)

var volumesRmCmd = &cobra.Command{
	Use:   "rm <agent-name>",
	Short: "Remove volumes for an agent",
	Args:  cobra.ExactArgs(1),
	RunE:  removeVolumes,
}

var volumesPruneCmd = &cobra.Command{
	Use:   "prune",
	Short: "Remove orphaned volumes",
	Long: `Remove moat-managed volumes that are not in use by any running container.

Use --force to skip confirmation.`,
	RunE: pruneVolumes,
}

func init() {
	rootCmd.AddCommand(volumesCmd)
	volumesCmd.AddCommand(volumesLsCmd)
	volumesCmd.AddCommand(volumesRmCmd)
	volumesRmCmd.Flags().BoolVarP(&volumesRmForce, "force", "f", false, "skip confirmation prompt")
	volumesCmd.AddCommand(volumesPruneCmd)
	volumesPruneCmd.Flags().BoolVarP(&volumesRmForce, "force", "f", false, "skip confirmation prompt")
}

func listVolumes(cmd *cobra.Command, args []string) error {
	return listVolumeDirs()
}

func listVolumeDirs() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	volumesDir := homeDir + "/.moat/volumes"
	agents, err := os.ReadDir(volumesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No managed volumes.")
			return nil
		}
		return fmt.Errorf("reading volumes directory: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "VOLUME\tAGENT\n")
	for _, agent := range agents {
		if !agent.IsDir() {
			continue
		}
		vols, err := os.ReadDir(volumesDir + "/" + agent.Name())
		if err != nil {
			continue
		}
		for _, vol := range vols {
			if !vol.IsDir() {
				continue
			}
			fmt.Fprintf(w, "moat_%s_%s\t%s\n", agent.Name(), vol.Name(), agent.Name())
		}
	}
	return w.Flush()
}

func removeVolumes(cmd *cobra.Command, args []string) error {
	return removeVolumeDirs(args[0])
}

func removeVolumeDirs(agentName string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	agentDir := homeDir + "/.moat/volumes/" + agentName
	if _, err := os.Stat(agentDir); os.IsNotExist(err) {
		fmt.Printf("No volumes found for agent %q.\n", agentName)
		return nil
	}

	if !volumesRmForce && !dryRun {
		fmt.Printf("Remove all volumes for agent %q? [y/N]: ", agentName)
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Canceled")
			return nil
		}
	}

	if dryRun {
		fmt.Println("Dry run - no changes made")
		return nil
	}

	if err := os.RemoveAll(agentDir); err != nil {
		return fmt.Errorf("removing volume directory: %w", err)
	}
	fmt.Println(ui.Green("Removed volumes for agent " + agentName))
	return nil
}

func pruneVolumes(cmd *cobra.Command, args []string) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("getting home directory: %w", err)
	}

	volumesDir := homeDir + "/.moat/volumes"
	agents, err := os.ReadDir(volumesDir)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Println("No managed volumes to prune.")
			return nil
		}
		return fmt.Errorf("reading volumes directory: %w", err)
	}

	// Collect all agent directories that have volumes
	var agentNames []string
	for _, agent := range agents {
		if agent.IsDir() {
			agentNames = append(agentNames, agent.Name())
		}
	}

	if len(agentNames) == 0 {
		fmt.Println("No managed volumes to prune.")
		return nil
	}

	fmt.Printf("Found volumes for %d agent(s):\n", len(agentNames))
	for _, name := range agentNames {
		fmt.Printf("  %s\n", name)
	}

	if !volumesRmForce && !dryRun {
		fmt.Print("\nRemove all managed volumes? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Canceled")
			return nil
		}
	}

	if dryRun {
		fmt.Println("Dry run - no changes made")
		return nil
	}

	if err := os.RemoveAll(volumesDir); err != nil {
		return fmt.Errorf("removing volumes directory: %w", err)
	}
	fmt.Printf("Removed volumes for %d agent(s)\n", len(agentNames))
	return nil
}
