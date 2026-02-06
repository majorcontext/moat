package aws

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
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/ui"
)

// credentialRefreshBuffer is the time before expiration when credentials should be refreshed.
const credentialRefreshBuffer = 5 * time.Minute

// Credentials holds temporary AWS credentials.
type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Expiration      time.Time
}

// EndpointHandler serves AWS credentials via HTTP in ECS container format.
type EndpointHandler struct {
	cfg       *Config
	authToken string // Optional auth token for endpoint security

	mu         sync.RWMutex
	cached     *Credentials
	expiration time.Time

	// stsClient for making AssumeRole calls (injectable for testing)
	stsClient STSAssumeRoler
}

// STSAssumeRoler interface for STS AssumeRole operation (enables testing).
type STSAssumeRoler interface {
	AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

// NewEndpointHandler creates a new AWS credential endpoint handler.
func NewEndpointHandler(cred *provider.Credential) *EndpointHandler {
	cfg, err := ConfigFromCredential(cred)
	if err != nil {
		// Log error but create handler with minimal config
		ui.Warnf("Failed to parse AWS config from credential: %v", err)
		cfg = &Config{
			RoleARN:         cred.Token,
			Region:          DefaultRegion,
			SessionDuration: DefaultSessionDuration,
		}
	}

	return &EndpointHandler{
		cfg: cfg,
	}
}

// SetAuthToken sets the required auth token for the credential endpoint.
func (h *EndpointHandler) SetAuthToken(token string) {
	h.authToken = token
}

// SetSTSClient sets a custom STS client (for testing).
func (h *EndpointHandler) SetSTSClient(client STSAssumeRoler) {
	h.stsClient = client
}

// ServeHTTP implements http.Handler, returning credentials in credential_process format.
func (h *EndpointHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// getCredentials returns cached credentials or fetches new ones via STS AssumeRole.
func (h *EndpointHandler) getCredentials(ctx context.Context) (*Credentials, error) {
	// Check context before proceeding
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	h.mu.RLock()
	// Return cached if valid with buffer before expiration
	if h.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(h.expiration) {
		creds := h.cached
		h.mu.RUnlock()
		return creds, nil
	}
	h.mu.RUnlock()

	// Need to refresh
	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check after acquiring write lock
	if h.cached != nil && time.Now().Add(credentialRefreshBuffer).Before(h.expiration) {
		return h.cached, nil
	}

	// Initialize STS client if needed
	if h.stsClient == nil {
		awsCfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(h.cfg.Region))
		if err != nil {
			return nil, fmt.Errorf("loading AWS config: %w", err)
		}
		h.stsClient = sts.NewFromConfig(awsCfg)
	}

	// Call STS AssumeRole
	sessionName := "moat-" + fmt.Sprintf("%d", time.Now().Unix())
	input := &sts.AssumeRoleInput{
		RoleArn:         aws.String(h.cfg.RoleARN),
		RoleSessionName: aws.String(sessionName),
		DurationSeconds: aws.Int32(int32(h.cfg.SessionDuration.Seconds())),
	}
	if h.cfg.ExternalID != "" {
		input.ExternalId = aws.String(h.cfg.ExternalID)
	}

	result, err := h.stsClient.AssumeRole(ctx, input)
	if err != nil {
		return nil, fmt.Errorf("assuming role %s: %w", h.cfg.RoleARN, err)
	}

	if result.Credentials == nil {
		return nil, fmt.Errorf("AWS returned empty credentials for role %s", h.cfg.RoleARN)
	}

	h.cached = &Credentials{
		AccessKeyID:     aws.ToString(result.Credentials.AccessKeyId),
		SecretAccessKey: aws.ToString(result.Credentials.SecretAccessKey),
		SessionToken:    aws.ToString(result.Credentials.SessionToken),
		Expiration:      aws.ToTime(result.Credentials.Expiration),
	}
	h.expiration = aws.ToTime(result.Credentials.Expiration)

	return h.cached, nil
}

// Region returns the configured AWS region.
func (h *EndpointHandler) Region() string {
	return h.cfg.Region
}

// RoleARN returns the configured IAM role ARN.
func (h *EndpointHandler) RoleARN() string {
	return h.cfg.RoleARN
}
