package configprovider

import (
	"strings"
	"testing"
)

func TestParseProviderDef(t *testing.T) {
	yaml := `
name: gitlab
description: "GitLab personal access token"
aliases: [gl]

hosts:
  - "gitlab.com"
  - "*.gitlab.com"

inject:
  header: "PRIVATE-TOKEN"

source_env: [GITLAB_TOKEN, GL_TOKEN]
container_env: GITLAB_TOKEN

validate:
  url: "https://gitlab.com/api/v4/user"

prompt: |
  Enter a GitLab Personal Access Token.
  Create one at: https://gitlab.com/-/user_settings/personal_access_tokens
`
	def, err := parseProviderDef([]byte(yaml))
	if err != nil {
		t.Fatalf("parseProviderDef() error: %v", err)
	}

	if def.Name != "gitlab" {
		t.Errorf("Name = %q, want %q", def.Name, "gitlab")
	}
	if def.Description != "GitLab personal access token" {
		t.Errorf("Description = %q, want %q", def.Description, "GitLab personal access token")
	}
	if len(def.Aliases) != 1 || def.Aliases[0] != "gl" {
		t.Errorf("Aliases = %v, want [gl]", def.Aliases)
	}
	if len(def.Hosts) != 2 {
		t.Errorf("Hosts = %v, want 2 entries", def.Hosts)
	}
	if def.Inject.Header != "PRIVATE-TOKEN" {
		t.Errorf("Inject.Header = %q, want %q", def.Inject.Header, "PRIVATE-TOKEN")
	}
	if def.Inject.Prefix != "" {
		t.Errorf("Inject.Prefix = %q, want empty", def.Inject.Prefix)
	}
	if len(def.SourceEnv) != 2 {
		t.Errorf("SourceEnv = %v, want 2 entries", def.SourceEnv)
	}
	if def.ContainerEnv != "GITLAB_TOKEN" {
		t.Errorf("ContainerEnv = %q, want %q", def.ContainerEnv, "GITLAB_TOKEN")
	}
	if def.Validate == nil {
		t.Fatal("Validate is nil, want non-nil")
	}
	if def.Validate.URL != "https://gitlab.com/api/v4/user" {
		t.Errorf("Validate.URL = %q", def.Validate.URL)
	}
	if def.Prompt == "" {
		t.Error("Prompt is empty, want non-empty")
	}
}

func TestParseProviderDefWithPrefix(t *testing.T) {
	yaml := `
name: vercel
description: "Vercel platform API token"
hosts:
  - "api.vercel.com"
inject:
  header: "Authorization"
  prefix: "Bearer "
source_env: [VERCEL_TOKEN]
container_env: VERCEL_TOKEN
validate:
  url: "https://api.vercel.com/v2/user"
`
	def, err := parseProviderDef([]byte(yaml))
	if err != nil {
		t.Fatalf("parseProviderDef() error: %v", err)
	}

	if def.Inject.Prefix != "Bearer " {
		t.Errorf("Inject.Prefix = %q, want %q", def.Inject.Prefix, "Bearer ")
	}
}

func TestParseProviderDefNoValidate(t *testing.T) {
	yaml := `
name: elevenlabs
description: "ElevenLabs text-to-speech API key"
hosts:
  - "api.elevenlabs.io"
inject:
  header: "xi-api-key"
source_env: [ELEVENLABS_API_KEY]
container_env: ELEVENLABS_API_KEY
`
	def, err := parseProviderDef([]byte(yaml))
	if err != nil {
		t.Fatalf("parseProviderDef() error: %v", err)
	}

	if def.Validate != nil {
		t.Error("Validate should be nil when not specified")
	}
}

func TestParseProviderDefInvalidYAML(t *testing.T) {
	yaml := `{invalid yaml`
	_, err := parseProviderDef([]byte(yaml))
	if err == nil {
		t.Error("expected error for invalid YAML")
	}
}

func TestParseProviderDefValidation(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		wantErr string
	}{
		{
			name:    "missing name",
			yaml:    "hosts: [example.com]\ninject:\n  header: Authorization\n",
			wantErr: "provider name is required",
		},
		{
			name:    "missing hosts",
			yaml:    "name: test\ndescription: Test\ninject:\n  header: Authorization\n",
			wantErr: "at least one host is required",
		},
		{
			name:    "missing description",
			yaml:    "name: test\nhosts: [example.com]\ninject:\n  header: Authorization\n",
			wantErr: "description is required",
		},
		{
			name:    "missing inject header",
			yaml:    "name: test\ndescription: Test\nhosts: [example.com]\n",
			wantErr: "inject.header is required",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseProviderDef([]byte(tt.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want to contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestLoadEmbeddedDefaults(t *testing.T) {
	defs := loadEmbedded()

	if len(defs) == 0 {
		t.Fatal("loadEmbedded() returned no definitions")
	}

	// Check that gitlab is present (one of our embedded defaults)
	if _, ok := defs["gitlab"]; !ok {
		t.Error("expected gitlab in embedded defaults")
	}

	// Check that brave-search is present
	if _, ok := defs["brave-search"]; !ok {
		t.Error("expected brave-search in embedded defaults")
	}

	// Verify a loaded definition has required fields
	gitlab := defs["gitlab"]
	if gitlab.Description == "" {
		t.Error("gitlab description should not be empty")
	}
	if len(gitlab.Hosts) == 0 {
		t.Error("gitlab hosts should not be empty")
	}
	if gitlab.Inject.Header == "" {
		t.Error("gitlab inject.header should not be empty")
	}
}

func TestLoadEmbeddedAllDefaults(t *testing.T) {
	defs := loadEmbedded()

	expected := []string{
		"gitlab",
		"brave-search",
		"elevenlabs",
		"linear",
		"vercel",
		"sentry",
		"datadog",
	}

	for _, name := range expected {
		if _, ok := defs[name]; !ok {
			t.Errorf("expected %q in embedded defaults", name)
		}
	}
}
