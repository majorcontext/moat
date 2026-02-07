package aws

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/aws/aws-sdk-go-v2/service/sts/types"
	"github.com/majorcontext/moat/internal/provider"
)

func TestProvider_Name(t *testing.T) {
	p := New()
	if got := p.Name(); got != "aws" {
		t.Errorf("Name() = %q, want %q", got, "aws")
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := New()
	deps := p.ImpliedDependencies()
	if len(deps) != 1 || deps[0] != "aws" {
		t.Errorf("ImpliedDependencies() = %v, want [aws]", deps)
	}
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := New()
	cred := &provider.Credential{Token: "arn:aws:iam::123456789012:role/Test"}
	env := p.ContainerEnv(cred)
	if env != nil {
		t.Errorf("ContainerEnv() = %v, want nil", env)
	}
}

func TestProvider_ContainerMounts(t *testing.T) {
	p := New()
	cred := &provider.Credential{Token: "arn:aws:iam::123456789012:role/Test"}
	mounts, cleanupPath, err := p.ContainerMounts(cred, "/home/user")
	if err != nil {
		t.Errorf("ContainerMounts() error = %v", err)
	}
	if mounts != nil {
		t.Errorf("ContainerMounts() mounts = %v, want nil", mounts)
	}
	if cleanupPath != "" {
		t.Errorf("ContainerMounts() cleanupPath = %q, want empty", cleanupPath)
	}
}

func TestParseRoleARN(t *testing.T) {
	tests := []struct {
		name    string
		arn     string
		wantARN string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid ARN",
			arn:     "arn:aws:iam::123456789012:role/MyRole",
			wantARN: "arn:aws:iam::123456789012:role/MyRole",
		},
		{
			name:    "valid ARN with path",
			arn:     "arn:aws:iam::123456789012:role/admin/MyAdminRole",
			wantARN: "arn:aws:iam::123456789012:role/admin/MyAdminRole",
		},
		{
			name:    "valid ARN aws-cn partition",
			arn:     "arn:aws-cn:iam::123456789012:role/MyRole",
			wantARN: "arn:aws-cn:iam::123456789012:role/MyRole",
		},
		{
			name:    "valid ARN aws-us-gov partition",
			arn:     "arn:aws-us-gov:iam::123456789012:role/MyRole",
			wantARN: "arn:aws-us-gov:iam::123456789012:role/MyRole",
		},
		{
			name:    "empty ARN",
			arn:     "",
			wantErr: true,
			errMsg:  "role ARN is required",
		},
		{
			name:    "not enough parts",
			arn:     "arn:aws:iam",
			wantErr: true,
			errMsg:  "expected 6 colon-separated parts",
		},
		{
			name:    "wrong prefix",
			arn:     "arm:aws:iam::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "must start with 'arn:'",
		},
		{
			name:    "invalid partition",
			arn:     "arn:aws-invalid:iam::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "invalid ARN partition",
		},
		{
			name:    "not IAM service",
			arn:     "arn:aws:s3::123456789012:role/MyRole",
			wantErr: true,
			errMsg:  "must be an IAM ARN",
		},
		{
			name:    "missing account ID",
			arn:     "arn:aws:iam:::role/MyRole",
			wantErr: true,
			errMsg:  "account ID is required",
		},
		{
			name:    "not a role",
			arn:     "arn:aws:iam::123456789012:user/MyUser",
			wantErr: true,
			errMsg:  "must be a role ARN",
		},
		{
			name:    "role without name",
			arn:     "arn:aws:iam::123456789012:role/",
			wantErr: true,
			errMsg:  "role name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseRoleARN(tt.arn)
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseRoleARN(%q) = nil error, want error containing %q", tt.arn, tt.errMsg)
					return
				}
				if tt.errMsg != "" && !containsString(err.Error(), tt.errMsg) {
					t.Errorf("ParseRoleARN(%q) error = %q, want error containing %q", tt.arn, err.Error(), tt.errMsg)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseRoleARN(%q) unexpected error: %v", tt.arn, err)
				return
			}
			if cfg.RoleARN != tt.wantARN {
				t.Errorf("ParseRoleARN(%q).RoleARN = %q, want %q", tt.arn, cfg.RoleARN, tt.wantARN)
			}
			if cfg.Region != DefaultRegion {
				t.Errorf("ParseRoleARN(%q).Region = %q, want %q", tt.arn, cfg.Region, DefaultRegion)
			}
		})
	}
}

