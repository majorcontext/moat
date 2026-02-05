package credential

import (
	"fmt"
	"strings"
)

// init registers AWS implied deps here (in the credential package) rather than
// in a separate provider package because AWS doesn't have a ProviderSetup — it
// uses the credential endpoint pattern instead of header injection.
func init() {
	RegisterImpliedDeps(ProviderAWS, AWSImpliedDeps)
}

// ParseRoleARN validates an IAM role ARN and returns an AWSConfig.
// ARN format: arn:PARTITION:iam::ACCOUNT_ID:role/ROLE_NAME
// Supported partitions: aws, aws-cn, aws-us-gov
func ParseRoleARN(arn string) (*AWSConfig, error) {
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

	return &AWSConfig{
		RoleARN: arn,
		Region:  "us-east-1", // default region
	}, nil
}

// AWSImpliedDeps returns the dependencies implied by an AWS grant.
func AWSImpliedDeps() []string {
	return []string{"aws"}
}
