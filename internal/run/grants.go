package run

import (
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

// GrantToCommand maps a grant string to its "moat grant <args>" form.
// Exported wrapper over grantToCommand for use by the CLI prompt layer.
func GrantToCommand(grant string) string { return grantToCommand(grant) }

// AppendMCPGrants returns grants plus any cfg MCP auth grants not already
// present. Exported wrapper over appendMCPGrants for the CLI pre-flight, which
// must detect against the same grant set Create builds.
func AppendMCPGrants(grants []string, cfg *config.Config) []string {
	return appendMCPGrants(grants, cfg)
}

// OpenDefaultStore opens the default-profile credential store. Mirrors the
// store construction inside Create so the CLI pre-flight reads the same source.
func OpenDefaultStore() (*credential.FileStore, error) {
	key, err := credential.DefaultEncryptionKey()
	if err != nil {
		return nil, err
	}
	return credential.NewFileStore(credential.DefaultStoreDir(), key)
}
