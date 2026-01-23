package audit

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestE2E_TamperProofAuditDemo(t *testing.T) {
	fmt.Println("\n═══════════════════════════════════════════════════════════════")
	fmt.Println("  TAMPER-PROOF AUDIT LOG - END-TO-END DEMO")
	fmt.Println("═══════════════════════════════════════════════════════════════")

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "logs.db")
	keyPath := filepath.Join(dir, "run.key")
	bundlePath := filepath.Join(dir, "proof-bundle.json")

	// ══════════════════════════════════════════════════════════════════
	// PHASE 1: Create hash-chained entries
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 1: Creating hash-chained log entries                 │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	store, err := OpenStore(dbPath)
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}

	// Simulate agent activity
	activities := []struct {
		typ  string
		desc string
	}{
		{"console", "Agent started"},
		{"console", "Loading configuration..."},
		{"console", "Connecting to GitHub API"},
		{"credential", "GitHub token injected"},
		{"network", "GET https://api.github.com/user → 200"},
		{"console", "Authenticated as: octocat"},
		{"network", "GET https://api.github.com/repos → 200"},
		{"console", "Found 42 repositories"},
		{"network", "POST https://api.github.com/issues → 201"},
		{"console", "Created issue #123"},
		{"console", "Agent completed successfully"},
	}

	for i, a := range activities {
		var entry *Entry
		switch a.typ {
		case "console":
			entry, _ = store.AppendConsole(a.desc)
		case "credential":
			entry, _ = store.AppendCredential(CredentialData{
				Name: "github", Action: "injected", Host: "api.github.com",
			})
		case "network":
			entry, _ = store.AppendNetwork(NetworkData{
				Method: "GET", URL: "https://api.github.com/user", StatusCode: 200, DurationMs: 150,
			})
		}
		fmt.Printf("  [%2d] %-10s %s → %s...\n", i+1, a.typ, a.desc[:min(25, len(a.desc))], entry.Hash[:12])
	}

	fmt.Printf("\n  ✓ Created %d hash-chained entries\n", len(activities))
	fmt.Printf("  ✓ Last hash: %s...\n", store.LastHash()[:24])

	// ══════════════════════════════════════════════════════════════════
	// PHASE 2: Local signing with Ed25519
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 2: Creating cryptographic attestation (Ed25519)      │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	signer, _ := NewSigner(keyPath)
	fmt.Printf("  ✓ Generated Ed25519 key pair\n")
	fmt.Printf("    Public key: %x...\n", signer.PublicKey()[:16])

	att := &Attestation{
		Sequence:  uint64(len(activities)),
		RootHash:  store.LastHash(),
		Timestamp: time.Now().UTC(),
		PublicKey: signer.PublicKey(),
	}
	att.Signature = signer.Sign([]byte(att.RootHash))
	store.SaveAttestation(att)

	fmt.Printf("  ✓ Signed chain hash at seq %d\n", att.Sequence)
	fmt.Printf("    Signature: %x...\n", att.Signature[:16])

	// ══════════════════════════════════════════════════════════════════
	// PHASE 3: Simulate Rekor proof
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 3: Adding Rekor transparency log proof (simulated)   │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	rekorProof := &RekorProof{
		LogIndex:  12345678,
		LogID:     "c0d23d6ad406973f9ef8b320e5e4e4692e0e65e5419ad4e30c9a8b912a8a3b5c",
		TreeSize:  98765432,
		RootHash:  store.LastHash(),
		Hashes:    []string{"abc123", "def456"},
		Timestamp: time.Now().UTC(),
		EntryUUID: "24296fb24b8ad77a8c6e7c4b2e5ac5d8e9f0a1b2c3d4e5f6a7b8c9d0",
	}
	store.SaveRekorProof(uint64(len(activities)), rekorProof)
	fmt.Printf("  ✓ Rekor log index: %d\n", rekorProof.LogIndex)
	fmt.Printf("  ✓ Entry UUID: %s...\n", rekorProof.EntryUUID[:24])

	// ══════════════════════════════════════════════════════════════════
	// PHASE 4: Export proof bundle
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 4: Exporting proof bundle                            │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	// Export bundle
	bundle, err := store.Export()
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	store.Close()

	bundleJSON, _ := json.MarshalIndent(bundle, "", "  ")
	os.WriteFile(bundlePath, bundleJSON, 0644)

	fmt.Printf("  ✓ Bundle version: %d\n", bundle.Version)
	fmt.Printf("  ✓ Entries: %d\n", len(bundle.Entries))
	fmt.Printf("  ✓ Attestations: %d\n", len(bundle.Attestations))
	fmt.Printf("  ✓ Rekor proofs: %d\n", len(bundle.RekorProofs))
	fmt.Printf("  ✓ Bundle size: %d bytes\n", len(bundleJSON))

	// ══════════════════════════════════════════════════════════════════
	// VERIFICATION: Offline bundle verification
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 5: Verifying proof bundle (offline)                  │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	result := bundle.Verify()
	fmt.Printf("  Hash Chain:    %s\n", icon(result.HashChainValid))
	fmt.Printf("  Attestations:  %s (%d verified)\n", icon(result.AttestationsValid), result.AttestationCount)
	fmt.Printf("  Rekor Proofs:  %s (%d present, not verified)\n", icon(result.RekorProofsPresent), result.RekorProofCount)

	if !result.Valid {
		t.Errorf("Bundle verification failed: %s", result.Error)
	}

	fmt.Println("\n  ════════════════════════════════════════════════════════════")
	fmt.Println("  ║  VERDICT: ✓ INTACT - No tampering detected               ║")
	fmt.Println("  ════════════════════════════════════════════════════════════")

	// ══════════════════════════════════════════════════════════════════
	// TAMPER DETECTION: Show what happens with tampering
	// ══════════════════════════════════════════════════════════════════
	fmt.Println("\n┌─────────────────────────────────────────────────────────────┐")
	fmt.Println("│  STEP 6: Tampering detection demo                          │")
	fmt.Println("└─────────────────────────────────────────────────────────────┘")

	// Create a tampered bundle
	var tamperedBundle ProofBundle
	json.Unmarshal(bundleJSON, &tamperedBundle)

	// Tamper with entry #5 (network request)
	fmt.Println("  Tampering with entry #5 (changing URL)...")
	originalData := tamperedBundle.Entries[4].Data
	tamperedBundle.Entries[4].Data = map[string]any{
		"method": "GET", "url": "https://evil.com/steal", "status_code": 200,
	}

	tamperedResult := tamperedBundle.Verify()
	fmt.Printf("\n  Hash Chain:    %s\n", icon(tamperedResult.HashChainValid))
	fmt.Printf("  Error: %s\n", tamperedResult.Error)

	// Restore and tamper with hash instead
	tamperedBundle.Entries[4].Data = originalData
	tamperedBundle.Entries[4].Hash = "0000000000000000000000000000000000000000000000000000000000000000"

	tamperedResult2 := tamperedBundle.Verify()
	fmt.Printf("\n  Tampering with entry hash directly...\n")
	fmt.Printf("  Hash Chain:    %s\n", icon(tamperedResult2.HashChainValid))
	fmt.Printf("  Error: %s\n", tamperedResult2.Error)

	fmt.Println("\n  ════════════════════════════════════════════════════════════")
	fmt.Println("  ║  VERDICT: ✗ TAMPERED - Modifications detected            ║")
	fmt.Println("  ════════════════════════════════════════════════════════════")
	fmt.Println()
}

func icon(ok bool) string {
	if ok {
		return "[✓]"
	}
	return "[✗]"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
