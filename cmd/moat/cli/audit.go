package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
	"github.com/spf13/cobra"
)

var auditExportFile string

var auditCmd = &cobra.Command{
	Use:   "audit <run>",
	Short: "Verify the integrity of a run's audit logs",
	Long: `Verify the cryptographic integrity of a run's audit logs.
Accepts a run ID or name.

Checks:
  - Hash chain: All entries are properly linked
  - Signatures: All attestations have valid signatures

Example:
  moat audit my-agent
  moat audit run_a1b2c3d4e5f6`,
	Args: cobra.ExactArgs(1),
	RunE: runAudit,
}

var verifyBundleCmd = &cobra.Command{
	Use:   "verify <file>",
	Short: "Verify a proof bundle file",
	Long: `Verifies the integrity of an exported proof bundle without the original database.

This allows offline verification of audit logs that were exported using 'moat audit --export'.

Example:
  moat audit verify ./run_a1b2c3d4e5f6.proof.json`,
	Args: cobra.ExactArgs(1),
	RunE: runVerifyBundle,
}

func init() {
	rootCmd.AddCommand(auditCmd)
	auditCmd.AddCommand(verifyBundleCmd)
	auditCmd.Flags().StringVarP(&auditExportFile, "export", "e", "", "Export proof bundle to file (JSON)")
}

func runAudit(cmd *cobra.Command, args []string) error {
	// Resolve argument to a single run
	manager, err := run.NewManager()
	if err != nil {
		return fmt.Errorf("creating run manager: %w", err)
	}
	defer manager.Close()

	runID, err := resolveRunArgSingle(manager, args[0])
	if err != nil {
		return err
	}

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
	fmt.Println(ui.Dim("───────────────────────────────────────────────────────────────"))
	fmt.Println()

	fmt.Println(ui.Bold("Log Integrity"))
	if result.HashChainValid {
		fmt.Printf("  %s Hash chain: %d entries, no gaps, all hashes valid\n", ui.Green("[ok]"), result.EntryCount)
	} else {
		fmt.Printf("  %s Hash chain: INVALID\n", ui.Red("[FAIL]"))
	}

	fmt.Println()
	fmt.Println(ui.Bold("Local Signatures"))
	if result.AttestationCount == 0 {
		fmt.Printf("  %s No attestations found\n", ui.Dim("-"))
	} else if result.AttestationsValid {
		fmt.Printf("  %s %d attestations, all signatures valid\n", ui.Green("[ok]"), result.AttestationCount)
	} else {
		fmt.Printf("  %s Attestations: INVALID\n", ui.Red("[FAIL]"))
	}

	fmt.Println()
	fmt.Println(ui.Bold("External Attestations (Sigstore/Rekor)"))
	if result.RekorProofCount == 0 {
		fmt.Printf("  %s No Rekor proofs found\n", ui.Dim("-"))
	} else {
		fmt.Printf("  %s %d entries anchored to Rekor (not verified - offline mode)\n", ui.Cyan("[info]"), result.RekorProofCount)
	}

	fmt.Println()
	fmt.Println(ui.Dim("───────────────────────────────────────────────────────────────"))

	if result.Valid {
		fmt.Printf("VERDICT: %s\n", ui.Green("[ok] INTACT — No tampering detected"))
		return nil
	}

	fmt.Printf("VERDICT: %s\n", ui.Red(fmt.Sprintf("[FAIL] TAMPERED — %s", result.Error)))
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
	fmt.Println(ui.Bold("Proof Bundle Verification"))
	fmt.Println(ui.Dim("───────────────────────────────────────────────────────────────"))
	fmt.Printf("Bundle Version: %d\n", bundle.Version)
	fmt.Printf("Created: %s\n", bundle.CreatedAt.Format("2006-01-02 15:04:05 UTC"))
	fmt.Printf("Entries: %d\n", result.EntryCount)
	fmt.Println()

	fmt.Println(ui.Bold("Log Integrity"))
	if result.HashChainValid {
		fmt.Printf("  %s Hash chain: %d entries verified\n", ui.Green("[ok]"), result.EntryCount)
		if len(bundle.LastHash) >= 16 {
			fmt.Printf("  %s Last hash: %s...\n", ui.Green("[ok]"), bundle.LastHash[:16])
		} else if bundle.LastHash != "" {
			fmt.Printf("  %s Last hash: %s\n", ui.Green("[ok]"), bundle.LastHash)
		}
	} else {
		fmt.Printf("  %s Hash chain: INVALID\n", ui.Red("[FAIL]"))
	}

	fmt.Println()
	fmt.Println(ui.Bold("Local Signatures"))
	if result.AttestationCount == 0 {
		fmt.Printf("  %s No attestations in bundle\n", ui.Dim("-"))
	} else if result.AttestationsValid {
		fmt.Printf("  %s %d attestation(s) verified\n", ui.Green("[ok]"), result.AttestationCount)
	} else {
		fmt.Printf("  %s Attestation signatures: INVALID\n", ui.Red("[FAIL]"))
	}

	fmt.Println()
	fmt.Println(ui.Bold("External Attestations (Sigstore/Rekor)"))
	if result.RekorProofCount == 0 {
		fmt.Printf("  %s No Rekor proofs in bundle\n", ui.Dim("-"))
	} else {
		fmt.Printf("  %s %d Rekor proof(s) included (not verified - offline mode)\n", ui.Cyan("[info]"), result.RekorProofCount)
	}

	fmt.Println()
	fmt.Println(ui.Dim("───────────────────────────────────────────────────────────────"))
	if result.Valid {
		fmt.Printf("VERDICT: %s\n", ui.Green("[ok] VALID"))
		return nil
	}

	fmt.Printf("VERDICT: %s\n", ui.Red(fmt.Sprintf("[FAIL] TAMPERED — %s", result.Error)))
	return fmt.Errorf("bundle verification failed")
}
