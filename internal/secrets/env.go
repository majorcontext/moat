package secrets

import (
	"context"
	"os"
	"strings"
)

// EnvResolver resolves secrets by reading host environment variables.
// This is less secure than external secret backends but convenient for
// low-risk secrets and local development.
type EnvResolver struct{}

// Scheme returns "env".
func (r *EnvResolver) Scheme() string {
	return "env"
}

// Resolve reads the named environment variable from the host.
// Reference format: env://VAR_NAME
// Returns an error if the variable is not set.
func (r *EnvResolver) Resolve(ctx context.Context, reference string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	if !strings.HasPrefix(reference, "env://") {
		return "", &InvalidReferenceError{
			Reference: reference,
			Reason:    "env references must start with env://",
		}
	}

	varName := strings.TrimPrefix(reference, "env://")
	if varName == "" {
		return "", &InvalidReferenceError{
			Reference: reference,
			Reason:    "variable name cannot be empty",
		}
	}

	val, ok := os.LookupEnv(varName)
	if !ok {
		return "", &NotFoundError{
			Reference: reference,
			Backend:   "host environment",
		}
	}

	return val, nil
}

func init() {
	Register(&EnvResolver{})
}
