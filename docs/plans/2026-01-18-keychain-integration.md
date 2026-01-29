# Keychain Integration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace hardcoded encryption key with platform-specific keychain storage (macOS Keychain, Linux Secret Service) with file-based fallback.

**Architecture:** New `internal/credential/keyring` package wraps `zalando/go-keyring`. On first use, generates random 32-byte key and stores in system keychain. Falls back to `~/.moat/encryption.key` file when keychain unavailable.

**Tech Stack:** Go, `github.com/zalando/go-keyring`, AES-256-GCM encryption

---

## Task 1: Add go-keyring dependency

**Files:**
- Modify: `go.mod`

**Step 1: Add dependency**

Run:
```bash
go get github.com/zalando/go-keyring
```

**Step 2: Verify dependency added**

Run:
```bash
grep go-keyring go.mod
```

Expected: `github.com/zalando/go-keyring v0.2.x`

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(deps): add zalando/go-keyring for keychain access"
```

---

## Task 2: Create keyring interface and types

**Files:**
- Create: `internal/credential/keyring/keyring.go`
- Test: `internal/credential/keyring/keyring_test.go`

**Step 1: Write test for key encoding/decoding round-trip**

Create `internal/credential/keyring/keyring_test.go`:

```go
package keyring

import (
	"bytes"
	"testing"
)

func TestEncodeDecodeKey(t *testing.T) {
	original := make([]byte, 32)
	for i := range original {
		original[i] = byte(i)
	}

	encoded := encodeKey(original)
	decoded, err := decodeKey(encoded)
	if err != nil {
		t.Fatalf("decodeKey failed: %v", err)
	}

	if !bytes.Equal(original, decoded) {
		t.Errorf("round-trip failed: got %v, want %v", decoded, original)
	}
}

func TestDecodeKeyInvalidBase64(t *testing.T) {
	_, err := decodeKey("not-valid-base64!!!")
	if err == nil {
		t.Error("expected error for invalid base64")
	}
}

