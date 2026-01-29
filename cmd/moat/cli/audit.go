package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/spf13/cobra"
)

var auditExportFile string

var auditCmd = &cobra.Command{
	Use:   "audit <run-id>",
	Short: "Verify the integrity of a run's audit logs",
	Long: `Verify the cryptographic integrity of a run's audit logs.

Checks:
  - Hash chain: All entries are properly linked
  - Signatures: All attestations have valid signatures

Example:
  moat audit run_a1b2c3d4e5f6`,
	Args: cobra.ExactArgs(1),
	RunE: runAudit,
}

var verifyBundleCmd = &cobra.Command{
	Use:   "verify-bundle <file>",
	Short: "Verify a proof bundle file",
	Long: `Verifies the integrity of an exported proof bundle without the original database.

This allows offline verification of audit logs that were exported using 'agent audit --export'.

Example:
  moat verify-bundle ./run_a1b2c3d4e5f6.proof.json`,
	Args: cobra.ExactArgs(1),
	RunE: runVerifyBundle,
}

func init() {
	rootCmd.AddCommand(auditCmd)
	rootCmd.AddCommand(verifyBundleCmd)
	auditCmd.Flags().StringVarP(&auditExportFile, "export", "e", "", "Export proof bundle to file (JSON)")
}

func runAudit(cmd *cobra.Command, args []string) error {
	runID := args[0]

	// Find run directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("finding home directory: %w", err)
	}
	runDir := filepath.Join(homeDir, ".moat", "runs", runID)
	dbPath := filepath.Join(runDir, "audit.db")

	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		return fmt.Errorf("run not found: %s", runID)
	}

	auditor, err := audit.NewAuditor(dbPath)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditor.Close()

	// Export if requested
	if auditExportFile != "" {
		bundle, exportErr := exportBundle(dbPath)
		if exportErr != nil {
			return fmt.Errorf("exporting bundle: %w", exportErr)
		}

		data, marshalErr := json.MarshalIndent(bundle, "", "  ")
		if marshalErr != nil {
			return fmt.Errorf("marshaling bundle: %w", marshalErr)
		}

		if writeErr := os.WriteFile(auditExportFile, data, 0644); writeErr != nil {
			return fmt.Errorf("writing bundle: %w", writeErr)
		}

		fmt.Printf("Proof bundle exported to: %s\n", auditExportFile)
	}

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
	fmt.Println("External Attestations (Sigstore/Rekor)")
	if result.RekorProofCount == 0 {
		fmt.Println("  - No Rekor proofs found")
	} else {
		fmt.Printf("  [info] %d entries anchored to Rekor (not verified - offline mode)\n", result.RekorProofCount)
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

func exportBundle(dbPath string) (*audit.ProofBundle, error) {
	store, err := audit.OpenStore(dbPath)
	if err != nil {
		return nil, err
	}
	defer store.Close()

	return store.Export()
}

func runVerifyBundle(cmd *cobra.Command, args []string) error {
	bundlePath := args[0]

	data, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("reading bundle: %w", err)
	}

	var bundle audit.ProofBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return fmt.Errorf("parsing bundle: %w", err)
	}

	result := bundle.Verify()

	// JSON output mode
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(result)
	}

	// Human-readable output
	fmt.Println("Proof Bundle Verification")
	fmt.Println("===============================================================")
	fmt.Printf("Bundle Version: %d\n", bundle.Version)
	fmt.Printf("Created: %s\n", bundle.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("Entries: %d\n", result.EntryCount)
	fmt.Println()

	fmt.Println("Log Integrity")
	if result.HashChainValid {
		fmt.Printf("  [ok] Hash chain: %d entries verified\n", result.EntryCount)
		if len(bundle.LastHash) >= 16 {
			fmt.Printf("  [ok] Last hash: %s...\n", bundle.LastHash[:16])
		} else if bundle.LastHash != "" {
			fmt.Printf("  [ok] Last hash: %s\n", bundle.LastHash)
		}
	} else {
		fmt.Println("  [FAIL] Hash chain: INVALID")
	}

	fmt.Println()
	fmt.Println("Local Signatures")
	if result.AttestationCount == 0 {
		fmt.Println("  - No attestations in bundle")
	} else if result.AttestationsValid {
		fmt.Printf("  [ok] %d attestation(s) verified\n", result.AttestationCount)
	} else {
		fmt.Println("  [FAIL] Attestation signatures: INVALID")
	}

	fmt.Println()
	fmt.Println("External Attestations (Sigstore/Rekor)")
	if result.RekorProofCount == 0 {
		fmt.Println("  - No Rekor proofs in bundle")
	} else {
		fmt.Printf("  [info] %d Rekor proof(s) included (not verified - offline mode)\n", result.RekorProofCount)
	}

	fmt.Println()
	fmt.Println("===============================================================")
	if result.Valid {
		fmt.Println("VERDICT: [ok] VALID")
		return nil
	}

	fmt.Printf("VERDICT: [FAIL] TAMPERED - %s\n", result.Error)
	return fmt.Errorf("bundle verification failed")
}
