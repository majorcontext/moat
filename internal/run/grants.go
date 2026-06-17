package run

import (
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/mcpcatalog"
	"github.com/majorcontext/moat/internal/provider"
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

// MissingReason explains why a grant is unavailable.
type MissingReason int

const (
	ReasonNotConfigured   MissingReason = iota // no credential stored
	ReasonDecryptFailed                        // stored but encryption key changed
	ReasonUnknownProvider                      // typo / not a registered provider
)

// MissingGrant describes a grant a run needs but does not have.
type MissingGrant struct {
	Grant      string // e.g. "oauth:notion", "mcp:render"
	Reason     MissingReason
	FixCommand string // e.g. "moat grant oauth notion"
	Promptable bool   // can be granted via an inline interactive flow
}

// classifyMissingReason maps a store.Get error to a MissingReason. A decrypt
// failure means the credential exists but the encryption key changed; anything
// else is treated as not configured. Shared by the generic and MCP detection
// paths so they classify identically.
func classifyMissingReason(err error) MissingReason {
	if strings.Contains(err.Error(), "decrypting credential") {
		return ReasonDecryptFailed
	}
	return ReasonNotConfigured
}

// DetectMissingGrants returns the grants a run needs but does not have. It
// mirrors the per-grant checks in validateGrants (generic) and
// validateMCPGrants (MCP) without formatting an error, so the CLI can prompt.
// SSH grants are intentionally ignored (handled later in Create; out of v1 scope).
func DetectMissingGrants(grants []string, cfg *config.Config, store *credential.FileStore) []MissingGrant {
	var missing []MissingGrant
	seen := map[string]bool{}

	add := func(m MissingGrant) {
		if seen[m.Grant] {
			return
		}
		seen[m.Grant] = true
		missing = append(missing, m)
	}

	// Generic grants (skips ssh and mcp, which have dedicated handling below).
	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]
		if grantName == "ssh" || mcpcatalog.IsGrant(grant) {
			continue
		}
		fix := "moat grant " + grantToCommand(grant)
		if provider.Get(grantName) == nil {
			add(MissingGrant{Grant: grant, Reason: ReasonUnknownProvider, FixCommand: fix, Promptable: false})
			continue
		}
		credName := credentialStoreKey(grantName, grant)
		if _, err := store.Get(credName); err != nil {
			reason := classifyMissingReason(err)
			// AWS needs mandatory flags (--role, …); cannot prompt cleanly.
			promptable := grantName != "aws"
			if grantName == "aws" {
				fix = "moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE"
			}
			add(MissingGrant{Grant: grant, Reason: reason, FixCommand: fix, Promptable: promptable})
		}
	}

	// MCP grants, mirroring validateMCPGrants (iterate cfg.MCP, not the slice).
	if cfg != nil {
		for _, mcp := range cfg.MCP {
			if mcp.Auth == nil || mcp.Auth.Grant == "" {
				continue
			}
			if _, err := store.Get(credential.Provider(mcp.Auth.Grant)); err != nil {
				add(MissingGrant{
					Grant:      mcp.Auth.Grant,
					Reason:     classifyMissingReason(err),
					FixCommand: "moat grant " + grantToCommand(mcp.Auth.Grant),
					Promptable: true,
				})
			}
		}
	}

	return missing
}
