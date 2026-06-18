// cmd/moat/cli/grant_inline_test.go
package cli

import (
	"bufio"
	"bytes"
	"context"
	stderrors "errors"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/run"
)

func TestGrantDispatchGenericForBareProvider(t *testing.T) {
	verb, args := grantDispatch("github")
	if verb != "generic" || len(args) != 1 || args[0] != "github" {
		t.Fatalf("github -> verb=%q args=%v", verb, args)
	}
}

func TestGrantDispatchOAuthAndMCP(t *testing.T) {
	if verb, args := grantDispatch("oauth:notion"); verb != "oauth" || strings.Join(args, ",") != "notion" {
		t.Fatalf("oauth:notion -> verb=%q args=%v", verb, args)
	}
	if verb, args := grantDispatch("mcp:render"); verb != "mcp" || strings.Join(args, ",") != "render" {
		t.Fatalf("mcp:render -> verb=%q args=%v", verb, args)
	}
}

func TestPromptLoopSkipsNonPromptable(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{
		{Grant: "aws", FixCommand: "moat grant aws --role=...", Promptable: false},
	}
	in := bufio.NewReader(strings.NewReader(""))
	granted := promptLoop(context.Background(), missing, in, &out, func(context.Context, string) error {
		t.Fatal("grantInline must not be called for non-promptable grants")
		return nil
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
	if !strings.Contains(out.String(), "moat grant aws") {
		t.Errorf("expected fix command in output, got: %s", out.String())
	}
}

func TestPromptLoopReadFailureSurfacesDetail(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{
		{Grant: "github", FixCommand: "moat grant github", Promptable: false, Detail: "reading credential file: permission denied"},
	}
	in := bufio.NewReader(strings.NewReader(""))
	granted := promptLoop(context.Background(), missing, in, &out, func(context.Context, string) error {
		t.Fatal("grantInline must not be called for a non-promptable read failure")
		return nil
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
	if !strings.Contains(out.String(), "permission denied") {
		t.Errorf("expected raw error in output, got: %s", out.String())
	}
}

func TestPromptLoopDefaultYesGrants(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{{Grant: "github", FixCommand: "moat grant github", Promptable: true}}
	in := bufio.NewReader(strings.NewReader("\n")) // bare Enter => default Y
	calls := 0
	granted := promptLoop(context.Background(), missing, in, &out, func(_ context.Context, g string) error {
		calls++
		if g != "github" {
			t.Fatalf("granted %q", g)
		}
		return nil
	})
	if granted != 1 || calls != 1 {
		t.Fatalf("granted=%d calls=%d, want 1/1", granted, calls)
	}
}

func TestPromptLoopDeclineSkips(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{{Grant: "github", FixCommand: "moat grant github", Promptable: true}}
	in := bufio.NewReader(strings.NewReader("n\n"))
	granted := promptLoop(context.Background(), missing, in, &out, func(context.Context, string) error {
		t.Fatal("grantInline must not be called when user declines")
		return nil
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
}

func TestPromptLoopGrantErrorStaysUngranted(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{{Grant: "github", FixCommand: "moat grant github", Promptable: true}}
	in := bufio.NewReader(strings.NewReader("y\n"))
	granted := promptLoop(context.Background(), missing, in, &out, func(context.Context, string) error {
		return errTestGrantFailed
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0 when grant fails", granted)
	}
}

var errTestGrantFailed = stderrors.New("boom")

// A malformed grant string (e.g. ":" from a bad mcp.auth.grant) must not panic
// grantDispatch — it maps to "moat grant " with no command, an empty fields
// slice. It should pass the raw grant through the generic path.
func TestGrantDispatchMalformedDoesNotPanic(t *testing.T) {
	for _, g := range []string{":", " ", "mcp:"} {
		verb, args := grantDispatch(g) // must not panic
		if verb == "" {
			t.Fatalf("grantDispatch(%q) returned empty verb", g)
		}
		_ = args
	}
}

// EOF (Ctrl-D) at the prompt must abort the loop, not be read as the default
// Yes and stampede every remaining grant against closed stdin.
func TestPromptLoopEOFAborts(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{
		{Grant: "github", FixCommand: "moat grant github", Promptable: true},
		{Grant: "oauth:notion", FixCommand: "moat grant oauth notion", Promptable: true},
	}
	in := bufio.NewReader(strings.NewReader("")) // immediate EOF
	granted := promptLoop(context.Background(), missing, in, &out, func(context.Context, string) error {
		t.Fatal("grantInline must not be called on EOF")
		return nil
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0 on EOF", granted)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected abort notice, got: %s", out.String())
	}
}

// A grant that fails because the context was canceled (Ctrl-C during an OAuth
// flow) must abort the loop, not prompt every remaining grant against the dead
// context and print a cascade of identical cancellation errors.
func TestPromptLoopCancelledContextAborts(t *testing.T) {
	var out bytes.Buffer
	missing := []run.MissingGrant{
		{Grant: "oauth:notion", FixCommand: "moat grant oauth notion", Promptable: true},
		{Grant: "oauth:linear", FixCommand: "moat grant oauth linear", Promptable: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // context already canceled, as after a Ctrl-C
	in := bufio.NewReader(strings.NewReader("y\ny\n"))
	calls := 0
	granted := promptLoop(ctx, missing, in, &out, func(c context.Context, _ string) error {
		calls++
		return c.Err()
	})
	if granted != 0 {
		t.Fatalf("granted=%d, want 0", granted)
	}
	if calls != 1 {
		t.Fatalf("grantFn called %d times, want 1 (loop must stop after cancellation)", calls)
	}
	if !strings.Contains(out.String(), "Aborted") {
		t.Errorf("expected abort notice, got: %s", out.String())
	}
}
