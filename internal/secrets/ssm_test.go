package secrets

import (
	"errors"
	"strings"
	"testing"
)

func TestParseSSMReference(t *testing.T) {
	tests := []struct {
		name       string
		ref        string
		wantRegion string
		wantPath   string
		wantErr    bool
	}{
		{
			name:       "simple path",
			ref:        "ssm:///production/database/url",
			wantRegion: "",
			wantPath:   "/production/database/url",
		},
		{
			name:       "with region",
			ref:        "ssm://us-west-2/production/api-key",
			wantRegion: "us-west-2",
			wantPath:   "/production/api-key",
		},
		{
			name:       "nested path",
			ref:        "ssm:///a/b/c/d/e",
			wantRegion: "",
			wantPath:   "/a/b/c/d/e",
		},
		{
			name:    "region without path",
			ref:     "ssm://us-west-2",
			wantErr: true,
		},
		{
			name:    "empty path",
			ref:     "ssm://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			region, path, err := parseSSMReference(tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if region != tt.wantRegion {
				t.Errorf("region = %q, want %q", region, tt.wantRegion)
			}
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
		})
	}
}

func TestSSMResolver_Scheme(t *testing.T) {
	r := &SSMResolver{}
	if r.Scheme() != "ssm" {
		t.Errorf("Scheme() = %q, want %q", r.Scheme(), "ssm")
	}
}

func TestSSMResolver_ParseAWSError_ParameterNotFound(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("An error occurred (ParameterNotFound) when calling the GetParameter operation")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("expected NotFoundError, got %T: %v", err, err)
	}
	if notFound.Backend != "AWS SSM" {
		t.Errorf("expected backend 'AWS SSM', got %q", notFound.Backend)
	}
}

func TestSSMResolver_ParseAWSError_AccessDenied(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("An error occurred (AccessDeniedException) when calling the GetParameter operation")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if !strings.Contains(backendErr.Reason, "access denied") {
		t.Errorf("expected reason to contain 'access denied', got %q", backendErr.Reason)
	}
	if !strings.Contains(backendErr.Fix, "IAM permissions") {
		t.Errorf("expected fix to mention IAM permissions, got %q", backendErr.Fix)
	}
}

func TestSSMResolver_ParseAWSError_ExpiredToken(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("An error occurred (ExpiredTokenException) when calling the GetParameter operation")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if !strings.Contains(backendErr.Reason, "credentials expired") {
		t.Errorf("expected reason to contain 'credentials expired', got %q", backendErr.Reason)
	}
	if !strings.Contains(backendErr.Fix, "aws sso login") {
		t.Errorf("expected fix to mention 'aws sso login', got %q", backendErr.Fix)
	}
}

func TestSSMResolver_ParseAWSError_NoCredentials(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("Unable to locate credentials. You can configure credentials by running \"aws configure\".")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if !strings.Contains(backendErr.Reason, "no AWS credentials found") {
		t.Errorf("expected reason to mention missing credentials, got %q", backendErr.Reason)
	}
	if !strings.Contains(backendErr.Fix, "aws configure") {
		t.Errorf("expected fix to mention 'aws configure', got %q", backendErr.Fix)
	}
}

func TestSSMResolver_ParseAWSError_EndpointError(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("Could not connect to the endpoint URL: \"https://ssm.invalid-region.amazonaws.com/\"")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if !strings.Contains(backendErr.Reason, "could not connect") {
		t.Errorf("expected reason to mention connection failure, got %q", backendErr.Reason)
	}
}

func TestSSMResolver_ParseAWSError_GenericError(t *testing.T) {
	r := &SSMResolver{}
	ref := "ssm:///test/param"

	stderr := []byte("some unexpected error message")
	err := r.parseAWSError(stderr, ref, "/test/param")

	var backendErr *BackendError
	if !errors.As(err, &backendErr) {
		t.Fatalf("expected BackendError, got %T: %v", err, err)
	}
	if backendErr.Backend != "AWS SSM" {
		t.Errorf("expected backend 'AWS SSM', got %q", backendErr.Backend)
	}
	if !strings.Contains(backendErr.Reason, "unexpected error") {
		t.Errorf("expected reason to contain error message, got %q", backendErr.Reason)
	}
}
