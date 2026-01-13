# Tamper-Proof Log Database Design

**Status:** Proposed
**Author:** Andy Bons
**Date:** 2026-01-12

## Overview

A cryptographically verifiable logging system that prevents tampering by malicious agents, compromised hosts, or post-hoc modification. Logs are hash-chained, organized in a Merkle tree, and anchored to external transparency logs for third-party auditability.

## Problem Statement

The current logging system stores plain JSONL files with no integrity verification. An attacker who gains access to `~/.agentops/runs/` can modify, delete, or reorder log entries without detection. A malicious agent could potentially hide evidence of data exfiltration or other harmful actions.

## Threat Model

| Threat | Description |
|--------|-------------|
| Malicious agent | Container attempts to tamper with its own audit trail |
| Compromised host (post-run) | Attacker modifies logs after run completes |
| Compromised host (mid-run) | Attacker gains access while agent is running |
| Audit compliance | Third parties require cryptographic proof of log integrity |

## Design Goals

1. **Agent isolation** - Agents cannot read or modify log files
2. **Tamper evidence** - Any modification is detectable
3. **Ordering proof** - Entries cannot be reordered or deleted without detection
4. **External anchoring** - Critical events are witnessed by external transparency logs
5. **Efficient verification** - Merkle proofs enable subset verification without full log replay
6. **Local-first** - Works offline with optional remote attestation

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│                         CONTAINER                                │
│  ┌─────────────┐                                                │
│  │   Agent     │                                                │
│  │  (untrusted)│                                                │
│  └──────┬──────┘                                                │
│         │ stdout/stderr                                         │
│         ▼                                                       │
│  ┌─────────────┐      ┌─────────────┐                          │
│  │ Log Socket  │      │   Proxy     │ (network requests)       │
│  │ (write-only)│      │             │                          │
│  └──────┬──────┘      └──────┬──────┘                          │
└─────────┼────────────────────┼──────────────────────────────────┘
          │                    │
          ▼                    ▼
┌─────────────────────────────────────────────────────────────────┐
│                      HOST (trusted)                              │
│  ┌──────────────────────────────────────────────────────────┐   │
│  │                    Log Collector                          │   │
│  │  ┌────────────┐  ┌────────────┐  ┌────────────────────┐  │   │
│  │  │ Console    │  │ Network    │  │ Credential Usage   │  │   │
│  │  │ Logs       │  │ Requests   │  │ Events             │  │   │
│  │  │ (batched)  │  │ (per-entry)│  │ (per-entry)        │  │   │
│  │  └─────┬──────┘  └─────┬──────┘  └─────────┬──────────┘  │   │
│  │        └───────────────┴───────────────────┘              │   │
│  │                        │                                  │   │
│  │                        ▼                                  │   │
│  │              ┌─────────────────┐                          │   │
│  │              │   Merkle Tree   │                          │   │
│  │              │   + Hash Chain  │                          │   │
│  │              └────────┬────────┘                          │   │
│  │                       │                                   │   │
│  │         ┌─────────────┼─────────────┐                     │   │
│  │         ▼             ▼             ▼                     │   │
│  │  ┌──────────┐  ┌────────────┐  ┌──────────┐              │   │
│  │  │ Local    │  │ Sigstore/  │  │ RFC 3161 │              │   │
│  │  │ Signing  │  │ Rekor      │  │ TSA      │              │   │
│  │  └──────────┘  └────────────┘  └──────────┘              │   │
│  └──────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────┘
```

## Data Structures

### Log Entry

Each entry includes a sequence number, hash of the previous entry, and its own hash:

```go
type LogEntry struct {
    Sequence    uint64    `json:"seq"`      // Monotonic, no gaps
    Timestamp   time.Time `json:"ts"`       // UTC
    Type        EntryType `json:"type"`     // console, network, credential
    PrevHash    string    `json:"prev"`     // SHA-256 of previous entry
    Data        any       `json:"data"`     // Type-specific payload
    Hash        string    `json:"hash"`     // SHA-256(seq || ts || type || prev || data)
}

