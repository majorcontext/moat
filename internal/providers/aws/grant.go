package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/provider/util"
	"github.com/majorcontext/moat/internal/ui"
)

// Metadata keys for AWS credentials.
const (
	MetaKeyRegion          = "region"
	MetaKeySessionDuration = "session_duration"
	MetaKeyExternalID      = "external_id"
	MetaKeyProfile         = "profile"
)

// Default values.
const (
	DefaultRegion          = "us-east-1"
	DefaultSessionDuration = 15 * time.Minute
)

// Context keys for passing grant options from CLI.
type ctxKey string

const (
	ctxKeyRole            ctxKey = "aws_role"
	ctxKeyRegion          ctxKey = "aws_region"
	ctxKeySessionDuration ctxKey = "aws_session_duration"
	ctxKeyExternalID      ctxKey = "aws_external_id"
	ctxKeyProfile         ctxKey = "aws_profile"
)

// WithGrantOptions returns a context with AWS grant options set.
// These options are used by Grant() instead of prompting interactively.
func WithGrantOptions(ctx context.Context, role, region, sessionDuration, externalID, profile string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyRole, role)
	ctx = context.WithValue(ctx, ctxKeyRegion, region)
	ctx = context.WithValue(ctx, ctxKeySessionDuration, sessionDuration)
	ctx = context.WithValue(ctx, ctxKeyExternalID, externalID)
	ctx = context.WithValue(ctx, ctxKeyProfile, profile)
	return ctx
}

// Config holds AWS IAM role configuration.
type Config struct {
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
	Profile         string // AWS shared config profile (AWS_PROFILE) used to assume the role
}

// grant acquires AWS credentials by prompting for an IAM role ARN.
func grant(ctx context.Context) (*provider.Credential, error) {
	var roleARN string
	var err error

	// Check for role ARN in context (from CLI flags)
	if v, ok := ctx.Value(ctxKeyRole).(string); ok && v != "" {
		roleARN = v
	}

	// Prompt if not provided via context
	if roleARN == "" {
		roleARN, err = util.PromptForToken("Enter IAM role ARN")
		if err != nil {
			return nil, &provider.GrantError{
				Provider: "aws",
				Cause:    err,
				Hint:     "The role ARN should be in format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME",
			}
		}
	}

	// Parse and validate ARN
	cfg, err := ParseRoleARN(roleARN)
	if err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint:     "Example: arn:aws:iam::123456789012:role/MyRole",
		}
	}

	// Apply overrides from context
	if v, ok := ctx.Value(ctxKeyRegion).(string); ok && v != "" {
		cfg.Region = v
	}
	if v, ok := ctx.Value(ctxKeySessionDuration).(string); ok && v != "" {
		if d, parseErr := time.ParseDuration(v); parseErr == nil {
			cfg.SessionDuration = d
		}
	}
	if v, ok := ctx.Value(ctxKeyExternalID).(string); ok && v != "" {
		cfg.ExternalID = v
	}

	// Capture AWS profile: explicit flag takes precedence, then AWS_PROFILE env var.
	// This is stored with the credential so the proxy daemon can assume the role
	// using the correct source identity regardless of its own environment.
	if v, ok := ctx.Value(ctxKeyProfile).(string); ok && v != "" {
		cfg.Profile = v
	} else if v := os.Getenv("AWS_PROFILE"); v != "" {
		cfg.Profile = v
		ui.Infof("Using AWS profile from AWS_PROFILE: %s (stored with credential)", v)
	}

	// Test AssumeRole to verify the role is accessible
	if err := testAssumeRole(ctx, cfg); err != nil {
		return nil, &provider.GrantError{
			Provider: "aws",
			Cause:    err,
			Hint: "Ensure you have permission to assume this role and that your AWS credentials are configured.\n" +
				"See: https://majorcontext.com/moat/concepts/credentials#aws",
		}
	}

	// Build credential with role ARN as token and config as metadata
	cred := &provider.Credential{
		Provider:  "aws",
		Token:     cfg.RoleARN,
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			MetaKeyRegion:          cfg.Region,
			MetaKeySessionDuration: cfg.SessionDuration.String(),
		},
	}

	if cfg.Profile != "" {
		cred.Metadata[MetaKeyProfile] = cfg.Profile
	}
	if cfg.ExternalID != "" {
		cred.Metadata[MetaKeyExternalID] = cfg.ExternalID
	}

	return cred, nil
}

