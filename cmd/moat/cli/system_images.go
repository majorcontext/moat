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

var systemImagesCmd = &cobra.Command{
	Use:   "images",
	Short: "List moat-managed images",
	Long: `List all container images created by moat.

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

	pool, err := container.NewRuntimePool(container.RuntimeOptions{Sandbox: false})
	if err != nil {
		return fmt.Errorf("initializing runtime: %w", err)
	}
	defer pool.Close()

	type runtimeImage struct {
		image  container.ImageInfo
		rtType container.RuntimeType
	}
	var all []runtimeImage
	pool.ForEachAvailable(func(rt container.Runtime) error {
		imgs, err := rt.ListImages(ctx)
		if err != nil {
			ui.Warnf("Failed to list %s images: %v", rt.Type(), err)
			return nil
		}
		for _, img := range imgs {
			all = append(all, runtimeImage{image: img, rtType: rt.Type()})
		}
		return nil
	})

	if jsonOut {
		images := make([]container.ImageInfo, len(all))
		for i, ri := range all {
			images[i] = ri.image
		}
		return json.NewEncoder(os.Stdout).Encode(images)
	}

	if len(all) == 0 {
		fmt.Println("No moat images found")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "IMAGE ID\tTAG\tRUNTIME\tSIZE\tCREATED")
	for _, ri := range all {
		img := ri.image
		id := img.ID
		if len(id) > 12 {
			id = id[:12]
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d MB\t%s\n",
			id, img.Tag, ri.rtType, img.Size/(1024*1024), formatAge(img.Created))
	}
	w.Flush()

	fmt.Println()
	fmt.Println("To remove an image:")
	fmt.Println("  docker rmi <image-id>            (Docker)")
	fmt.Println("  container image rm <image-id>    (Apple)")

	return nil
}