type EntryType string

const (
    EntryConsole    EntryType = "console"
    EntryNetwork    EntryType = "network"
    EntryCredential EntryType = "credential"
)
```

### Network Request Entry

Network requests include credential usage information:

```go
type NetworkRequestData struct {
    Method         string `json:"method"`
    URL            string `json:"url"`
    StatusCode     int    `json:"status_code"`
    DurationMs     int64  `json:"duration_ms"`
    CredentialUsed string `json:"credential_used,omitempty"` // e.g., "github"
    Error          string `json:"error,omitempty"`
}
```

### Merkle Tree

Entries are organized in a Merkle tree for efficient subset proofs:

```go
type MerkleNode struct {
    Hash     string       `json:"hash"`
    Left     *MerkleNode  `json:"left,omitempty"`
    Right    *MerkleNode  `json:"right,omitempty"`
    EntrySeq uint64       `json:"seq,omitempty"` // Leaf nodes only
}
```

## Storage Layout

```
~/.agentops/runs/<run-id>/
├── logs.db                    # SQLite: entries + merkle nodes
├── attestations/
│   ├── root-0001.sig          # Local signature of merkle root
│   ├── root-0001.rekor        # Sigstore inclusion proof
│   └── ...
└── keys/
    └── run.pub                # Public key for this run
```

SQLite provides atomic transactions, efficient range queries, and built-in integrity checks.

## Agent Isolation

Agents write logs through a Unix domain socket. The agent cannot read back from the socket or access log files directly.

**Host side:**

```go
func (c *LogCollector) Start(runDir string) error {
    c.socketPath = filepath.Join(runDir, "log.sock")
    listener, err := net.Listen("unix", c.socketPath)
    if err != nil {
        return err
    }
    os.Chmod(c.socketPath, 0222) // Write-only
    go c.acceptConnections(listener)
    return nil
}
```

**Container mounts:**

```go
Mounts: []Mount{
    {
        Source:   filepath.Join(runDir, "log.sock"),
        Target:   "/run/agentops/log.sock",
    },
},
Env: []string{
    "AGENTOPS_LOG_SOCKET=/run/agentops/log.sock",
},
```

The collector assigns sequence numbers server-side, ignoring any client-provided values.

## Attestation Tiers

Events are attested based on criticality:

| Type | Volume | Attestation |
|------|--------|-------------|
| Network requests | Low | Per-entry, immediate |
| Credential usage | Very low | Per-entry, immediate |
| Console logs | High | Batched (10s or 100 entries) |

### Critical Event Flow

```go
func (c *Collector) OnNetworkRequest(req NetworkRequest) error {
    entry := c.createEntry(EntryNetwork, req)
    c.merkle.Append(entry)

    root := c.merkle.Root()
    sig := c.signer.Sign(root)
    c.storeAttestation(entry.Sequence, sig)

    go c.submitToRekor(entry.Sequence, root) // Async
    return nil
}
```

Sigstore submission is asynchronous to avoid blocking. Local signatures provide immediate integrity; Sigstore provides external anchoring.

### Batched Event Flow

```go
func (c *Collector) OnConsoleLog(line string) {
    entry := c.createEntry(EntryConsole, ConsoleLine{Line: line})
    c.merkle.Append(entry)
    c.batchBuffer = append(c.batchBuffer, entry)

    if len(c.batchBuffer) >= 100 || time.Since(c.lastBatch) > 10*time.Second {
        c.flushBatch()
    }
}
```

## External Attestation (Sigstore/Rekor)

Rekor is a public, append-only transparency log. Once a Merkle root is submitted, it cannot be removed or modified.

**Submission:**

```go
func (c *Collector) submitToRekor(seq uint64, root []byte) error {
    client, _ := rekor.NewClient("https://rekor.sigstore.dev")

    entry, err := client.Upload(context.Background(), &rekor.Entry{
        Kind:      "hashedrekord",
        Hash:      hex.EncodeToString(root),
        Signature: c.signer.Sign(root),
        PublicKey: c.signer.PublicKey(),
    })
    if err != nil {
        return err
    }

    c.storeRekorProof(seq, entry.LogIndex, entry.InclusionProof)
    return nil
}
```

**Stored proof:**

```json
{
  "log_index": 12345678,
  "log_id": "c0d23d6ad406973f...",
  "inclusion_proof": {
    "tree_size": 98765432,
    "root_hash": "abc123...",
    "hashes": ["def456...", "789abc..."],
    "log_index": 12345678
  },
  "signed_entry_timestamp": "MEUCIQD..."
}
```

## Verification

### CLI

```bash
$ agent audit <run-id>

