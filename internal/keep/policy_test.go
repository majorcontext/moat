package keep

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestPolicyConfigUnmarshalYAML_StarterPack(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`"linear-readonly"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, "linear-readonly", pc.Pack)
	assert.Empty(t, pc.File)
}

func TestPolicyConfigUnmarshalYAML_FilePath(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`".keep/linear.yaml"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, ".keep/linear.yaml", pc.File)
	assert.Empty(t, pc.Pack)
}

func TestPolicyConfigUnmarshalYAML_FilePathNoSlash(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`"rules.yaml"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, "rules.yaml", pc.File)
	assert.Empty(t, pc.Pack)
}

func TestPolicyConfigUnmarshalYAML_FilePathYml(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`"rules.yml"`), &pc)
	require.NoError(t, err)
	assert.Equal(t, "rules.yml", pc.File)
	assert.Empty(t, pc.Pack)
}

func TestPolicyConfigUnmarshalYAML_Inline(t *testing.T) {
	input := `
deny:
  - delete_issue
mode: enforce
`
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(input), &pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"delete_issue"}, pc.Deny)
	assert.Equal(t, "enforce", pc.Mode)
	assert.Empty(t, pc.Pack)
	assert.Empty(t, pc.File)
}

func TestPolicyConfigUnmarshalYAML_InvalidNode(t *testing.T) {
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(`[1, 2, 3]`), &pc)
	assert.Error(t, err)
}

func TestToKeepYAML_DenyOnly(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"delete_issue"},
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "scope: mcp-tools")
	assert.Contains(t, yamlStr, "mode: enforce")
	assert.Contains(t, yamlStr, "operation: delete_issue")
	assert.Contains(t, yamlStr, "action: deny")
}

func TestToKeepYAML_MultipleDeny(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"Edit", "Write", "Bash"},
		Mode: "enforce",
	}
	yamlBytes, err := pc.ToKeepYAML("llm-gateway")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "operation: Edit")
	assert.Contains(t, yamlStr, "operation: Write")
	assert.Contains(t, yamlStr, "operation: Bash")
	assert.Equal(t, 3, len(pc.Deny))
}

func TestToKeepYAML_AuditMode(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"delete_issue"},
		Mode: "audit",
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "mode: audit_only")
}

func TestToKeepYAML_DefaultMode(t *testing.T) {
	pc := PolicyConfig{
		Deny: []string{"delete_issue"},
	}
	yamlBytes, err := pc.ToKeepYAML("test-scope")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "mode: enforce")
}

func TestPolicyConfigUnmarshalYAML_AllowField(t *testing.T) {
	input := `
allow:
  - get_issue
  - list_issues
deny:
  - delete_issue
mode: enforce
`
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(input), &pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"get_issue", "list_issues"}, pc.Allow)
	assert.Equal(t, []string{"delete_issue"}, pc.Deny)
	assert.True(t, pc.IsInline())
}

func TestPolicyConfigUnmarshalYAML_AllowOnly(t *testing.T) {
	input := `
allow:
  - get_issue
`
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(input), &pc)
	require.NoError(t, err)
	assert.Equal(t, []string{"get_issue"}, pc.Allow)
	assert.Empty(t, pc.Deny)
	assert.True(t, pc.IsInline())
}

func TestPolicyConfigUnmarshalYAML_InvalidMode(t *testing.T) {
	input := `
deny:
  - delete_issue
mode: monitor
`
	var pc PolicyConfig
	err := yaml.Unmarshal([]byte(input), &pc)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid policy mode")
}

func TestToKeepYAML_AllowWithDefaultDeny(t *testing.T) {
	pc := PolicyConfig{
		Allow: []string{"get_issue", "list_issues"},
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "name: allow-get_issue")
	assert.Contains(t, yamlStr, "name: allow-list_issues")
	assert.Contains(t, yamlStr, "action: allow")
	assert.Contains(t, yamlStr, "name: default-deny")
	assert.Contains(t, yamlStr, "operation: '*'")
	assert.Contains(t, yamlStr, "action: deny")
}

func TestToKeepYAML_AllowAndDeny(t *testing.T) {
	pc := PolicyConfig{
		Allow: []string{"get_issue"},
		Deny:  []string{"delete_issue"},
	}
	yamlBytes, err := pc.ToKeepYAML("mcp-tools")
	require.NoError(t, err)

	yamlStr := string(yamlBytes)
	assert.Contains(t, yamlStr, "name: allow-get_issue")
	assert.Contains(t, yamlStr, "name: deny-delete_issue")
	assert.Contains(t, yamlStr, "name: default-deny")
}

func TestToKeepYAML_NonInlineErrors(t *testing.T) {
	pc := PolicyConfig{File: "rules.yaml"}
	_, err := pc.ToKeepYAML("test")
	require.Error(t, err)
}

func TestResolvePolicyYAML_InlineRules(t *testing.T) {
	pc := &PolicyConfig{
		Deny: []string{"delete_issue"},
	}
	yamlBytes, err := ResolvePolicyYAML(pc, "linear", "")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: linear")
}

func TestResolvePolicyYAML_FileReference(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	os.WriteFile(path, []byte("scope: test\nrules: []\n"), 0o644)

	pc := &PolicyConfig{File: "rules.yaml"}
	yamlBytes, err := ResolvePolicyYAML(pc, "test", dir)
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: test")
}

func TestResolvePolicyYAML_AbsoluteFilePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rules.yaml")
	os.WriteFile(path, []byte("scope: test\nrules: []\n"), 0o644)

	pc := &PolicyConfig{File: path}
	yamlBytes, err := ResolvePolicyYAML(pc, "test", "")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: test")
}

func TestResolvePolicyYAML_MissingFile(t *testing.T) {
	pc := &PolicyConfig{File: "/nonexistent/rules.yaml"}
	_, err := ResolvePolicyYAML(pc, "test", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "policy file not found")
}

func TestResolvePolicyYAML_UnknownPack(t *testing.T) {
	pc := &PolicyConfig{Pack: "nonexistent"}
	_, err := ResolvePolicyYAML(pc, "test", "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown starter pack")
}
