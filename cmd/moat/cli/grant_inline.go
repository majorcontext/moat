// cmd/moat/cli/grant_inline.go
package cli

import (
	"context"
	"strings"

	"github.com/majorcontext/moat/internal/run"
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
