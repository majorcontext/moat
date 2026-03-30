package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeKeepTestConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644)
	return dir
}

func TestMCPServerPolicy_StarterPack(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy: linear-readonly
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.Len(t, cfg.MCP, 1)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, "linear-readonly", cfg.MCP[0].Policy.Pack)
}

func TestMCPServerPolicy_FilePath(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy: .keep/linear.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, ".keep/linear.yaml", cfg.MCP[0].Policy.File)
}

func TestMCPServerPolicy_Inline(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy:
      deny: [delete_issue, close_issue]
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.MCP[0].Policy)
	assert.Equal(t, []string{"delete_issue", "close_issue"}, cfg.MCP[0].Policy.Deny)
}

func TestNetworkKeepPolicy(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
network:
  policy: strict
  keep_policy: .keep/api-policy.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.Network.KeepPolicy)
	assert.Equal(t, ".keep/api-policy.yaml", cfg.Network.KeepPolicy.File)
}

func TestClaudeLLMGateway(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
claude:
  llm-gateway:
    policy: .keep/llm-rules.yaml
`)
	cfg, err := Load(dir)
	require.NoError(t, err)
	require.NotNil(t, cfg.Claude.LLMGateway)
	require.NotNil(t, cfg.Claude.LLMGateway.Policy)
	assert.Equal(t, ".keep/llm-rules.yaml", cfg.Claude.LLMGateway.Policy.File)
}

func TestClaudeLLMGateway_ConflictsWithBaseURL(t *testing.T) {
	dir := writeKeepTestConfig(t, `
name: test
claude:
  base_url: http://localhost:8080
  llm-gateway:
    policy: .keep/llm-rules.yaml
`)
	_, err := Load(dir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "mutually exclusive")
}
