package claude

import (
	"strings"
	"testing"
)

func TestGenerateDockerfileSnippet(t *testing.T) {
	marketplaces := []MarketplaceConfig{
		{Name: "claude-plugins-official", Source: "github", Repo: "anthropics/claude-plugins-official"},
		{Name: "aws-agent-skills", Source: "github", Repo: "itsmostafa/aws-agent-skills"},
	}
	plugins := []string{
		"claude-md-management@claude-plugins-official",
		"aws-agent-skills@aws-agent-skills",
	}

	result := GenerateDockerfileSnippet(marketplaces, plugins, "moatuser")

	// Should have section header
	if !strings.Contains(result, "# Claude Code plugins") {
		t.Error("should have Claude Code plugins section")
	}

	// Should switch to moatuser
	if !strings.Contains(result, "USER moatuser") {
		t.Error("should switch to moatuser")
	}

	// Should add marketplaces (failures are non-fatal, in sorted order)
	if !strings.Contains(result, "marketplace add anthropics/claude-plugins-official &&") {
		t.Error("should add claude-plugins-official marketplace")
	}
	if !strings.Contains(result, "marketplace add itsmostafa/aws-agent-skills &&") {
		t.Error("should add aws-agent-skills marketplace")
	}

	// Should install plugins (failures are non-fatal, in sorted order)
	if !strings.Contains(result, "plugin install aws-agent-skills@aws-agent-skills &&") {
		t.Error("should install aws-agent-skills plugin")
	}
	if !strings.Contains(result, "plugin install claude-md-management@claude-plugins-official &&") {
		t.Error("should install claude-md-management plugin")
	}

	// Should switch back to root
	if !strings.Contains(result, "USER root") {
		t.Error("should switch back to USER root")
	}
}

func TestGenerateDockerfileSnippetEmpty(t *testing.T) {
	result := GenerateDockerfileSnippet(nil, nil, "moatuser")
	if result != "" {
		t.Error("empty input should return empty string")
	}

	result = GenerateDockerfileSnippet([]MarketplaceConfig{}, []string{}, "moatuser")
	if result != "" {
		t.Error("empty slices should return empty string")
	}
}

func TestGenerateDockerfileSnippetValidation(t *testing.T) {
	t.Run("invalid marketplace repo", func(t *testing.T) {
		marketplaces := []MarketplaceConfig{
			{Name: "good", Source: "github", Repo: "valid/repo"},
			{Name: "evil", Source: "github", Repo: "; rm -rf /"},
		}

		result := GenerateDockerfileSnippet(marketplaces, nil, "moatuser")

		// Valid repo should be included
		if !strings.Contains(result, "marketplace add valid/repo") {
			t.Error("valid marketplace should be included")
		}
		// Invalid repo should trigger warning message
		if !strings.Contains(result, "WARNING: Invalid marketplace repo format: evil") {
			t.Error("invalid marketplace should show warning message with name")
		}
		// The malicious repo value should NOT appear in the output
		if strings.Contains(result, "; rm -rf /") {
			t.Error("invalid repo value should not appear in output")
		}
	})

	t.Run("invalid plugin key", func(t *testing.T) {
		plugins := []string{
			"valid-plugin@valid-market",
			"bad;rm -rf /@market",
		}

		result := GenerateDockerfileSnippet(nil, plugins, "moatuser")

		// Valid plugin should be included
		if !strings.Contains(result, "plugin install valid-plugin@valid-market") {
			t.Error("valid plugin should be included")
		}
		// Invalid plugin should trigger warning message
		if !strings.Contains(result, "WARNING: Invalid plugin format") {
			t.Error("invalid plugin should show warning message")
		}
	})
}
