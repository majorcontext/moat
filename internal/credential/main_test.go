package credential

import (
	"os"
	"testing"
)

// TestMain disables the system keychain for the whole credential test binary.
// Tests such as TestClaudeCodeCredentials_HasClaudeCodeCredentials exercise
// GetClaudeCodeCredentials, which on macOS reads the real "Claude Code-credentials"
// keychain item via the `security` CLI — that pops a blocking authorization
// prompt on an unsigned test binary and makes the test depend on host state
// instead of its own fixture. Forcing file-based sources keeps tests hermetic.
func TestMain(m *testing.M) {
	os.Setenv("MOAT_KEYRING_BACKEND", "file")
	os.Exit(m.Run())
}