// ParseRoleARN validates an IAM role ARN and returns a Config.
// ARN format: arn:PARTITION:iam::ACCOUNT_ID:role/ROLE_NAME
// Supported partitions: aws, aws-cn, aws-us-gov
func ParseRoleARN(arn string) (*Config, error) {
	if arn == "" {
		return nil, fmt.Errorf("role ARN is required")
	}

	parts := strings.Split(arn, ":")
	if len(parts) != 6 {
		return nil, fmt.Errorf("invalid ARN format: expected 6 colon-separated parts, got %d", len(parts))
	}

	prefix, partition, service, _, account, resource := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]

	if prefix != "arn" {
		return nil, fmt.Errorf("invalid ARN: must start with 'arn:'")
	}

	// Validate partition
	switch partition {
	case "aws", "aws-cn", "aws-us-gov":
		// valid
	default:
		return nil, fmt.Errorf("invalid ARN partition: %s (expected aws, aws-cn, or aws-us-gov)", partition)
	}

	if service != "iam" {
		return nil, fmt.Errorf("invalid ARN: must be an IAM ARN (got %s)", service)
	}

	if account == "" {
		return nil, fmt.Errorf("invalid ARN: account ID is required")
	}

	if !strings.HasPrefix(resource, "role/") {
		return nil, fmt.Errorf("invalid ARN: must be a role ARN (got %s)", resource)
	}

	roleName := strings.TrimPrefix(resource, "role/")
	if roleName == "" {
		return nil, fmt.Errorf("invalid ARN: role name is required")
	}

	return &Config{
		RoleARN:         arn,
		Region:          DefaultRegion,
		SessionDuration: DefaultSessionDuration,
	}, nil
}

// testAssumeRole verifies the role can be assumed with current AWS credentials.
func testAssumeRole(ctx context.Context, cfg *Config) error {
	// Load AWS config from environment, using the explicit profile if set.
	opts := []func(*config.LoadOptions) error{config.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(cfg.Profile))
	}
	awsCfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return fmt.Errorf("loading AWS config: %w", err)
	}

	stsClient := sts.NewFromConfig(awsCfg)

	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(cfg.RoleARN),
		RoleSessionName: aws.String("moat-grant-test"),
		DurationSeconds: aws.Int32(int32(cfg.SessionDuration.Seconds())),
	}

	if cfg.ExternalID != "" {
		input.ExternalId = aws.String(cfg.ExternalID)
	}

	_, err = stsClient.AssumeRole(ctx, input)
	if err != nil {
		return fmt.Errorf("assuming role: %w", err)
	}

	return nil
}

// ConfigFromCredential extracts Config from a stored credential.
// Supports both new format (Metadata) and legacy format (Scopes) for backwards compatibility.
func ConfigFromCredential(cred *provider.Credential) (*Config, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}

	cfg := &Config{
		RoleARN: cred.Token,
		Region:  DefaultRegion,
	}

	// Try new Metadata format first
	if cred.Metadata != nil {
		if region := cred.Metadata[MetaKeyRegion]; region != "" {
			cfg.Region = region
		}

		if durationStr := cred.Metadata[MetaKeySessionDuration]; durationStr != "" {
			d, err := time.ParseDuration(durationStr)
			if err != nil {
				return nil, fmt.Errorf("invalid session duration %q: %w", durationStr, err)
			}
			cfg.SessionDuration = d
		}

		if externalID := cred.Metadata[MetaKeyExternalID]; externalID != "" {
			cfg.ExternalID = externalID
		}
		if profile := cred.Metadata[MetaKeyProfile]; profile != "" {
			cfg.Profile = profile
		}
	}

	// Fallback to legacy Scopes format: [region, sessionDuration, externalID]
	if cfg.Region == DefaultRegion && len(cred.Scopes) > 0 && cred.Scopes[0] != "" {
		cfg.Region = cred.Scopes[0]
	}
	if cfg.SessionDuration == 0 && len(cred.Scopes) > 1 && cred.Scopes[1] != "" {
		d, err := time.ParseDuration(cred.Scopes[1])
		if err == nil {
			cfg.SessionDuration = d
		}
	}
	if cfg.ExternalID == "" && len(cred.Scopes) > 2 {
		cfg.ExternalID = cred.Scopes[2]
	}

	if cfg.SessionDuration == 0 {
		cfg.SessionDuration = DefaultSessionDuration
	}

	return cfg, nil
}

// ConfigToJSON serializes Config to JSON for storage.
func ConfigToJSON(cfg *Config) (string, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshaling AWS config: %w", err)
	}
	return string(data), nil
}
