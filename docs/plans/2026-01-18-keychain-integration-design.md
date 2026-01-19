# System Keychain for Credential Encryption Key

**Issue:** https://github.com/andybons/moat/issues/13
**Date:** 2026-01-18
**Status:** Proposed

## Problem

The credential store uses a hardcoded encryption key in `internal/credential/store.go`:

```go
func DefaultEncryptionKey() []byte {
    return []byte("moat-default-encryption-key-32b!")
}
```

Anyone with source code access can decrypt all credentials on any system.

## Solution

Use platform-specific secure storage for the encryption key, matching GitHub CLI's approach:

- **macOS:** Login Keychain via `go-keyring`
- **Linux:** Secret Service API (GNOME Keyring, KWallet, KeePassXC) via `go-keyring`
- **Fallback:** File-based storage when keychain unavailable

## Design

### Package Structure

```
internal/credential/
  keyring/
    keyring.go           # Interface + GetOrCreateKey()
    keyring_darwin.go    # macOS-specific (build tag)
    keyring_linux.go     # Linux-specific (build tag)
    keyring_fallback.go  # File-based fallback
    keyring_test.go      # Unit tests
```

### Storage Locations

**macOS Keychain:**
- Keychain: Login keychain (`~/Library/Keychains/login.keychain-db`)
- Kind: Generic password
- Service: `moat`
- Account: `encryption-key`

**Linux Secret Service:**
- Backend: GNOME Keyring, KWallet, or KeePassXC
- Collection: Default/login
- Label: `moat:encryption-key`
- Attributes: `{service: "moat", username: "encryption-key"}`

**File Fallback:**
- Path: `~/.moat/encryption.key`
- Format: 32 bytes, base64-encoded
- Permissions: `0600`

### Key Management Logic

```go
func GetOrCreateEncryptionKey() ([]byte, error) {
    // 1. Try keychain
    if key, err := keyring.Get("moat", "encryption-key"); err == nil {
        return decodeKey(key), nil
    }

    // 2. Try file fallback
    if key, err := readKeyFile(); err == nil {
        return key, nil
    }

    // 3. Generate new key
    key := generateRandomKey(32)

    // 4. Store in keychain (preferred)
    if err := keyring.Set("moat", "encryption-key", encodeKey(key)); err == nil {
        return key, nil
    }

    // 5. Store in file (fallback)
    return key, writeKeyFile(key)
}
```

### Error Handling

| Scenario | Behavior |
|----------|----------|
| Keychain unavailable | Silent fallback to file storage, log info message |
| Keychain locked/denied | Fallback to file storage |
| Key file world-readable | Log warning, continue |
| Corrupted key | Error with instructions to delete and re-grant |
| Concurrent writes | File locking (`flock`) during write |

### Migration

None. Existing credentials encrypted with the old hardcoded key will fail to decrypt. Users re-run `moat grant` to create new credentials with the new key.

## Dependencies

Add `github.com/zalando/go-keyring` - the same library used by GitHub CLI.

## Files to Modify

1. **New:** `internal/credential/keyring/` package (4 files)
2. **Modify:** `internal/credential/store.go` - replace `DefaultEncryptionKey()`
3. **Modify:** `go.mod` - add go-keyring dependency

No changes needed to CLI commands (`grant.go`, `revoke.go`) or `run/manager.go` - they already call `DefaultEncryptionKey()`.

## Testing

**Unit tests:**
- Mock keyring interface
- Key generation and encode/decode round-trip
- Fallback path when keyring errors

**Integration tests:**
- Real keychain on macOS CI
- File fallback on Linux CI (no keychain)
- Verify file permissions

**Manual checklist:**
- [ ] Fresh install: key stored in keychain
- [ ] Second run: key retrieved from keychain
- [ ] Headless Linux: falls back to file
- [ ] Existing credentials: fail to decrypt, re-grant works
- [ ] End-to-end: `moat grant` then `moat run --grant`

## References

- [zalando/go-keyring](https://github.com/zalando/go-keyring) - Cross-platform keyring library
- [GitHub CLI credential storage](https://github.com/cli/cli/discussions/8980) - Similar approach
