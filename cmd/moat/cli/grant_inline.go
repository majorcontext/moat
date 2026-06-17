// cmd/moat/cli/grant_inline.go
package cli

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/majorcontext/moat/internal/run"
	"github.com/majorcontext/moat/internal/ui"
)

// grantDispatch maps a grant string to a grant verb and its args, derived from
// run.GrantToCommand ("oauth:notion" -> "oauth notion"). A bare provider name
// ("github") has no verb prefix and dispatches to the generic grant path.
func grantDispatch(grant string) (verb string, args []string) {
	fields := strings.Fields(run.GrantToCommand(grant))
	switch {
	case len(fields) >= 2 && fields[0] == "oauth":
		return "oauth", fields[1:]
	case len(fields) >= 2 && fields[0] == "mcp":
		return "mcp", fields[1:]
	default:
		return "generic", fields[:1]
	}
}

// promptForMissingGrants runs the interactive prompt loop against stdin/stdout.
// Returns the number of grants successfully granted.
func promptForMissingGrants(ctx context.Context, missing []run.MissingGrant) int {
	return promptLoop(ctx, missing, bufio.NewReader(os.Stdin), os.Stdout, grantInline)
}

// promptLoop is the testable core: prints a summary, then prompts per
// promptable grant (default Y) and invokes grantFn. Non-promptable grants are
// listed with their fix command and skipped. Returns the count granted.
func promptLoop(
	ctx context.Context,
	missing []run.MissingGrant,
	in *bufio.Reader,
	out io.Writer,
	grantFn func(context.Context, string) error,
) int {
	fmt.Fprintf(out, "\n%d grant(s) needed before this run can start:\n", len(missing))
	for _, m := range missing {
		fmt.Fprintf(out, "  • %s\n", m.Grant)
	}
	fmt.Fprintln(out)

	granted := 0
	for _, m := range missing {
		if !m.Promptable {
			fmt.Fprintf(out, "%s: cannot grant interactively — run: %s\n", m.Grant, m.FixCommand)
			continue
		}
		fmt.Fprintf(out, "Grant %s now? [Y/n] ", m.Grant)
		line, _ := in.ReadString('\n')
		ans := strings.ToLower(strings.TrimSpace(line))
		if ans == "n" || ans == "no" {
			fmt.Fprintf(out, "  Skipped. Run later with: %s\n", m.FixCommand)
			continue
		}
		if err := grantFn(ctx, m.Grant); err != nil {
			fmt.Fprintf(out, "  %s %s: %v\n", ui.Red("✗"), m.Grant, err)
			continue
		}
		fmt.Fprintf(out, "  %s %s granted\n", ui.Green("✓"), m.Grant)
		granted++
	}
	return granted
}

// stdinIsInteractive reports whether both stdin and stdout are terminals, so an
// inline prompt makes sense.
func stdinIsInteractive() bool {
	return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd()))
}

// grantInline runs the existing interactive grant flow for a single grant,
// reusing the same RunE the `moat grant ...` subcommands use.
func grantInline(ctx context.Context, grant string) error {
	verb, args := grantDispatch(grant)
	switch verb {
	case "oauth":
		grantOAuthCmd.SetContext(ctx)
		return runGrantOAuth(grantOAuthCmd, args)
	case "mcp":
		grantMCPCmd.SetContext(ctx)
		return runGrantMCP(grantMCPCmd, args)
	default:
		grantCmd.SetContext(ctx)
		return runGrant(grantCmd, args)
	}
}
