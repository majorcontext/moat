package gcloud

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/ui"
)

// Metadata keys for gcloud credentials.
const (
	MetaKeyProject     = "project"
	MetaKeyScopes      = "scopes"
	MetaKeyImpersonate = "impersonate"
	MetaKeyKeyFile     = "key_file"
	MetaKeyEmail       = "email"
)

// DefaultScope is the default OAuth scope requested for Google Cloud credentials.
const DefaultScope = "https://www.googleapis.com/auth/cloud-platform"

// Context keys for passing grant options from CLI.
type ctxKey string

const (
	ctxKeyProject     ctxKey = "gcloud_project"
	ctxKeyScopes      ctxKey = "gcloud_scopes"
	ctxKeyImpersonate ctxKey = "gcloud_impersonate"
	ctxKeyKeyFile     ctxKey = "gcloud_key_file"
)

// WithGrantOptions returns a context with gcloud grant options set.
// These options are used by Grant() instead of prompting interactively.
func WithGrantOptions(ctx context.Context, project, scopes, impersonate, keyFile string) context.Context {
	ctx = context.WithValue(ctx, ctxKeyProject, project)
	ctx = context.WithValue(ctx, ctxKeyScopes, scopes)
	ctx = context.WithValue(ctx, ctxKeyImpersonate, impersonate)
	ctx = context.WithValue(ctx, ctxKeyKeyFile, keyFile)
	return ctx
}

// Config holds Google Cloud credential configuration.
type Config struct {
	ProjectID     string
	Scopes        []string
	ImpersonateSA string // service account email to impersonate
	KeyFile       string // path to service account key file
	Email         string // email of the authenticated identity
}

// getenv is a variable for testing. Default implementation uses os.Getenv.
var getenv = os.Getenv

// grant acquires Google Cloud credentials from the host environment.
func grant(ctx context.Context) (*provider.Credential, error) {
	cfg := &Config{
		Scopes: []string{DefaultScope},
	}

	// Read options from context (set by CLI flags).
	if v, ok := ctx.Value(ctxKeyProject).(string); ok && v != "" {
		cfg.ProjectID = v
	}
	if v, ok := ctx.Value(ctxKeyScopes).(string); ok && v != "" {
		cfg.Scopes = splitScopes(v)
	}
	if v, ok := ctx.Value(ctxKeyImpersonate).(string); ok && v != "" {
		cfg.ImpersonateSA = v
	}
	if v, ok := ctx.Value(ctxKeyKeyFile).(string); ok && v != "" {
		cfg.KeyFile = v
	}

	// Detect project if not provided.
	if cfg.ProjectID == "" {
		project, err := detectProject()
		if err != nil {
			return nil, &provider.GrantError{
				Provider: "gcloud",
				Cause:    err,
				Hint: "Set a project with one of:\n" +
					"  moat grant gcloud --project MY_PROJECT\n" +
					"  export GOOGLE_CLOUD_PROJECT=MY_PROJECT\n" +
					"  gcloud config set project MY_PROJECT",
			}
		}
		cfg.ProjectID = project
	}

	// Verify credentials work by building a token source.
	if err := testCredentials(ctx, cfg); err != nil {
		return nil, &provider.GrantError{
			Provider: "gcloud",
			Cause:    err,
			Hint: "Ensure Google Cloud credentials are configured on your host.\n" +
				"Run 'gcloud auth application-default login' or set GOOGLE_APPLICATION_CREDENTIALS.",
		}
	}

	ui.Infof("Using Google Cloud project: %s", cfg.ProjectID)

	cred := &provider.Credential{
		Provider:  "gcloud",
		Token:     "", // no static token; tokens are minted on demand
		CreatedAt: time.Now(),
		Metadata: map[string]string{
			MetaKeyProject: cfg.ProjectID,
			MetaKeyScopes:  strings.Join(cfg.Scopes, ","),
		},
	}

	if cfg.ImpersonateSA != "" {
		cred.Metadata[MetaKeyImpersonate] = cfg.ImpersonateSA
	}
	if cfg.KeyFile != "" {
		cred.Metadata[MetaKeyKeyFile] = cfg.KeyFile
	}
	if cfg.Email != "" {
		cred.Metadata[MetaKeyEmail] = cfg.Email
	}

	return cred, nil
}

// splitScopes splits a comma-separated scope string into a slice.
func splitScopes(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// detectProject attempts to detect the Google Cloud project from the environment.
// It checks GOOGLE_CLOUD_PROJECT, CLOUDSDK_CORE_PROJECT, then gcloud CLI.
func detectProject() (string, error) {
	// Check environment variables first.
	for _, env := range []string{"GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		if v := getenv(env); v != "" {
			return v, nil
		}
	}

	// Try gcloud CLI.
	out, err := exec.Command("gcloud", "config", "get-value", "project").Output()
	if err == nil {
		project := strings.TrimSpace(string(out))
		if project != "" && project != "(unset)" {
			return project, nil
		}
	}

	return "", fmt.Errorf("could not detect Google Cloud project")
}

// ConfigFromCredential reconstructs Config from a stored credential's metadata.
func ConfigFromCredential(cred *provider.Credential) (*Config, error) {
	if cred == nil {
		return nil, fmt.Errorf("credential is nil")
	}

	project := cred.Metadata[MetaKeyProject]
	if project == "" {
		return nil, fmt.Errorf("gcloud credential missing project ID")
	}

	cfg := &Config{
		ProjectID:     project,
		ImpersonateSA: cred.Metadata[MetaKeyImpersonate],
		KeyFile:       cred.Metadata[MetaKeyKeyFile],
		Email:         cred.Metadata[MetaKeyEmail],
	}

	if scopeStr := cred.Metadata[MetaKeyScopes]; scopeStr != "" {
		cfg.Scopes = splitScopes(scopeStr)
	}
	if len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{DefaultScope}
	}

	return cfg, nil
}

// testCredentials verifies that credentials can be obtained for the given config.
func testCredentials(ctx context.Context, cfg *Config) error {
	cp, err := NewCredentialProvider(ctx, cfg)
	if err != nil {
		return err
	}
	tok, err := cp.GetToken(ctx)
	if err != nil {
		return err
	}
	if tok.AccessToken == "" {
		return fmt.Errorf("received empty access token")
	}
	return nil
}
