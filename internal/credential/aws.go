package credential

import (
	"fmt"
	"strings"
)

// ParseRoleARN validates an IAM role ARN and returns an AWSConfig.
// ARN format: arn:aws:iam::ACCOUNT_ID:role/ROLE_NAME
func ParseRoleARN(arn string) (*AWSConfig, error) {
	if arn == "" {
		return nil, fmt.Errorf("role ARN is required")
	}

	parts := strings.Split(arn, ":")
	if len(parts) < 6 {
		return nil, fmt.Errorf("invalid ARN format: %s", arn)
	}

	if parts[0] != "arn" {
		return nil, fmt.Errorf("invalid ARN: must start with 'arn:'")
	}

	if parts[2] != "iam" {
		return nil, fmt.Errorf("invalid ARN: must be an IAM ARN (got %s)", parts[2])
	}

	resource := parts[5]
	if !strings.HasPrefix(resource, "role/") {
		return nil, fmt.Errorf("invalid ARN: must be a role ARN (got %s)", resource)
	}

	return &AWSConfig{
		RoleARN: arn,
		Region:  "us-east-1", // default region
	}, nil
}
