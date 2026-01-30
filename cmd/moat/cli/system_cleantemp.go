package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/majorcontext/moat/internal/system"
)

var cleanTempCmd = &cobra.Command{
	Use:   "clean-temp",
	Short: "Clean up orphaned temporary directories",
	Long: `Scan for and remove orphaned moat temporary directories in /tmp.

Moat creates temporary directories for AWS credentials, Claude config, and Codex config.
These are normally cleaned up when a run completes, but may accumulate if moat crashes.

This command finds and removes temporary directories that:
  - Match moat's temp directory patterns
  - Are older than the specified age (default: 1 hour)

Patterns scanned:
  - agentops-aws-*           (AWS credential helpers)
  - moat-claude-staging-*    (Claude configuration)
  - moat-codex-staging-*     (Codex configuration)`,
	RunE: runCleanTemp,
}

var (
	cleanTempMinAge time.Duration
	cleanTempForce  bool
	cleanTempDryRun bool
)

func init() {
	systemCmd.AddCommand(cleanTempCmd)
	cleanTempCmd.Flags().DurationVar(&cleanTempMinAge, "min-age", 1*time.Hour,
		"Minimum age of temp directories to clean (e.g., 1h, 24h, 168h)")
	cleanTempCmd.Flags().BoolVarP(&cleanTempForce, "force", "f", false,
		"Skip confirmation prompt")
	cleanTempCmd.Flags().BoolVar(&cleanTempDryRun, "dry-run", false,
		"Show what would be cleaned without removing anything")
}

func runCleanTemp(cmd *cobra.Command, args []string) error {
	// Find orphaned temp directories
	orphaned, err := system.FindOrphanedTempDirs(cleanTempMinAge)
	if err != nil {
		return fmt.Errorf("scanning for orphaned temp directories: %w", err)
	}

	if len(orphaned) == 0 {
		fmt.Println("No orphaned temporary directories found.")
		return nil
	}

	// Display found directories
	fmt.Printf("Found %d orphaned temporary director%s:\n\n",
		len(orphaned), plural(len(orphaned), "y", "ies"))

	var totalSize int64
	for _, dir := range orphaned {
		totalSize += dir.Size
		age := time.Since(dir.ModTime)
		fmt.Printf("  %s\n", dir.Path)
		fmt.Printf("    Pattern: %s (%s)\n", dir.Pattern, dir.Description)
		fmt.Printf("    Age: %s  Size: %s\n", formatDuration(age), system.FormatSize(dir.Size))
		fmt.Println()
	}

	fmt.Printf("Total size: %s\n\n", system.FormatSize(totalSize))

	if cleanTempDryRun {
		fmt.Println("Dry run mode - nothing was removed.")
		return nil
	}

	// Confirm deletion unless --force
	if !cleanTempForce {
		fmt.Print("Remove these directories? [y/N]: ")
		reader := bufio.NewReader(os.Stdin)
		response, _ := reader.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response != "y" && response != "yes" {
			fmt.Println("Canceled.")
			return nil
		}
		fmt.Println()
	}

	// Remove directories (with age re-verification to prevent race conditions)
	if err := system.CleanOrphanedTempDirs(orphaned, cleanTempMinAge); err != nil {
		return err
	}

	fmt.Printf("Successfully removed %d temporary director%s.\n",
		len(orphaned), plural(len(orphaned), "y", "ies"))

	return nil
}

func plural(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%.0fs", d.Seconds())
	}
	if d < time.Hour {
		return fmt.Sprintf("%.0fm", d.Minutes())
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%.1fh", d.Hours())
	}
	days := d.Hours() / 24
	return fmt.Sprintf("%.0fd", days)
}
