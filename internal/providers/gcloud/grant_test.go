package gcloud

import (
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestConfigFromCredential(t *testing.T) {
	cred := &provider.Credential{
		Provider: "gcloud",
		Token:    "",
		Metadata: map[string]string{
			MetaKeyProject:     "my-proj",
			MetaKeyScopes:      "https://www.googleapis.com/auth/cloud-platform",
			MetaKeyImpersonate: "sa@my-proj.iam.gserviceaccount.com",
			MetaKeyKeyFile:     "",
		},
	}
	cfg, err := ConfigFromCredential(cred)
	if err != nil {
		t.Fatalf("ConfigFromCredential: %v", err)
	}
	if cfg.ProjectID != "my-proj" {
		t.Errorf("ProjectID = %q", cfg.ProjectID)
	}
	if cfg.ImpersonateSA != "sa@my-proj.iam.gserviceaccount.com" {
		t.Errorf("ImpersonateSA = %q", cfg.ImpersonateSA)
	}
	if len(cfg.Scopes) != 1 || cfg.Scopes[0] != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("Scopes = %v", cfg.Scopes)
	}
}

func TestConfigFromCredentialDefaultScope(t *testing.T) {
	cred := &provider.Credential{
		Provider: "gcloud",
		Metadata: map[string]string{MetaKeyProject: "p"},
	}
	cfg, _ := ConfigFromCredential(cred)
	if len(cfg.Scopes) == 0 {
		t.Error("expected default scope when none specified")
	}
}

func TestConfigFromCredentialMissingProject(t *testing.T) {
	cred := &provider.Credential{Provider: "gcloud", Metadata: map[string]string{}}
	_, err := ConfigFromCredential(cred)
	if err == nil {
		t.Error("expected error when project is missing")
	}
}

func TestSplitScopes(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"https://www.googleapis.com/auth/cloud-platform", 1},
		{"scope1,scope2,scope3", 3},
		{"scope1, scope2 , scope3", 3},
	}
	for _, tt := range tests {
		got := splitScopes(tt.input)
		if len(got) != tt.want {
			t.Errorf("splitScopes(%q) = %d scopes, want %d", tt.input, len(got), tt.want)
		}
	}
}

func TestConfigFromCredentialNil(t *testing.T) {
	_, err := ConfigFromCredential(nil)
	if err == nil {
		t.Error("expected error for nil credential")
	}
}
