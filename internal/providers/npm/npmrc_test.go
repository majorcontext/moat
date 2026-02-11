package npm

import (
	"strings"
	"testing"
)

func TestParseNpmrc_BasicTokenLine(t *testing.T) {
	input := `//registry.npmjs.org/:_authToken=npm_abc123`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "registry.npmjs.org" {
		t.Errorf("expected host registry.npmjs.org, got %s", entries[0].Host)
	}
	if entries[0].Token != "npm_abc123" {
		t.Errorf("expected token npm_abc123, got %s", entries[0].Token)
	}
	if entries[0].TokenSource != SourceNpmrc {
		t.Errorf("expected source %s, got %s", SourceNpmrc, entries[0].TokenSource)
	}
}

func TestParseNpmrc_MultipleRegistries(t *testing.T) {
	input := `//registry.npmjs.org/:_authToken=npm_default
//npm.company.com/:_authToken=npm_company
@myorg:registry=https://npm.company.com/`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}

	// Check default registry
	if entries[0].Host != "registry.npmjs.org" {
		t.Errorf("expected first host registry.npmjs.org, got %s", entries[0].Host)
	}
	if entries[0].Token != "npm_default" {
		t.Errorf("expected token npm_default, got %s", entries[0].Token)
	}
	if len(entries[0].Scopes) != 0 {
		t.Errorf("expected no scopes for default, got %v", entries[0].Scopes)
	}

	// Check company registry with scope
	if entries[1].Host != "npm.company.com" {
		t.Errorf("expected second host npm.company.com, got %s", entries[1].Host)
	}
	if entries[1].Token != "npm_company" {
		t.Errorf("expected token npm_company, got %s", entries[1].Token)
	}
	if len(entries[1].Scopes) != 1 || entries[1].Scopes[0] != "@myorg" {
		t.Errorf("expected scopes [@myorg], got %v", entries[1].Scopes)
	}
}

func TestParseNpmrc_EnvVarReference(t *testing.T) {
	input := `//registry.npmjs.org/:_authToken=${NPM_TOKEN}`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	// Token should be empty for env var references
	if entries[0].Token != "" {
		t.Errorf("expected empty token for env var ref, got %s", entries[0].Token)
	}
}

func TestParseNpmrc_CommentsAndBlankLines(t *testing.T) {
	input := `# This is a comment
; Another comment

//registry.npmjs.org/:_authToken=npm_abc123

# trailing comment`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
}

func TestParseNpmrc_MultipleScopes(t *testing.T) {
	input := `@org1:registry=https://npm.company.com/
@org2:registry=https://npm.company.com/
//npm.company.com/:_authToken=npm_company`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if len(entries[0].Scopes) != 2 {
		t.Fatalf("expected 2 scopes, got %d: %v", len(entries[0].Scopes), entries[0].Scopes)
	}
}

func TestParseNpmrc_HostWithPath(t *testing.T) {
	input := `//npm.pkg.github.com/:_authToken=ghp_abc123`
	entries, err := ParseNpmrc(strings.NewReader(input))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].Host != "npm.pkg.github.com" {
		t.Errorf("expected host npm.pkg.github.com, got %s", entries[0].Host)
	}
}

func TestParseNpmrc_Empty(t *testing.T) {
	entries, err := ParseNpmrc(strings.NewReader(""))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(entries))
	}
}

func TestGenerateNpmrc_SingleRegistry(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_real"},
	}
	result := GenerateNpmrc(entries, NpmTokenPlaceholder)

	if !strings.Contains(result, "//registry.npmjs.org/:_authToken="+NpmTokenPlaceholder) {
		t.Errorf("expected token line in output:\n%s", result)
	}
}

func TestGenerateNpmrc_WithScopes(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "registry.npmjs.org", Token: "npm_default"},
		{Host: "npm.company.com", Token: "npm_company", Scopes: []string{"@myorg", "@other"}},
	}
	result := GenerateNpmrc(entries, NpmTokenPlaceholder)

	if !strings.Contains(result, "@myorg:registry=https://npm.company.com/") {
		t.Errorf("expected @myorg scope routing in output:\n%s", result)
	}
	if !strings.Contains(result, "@other:registry=https://npm.company.com/") {
		t.Errorf("expected @other scope routing in output:\n%s", result)
	}
	if !strings.Contains(result, "//registry.npmjs.org/:_authToken="+NpmTokenPlaceholder) {
		t.Errorf("expected default registry token line in output:\n%s", result)
	}
	if !strings.Contains(result, "//npm.company.com/:_authToken="+NpmTokenPlaceholder) {
		t.Errorf("expected company registry token line in output:\n%s", result)
	}
}

func TestGenerateNpmrc_ScopesBeforeTokens(t *testing.T) {
	entries := []RegistryEntry{
		{Host: "npm.company.com", Token: "npm_company", Scopes: []string{"@myorg"}},
	}
	result := GenerateNpmrc(entries, NpmTokenPlaceholder)

	scopeIdx := strings.Index(result, "@myorg:registry=")
	tokenIdx := strings.Index(result, "//npm.company.com/:_authToken=")

	if scopeIdx == -1 || tokenIdx == -1 {
		t.Fatalf("expected both scope and token lines in output:\n%s", result)
	}
	if scopeIdx >= tokenIdx {
		t.Errorf("expected scope line before token line, but scope at %d, token at %d", scopeIdx, tokenIdx)
	}
}
