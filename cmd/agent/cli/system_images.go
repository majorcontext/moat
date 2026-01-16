package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/andybons/agentops/internal/container"
	"github.com/spf13/cobra"
)

var systemImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List agentops-managed images",
	Long: `List all container images created by agentops.

This is an escape hatch for debugging. Use 'agent status' for normal operations.

To remove an image, use the native container CLI:
  docker rmi <image-id>
  container image rm <image-id>`,
	RunE: listSystemImages,
}

func init() {
	systemCmd.AddCommand(systemImagesCmd)
}

func listSystemImages(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	rt, err := container.NewRuntime()
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer rt.Close()

	images, err := rt.ListImages(ctx)
	if err != nil {
		return fmt.Errorf("listing images: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(images)
	}

	if len(images) == 0 {
		fmt.Println("No agentops images found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE ID\tTAG\tSIZE\tCREATED")
	for _, img := range images {
		id := img.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%d MB\t%s\n",
			id, img.Tag, img.Size/(1024*1024), formatAge(img.Created))
	}
	w.Flush()

	fmt.Println()
	if rt.Type() == container.RuntimeDocker {
		fmt.Println("To remove an image: docker rmi <image-id>")
	} else {
		fmt.Println("To remove an image: container image rm <image-id>")
	}

	return nil
}