func TestConfigFromCredential(t *testing.T) {
	t.Run("basic credential", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.RoleARN != cred.Token {
			t.Errorf("RoleARN = %q, want %q", cfg.RoleARN, cred.Token)
		}
		if cfg.Region != DefaultRegion {
			t.Errorf("Region = %q, want %q", cfg.Region, DefaultRegion)
		}
		if cfg.SessionDuration != DefaultSessionDuration {
			t.Errorf("SessionDuration = %v, want %v", cfg.SessionDuration, DefaultSessionDuration)
		}
	})

	t.Run("with metadata", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
			Metadata: map[string]string{
				MetaKeyRegion:          "eu-west-1",
				MetaKeySessionDuration: "1h",
				MetaKeyExternalID:      "ext-123",
			},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "eu-west-1" {
			t.Errorf("Region = %q, want %q", cfg.Region, "eu-west-1")
		}
		if cfg.SessionDuration != time.Hour {
			t.Errorf("SessionDuration = %v, want %v", cfg.SessionDuration, time.Hour)
		}
		if cfg.ExternalID != "ext-123" {
			t.Errorf("ExternalID = %q, want %q", cfg.ExternalID, "ext-123")
		}
	})

	t.Run("nil credential", func(t *testing.T) {
		_, err := ConfigFromCredential(nil)
		if err == nil {
			t.Error("expected error for nil credential")
		}
	})

	t.Run("invalid duration", func(t *testing.T) {
		cred := &provider.Credential{
			Token: "arn:aws:iam::123456789012:role/Test",
			Metadata: map[string]string{
				MetaKeySessionDuration: "invalid",
			},
		}
		_, err := ConfigFromCredential(cred)
		if err == nil {
			t.Error("expected error for invalid duration")
		}
	})

	t.Run("legacy scopes format", func(t *testing.T) {
		// Old credentials stored config in Scopes array: [region, sessionDuration, externalID]
		cred := &provider.Credential{
			Token:  "arn:aws:iam::123456789012:role/Test",
			Scopes: []string{"ap-southeast-2", "30m", "legacy-ext-id"},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "ap-southeast-2" {
			t.Errorf("Region = %q, want %q (from legacy Scopes)", cfg.Region, "ap-southeast-2")
		}
		if cfg.SessionDuration != 30*time.Minute {
			t.Errorf("SessionDuration = %v, want %v (from legacy Scopes)", cfg.SessionDuration, 30*time.Minute)
		}
		if cfg.ExternalID != "legacy-ext-id" {
			t.Errorf("ExternalID = %q, want %q (from legacy Scopes)", cfg.ExternalID, "legacy-ext-id")
		}
	})

	t.Run("metadata takes precedence over legacy scopes", func(t *testing.T) {
		// When both are present, Metadata should win
		cred := &provider.Credential{
			Token:  "arn:aws:iam::123456789012:role/Test",
			Scopes: []string{"ap-southeast-2", "30m", "legacy-ext-id"},
			Metadata: map[string]string{
				MetaKeyRegion:          "eu-central-1",
				MetaKeySessionDuration: "2h",
				MetaKeyExternalID:      "new-ext-id",
			},
		}
		cfg, err := ConfigFromCredential(cred)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.Region != "eu-central-1" {
			t.Errorf("Region = %q, want %q (from Metadata)", cfg.Region, "eu-central-1")
		}
		if cfg.SessionDuration != 2*time.Hour {
			t.Errorf("SessionDuration = %v, want %v (from Metadata)", cfg.SessionDuration, 2*time.Hour)
		}
		if cfg.ExternalID != "new-ext-id" {
			t.Errorf("ExternalID = %q, want %q (from Metadata)", cfg.ExternalID, "new-ext-id")
		}
	})
}

