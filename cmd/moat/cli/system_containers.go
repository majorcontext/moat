package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/andybons/moat/internal/container"
	"github.com/spf13/cobra"
)

var systemContainersCmd = &cobra.Command{
	Use:   "containers",
	Short: "List moat-managed containers",
	Long: `List all containers created by moat.

This is an escape hatch for debugging. Use 'agent status' for normal operations.

To remove a container, use the native container CLI:
  docker rm <container-id>
  container rm <container-id>`,
	RunE: listSystemContainers,
}

func init() {
	systemCmd.AddCommand(systemContainersCmd)
}

func listSystemContainers(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	// No sandbox needed for listing containers
	rt, err := container.NewRuntimeWithOptions(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	containers, err := rt.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("listing containers: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(containers)
	}

	if len(containers) == 0 {
		fmt.Println("No moat containers found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tSTATUS\tCREATED")
	for _, c := range containers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
			c.ID, c.Name, c.Status, formatAge(c.Created))
	}
	w.Flush()

	fmt.Println()
	if rt.Type() == container.RuntimeDocker {
		fmt.Println("To remove a container: docker rm <container-id>")
		fmt.Println("To view logs: docker logs <container-id>")
	} else {
		fmt.Println("To remove a container: container rm <container-id>")
		fmt.Println("To view logs: container logs <container-id>")
	}

	return nil
}
