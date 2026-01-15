package secrets

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
)

// OnePasswordResolver resolves secrets from 1Password using the op CLI.
type OnePasswordResolver struct{}

// Scheme returns "op".
func (r *OnePasswordResolver) Scheme() string {
	return "op"
}

// Resolve fetches a secret using `op read`.
func (r *OnePasswordResolver) Resolve(ctx context.Context, reference string) (string, error) {
	// Check for context cancellation before expensive operations
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Check op CLI is available
	if _, err := exec.LookPath("op"); err != nil {
		return "", &BackendError{
			Backend: "1Password",
			Reason:  "op CLI not found in PATH",
			Fix:     "Install from https://1password.com/downloads/command-line/\nThen run: op signin",
		}
	}

	cmd := exec.CommandContext(ctx, "op", "read", reference)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", r.parseOpError(stderr.Bytes(), reference)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// parseOpError converts op CLI errors to actionable error types.
func (r *OnePasswordResolver) parseOpError(stderr []byte, reference string) error {
	msg := string(stderr)

	// Not signed in
	if strings.Contains(msg, "not currently signed in") || strings.Contains(msg, "not signed in") {
		return &BackendError{
			Backend:   "1Password",
			Reference: reference,
			Reason:    "not signed in",
			Fix:       "Run: eval $(op signin)\n\nOr for CI/automation, set OP_SERVICE_ACCOUNT_TOKEN.",
		}
	}

	// Item not found
	if strings.Contains(msg, "isn't an item") || strings.Contains(msg, "could not be found") {
		return &NotFoundError{
			Reference: reference,
			Backend:   "1Password",
		}
	}

	// Vault not found / access denied
	if strings.Contains(msg, "isn't a vault") || (strings.Contains(msg, "vault") && strings.Contains(msg, "not found")) {
		// Extract vault name from reference: op://VaultName/Item/Field
		parts := strings.Split(strings.TrimPrefix(reference, "op://"), "/")
		vaultName := "unknown"
		if len(parts) > 0 {
			vaultName = parts[0]
		}
		return &BackendError{
			Backend:   "1Password",
			Reference: reference,
			Reason:    "vault not found or not accessible",
			Fix:       "Vault \"" + vaultName + "\" not found.\n\nList available vaults with: op vault list",
		}
	}

	// Generic error
	return &BackendError{
		Backend:   "1Password",
		Reference: reference,
		Reason:    strings.TrimSpace(msg),
	}
}

func init() {
	Register(&OnePasswordResolver{})
}