func TestDecodeKeyWrongLength(t *testing.T) {
	encoded := encodeKey([]byte("too-short"))
	_, err := decodeKey(encoded)
	if err == nil {
		t.Error("expected error for wrong key length")
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./internal/credential/keyring/... -v
```

Expected: FAIL - package doesn't exist

**Step 3: Write minimal implementation**

Create `internal/credential/keyring/keyring.go`:

```go
// Package keyring provides secure storage for the credential encryption key.
package keyring

import (
	"encoding/base64"
	"fmt"
)

const (
	// ServiceName is the keyring service identifier.
	ServiceName = "moat"
	// AccountName is the keyring account identifier.
	AccountName = "encryption-key"
	// KeySize is the required encryption key size in bytes.
	KeySize = 32
)

// encodeKey converts a raw key to base64 for keychain storage.
func encodeKey(key []byte) string {
	return base64.StdEncoding.EncodeToString(key)
}

// decodeKey converts a base64-encoded key back to raw bytes.
func decodeKey(encoded string) ([]byte, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("invalid key encoding: %w", err)
	}
	if len(key) != KeySize {
		return nil, fmt.Errorf("invalid key length: expected %d bytes, got %d", KeySize, len(key))
	}
	return key, nil
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./internal/credential/keyring/... -v
```

Expected: PASS (3 tests)

**Step 5: Commit**

```bash
git add internal/credential/keyring/
git commit -m "feat(keyring): add key encoding/decoding utilities"
```

---

## Task 3: Implement keychain backend

**Files:**
- Modify: `internal/credential/keyring/keyring.go`
- Modify: `internal/credential/keyring/keyring_test.go`

**Step 1: Write test for keychain operations (with mock)**

Add to `internal/credential/keyring/keyring_test.go`:

```go
func TestKeychainBackend(t *testing.T) {
	backend := &keychainBackend{}

	// Generate a test key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 2)
	}

	// Store it
	if err := backend.Set(key); err != nil {
		// Skip if keychain unavailable (CI environment)
		t.Skipf("keychain unavailable: %v", err)
	}

	// Retrieve it
	retrieved, err := backend.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(key, retrieved) {
		t.Errorf("retrieved key doesn't match: got %v, want %v", retrieved, key)
	}

	// Clean up
	_ = backend.Delete()
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestKeychainBackend
```

Expected: FAIL - keychainBackend not defined

**Step 3: Write implementation**

Add to `internal/credential/keyring/keyring.go`:

```go
import (
	"encoding/base64"
	"fmt"

	"github.com/zalando/go-keyring"
)

// Backend defines the interface for key storage.
type Backend interface {
	Get() ([]byte, error)
	Set(key []byte) error
	Delete() error
	Name() string
}

// keychainBackend stores keys in the system keychain.
type keychainBackend struct{}

func (k *keychainBackend) Get() ([]byte, error) {
	encoded, err := keyring.Get(ServiceName, AccountName)
	if err != nil {
		return nil, fmt.Errorf("keychain get: %w", err)
	}
	return decodeKey(encoded)
}

func (k *keychainBackend) Set(key []byte) error {
	encoded := encodeKey(key)
	if err := keyring.Set(ServiceName, AccountName, encoded); err != nil {
		return fmt.Errorf("keychain set: %w", err)
	}
	return nil
}

func (k *keychainBackend) Delete() error {
	if err := keyring.Delete(ServiceName, AccountName); err != nil {
		return fmt.Errorf("keychain delete: %w", err)
	}
	return nil
}

func (k *keychainBackend) Name() string {
	return "system keychain"
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestKeychainBackend
```

Expected: PASS (or SKIP if keychain unavailable)

**Step 5: Commit**

```bash
git add internal/credential/keyring/
git commit -m "feat(keyring): add system keychain backend"
```

---

## Task 4: Implement file-based fallback backend

**Files:**
- Modify: `internal/credential/keyring/keyring.go`
- Modify: `internal/credential/keyring/keyring_test.go`

**Step 1: Write test for file backend**

Add to `internal/credential/keyring/keyring_test.go`:

```go
import (
	"os"
	"path/filepath"
)

func TestFileBackend(t *testing.T) {
	// Use temp directory
	tmpDir := t.TempDir()
	backend := &fileBackend{path: filepath.Join(tmpDir, "test.key")}

	// Generate a test key
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i * 3)
	}

	// Store it
	if err := backend.Set(key); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify file permissions
	info, err := os.Stat(backend.path)
	if err != nil {
		t.Fatalf("Stat failed: %v", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Errorf("wrong permissions: got %o, want 0600", info.Mode().Perm())
	}

	// Retrieve it
	retrieved, err := backend.Get()
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if !bytes.Equal(key, retrieved) {
		t.Errorf("retrieved key doesn't match: got %v, want %v", retrieved, key)
	}

	// Delete it
	if err := backend.Delete(); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify deleted
	if _, err := os.Stat(backend.path); !os.IsNotExist(err) {
		t.Error("file should be deleted")
	}
}

func TestFileBackendNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	backend := &fileBackend{path: filepath.Join(tmpDir, "nonexistent.key")}

	_, err := backend.Get()
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestFileBackend
```

Expected: FAIL - fileBackend not defined

**Step 3: Write implementation**

Add to `internal/credential/keyring/keyring.go`:

```go
import (
	"os"
	"path/filepath"
)

// fileBackend stores keys in a file with restricted permissions.
type fileBackend struct {
	path string
}

func (f *fileBackend) Get() ([]byte, error) {
	data, err := os.ReadFile(f.path)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}
	return decodeKey(string(data))
}

func (f *fileBackend) Set(key []byte) error {
	dir := filepath.Dir(f.path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("creating key directory: %w", err)
	}

	encoded := encodeKey(key)
	if err := os.WriteFile(f.path, []byte(encoded), 0600); err != nil {
		return fmt.Errorf("writing key file: %w", err)
	}
	return nil
}

func (f *fileBackend) Delete() error {
	if err := os.Remove(f.path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("deleting key file: %w", err)
	}
	return nil
}

func (f *fileBackend) Name() string {
	return "file (" + f.path + ")"
}

// DefaultKeyFilePath returns the default path for the fallback key file.
func DefaultKeyFilePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".moat", "encryption.key")
	}
	return filepath.Join(home, ".moat", "encryption.key")
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestFileBackend
```

Expected: PASS (2 tests)

**Step 5: Commit**

```bash
git add internal/credential/keyring/
git commit -m "feat(keyring): add file-based fallback backend"
```

---

## Task 5: Implement GetOrCreateKey with fallback logic

**Files:**
- Modify: `internal/credential/keyring/keyring.go`
- Modify: `internal/credential/keyring/keyring_test.go`

**Step 1: Write test for GetOrCreateKey**

Add to `internal/credential/keyring/keyring_test.go`:

```go
import "crypto/rand"