func TestEndpointHandler_ServeHTTP(t *testing.T) {
	t.Run("returns credentials in credential_process format", func(t *testing.T) {
		expiration := time.Now().Add(15 * time.Minute)
		handler := &EndpointHandler{
			cfg: &Config{
				RoleARN:         "arn:aws:iam::123456789012:role/Test",
				Region:          "us-east-1",
				SessionDuration: 15 * time.Minute,
			},
			stsClient: &mockSTSClient{
				assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
					return &sts.AssumeRoleOutput{
						Credentials: &types.Credentials{
							AccessKeyId:     aws.String("AKIAIOSFODNN7EXAMPLE"),
							SecretAccessKey: aws.String("wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"),
							SessionToken:    aws.String("FwoGZXIvYXdzEBY..."),
							Expiration:      aws.Time(expiration),
						},
					}, nil
				},
			},
		}

		req := httptest.NewRequest("GET", "/aws-credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}

		// Check Version field (required by credential_process)
		if resp["Version"] != float64(1) {
			t.Errorf("Version = %v, want 1", resp["Version"])
		}
		if resp["AccessKeyId"] != "AKIAIOSFODNN7EXAMPLE" {
			t.Errorf("AccessKeyId = %v, want AKIAIOSFODNN7EXAMPLE", resp["AccessKeyId"])
		}
		if resp["SecretAccessKey"] != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
			t.Errorf("SecretAccessKey missing or wrong")
		}
		if resp["SessionToken"] != "FwoGZXIvYXdzEBY..." {
			t.Errorf("SessionToken = %v, want FwoGZXIvYXdzEBY...", resp["SessionToken"])
		}
		if _, ok := resp["Expiration"]; !ok {
			t.Error("Expiration missing from response")
		}
	})

	t.Run("returns 500 on provider error", func(t *testing.T) {
		handler := &EndpointHandler{
			cfg: &Config{
				RoleARN:         "arn:aws:iam::123456789012:role/Test",
				Region:          "us-east-1",
				SessionDuration: 15 * time.Minute,
			},
			stsClient: &mockSTSClient{
				assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
					return nil, fmt.Errorf("STS error")
				},
			},
		}

		req := httptest.NewRequest("GET", "/aws-credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})

	t.Run("returns 401 when auth token required but missing", func(t *testing.T) {
		handler := &EndpointHandler{
			cfg: &Config{
				RoleARN:         "arn:aws:iam::123456789012:role/Test",
				Region:          "us-east-1",
				SessionDuration: 15 * time.Minute,
			},
			authToken: "secret-token",
			stsClient: &mockSTSClient{
				assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
					return &sts.AssumeRoleOutput{
						Credentials: &types.Credentials{
							AccessKeyId:     aws.String("AKIAIOSFODNN7EXAMPLE"),
							SecretAccessKey: aws.String("secret"),
							SessionToken:    aws.String("token"),
							Expiration:      aws.Time(time.Now().Add(15 * time.Minute)),
						},
					}, nil
				},
			},
		}

		req := httptest.NewRequest("GET", "/aws-credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("returns 401 when auth token is invalid", func(t *testing.T) {
		handler := &EndpointHandler{
			cfg: &Config{
				RoleARN:         "arn:aws:iam::123456789012:role/Test",
				Region:          "us-east-1",
				SessionDuration: 15 * time.Minute,
			},
			authToken: "secret-token",
			stsClient: &mockSTSClient{
				assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
					return &sts.AssumeRoleOutput{
						Credentials: &types.Credentials{
							AccessKeyId:     aws.String("AKIAIOSFODNN7EXAMPLE"),
							SecretAccessKey: aws.String("secret"),
							SessionToken:    aws.String("token"),
							Expiration:      aws.Time(time.Now().Add(15 * time.Minute)),
						},
					}, nil
				},
			},
		}

		req := httptest.NewRequest("GET", "/aws-credentials", nil)
		req.Header.Set("Authorization", "Bearer wrong-token")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Errorf("status = %d, want 401", w.Code)
		}
	})

	t.Run("returns credentials when auth token is valid", func(t *testing.T) {
		handler := &EndpointHandler{
			cfg: &Config{
				RoleARN:         "arn:aws:iam::123456789012:role/Test",
				Region:          "us-east-1",
				SessionDuration: 15 * time.Minute,
			},
			authToken: "secret-token",
			stsClient: &mockSTSClient{
				assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
					return &sts.AssumeRoleOutput{
						Credentials: &types.Credentials{
							AccessKeyId:     aws.String("AKIAIOSFODNN7EXAMPLE"),
							SecretAccessKey: aws.String("secret"),
							SessionToken:    aws.String("token"),
							Expiration:      aws.Time(time.Now().Add(15 * time.Minute)),
						},
					}, nil
				},
			},
		}

		req := httptest.NewRequest("GET", "/aws-credentials", nil)
		req.Header.Set("Authorization", "Bearer secret-token")
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}

		var resp map[string]interface{}
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp["AccessKeyId"] != "AKIAIOSFODNN7EXAMPLE" {
			t.Errorf("AccessKeyId = %v, want AKIAIOSFODNN7EXAMPLE", resp["AccessKeyId"])
		}
	})
}

