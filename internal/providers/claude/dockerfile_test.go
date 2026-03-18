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

	// Should have section header in Dockerfile snippet
	if !strings.Contains(result.DockerfileSnippet, "# Claude Code plugins") {
		t.Error("should have Claude Code plugins section")
	}

	// Should switch to moatuser
	if !strings.Contains(result.DockerfileSnippet, "USER moatuser") {
		t.Error("should switch to moatuser")
	}

	// Should NOT include USER root — caller restores root context when needed
	if strings.Contains(result.DockerfileSnippet, "USER root") {
		t.Error("snippet should not include USER root (caller handles root restoration)")
	}

	// Should COPY and run the plugin script
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser claude-plugins.sh") {
		t.Error("should COPY plugin install script with correct ownership")
	}
	if !strings.Contains(result.DockerfileSnippet, "RUN bash /tmp/claude-plugins.sh") {
		t.Error("should run plugin install script")
	}

	// Should produce a context file
	if result.ScriptName != "claude-plugins.sh" {
		t.Errorf("expected script name claude-plugins.sh, got %s", result.ScriptName)
	}
	if result.ScriptContent == nil {
		t.Fatal("script content should not be nil")
	}

	scriptStr := string(result.ScriptContent)

	// Script should export PATH so claude CLI is findable
	if !strings.Contains(scriptStr, `export PATH="/home/moatuser/.claude/local/bin:/home/moatuser/.local/bin:$PATH"`) {
		t.Error("script should export PATH with Claude CLI locations")
	}

	// Script should use set -e
	if !strings.Contains(scriptStr, "set -e") {
		t.Error("script should use set -e")
	}

	// Script should track failures
	if !strings.Contains(scriptStr, "failures=0") {
		t.Error("script should initialize failure counter")
	}

	// Script should add marketplaces (in sorted order)
	if !strings.Contains(scriptStr, "if claude plugin marketplace add anthropics/claude-plugins-official; then") {
		t.Error("should add claude-plugins-official marketplace")
	}
	if !strings.Contains(scriptStr, "if claude plugin marketplace add itsmostafa/aws-agent-skills; then") {
		t.Error("should add aws-agent-skills marketplace")
	}

	// Script should install plugins (in sorted order)
	if !strings.Contains(scriptStr, "if claude plugin install aws-agent-skills@aws-agent-skills; then") {
		t.Error("should install aws-agent-skills plugin")
	}
	if !strings.Contains(scriptStr, "if claude plugin install claude-md-management@claude-plugins-official; then") {
		t.Error("should install claude-md-management plugin")
	}

	// Script should exit with failure if any operations failed
	if !strings.Contains(scriptStr, "exit 1") {
		t.Error("script should exit with failure on errors")
	}
}

func TestGenerateDockerfileSnippetEmpty(t *testing.T) {
	result := GenerateDockerfileSnippet(nil, nil, "moatuser")
	if result.DockerfileSnippet != "" {
		t.Error("empty input should return empty snippet")
	}
	if result.ScriptName != "" {
		t.Error("empty input should return empty script name")
	}

	result = GenerateDockerfileSnippet([]MarketplaceConfig{}, []string{}, "moatuser")
	if result.DockerfileSnippet != "" {
		t.Error("empty slices should return empty snippet")
	}
}

