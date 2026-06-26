package daemon

import (
	"os"
	"testing"
)

// TestMain forces file-based encryption-key storage for the whole daemon test
// binary. Tests such as TestRestoreRuns_ScopesStoreToRunProfile open the
// credential store via credential.DefaultEncryptionKey, which would otherwise
// hit the real system keychain — on macOS that pops a blocking GUI prompt,
// hanging headless/CI runs, and risks reading or clobbering the developer's
// real key. File storage sidesteps both.
func TestMain(m *testing.M) {
	os.Setenv("MOAT_KEYRING_BACKEND", "file")
	os.Exit(m.Run())
}
