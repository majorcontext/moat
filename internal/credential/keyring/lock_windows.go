//go:build windows

package keyring

import "os"

// lockFile on Windows is a no-op since Windows has different locking semantics.
// The file-based key storage still works, but without protection against
// concurrent first-time key generation by multiple processes.
// This is acceptable because:
// 1. The race condition only affects first-time setup
// 2. Windows keychain (Credential Manager) is the primary backend
// 3. File fallback is mainly for headless/CI environments
func lockFile(_ *os.File) (unlock func(), err error) {
	return func() {}, nil
}
