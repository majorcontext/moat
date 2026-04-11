package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/ui"
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

	pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer pool.Close()

	type runtimeContainer struct {
		info   container.Info
		rtType container.RuntimeType
	}
	var all []runtimeContainer
	pool.ForEachAvailable(func(rt container.Runtime) error {
		containers, err := rt.ListContainers(ctx)
		if err != nil {
			ui.Warnf("Failed to list %s containers: %v", rt.Type(), err)
			return nil
		}
		for _, c := range containers {
			all = append(all, runtimeContainer{info: c, rtType: rt.Type()})
		}
		return nil
	})

	if jsonOut {
		containers := make([]container.Info, len(all))
		for i, rc := range all {
			containers[i] = rc.info
		}
		return json.NewEncoder(os.Stdout).Encode(containers)
	}

	if len(all) == 0 {
		fmt.Println("No moat containers found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "CONTAINER ID\tNAME\tRUNTIME\tSTATUS\tCREATED")
	for _, rc := range all {
		c := rc.info
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
			c.ID, c.Name, rc.rtType, c.Status, formatAge(c.Created))
	}
	w.Flush()

	fmt.Println()
	fmt.Println("To remove a container:")
	fmt.Println("  docker rm <container-id>      (Docker)")
	fmt.Println("  container rm <container-id>    (Apple)")

	return nil
}