Auditing run: run-abc123def456
═══════════════════════════════════════════════════════════════

Log Integrity
  ✓ Hash chain: 1,247 entries, no gaps, all hashes valid
  ✓ Merkle tree: root matches computed root
  ✓ Sequence: monotonic, no duplicates

Local Signatures
  ✓ 15 checkpoints signed
  ✓ All signatures valid

External Attestations (Sigstore/Rekor)
  ✓ 42 critical entries attested
  ✓ 15 batch checkpoints attested
  ✓ All inclusion proofs verified

═══════════════════════════════════════════════════════════════
VERDICT: ✓ INTACT - No tampering detected
```

### Exportable Proof Bundle

```bash
$ agent audit --export <run-id> -o audit-bundle.zip
```

Contents:
- `logs.db` - Full log database
- `merkle-root.json` - Final tree root
- `attestations/` - All signatures and Rekor proofs
- `verify.go` - Standalone verification code
- `README.md` - Verification instructions

Third-party auditors can verify without AgentOps installed.

### Go API

```go
import "github.com/anthropic/agentops/audit"

run, _ := audit.LoadRun("~/.agentops/runs/run-abc123")

result, _ := audit.Verify(run, audit.VerifyOptions{
    CheckHashChain:  true,
    CheckMerkleTree: true,
    CheckLocalSigs:  true,
    CheckRekor:      true,
})

fmt.Printf("Verdict: %s\n", result.Verdict) // INTACT or TAMPERED

// Merkle proof for specific range
proof, _ := audit.ProveEntries(run, 50, 100)
valid := audit.VerifyProof(proof, run.MerkleRoot())
```

## Configuration

```yaml
# agent.yaml
audit:
  attestation: sigstore           # sigstore | tsa | local-only
  batch_interval: 10s             # Console log batch interval
  batch_size: 100                 # Max entries per batch
  critical_patterns:              # Additional patterns for immediate attestation
    - "ERROR"
    - "SECURITY"
```

## Security Properties

| Attack | Defense |
|--------|---------|
| Agent reads logs | Socket isolation - logs not mounted |
| Agent modifies logs | No file access |
| Entry modification | Hash chain breaks |
| Entry deletion | Sequence gap detected |
| Entry reordering | Hash chain + sequence numbers |
| Post-run tampering | Sigstore has original roots |
| Mid-run tampering | Critical events attested immediately |

## Dependencies

- `github.com/sigstore/sigstore-go` - Rekor client
- `modernc.org/sqlite` - Pure Go SQLite (no CGO)

## Future Considerations

- RFC 3161 TSA support for enterprise compliance
- Hardware-backed signing (TPM/Secure Enclave) for high-security deployments
- Log streaming to remote collectors for real-time monitoring

## Implementation Plan

1. **Phase 1:** Log collector with hash chain and SQLite storage
2. **Phase 2:** Merkle tree implementation with proof generation
3. **Phase 3:** Local signing and verification CLI
4. **Phase 4:** Sigstore/Rekor integration
5. **Phase 5:** Exportable proof bundles and Go API
