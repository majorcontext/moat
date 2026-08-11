package run

import (
	"reflect"
	"testing"

	"github.com/majorcontext/moat/internal/deps"
)

func TestComputeJoinableAgents(t *testing.T) {
	tests := []struct {
		name          string
		initProviders []string
		depList       []deps.Dependency
		want          []string
	}{
		{
			name:          "staged with CLI dep is joinable",
			initProviders: []string{"claude"},
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{"claude"},
		},
		{
			name:          "staged without CLI dep is not joinable",
			initProviders: []string{"claude"},
			depList:       []deps.Dependency{{Name: "git"}},
			want:          []string{},
		},
		{
			name:          "CLI dep without staging is not joinable",
			initProviders: nil,
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{},
		},
		{
			name:          "dual agent",
			initProviders: []string{"claude", "codex"},
			depList:       []deps.Dependency{{Name: "claude-code"}, {Name: "codex-cli"}},
			want:          []string{"claude", "codex"},
		},
		{
			name:          "result is sorted regardless of input order",
			initProviders: []string{"codex", "claude"},
			depList:       []deps.Dependency{{Name: "codex-cli"}, {Name: "claude-code"}},
			want:          []string{"claude", "codex"},
		},
		{
			name:          "unknown provider name is ignored",
			initProviders: []string{"nonsense"},
			depList:       []deps.Dependency{{Name: "claude-code"}},
			want:          []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeJoinableAgents(tt.initProviders, tt.depList)
			if got == nil {
				t.Fatal("computeJoinableAgents must never return nil — nil means 'pre-upgrade run' downstream")
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("computeJoinableAgents() = %v, want %v", got, tt.want)
			}
		})
	}
}
