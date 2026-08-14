package codex

import (
	"reflect"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/provider"
)

func TestJoinCommand(t *testing.T) {
	p := &Provider{}

	// Interactive: bare codex, mirroring BuildCommand.
	got, err := p.JoinCommand(provider.JoinOpts{})
	if err != nil {
		t.Fatalf("JoinCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"codex"}) {
		t.Errorf("interactive join = %v, want [codex]", got)
	}

	// Companion: headless uses codex exec.
	got, err = p.JoinCommand(provider.JoinOpts{Prompt: "summarize the diff"})
	if err != nil {
		t.Fatalf("JoinCommand: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"codex", "exec", "summarize the diff"}) {
		t.Errorf("headless join = %v, want [codex exec summarize the diff]", got)
	}
}

func TestJoinCommandRejectsUnsupportedSessionFlags(t *testing.T) {
	p := &Provider{}

	if _, err := p.JoinCommand(provider.JoinOpts{Continue: true}); err == nil {
		t.Error("--continue should error rather than being silently dropped")
	} else if !strings.Contains(err.Error(), "--continue") {
		t.Errorf("error should name the flag; got %q", err)
	}

	// Companion: --resume errors the same way.
	if _, err := p.JoinCommand(provider.JoinOpts{Resume: "abc123"}); err == nil {
		t.Error("--resume should error rather than being silently dropped")
	} else if !strings.Contains(err.Error(), "--resume") {
		t.Errorf("error should name the flag; got %q", err)
	}
}

func TestIdentifiesAs(t *testing.T) {
	p := &Provider{}
	if !p.IdentifiesAs("codex") {
		t.Error("IdentifiesAs(codex) should be true")
	}
	// Companion: it must not claim other agents' runs.
	if p.IdentifiesAs("claude") {
		t.Error("IdentifiesAs(claude) should be false")
	}
}