func TestGenerateDockerfileSnippetValidation(t *testing.T) {
	t.Run("invalid marketplace repo", func(t *testing.T) {
		marketplaces := []MarketplaceConfig{
			{Name: "good", Source: "github", Repo: "valid/repo"},
			{Name: "evil", Source: "github", Repo: "; rm -rf /"},
		}

		result := GenerateDockerfileSnippet(marketplaces, nil, "moatuser")
		scriptStr := string(result.ScriptContent)

		// Valid repo should be included
		if !strings.Contains(scriptStr, "marketplace add valid/repo") {
			t.Error("valid marketplace should be included")
		}
		// Invalid repo should trigger error message
		if !strings.Contains(scriptStr, "ERROR: Invalid marketplace repo format: evil") {
			t.Error("invalid marketplace should show error message with name")
		}
		// The malicious repo value should NOT appear in the output
		if strings.Contains(scriptStr, "; rm -rf /") {
			t.Error("invalid repo value should not appear in output")
		}
	})

	t.Run("invalid plugin key", func(t *testing.T) {
		plugins := []string{
			"valid-plugin@valid-market",
			"bad;rm -rf /@market",
		}

		result := GenerateDockerfileSnippet(nil, plugins, "moatuser")
		scriptStr := string(result.ScriptContent)

		// Valid plugin should be included
		if !strings.Contains(scriptStr, "plugin install valid-plugin@valid-market") {
			t.Error("valid plugin should be included")
		}
		// Invalid plugin should trigger error message
		if !strings.Contains(scriptStr, "ERROR: Invalid plugin format") {
			t.Error("invalid plugin should show error message")
		}
		// The malicious plugin value should NOT appear in the output
		if strings.Contains(scriptStr, "bad;rm -rf /") {
			t.Error("invalid plugin value should not appear in output")
		}
	})

	t.Run("invalid marketplace name", func(t *testing.T) {
		marketplaces := []MarketplaceConfig{
			{Name: "it's-bad", Source: "github", Repo: "valid/repo"},
		}

		result := GenerateDockerfileSnippet(marketplaces, nil, "moatuser")
		scriptStr := string(result.ScriptContent)

		// Invalid name should trigger error but not embed the unsafe name
		if !strings.Contains(scriptStr, "ERROR: Invalid marketplace name") {
			t.Error("invalid marketplace name should show error message")
		}
		// The name with single quote should NOT appear in the script
		if strings.Contains(scriptStr, "it's-bad") {
			t.Error("invalid marketplace name should not appear in output")
		}
	})

	t.Run("path traversal in pre-cloned path", func(t *testing.T) {
		marketplaces := []MarketplaceConfig{
			{Name: "legit", Source: "github", Repo: "org/legit", PreCloned: "marketplace-legit.tar"},
			{Name: "evil", Source: "github", Repo: "org/evil", PreCloned: "../../../etc/passwd"},
		}

		result := GenerateDockerfileSnippet(marketplaces, nil, "moatuser")

		// Legitimate marketplace should get COPY + tar extract
		if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser marketplace-legit.tar") {
			t.Error("valid pre-cloned marketplace should get COPY directive")
		}
		if !strings.Contains(result.DockerfileSnippet, "tar xf /tmp/marketplace-legit.tar") {
			t.Error("valid pre-cloned marketplace should get tar extract")
		}
		// Path traversal should be rejected — no COPY for it
		if strings.Contains(result.DockerfileSnippet, "etc/passwd") {
			t.Error("path traversal in PreCloned should be rejected from Dockerfile")
		}
		// known_marketplaces.json should only contain the valid entry
		if result.ExtraContextFiles == nil {
			t.Fatal("ExtraContextFiles should not be nil")
		}
		knownJSON := string(result.ExtraContextFiles["known-marketplaces.json"])
		if !strings.Contains(knownJSON, "legit") {
			t.Error("known_marketplaces.json should contain the valid marketplace")
		}
		if strings.Contains(knownJSON, "evil") {
			t.Error("known_marketplaces.json should NOT contain the rejected marketplace")
		}
	})
}

func TestGenerateDockerfileSnippetPreCloned(t *testing.T) {
	// Mixed: one pre-cloned, one remote marketplace.
	marketplaces := []MarketplaceConfig{
		{Name: "private-market", Source: "github", Repo: "org/private-market", PreCloned: "marketplace-private-market.tar"},
		{Name: "public-market", Source: "github", Repo: "org/public-market"},
	}
	plugins := []string{
		"my-plugin@private-market",
		"other-plugin@public-market",
	}

	result := GenerateDockerfileSnippet(marketplaces, plugins, "moatuser")

	// --- Dockerfile snippet checks ---

	// Pre-cloned marketplace should get COPY + tar extract
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser marketplace-private-market.tar /tmp/marketplace-private-market.tar") {
		t.Error("should COPY pre-cloned marketplace tar")
	}
	if !strings.Contains(result.DockerfileSnippet, "tar xf /tmp/marketplace-private-market.tar -C /home/moatuser/.claude/plugins/marketplaces/private-market") {
		t.Error("should extract tar into marketplace directory")
	}
	// known_marketplaces.json should get a COPY command
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser known-marketplaces.json /home/moatuser/.claude/plugins/known_marketplaces.json") {
		t.Error("should COPY known-marketplaces.json")
	}

	// Script COPY and RUN should still be present
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser claude-plugins.sh /tmp/claude-plugins.sh") {
		t.Error("should COPY plugin install script")
	}
	if !strings.Contains(result.DockerfileSnippet, "RUN bash /tmp/claude-plugins.sh") {
		t.Error("should run plugin install script")
	}

	// --- Script checks ---
	scriptStr := string(result.ScriptContent)

	// Remote marketplace should get marketplace add
	if !strings.Contains(scriptStr, "marketplace add org/public-market") {
		t.Error("remote marketplace should use marketplace add")
	}
	// Pre-cloned marketplace should NOT get marketplace add
	if strings.Contains(scriptStr, "marketplace add org/private-market") {
		t.Error("pre-cloned marketplace should NOT use marketplace add")
	}

	// ALL plugins should get plugin install
	if !strings.Contains(scriptStr, "plugin install my-plugin@private-market") {
		t.Error("should install plugin from pre-cloned marketplace")
	}
	if !strings.Contains(scriptStr, "plugin install other-plugin@public-market") {
		t.Error("should install plugin from remote marketplace")
	}

	// --- ExtraContextFiles checks ---
	if result.ExtraContextFiles == nil {
		t.Fatal("ExtraContextFiles should not be nil when pre-cloned marketplaces exist")
	}
	knownJSON, ok := result.ExtraContextFiles["known-marketplaces.json"]
	if !ok {
		t.Fatal("ExtraContextFiles should contain known-marketplaces.json")
	}
	// Verify it's valid JSON containing the pre-cloned marketplace
	if !strings.Contains(string(knownJSON), "private-market") {
		t.Error("known-marketplaces.json should contain the pre-cloned marketplace name")
	}
	// Should NOT contain the remote marketplace
	if strings.Contains(string(knownJSON), "public-market") {
		t.Error("known-marketplaces.json should NOT contain remote marketplace")
	}
}

