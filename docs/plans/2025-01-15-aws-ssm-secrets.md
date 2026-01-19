# AWS SSM Parameter Store Secrets Backend

## Overview

Add AWS Systems Manager Parameter Store as a secrets backend, following the same CLI delegation pattern as 1Password.

## URI Format

```
ssm:///parameter/path
ssm://us-west-2/parameter/path    # explicit region
```

Examples:
```yaml
secrets:
  DATABASE_URL: ssm:///production/database/url
  API_KEY: ssm:///production/myapp/api-key
  REDIS_URL: ssm://us-east-1/production/redis/url  # cross-region
```

**Notes:**
- Parameter path must start with `/`
- Region is optional; defaults to AWS CLI's configured region
- SecureString parameters are automatically decrypted

## Implementation

### New Files

#### `internal/secrets/ssm.go`

```go
package secrets

import (
	"bytes"
	"context"
	"net/url"
	"os/exec"
	"strings"
)

// SSMResolver resolves secrets from AWS Systems Manager Parameter Store.
type SSMResolver struct{}

// Scheme returns "ssm".
func (r *SSMResolver) Scheme() string {
	return "ssm"
}

// Resolve fetches a parameter using `aws ssm get-parameter`.
func (r *SSMResolver) Resolve(ctx context.Context, reference string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	// Check aws CLI is available
	if _, err := exec.LookPath("aws"); err != nil {
		return "", &BackendError{
			Backend: "AWS SSM",
			Reason:  "aws CLI not found in PATH",
			Fix:     "Install from https://aws.amazon.com/cli/",
		}
	}

	// Parse reference: ssm:///path or ssm://region/path
	region, paramPath, err := parseSSMReference(reference)
	if err != nil {
		return "", err
	}

	// Build command
	args := []string{
		"ssm", "get-parameter",
		"--name", paramPath,
		"--with-decryption",
		"--query", "Parameter.Value",
		"--output", "text",
	}
	if region != "" {
		args = append(args, "--region", region)
	}

	cmd := exec.CommandContext(ctx, "aws", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", r.parseAWSError(stderr.Bytes(), reference, paramPath)
	}

	return strings.TrimSpace(stdout.String()), nil
}

// parseSSMReference extracts region and parameter path from ssm:// URI.
// ssm:///path/to/param -> ("", "/path/to/param")
// ssm://us-west-2/path/to/param -> ("us-west-2", "/path/to/param")
func parseSSMReference(ref string) (region, path string, err error) {
	u, err := url.Parse(ref)
	if err != nil {
		return "", "", &InvalidReferenceError{
			Reference: ref,
			Reason:    "invalid URI",
		}
	}

	if u.Scheme != "ssm" {
		return "", "", &InvalidReferenceError{
			Reference: ref,
			Reason:    "expected ssm:// scheme",
		}
	}

	// Host is region (empty for ssm:///path)
	region = u.Host

	// Path must start with /
	path = u.Path
	if path == "" || path[0] != '/' {
		return "", "", &InvalidReferenceError{
			Reference: ref,
			Reason:    "parameter path must start with /",
		}
	}

	return region, path, nil
}

// parseAWSError converts AWS CLI errors to actionable error types.
func (r *SSMResolver) parseAWSError(stderr []byte, reference, paramPath string) error {
	msg := string(stderr)

	// Parameter not found
	if strings.Contains(msg, "ParameterNotFound") {
		return &NotFoundError{
			Reference: reference,
			Backend:   "AWS SSM",
		}
	}

	// Access denied
	if strings.Contains(msg, "AccessDeniedException") {
		return &BackendError{
			Backend:   "AWS SSM",
			Reference: reference,
			Reason:    "access denied",
			Fix:       "Check IAM permissions for ssm:GetParameter on " + paramPath,
		}
	}

	// Expired credentials
	if strings.Contains(msg, "ExpiredToken") || strings.Contains(msg, "ExpiredTokenException") {
		return &BackendError{
			Backend:   "AWS SSM",
			Reference: reference,
			Reason:    "AWS credentials expired",
			Fix:       "Run: aws sso login\nOr refresh your credentials.",
		}
	}

	// No credentials
	if strings.Contains(msg, "Unable to locate credentials") {
		return &BackendError{
			Backend:   "AWS SSM",
			Reference: reference,
			Reason:    "no AWS credentials found",
			Fix:       "Configure credentials:\n  aws configure\n  Or set AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY\n  Or run: aws sso login",
		}
	}

	// Invalid region
	if strings.Contains(msg, "Could not connect to the endpoint URL") {
		return &BackendError{
			Backend:   "AWS SSM",
			Reference: reference,
			Reason:    "could not connect to AWS endpoint",
			Fix:       "Check your region setting and network connectivity.",
		}
	}

	// Generic error
	return &BackendError{
		Backend:   "AWS SSM",
		Reference: reference,
		Reason:    strings.TrimSpace(msg),
	}
}

func init() {
	Register(&SSMResolver{})
}
```

#### `internal/secrets/ssm_test.go`