func TestEndpointHandler_Caching(t *testing.T) {
	callCount := 0
	expiration := time.Now().Add(15 * time.Minute)

	handler := &EndpointHandler{
		cfg: &Config{
			RoleARN:         "arn:aws:iam::123456789012:role/Test",
			Region:          "us-east-1",
			SessionDuration: 15 * time.Minute,
		},
		stsClient: &mockSTSClient{
			assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
				callCount++
				return &sts.AssumeRoleOutput{
					Credentials: &types.Credentials{
						AccessKeyId:     aws.String("AKIA" + fmt.Sprintf("%d", callCount)),
						SecretAccessKey: aws.String("secret"),
						SessionToken:    aws.String("token"),
						Expiration:      aws.Time(expiration),
					},
				}, nil
			},
		},
	}

	// First call should hit STS
	creds1, err := handler.getCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should use cache
	creds2, err := handler.getCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1 (cached)", callCount)
	}

	// Should return same credentials
	if creds1.AccessKeyID != creds2.AccessKeyID {
		t.Errorf("cached credentials should match")
	}
}

func TestEndpointHandler_RefreshesExpiredCredentials(t *testing.T) {
	callCount := 0

	handler := &EndpointHandler{
		cfg: &Config{
			RoleARN:         "arn:aws:iam::123456789012:role/Test",
			Region:          "us-east-1",
			SessionDuration: 15 * time.Minute,
		},
		stsClient: &mockSTSClient{
			assumeRoleFn: func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
				callCount++
				// Return credentials that expire soon (within 5 min buffer)
				expiration := time.Now().Add(3 * time.Minute)
				return &sts.AssumeRoleOutput{
					Credentials: &types.Credentials{
						AccessKeyId:     aws.String("AKIA" + fmt.Sprintf("%d", callCount)),
						SecretAccessKey: aws.String("secret"),
						SessionToken:    aws.String("token"),
						Expiration:      aws.Time(expiration),
					},
				}, nil
			},
		},
	}

	// First call
	_, err := handler.getCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should refresh because credentials expire within 5 min
	_, err = handler.getCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (should refresh near-expiry credentials)", callCount)
	}
}

func TestEndpointHandler_Region(t *testing.T) {
	handler := &EndpointHandler{
		cfg: &Config{
			Region: "eu-west-1",
		},
	}
	if got := handler.Region(); got != "eu-west-1" {
		t.Errorf("Region() = %q, want %q", got, "eu-west-1")
	}
}

func TestEndpointHandler_RoleARN(t *testing.T) {
	handler := &EndpointHandler{
		cfg: &Config{
			RoleARN: "arn:aws:iam::123456789012:role/Test",
		},
	}
	if got := handler.RoleARN(); got != "arn:aws:iam::123456789012:role/Test" {
		t.Errorf("RoleARN() = %q, want %q", got, "arn:aws:iam::123456789012:role/Test")
	}
}

// mockSTSClient implements STSAssumeRoler for testing.
type mockSTSClient struct {
	assumeRoleFn func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

func (m *mockSTSClient) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.assumeRoleFn(ctx, params, optFns...)
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsStringHelper(s, substr))
}

func containsStringHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
