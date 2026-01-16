package proxy

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
)

func TestAWSCredentialHandler_ServeHTTP(t *testing.T) {
	t.Run("returns credentials in ECS format", func(t *testing.T) {
		expiration := time.Now().Add(15 * time.Minute)
		handler := &AWSCredentialHandler{
			getCredentials: func(ctx context.Context) (*AWSCredentials, error) {
				return &AWSCredentials{
					AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
					SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
					SessionToken:    "FwoGZXIvYXdzEBY...",
					Expiration:      expiration,
				}, nil
			},
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
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
		if resp["SecretAccessKey"] != "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY" {
			t.Errorf("SecretAccessKey missing or wrong")
		}
		if resp["Token"] != "FwoGZXIvYXdzEBY..." {
			t.Errorf("Token = %v, want FwoGZXIvYXdzEBY...", resp["Token"])
		}
		if _, ok := resp["Expiration"]; !ok {
			t.Error("Expiration missing from response")
		}
	})

	t.Run("returns 500 on provider error", func(t *testing.T) {
		handler := &AWSCredentialHandler{
			getCredentials: func(ctx context.Context) (*AWSCredentials, error) {
				return nil, context.DeadlineExceeded
			},
		}

		req := httptest.NewRequest("GET", "/_aws/credentials", nil)
		w := httptest.NewRecorder()

		handler.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("status = %d, want 500", w.Code)
		}
	})
}

func TestAWSCredentialProvider_Caching(t *testing.T) {
	callCount := 0
	expiration := time.Now().Add(15 * time.Minute)

	mockSTS := &mockSTSClient{
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
	}

	provider := &AWSCredentialProvider{
		roleARN:         "arn:aws:iam::123456789012:role/Test",
		region:          "us-east-1",
		sessionDuration: 15 * time.Minute,
		sessionName:     "test",
		stsClient:       mockSTS,
	}

	// First call should hit STS
	creds1, err := provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should use cache
	creds2, err := provider.GetCredentials(context.Background())
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

func TestAWSCredentialProvider_RefreshesExpiredCredentials(t *testing.T) {
	callCount := 0

	mockSTS := &mockSTSClient{
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
	}

	provider := &AWSCredentialProvider{
		roleARN:         "arn:aws:iam::123456789012:role/Test",
		region:          "us-east-1",
		sessionDuration: 15 * time.Minute,
		sessionName:     "test",
		stsClient:       mockSTS,
	}

	// First call
	_, err := provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 1 {
		t.Errorf("callCount = %d, want 1", callCount)
	}

	// Second call should refresh because credentials expire within 5 min
	_, err = provider.GetCredentials(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2 (should refresh near-expiry credentials)", callCount)
	}
}

type mockSTSClient struct {
	assumeRoleFn func(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error)
}

func (m *mockSTSClient) AssumeRole(ctx context.Context, params *sts.AssumeRoleInput, optFns ...func(*sts.Options)) (*sts.AssumeRoleOutput, error) {
	return m.assumeRoleFn(ctx, params, optFns...)
}