```go
package secrets

import (
	"testing"
)

func TestParseSSMReference(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantRegion string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "simple path",
			ref:        "ssm:///production/database/url",
			wantRegion: "",
			wantPath:   "/production/database/url",
		},
		{
			name:       "with region",
			ref:        "ssm://us-west-2/production/api-key",
			wantRegion: "us-west-2",
			wantPath:   "/production/api-key",
		},
		{
			name:       "nested path",
			ref:        "ssm:///a/b/c/d/e",
			wantRegion: "",
			wantPath:   "/a/b/c/d/e",
		},
		{
			name:    "missing leading slash",
			ref:     "ssm://us-west-2production/key",
			wantErr: true,
		},
		{
			name:    "empty path",
			ref:     "ssm://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region, path, err := parseSSMReference(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if region != tt.wantRegion {
				t.Errorf("region = %q, want %q", region, tt.wantRegion)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}

func TestSSMResolver_Scheme(t *testing.T) {
	r := &SSMResolver{}
	if r.Scheme() != "ssm" {
		t.Errorf("Scheme() = %q, want %q", r.Scheme(), "ssm")
	}
}

func TestSSMResolver_ParseAWSError(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	tests := []struct {
		name    string
		stderr  string
		wantErr string
	}{
		{
			name:    "parameter not found",
			stderr:  "An error occurred (ParameterNotFound) when calling the GetParameter operation",
			wantErr: "secret not found",
		},
		{
			name:    "access denied",
			stderr:  "An error occurred (AccessDeniedException) when calling the GetParameter operation",
			wantErr: "access denied",
		},
		{
			name:    "expired token",
			stderr:  "An error occurred (ExpiredTokenException) when calling the GetParameter operation",
			wantErr: "credentials expired",
		},
		{
			name:    "no credentials",
			stderr:  "Unable to locate credentials. You can configure credentials by running \"aws configure\".",
			wantErr: "no AWS credentials found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := r.parseAWSError([]byte(tt.stderr), ref, "/test/param")
			if err == nil {
				t.Fatal("expected error")
			}
			if !contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q should contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchString(s, substr)))
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
```

#### `internal/secrets/integration_ssm_test.go`

```go
//go:build integration

package secrets

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSSMResolver_Integration(t *testing.T) {
	// Skip if aws CLI not available
	if _, err := exec.LookPath("aws"); err != nil {
		t.Skip("aws CLI not installed, skipping integration test")
	}

	// Skip if not authenticated (check with aws sts get-caller-identity)
	cmd := exec.Command("aws", "sts", "get-caller-identity")
	if err := cmd.Run(); err != nil {
		t.Skip("not authenticated to AWS, skipping integration test")
	}

	// Configure via environment variables:
	//   SSM_TEST_PARAM - parameter path (default: "/moat/test/secret")
	//   SSM_TEST_REGION - AWS region (optional)
	paramPath := os.Getenv("SSM_TEST_PARAM")
	if paramPath == "" {
		paramPath = "/moat/test/secret"
	}

	testRef := "ssm://" + paramPath
	if region := os.Getenv("SSM_TEST_REGION"); region != "" {
		testRef = "ssm://" + region + paramPath
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resolver := &SSMResolver{}
	val, err := resolver.Resolve(ctx, testRef)
	if err != nil {
		t.Fatalf("failed to resolve %s: %v", testRef, err)
	}

	if val == "" {
		t.Error("resolved value is empty")
	}

	t.Logf("Successfully resolved secret (length: %d)", len(val))
}
```

### Modified Files

#### `README.md`

Update the supported backends table:

```markdown
| Backend | URI Scheme | Required CLI |
|---------|------------|--------------|
| 1Password | `op://vault/item/field` | `op` (`brew install 1password-cli`) |
| AWS SSM | `ssm:///parameter/path` | `aws` (`brew install awscli`) |
```

Update optional dependencies:

```markdown
**Optional dependencies:**
- **1Password CLI** - Required for 1Password secrets (`brew install 1password-cli`)
- **AWS CLI** - Required for AWS SSM secrets (`brew install awscli`)
```

#### `examples/secrets-ssm/agent.yaml`

```yaml
# Example: Using AWS SSM Parameter Store for secrets
#
# Prerequisites:
#   1. Install AWS CLI: brew install awscli
#   2. Configure credentials: aws configure
#   3. Create test parameters:
#      aws ssm put-parameter --name "/myapp/api-key" --value "sk-xxx" --type SecureString
#      aws ssm put-parameter --name "/myapp/database-url" --value "postgres://..." --type SecureString

agent: my-agent

dependencies:
  - node@20

secrets:
  API_KEY: ssm:///myapp/api-key
  DATABASE_URL: ssm:///myapp/database-url
  # Cross-region secret:
  # ANALYTICS_KEY: ssm://us-east-1/shared/analytics-key

env:
  NODE_ENV: production
```

## Tasks

1. [ ] Create `internal/secrets/ssm.go` with SSMResolver
2. [ ] Create `internal/secrets/ssm_test.go` with unit tests
3. [ ] Create `internal/secrets/integration_ssm_test.go` with integration test
4. [ ] Update README.md with SSM documentation
5. [ ] Create `examples/secrets-ssm/agent.yaml` example
6. [ ] Run all tests
7. [ ] Manual test with real AWS account

## Testing

```bash
# Unit tests
go test ./internal/secrets/... -v

# Integration test (requires AWS credentials and test parameter)
# First create: aws ssm put-parameter --name "/moat/test/secret" --value "test-value" --type SecureString
go test -tags=integration -v ./internal/secrets/... -run SSM
```

## Estimated Size

- ~150 lines of code (resolver + tests)
- 0 bytes additional binary size (CLI delegation)
