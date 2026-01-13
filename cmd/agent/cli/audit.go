package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andybons/agentops/internal/audit"
	"github.com/spf13/cobra"
)

var auditCmd = &cobra.Command{
	Use:   "audit <run-id>",
	Short: "Verify the integrity of a run's audit logs",
	Long: `Verify the cryptographic integrity of a run's audit logs.

Checks:
  - Hash chain: All entries are properly linked
  - Merkle tree: Root matches computed root from entries
  - Signatures: All attestations have valid signatures

Example:
  agent audit run-abc123def456`,
	Args: cobra.ExactArgs(1),
	RunE: runAudit,
}

func init() {
	rootCmd.AddCommand(auditCmd)
}

func runAudit(cmd *cobra.Command, args []string) error {
	runID := args[0]

	// Find run directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	runDir := filepath.Join(homeDir, ".agentops", "runs", runID)
	dbPath := filepath.Join(runDir, "logs.db")

	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		return fmt.Errorf("run not found: %s", runID)
	}

	auditor, err := audit.NewAuditor(dbPath)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditor.Close()

	result, err := auditor.Verify()
	if err != nil {
		return fmt.Errorf("verification error: %w", err)
	}

	// JSON output mode
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	// Human-readable output
	fmt.Printf("Auditing run: %s\n", runID)
	fmt.Println("===============================================================")
	fmt.Println()

	fmt.Println("Log Integrity")
	if result.HashChainValid {
		fmt.Printf("  [ok] Hash chain: %d entries, no gaps, all hashes valid\n", result.EntryCount)
	} else {
		fmt.Printf("  [FAIL] Hash chain: INVALID\n")
	}

	if result.MerkleRootValid {
		fmt.Println("  [ok] Merkle tree: root matches computed root")
	} else {
		fmt.Println("  [FAIL] Merkle tree: INVALID")
	}

	fmt.Println()
	fmt.Println("Local Signatures")
	if result.AttestationCount == 0 {
		fmt.Println("  - No attestations found")
	} else if result.AttestationsValid {
		fmt.Printf("  [ok] %d attestations, all signatures valid\n", result.AttestationCount)
	} else {
		fmt.Printf("  [FAIL] Attestations: INVALID\n")
	}

	fmt.Println()
	fmt.Println("===============================================================")

	if result.Valid {
		fmt.Println("VERDICT: [ok] INTACT - No tampering detected")
		return nil
	}

	fmt.Printf("VERDICT: [FAIL] TAMPERED - %s\n", result.Error)
	// Return error so Cobra exits with code 1
	return fmt.Errorf("tampering detected")
}