func TestGenerateDockerfileSnippetAllPreCloned(t *testing.T) {
	// All marketplaces are pre-cloned — no marketplace add commands at all.
	marketplaces := []MarketplaceConfig{
		{Name: "alpha", Source: "github", Repo: "org/alpha", PreCloned: "marketplace-alpha.tar"},
		{Name: "beta", Source: "git", Repo: "https://git.example.com/beta.git", PreCloned: "marketplace-beta.tar"},
	}
	plugins := []string{
		"tool-a@alpha",
		"tool-b@beta",
	}

	result := GenerateDockerfileSnippet(marketplaces, plugins, "moatuser")

	// --- Script checks ---
	scriptStr := string(result.ScriptContent)

	// No marketplace add at all
	if strings.Contains(scriptStr, "marketplace add") {
		t.Error("all pre-cloned: should have NO marketplace add commands")
	}

	// Plugin install should still work
	if !strings.Contains(scriptStr, "plugin install tool-a@alpha") {
		t.Error("should install tool-a plugin")
	}
	if !strings.Contains(scriptStr, "plugin install tool-b@beta") {
		t.Error("should install tool-b plugin")
	}

	// --- Dockerfile snippet checks ---

	// Both marketplaces should have COPY + tar extract
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser marketplace-alpha.tar /tmp/marketplace-alpha.tar") {
		t.Error("should COPY alpha marketplace tar")
	}
	if !strings.Contains(result.DockerfileSnippet, "tar xf /tmp/marketplace-alpha.tar -C /home/moatuser/.claude/plugins/marketplaces/alpha") {
		t.Error("should extract alpha tar")
	}
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser marketplace-beta.tar /tmp/marketplace-beta.tar") {
		t.Error("should COPY beta marketplace tar")
	}
	if !strings.Contains(result.DockerfileSnippet, "tar xf /tmp/marketplace-beta.tar -C /home/moatuser/.claude/plugins/marketplaces/beta") {
		t.Error("should extract beta tar")
	}

	// known_marketplaces.json should be present
	if !strings.Contains(result.DockerfileSnippet, "COPY --chown=moatuser known-marketplaces.json /home/moatuser/.claude/plugins/known_marketplaces.json") {
		t.Error("should COPY known-marketplaces.json")
	}

	// --- ExtraContextFiles checks ---
	if result.ExtraContextFiles == nil {
		t.Fatal("ExtraContextFiles should not be nil")
	}
	knownJSON, ok := result.ExtraContextFiles["known-marketplaces.json"]
	if !ok {
		t.Fatal("ExtraContextFiles should contain known-marketplaces.json")
	}
	if !strings.Contains(string(knownJSON), "alpha") {
		t.Error("known-marketplaces.json should contain alpha")
	}
	if !strings.Contains(string(knownJSON), "beta") {
		t.Error("known-marketplaces.json should contain beta")
	}
}

func TestGenerateDockerfileSnippetKeepsDockerfileSmall(t *testing.T) {
	// Verify the Dockerfile snippet is small regardless of plugin count
	var plugins []string
	for i := 0; i < 50; i++ {
		plugins = append(plugins, "plugin-name@marketplace-name")
	}

	result := GenerateDockerfileSnippet(nil, plugins, "moatuser")

	// Dockerfile snippet should be tiny (just COPY + RUN)
	if len(result.DockerfileSnippet) > 500 {
		t.Errorf("Dockerfile snippet too large (%d bytes), should be under 500", len(result.DockerfileSnippet))
	}

	// Script should contain all the commands
	if !strings.Contains(string(result.ScriptContent), "plugin install") {
		t.Error("script should contain plugin install commands")
	}
}

func TestGenerateDockerfileSnippetTarCleanup(t *testing.T) {
	// Verify the Dockerfile removes the tar after extraction
	marketplaces := []MarketplaceConfig{
		{Name: "test-market", Source: "github", Repo: "org/test", PreCloned: "marketplace-test-market.tar"},
	}

	result := GenerateDockerfileSnippet(marketplaces, nil, "moatuser")

	if !strings.Contains(result.DockerfileSnippet, "rm /tmp/marketplace-test-market.tar") {
		t.Error("should clean up tar file after extraction")
	}
}
