package keep

import (
	"testing"

	keeplib "github.com/majorcontext/keep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetStarterPack_Known(t *testing.T) {
	data, err := GetStarterPack("linear-readonly")
	require.NoError(t, err)
	assert.Contains(t, string(data), "scope:")
	assert.Contains(t, string(data), "rules:")
}

func TestGetStarterPack_Unknown(t *testing.T) {
	_, err := GetStarterPack("nonexistent-pack")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown starter pack")
}

func TestListStarterPacks(t *testing.T) {
	packs := ListStarterPacks()
	assert.Contains(t, packs, "linear-readonly")
}

func TestLinearReadonlyPack_ScopeRewrite(t *testing.T) {
	pc := &PolicyConfig{Pack: "linear-readonly"}
	yamlBytes, err := ResolvePolicyYAML(pc, "mcp-linear", "")
	require.NoError(t, err)
	assert.Contains(t, string(yamlBytes), "scope: mcp-linear")

	// Compile and evaluate to verify the pack works end-to-end.
	eng, compileErr := keeplib.LoadFromBytes(yamlBytes)
	require.NoError(t, compileErr)
	defer eng.Close()

	// Read operations should be allowed.
	for _, op := range []string{"list_issues", "get_issue", "search_issues"} {
		call := NormalizeMCPCall(op, nil, "mcp-linear")
		result, evalErr := SafeEvaluate(eng, call, "mcp-linear")
		require.NoError(t, evalErr, "operation %s", op)
		assert.Equal(t, keeplib.Allow, result.Decision, "operation %s should be allowed", op)
	}

	// Write operations should be denied.
	for _, op := range []string{"create_issue", "delete_issue", "update_issue"} {
		call := NormalizeMCPCall(op, nil, "mcp-linear")
		result, evalErr := SafeEvaluate(eng, call, "mcp-linear")
		require.NoError(t, evalErr, "operation %s", op)
		assert.Equal(t, keeplib.Deny, result.Decision, "operation %s should be denied", op)
	}
}
