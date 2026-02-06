package proxy

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
)

// credentialRefreshBuffer is the time before expiration when credentials should be refreshed.
const credentialRefreshBuffer = 5 * time.Minute

// AWSCredentials holds temporary AWS credentials.
type AWSCredentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// AWSCredentialHandler serves AWS credentials via HTTP in ECS container format.
type AWSCredentialHandler struct {
	getCredentials func(ctx context.Context) (*AWSCredentials, error)
	authToken      string // Required auth token (from AWS_CONTAINER_AUTHORIZATION_TOKEN)
}

// ServeHTTP implements http.Handler, returning credentials in ECS format.
func (h *AWSCredentialHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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
		log.Error("AWS credential fetch error", "error", err)
		http.Error(w, "failed to get credentials", http.StatusInternalServerError)
		return
	}

	// AWS credential_process format
	// See: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
	resp := map[string]interface{}{
		"Version":         1,
		"AccessKeyId":     creds.AccessKeyID,
		"SecretAccessKey": creds.SecretAccessKey,
		"SessionToken":    creds.SessionToken,
		"Expiration":      creds.Expiration.Format(time.RFC3339),
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		// Response already started, can't send HTTP error. Log and continue.
		ui.Warnf("Failed to encode AWS credentials response: %v", err)
	}
}

// STSAssumeRoler interface for STS AssumeRole operation (enables testing).
type STSAssumeRoler interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// AWSCredentialProvider manages AWS credential fetching and caching.
type AWSCredentialProvider struct {
	roleARN         string
	region          string
	sessionDuration time.Duration
	externalID      string
	sessionName     string
	authToken       string // Auth token for credential endpoint

	mu         sync.RWMutex
	cached     *AWSCredentials
	expiration time.Time

	// stsClient for making AssumeRole calls (injectable for testing)
	stsClient STSAssumeRoler
}

// NewAWSCredentialProvider creates a new AWS credential provider.
func NewAWSCredentialProvider(ctx context.Context, roleARN, region string, sessionDuration time.Duration, externalID, sessionName string) (*AWSCredentialProvider, error) {
	// Load AWS config from host environment
	cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
	if err != nil {
		return nil, fmt.Errorf("loading AWS config: %w", err)
	}

	return &AWSCredentialProvider{
		roleARN:         roleARN,
		region:          region,
		sessionDuration: sessionDuration,
		externalID:      externalID,
		sessionName:     sessionName,
		stsClient:       sts.NewFromConfig(cfg),
	}, nil
}

// SetAuthToken sets the required auth token for the credential endpoint.
func (p *AWSCredentialProvider) SetAuthToken(token string) {
	p.authToken = token
}

// Handler returns an HTTP handler for serving credentials.
func (p *AWSCredentialProvider) Handler() http.Handler {
	return &AWSCredentialHandler{
		getCredentials: p.GetCredentials,
		authToken:      p.authToken,
	}
}

// Region returns the configured AWS region.
func (p *AWSCredentialProvider) Region() string {
	return p.region
}

// RoleARN returns the configured IAM role ARN.
func (p *AWSCredentialProvider) RoleARN() string {
	return p.roleARN
}

// GetCredentials returns cached credentials or fetches new ones.
func (p *AWSCredentialProvider) GetCredentials(ctx context.Context) (*AWSCredentials, error) {
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
		RoleArn:         aws.String(p.roleARN),
		RoleSessionName: aws.String(p.sessionName),
		DurationSeconds: aws.Int32(int32(p.sessionDuration.Seconds())),
	}
	if p.externalID != "" {
		input.ExternalId = aws.String(p.externalID)
	}

	result, err := p.stsClient.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assuming role %s: %w", p.roleARN, err)
	}

	if result.Credentials == nil {
		return nil, fmt.Errorf("AWS returned empty credentials for role %s", p.roleARN)
	}

	p.cached = &AWSCredentials{
		AccessKeyID:     aws.ToString(result.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(result.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(result.Credentials.SessionToken),
		Expiration:      aws.ToTime(result.Credentials.Expiration),
	}
	p.expiration = aws.ToTime(result.Credentials.Expiration)

	return p.cached, nil
}
