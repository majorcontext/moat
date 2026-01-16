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
		return &BackendError{
			Backend:   "AWS SSM",
			Reference: reference,
			Reason:    "parameter not found",
			Fix:       "Create it with:\n  aws ssm put-parameter --name \"" + paramPath + "\" --value \"your-value\" --type SecureString\n\nOr list existing parameters:\n  aws ssm describe-parameters --query 'Parameters[].Name'",
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
