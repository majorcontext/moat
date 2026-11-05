package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadYAML(t *testing.T, content string) (*Config, error) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "moat.yaml"), []byte(content), 0o644))
	return Load(dir)
}

func TestLoadGitHubScope(t *testing.T) {
	cfg, err := loadYAML(t, `
grants: [github]
github:
  scope: workspace
  other_repos: none
  extra_repos:
    - MajorContext/Keep
    - majorcontext/keep.git
`)
	require.NoError(t, err)
	assert.Equal(t, "workspace", cfg.GitHub.Scope)
	assert.Equal(t, "none", cfg.GitHub.OtherRepos)
	assert.Equal(t, []string{"majorcontext/keep"}, cfg.GitHub.ExtraRepos, "normalized and deduplicated")
	assert.True(t, cfg.GitHub.Settings().Enabled())

	// Companion: omitting the block leaves the scope off.
	cfg, err = loadYAML(t, "grants: [github]\n")
	require.NoError(t, err)
	assert.False(t, cfg.GitHub.Settings().Enabled())
}

func TestLoadGitHubScopeInvalid(t *testing.T) {
	for name, tc := range map[string]struct{ yaml, want string }{
		"bad scope":             {"github:\n  scope: repo\n", "github.scope"},
		"bad other_repos":       {"github:\n  scope: workspace\n  other_repos: write\n", "github.other_repos"},
		"other_repos alone":     {"github:\n  other_repos: none\n", "only apply with github.scope: workspace"},
		"extra_repos with any":  {"github:\n  scope: any\n  extra_repos: [a/b]\n", "only apply with github.scope: workspace"},
		"malformed extra repo":  {"github:\n  scope: workspace\n  extra_repos: [justname]\n", "github.extra_repos"},
		"empty extra repo name": {"github:\n  scope: workspace\n  extra_repos: [\"\"]\n", "github.extra_repos"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadYAML(t, tc.yaml)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}
