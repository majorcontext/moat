package aws

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

// CredentialProviderConfig holds the configuration needed to create a CredentialProvider.
type CredentialProviderConfig struct {
	RoleARN         string
	Region          string
	SessionDuration time.Duration
	ExternalID      string
	Profile         string // AWS shared config profile (AWS_PROFILE) used to assume the role
}

// CredentialProvider manages AWS credential fetching and caching for proxy use.
// It creates an http.Handler that serves credentials in ECS container format.
type CredentialProvider struct {
	roleARN         string
	region          string
	sessionDuration time.Duration
	externalID      string
	sessionName     string
	authToken       string // Auth token for credential endpoint

	mu         sync.RWMutex
	cached     *Credentials
	expiration time.Time

	// stsClient for making AssumeRole calls (injectable for testing)
	stsClient STSAssumeRoler
}

// NewCredentialProvider creates a new AWS credential provider.
// If cfg.Profile is non-empty, it is used as the AWS shared config profile
// (equivalent to AWS_PROFILE) so the correct source identity is used
// for AssumeRole regardless of which process creates the provider.
func NewCredentialProvider(ctx context.Context, cfg CredentialProviderConfig, sessionName string) (*CredentialProvider, error) {
	// Load AWS config from host environment
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if cfg.Profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(cfg.Profile))
		slog.Debug("AWS credential provider using named profile", "profile", cfg.Profile, "role_arn", cfg.RoleARN)
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &CredentialProvider{
		roleARN:         cfg.RoleARN,
		region:          cfg.Region,
		sessionDuration: cfg.SessionDuration,
		externalID:      cfg.ExternalID,
		sessionName:     sessionName,
		stsClient:       sts.NewFromConfig(awsCfg),
	}, nil
}

// SetAuthToken sets the required auth token for the credential endpoint.
func (p *CredentialProvider) SetAuthToken(token string) {
	p.authToken = token
}

// Handler returns an HTTP handler for serving credentials.
func (p *CredentialProvider) Handler() http.Handler {
	return &credentialProviderHandler{
		getCredentials: p.GetCredentials,
		authToken:      p.authToken,
	}
}

// Region returns the configured AWS region.
func (p *CredentialProvider) Region() string {
	return p.region
}

// RoleARN returns the configured IAM role ARN.
func (p *CredentialProvider) RoleARN() string {
	return p.roleARN
}

// GetCredentials returns cached credentials or fetches new ones.
func (p *CredentialProvider) GetCredentials(ctx context.Context) (*Credentials, error) {
	// Check context before proceeding
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	p.mu.RLock()
	// Return cached if valid with buffer before expiration
	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		creds := p.cached
		p.mu.RUnlock()
		return creds, nil
	}
	p.mu.RUnlock()

	// Need to refresh
	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(p.expiration) {
		return p.cached, nil
	}

	// Call STS AssumeRole
	input := &sts.AssumeRoleInput{
		RoleArn:         awssdk.String(p.roleARN),
		RoleSessionName: awssdk.String(p.sessionName),
		DurationSeconds: awssdk.Int32(int32(p.sessionDuration.Seconds())),
	}
	if p.externalID != "" {
		input.ExternalId = awssdk.String(p.externalID)
	}

	result, err := p.stsClient.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assuming role %s: %w", p.roleARN, err)
	}

	if result.Credentials == nil {
		return nil, fmt.Errorf("AWS returned empty credentials for role %s", p.roleARN)
	}

	p.cached = &Credentials{
		AccessKeyID:     awssdk.ToString(result.Credentials.AccessKeyId),
		SecretAccessKey: awssdk.ToString(result.Credentials.SecretAccessKey),
		SessionToken:    awssdk.ToString(result.Credentials.SessionToken),
		Expiration:      awssdk.ToTime(result.Credentials.Expiration),
	}
	p.expiration = awssdk.ToTime(result.Credentials.Expiration)

	return p.cached, nil
}

// credentialProviderHandler serves AWS credentials via HTTP in ECS container format.
type credentialProviderHandler struct {
	getCredentials func(ctx context.Context) (*Credentials, error)
	authToken      string // Required auth token (from AWS_CONTAINER_AUTHORIZATION_TOKEN)
}

// ServeHTTP implements http.Handler, returning credentials in ECS format.
func (h *credentialProviderHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Verify auth token if required
	if h.authToken != "" {
		auth := r.Header.Get("Authorization")
		expectedAuth := "Bearer " + h.authToken
		// Use constant-time comparison to prevent timing attacks
		if auth == "" || subtle.ConstantTimeCompare([]byte(auth), []byte(expectedAuth)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}

	creds, err := h.getCredentials(r.Context())
	if err != nil {
		// Log detailed error server-side but return generic message to prevent leaking sensitive info
		slog.Error("AWS credential fetch error", "error", err)
		http.Error(w, "failed to get credentials", http.StatusInternalServerError)
		return
	}

	// AWS credential_process format
	// See: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
	resp := map[string]any{
		"Version":         1,
		"AccessKeyId":     creds.AccessKeyID,
		"SecretAccessKey": creds.SecretAccessKey,
		"SessionToken":    creds.SessionToken,
		"Expiration":      creds.Expiration.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Response already started, can't send HTTP error. Log and continue.
		slog.Warn("Failed to encode AWS credentials response", "error", err)
	}
}
