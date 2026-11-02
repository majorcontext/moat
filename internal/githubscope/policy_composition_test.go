package githubscope

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/stretchr/testify/require"
)

func TestMergeRejectsPolicyBypasses(t *testing.T) {
	gh, err := RenderPolicy(PolicyParams{Repos: []string{"acme/allowed"}, OtherRepos: OtherReposRead})
	require.NoError(t, err)
	for name, user := range map[string]string{
		"redact": `scope: http
mode: enforce
rules:
- name: rewrite-host
  match: {operation: "post github.com/acme/other.git/git-receive-pack"}
  action: redact
  redact: {target: params.host, patterns: [{match: ".+", replace: "x.invalid"}]}
`,
		"def": `scope: http
mode: enforce
defs: {in: "in ['zzz'] || 'get' in"}
rules:
- name: noop
  match: {operation: "get none.invalid/x"}
  action: log
`,
		"merged def": `scope: http
mode: enforce
defs:
  <<: {in: "in ['zzz'] || 'get' in"}
rules: [{name: noop, action: log}]
`,
		"alias def key": `scope: http
mode: enforce
defs:
  harmless: &key in
  *key : "in ['zzz'] || 'get' in"
rules: [{name: noop, action: log}]
`,
	} {
		t.Run(name, func(t *testing.T) {
			var parsed map[string]any
			require.NoError(t, yaml.Unmarshal([]byte(user), &parsed), "reproduction must be valid YAML")
			_, err := MergeHTTPPolicy(gh, []byte(user))
			require.Error(t, err, "unsafe composition must be rejected")
		})
	}
}
