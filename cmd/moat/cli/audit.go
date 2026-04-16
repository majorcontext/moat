package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/majorcontext/moat/internal/audit"
	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/storage"
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
	runDir := filepath.Join(storage.DefaultBaseDir(), runID)
	dbPath := filepath.Join(runDir, "audit.db")

	if _, statErr := os.Stat(dbPath); os.IsNotExist(statErr) {
		return fmt.Errorf("run not found: %s", runID)
	}

	auditor, err := audit.NewAuditor(dbPath)
	if err != nil {
		return fmt.Errorf("opening audit log: %w", err)
	}
	defer auditor.Close()

	// Open store for reading entries.
	store, storeErr := audit.OpenStore(dbPath)
	if storeErr != nil {
		return fmt.Errorf("opening audit store: %w", storeErr)
	}
	defer store.Close()

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
	} else {
		fmt.Printf("VERDICT: %s\n", ui.Red(fmt.Sprintf("[FAIL] TAMPERED — %s", result.Error)))
	}

	// Show event log.
	count, countErr := store.Count()
	if countErr == nil && count > 0 {
		entries, rangeErr := store.Range(1, count)
		if rangeErr == nil && len(entries) > 0 {
			fmt.Println()
			fmt.Println(ui.Bold("Event Log"))
			for _, e := range entries {
				ts := e.Timestamp.Local().Format("15:04:05")
				fmt.Printf("  %s  %-12s  %s\n", ui.Dim(ts), e.Type, formatEntryData(e))
			}
		}
	}

	if !result.Valid {
		return fmt.Errorf("tampering detected")
	}
	return nil
}

// formatEntryData returns a human-readable summary of an audit entry's data.
func formatEntryData(e *audit.Entry) string {
	data, ok := e.Data.(map[string]any)
	if !ok {
		return ""
	}

	switch e.Type {
	case audit.EntryContainer:
		action, _ := data["action"].(string)
		return action

	case audit.EntryPolicy:
		scope, _ := data["scope"].(string)
		operation, _ := data["operation"].(string)
		decision, _ := data["decision"].(string)
		rule, _ := data["rule"].(string)
		message, _ := data["message"].(string)
		s := fmt.Sprintf("%s %s", scope, decision)
		if operation != "" {
			s += " " + operation
		}
		if rule != "" {
			s += fmt.Sprintf(" rule=%s", rule)
		}
		if message != "" {
			s += fmt.Sprintf(" — %s", message)
		}
		return s

	case audit.EntryNetwork:
		method, _ := data["method"].(string)
		url, _ := data["url"].(string)
		status, _ := data["status_code"].(float64)
		return fmt.Sprintf("%s %s → %d", method, url, int(status))

	case audit.EntryCredential:
		name, _ := data["name"].(string)
		action, _ := data["action"].(string)
		host, _ := data["host"].(string)
		return fmt.Sprintf("%s %s (%s)", action, name, host)

	case audit.EntryExec:
		cmd, _ := data["command"].([]any)
		if len(cmd) > 0 {
			first, _ := cmd[0].(string)
			return first
		}
		return ""

	case audit.EntryConsole:
		line, _ := data["line"].(string)
		if len(line) > 80 {
			return line[:80] + "…"
		}
		return line

	default:
		b, _ := json.Marshal(data)
		s := string(b)
		if len(s) > 80 {
			return s[:80] + "…"
		}
		return s
	}
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
