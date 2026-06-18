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
	ReasonReadFailed                           // store read failed (e.g. permission denied, corrupt file)
)

// MissingGrant describes a grant a run needs but does not have.
type MissingGrant struct {
	Grant      string // e.g. "oauth:notion", "mcp:render"
	Reason     MissingReason
	FixCommand string // e.g. "moat grant oauth notion"
	Promptable bool   // can be granted via an inline interactive flow
	Detail     string // raw store error, populated for ReasonReadFailed
}

// classifyMissingReason maps a store.Get error to a MissingReason. A decrypt
// failure means the credential exists but the encryption key changed; a
// not-found error means no credential is stored. Anything else (a permission
// error or a corrupt file) is a read failure the user can't fix by re-granting
// blindly, so it is surfaced rather than prompted. Mirrors the buckets in
// validateGrants so the detector and the validator classify identically.
func classifyMissingReason(err error) MissingReason {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "decrypting credential"):
		return ReasonDecryptFailed
	case strings.Contains(msg, "credential not found"):
		return ReasonNotConfigured
	default:
		return ReasonReadFailed
	}
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
			// AWS needs mandatory flags (--role, …); cannot prompt cleanly. A
			// read failure (permission/corrupt file) isn't fixed by re-granting
			// blindly, so surface the raw error instead of prompting.
			promptable := grantName != "aws" && reason != ReasonReadFailed
			if grantName == "aws" {
				fix = "moat grant aws --role=arn:aws:iam::ACCOUNT:role/ROLE"
			}
			detail := ""
			if reason == ReasonReadFailed {
				detail = err.Error()
			}
			add(MissingGrant{Grant: grant, Reason: reason, FixCommand: fix, Promptable: promptable, Detail: detail})
		}
	}

	// MCP grants, mirroring validateMCPGrants (iterate cfg.MCP, not the slice).
	if cfg != nil {
		for _, mcp := range cfg.MCP {
			if mcp.Auth == nil || mcp.Auth.Grant == "" {
				continue
			}
			if _, err := store.Get(credential.Provider(mcp.Auth.Grant)); err != nil {
				reason := classifyMissingReason(err)
				detail := ""
				if reason == ReasonReadFailed {
					detail = err.Error()
				}
				add(MissingGrant{
					Grant:      mcp.Auth.Grant,
					Reason:     reason,
					FixCommand: "moat grant " + grantToCommand(mcp.Auth.Grant),
					Promptable: reason != ReasonReadFailed,
					Detail:     detail,
				})
			}
		}
	}

	return missing
}
