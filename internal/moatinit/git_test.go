package moatinit

import (
	"reflect"
	"testing"
)

func TestGitConfigCommands(t *testing.T) {
	safeDir := []string{"git", "config", "--system", "--add", "safe.directory", "/workspace"}
	proxyAuth := []string{"git", "config", "--system", "http.proxyAuthMethod", "basic"}
	insteadOf := []string{"git", "config", "--system", "url.git@github.com:.insteadOf", "https://github.com/"}

	tests := []struct {
		name string
		cfg  Config
		want [][]string
	}{
		// GIT-02/GIT-05: safe.directory and proxyAuthMethod are
		// unconditional (present with no MOAT_GIT_* env at all).
		{"no env", Config{}, [][]string{safeDir, proxyAuth}},
		// GIT-03: user.name only.
		{
			"name only",
			Config{GitUserName: "Ada Lovelace"},
			[][]string{safeDir, {"git", "config", "--system", "user.name", "Ada Lovelace"}, proxyAuth},
		},
		// GIT-04: user.email independent of user.name.
		{
			"email only",
			Config{GitUserEmail: "ada@example.com"},
			[][]string{safeDir, {"git", "config", "--system", "user.email", "ada@example.com"}, proxyAuth},
		},
		{
			"both identity",
			Config{GitUserName: "Ada", GitUserEmail: "ada@example.com"},
			[][]string{
				safeDir,
				{"git", "config", "--system", "user.name", "Ada"},
				{"git", "config", "--system", "user.email", "ada@example.com"},
				proxyAuth,
			},
		},
		// GIT-06: insteadOf requires exactly "1"...
		{"ssh github on", Config{GitSSHGitHub: "1"}, [][]string{safeDir, proxyAuth, insteadOf}},
		// ...companions: "0", "true", and empty all skip it.
		{"ssh github opt-out", Config{GitSSHGitHub: "0"}, [][]string{safeDir, proxyAuth}},
		{"ssh github non-1", Config{GitSSHGitHub: "true"}, [][]string{safeDir, proxyAuth}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gitConfigCommands(&tt.cfg)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("gitConfigCommands() =\n%v\nwant\n%v", got, tt.want)
			}
		})
	}
}