// mockBackend for testing fallback behavior
type mockBackend struct {
	key      []byte
	getErr   error
	setErr   error
	getCalls int
	setCalls int
}

func (m *mockBackend) Get() ([]byte, error) {
	m.getCalls++
	if m.getErr != nil {
		return nil, m.getErr
	}
	if m.key == nil {
		return nil, fmt.Errorf("key not found")
	}
	return m.key, nil
}

func (m *mockBackend) Set(key []byte) error {
	m.setCalls++
	if m.setErr != nil {
		return m.setErr
	}
	m.key = key
	return nil
}

func (m *mockBackend) Delete() error {
	m.key = nil
	return nil
}

func (m *mockBackend) Name() string {
	return "mock"
}

func TestGetOrCreateKeyExisting(t *testing.T) {
	existingKey := make([]byte, 32)
	rand.Read(existingKey)

	primary := &mockBackend{key: existingKey}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(key, existingKey) {
		t.Error("should return existing key from primary")
	}
	if primary.getCalls != 1 {
		t.Errorf("primary.Get called %d times, want 1", primary.getCalls)
	}
	if fallback.getCalls != 0 {
		t.Error("fallback should not be checked when primary succeeds")
	}
}

func TestGetOrCreateKeyFallbackExisting(t *testing.T) {
	existingKey := make([]byte, 32)
	rand.Read(existingKey)

	primary := &mockBackend{getErr: fmt.Errorf("keychain unavailable")}
	fallback := &mockBackend{key: existingKey}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !bytes.Equal(key, existingKey) {
		t.Error("should return existing key from fallback")
	}
}

func TestGetOrCreateKeyGeneratesNew(t *testing.T) {
	primary := &mockBackend{getErr: fmt.Errorf("keychain unavailable")}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("generated key wrong length: got %d, want 32", len(key))
	}
	if fallback.setCalls != 1 {
		t.Error("should store new key in fallback when primary unavailable")
	}
}

func TestGetOrCreateKeyStoresInPrimary(t *testing.T) {
	primary := &mockBackend{}
	fallback := &mockBackend{}

	key, err := getOrCreateKeyWithBackends(primary, fallback)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(key) != 32 {
		t.Errorf("generated key wrong length: got %d, want 32", len(key))
	}
	if primary.setCalls != 1 {
		t.Error("should store new key in primary when available")
	}
	if fallback.setCalls != 0 {
		t.Error("should not use fallback when primary works")
	}
}
```

**Step 2: Run test to verify it fails**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestGetOrCreateKey
```

Expected: FAIL - getOrCreateKeyWithBackends not defined

**Step 3: Write implementation**

Add to `internal/credential/keyring/keyring.go`:

```go
import (
	"crypto/rand"
	"log/slog"
)

// generateKey creates a new random encryption key.
func generateKey() ([]byte, error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, fmt.Errorf("generating random key: %w", err)
	}
	return key, nil
}

// getOrCreateKeyWithBackends retrieves or creates an encryption key using the provided backends.
func getOrCreateKeyWithBackends(primary, fallback Backend) ([]byte, error) {
	// 1. Try primary backend (keychain)
	if key, err := primary.Get(); err == nil {
		return key, nil
	}

	// 2. Try fallback backend (file)
	if key, err := fallback.Get(); err == nil {
		return key, nil
	}

	// 3. Generate new key
	key, err := generateKey()
	if err != nil {
		return nil, err
	}

	// 4. Try to store in primary
	if err := primary.Set(key); err == nil {
		return key, nil
	}

	// 5. Fall back to file storage
	slog.Info("system keychain unavailable, using file-based key storage")
	if err := fallback.Set(key); err != nil {
		return nil, fmt.Errorf("storing encryption key: %w", err)
	}

	return key, nil
}

// GetOrCreateKey retrieves the encryption key from keychain or file, generating a new one if needed.
func GetOrCreateKey() ([]byte, error) {
	primary := &keychainBackend{}
	fallback := &fileBackend{path: DefaultKeyFilePath()}
	return getOrCreateKeyWithBackends(primary, fallback)
}
```

