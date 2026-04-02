package credentialsource

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

// SecretsManagerClient abstracts the AWS Secrets Manager API for testing.
type SecretsManagerClient interface {
	GetSecretValue(ctx context.Context, secretID string) (string, error)
}

type awsSMSource struct {
	secretID string
	client   SecretsManagerClient
}

// NewAWSSecretsManagerSource creates a CredentialSource backed by AWS Secrets Manager.
func NewAWSSecretsManagerSource(secretID, region string) (CredentialSource, error) {
	client, err := newRealSMClient(region)
	if err != nil {
		return nil, fmt.Errorf("creating AWS Secrets Manager client: %w", err)
	}
	return newAWSSecretsManagerSourceWithClient(secretID, client), nil
}

func newAWSSecretsManagerSourceWithClient(secretID string, client SecretsManagerClient) CredentialSource {
	return &awsSMSource{secretID: secretID, client: client}
}

func (s *awsSMSource) Fetch(ctx context.Context) (string, error) {
	return s.client.GetSecretValue(ctx, s.secretID)
}

func (s *awsSMSource) Type() string { return "aws-secretsmanager" }

// realSMClient wraps the AWS SDK Secrets Manager client.
type realSMClient struct {
	client *secretsmanager.Client
}

func newRealSMClient(region string) (*realSMClient, error) {
	opts := []func(*awsconfig.LoadOptions) error{}
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	return &realSMClient{client: secretsmanager.NewFromConfig(cfg)}, nil
}

func (c *realSMClient) GetSecretValue(ctx context.Context, secretID string) (string, error) {
	out, err := c.client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: aws.String(secretID),
	})
	if err != nil {
		return "", err
	}
	if out.SecretString == nil {
		return "", fmt.Errorf("secret %s has no string value", secretID)
	}
	return *out.SecretString, nil
}
