package claude

import (
	"reflect"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestOAuthProvider_JoinCommand(t *testing.T) {
	p := &OAuthProvider{}

	tests := []struct {
		name string
		opts provider.JoinOpts
		want []string
	}{
		{
			name: "bare",
			opts: provider.JoinOpts{},
			want: []string{"claude", "--dangerously-skip-permissions"},
		},
		{
			name: "continue",
			opts: provider.JoinOpts{Continue: true},
			want: []string{"claude", "--dangerously-skip-permissions", "--continue"},
		},
		{
			name: "resume",
			opts: provider.JoinOpts{Resume: "abc123"},
			want: []string{"claude", "--dangerously-skip-permissions", "--resume", "abc123"},
		},
		{
			name: "prompt",
			opts: provider.JoinOpts{Prompt: "hi"},
			want: []string{"claude", "--dangerously-skip-permissions", "-p", "hi"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := p.JoinCommand(tt.opts)
			if err != nil {
				t.Fatalf("JoinCommand: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("JoinCommand = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestOAuthProvider_IdentifiesAs(t *testing.T) {
	p := &OAuthProvider{}
	for _, agent := range []string{"claude", "claude-code"} {
		if !p.IdentifiesAs(agent) {
			t.Errorf("IdentifiesAs(%q) = false, want true", agent)
		}
	}
	for _, agent := range []string{"codex", "gemini", ""} {
		if p.IdentifiesAs(agent) {
			t.Errorf("IdentifiesAs(%q) = true, want false", agent)
		}
	}
}

// Compile-time assertion that the provider satisfies JoinableAgent.
var _ provider.JoinableAgent = (*OAuthProvider)(nil)