**Step 4: Run test to verify it passes**

Run:
```bash
go test ./internal/credential/keyring/... -v -run TestGetOrCreateKey
```

Expected: PASS (4 tests)

**Step 5: Commit**

```bash
git add internal/credential/keyring/
git commit -m "feat(keyring): implement GetOrCreateKey with fallback logic"
```

---

## Task 6: Update store.go to use keyring

**Files:**
- Modify: `internal/credential/store.go`
- Modify: `internal/credential/store_test.go`

**Step 1: Update DefaultEncryptionKey**

In `internal/credential/store.go`, replace `DefaultEncryptionKey()`:

```go
import (
	"github.com/majorcontext/moat/internal/credential/keyring"
)

// DefaultEncryptionKey retrieves the encryption key from secure storage.
// Uses system keychain when available, falls back to file-based storage.
func DefaultEncryptionKey() ([]byte, error) {
	return keyring.GetOrCreateKey()
}
```

**Step 2: Update callers to handle error**

The signature changed from `[]byte` to `([]byte, error)`. Update these files:

In `cmd/moat/cli/grant.go`, update `saveCredential`:

```go
func saveCredential(cred credential.Credential) (string, error) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return "", fmt.Errorf("getting encryption key: %w", err)
	}
	store, err := credential.NewFileStore(
		credential.DefaultStoreDir(),
		key,
	)
	if err != nil {
		return "", fmt.Errorf("opening credential store: %w", err)
	}
	// ... rest unchanged
}
```

In `cmd/moat/cli/revoke.go`, update similarly.

In `internal/run/manager.go`, update similarly.

**Step 3: Update tests**

In `internal/credential/store_test.go`, tests use a hardcoded test key which is fine - no changes needed there.

**Step 4: Run all tests**

Run:
```bash
go test ./...
```

Expected: All tests PASS

**Step 5: Commit**

```bash
git add internal/credential/store.go cmd/moat/cli/grant.go cmd/moat/cli/revoke.go internal/run/manager.go
git commit -m "feat(credential): use keyring for encryption key storage

BREAKING: Existing credentials encrypted with the old hardcoded key
will fail to decrypt. Re-run 'moat grant' to create new credentials."
```

---

## Task 7: Manual verification

**Step 1: Build and test fresh grant flow**

```bash
go build -o moat ./cmd/moat

# Remove any existing credentials
rm -rf ~/.moat/credentials/

# Grant a credential (will generate new key in keychain)
./moat grant anthropic
# Enter test key when prompted, skip validation

# Verify credential was saved
ls -la ~/.moat/credentials/
```

**Step 2: Verify key is in keychain (macOS)**

```bash
security find-generic-password -s moat -a encryption-key
```

Expected: Shows keychain entry with base64-encoded key

**Step 3: Test credential retrieval works**

```bash
# Run should be able to use the credential
./moat run --help  # Just verify CLI works
```

**Step 4: Commit any final fixes**

If any issues found, fix and commit.

---

## Summary

| Task | Description | Files |
|------|-------------|-------|
| 1 | Add go-keyring dependency | go.mod |
| 2 | Key encoding/decoding | keyring/keyring.go |
| 3 | Keychain backend | keyring/keyring.go |
| 4 | File fallback backend | keyring/keyring.go |
| 5 | GetOrCreateKey logic | keyring/keyring.go |
| 6 | Update store.go | store.go, grant.go, revoke.go, manager.go |
| 7 | Manual verification | - |
