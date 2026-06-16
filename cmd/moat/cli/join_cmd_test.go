package cli

import (
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

// fakeJoinable implements provider.JoinableAgent for validation tests.
type fakeJoinable struct{ identifies bool }

func (f fakeJoinable) JoinCommand(provider.JoinOpts) ([]string, error) { return []string{"x"}, nil }
func (f fakeJoinable) IdentifiesAs(string) bool                        { return f.identifies }

func TestValidateJoinAgent_OK(t *testing.T) {
	if err := validateJoinAgent(fakeJoinable{identifies: true}, "claude", "claude-code"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateJoinAgent_WrongProvider(t *testing.T) {
	err := validateJoinAgent(fakeJoinable{identifies: false}, "codex", "claude-code")
	if err == nil || !strings.Contains(err.Error(), "no codex configuration") {
		t.Fatalf("got %v, want a clear 'no codex configuration' error", err)
	}
}
