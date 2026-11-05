package daemon

import (
	"fmt"
	"strings"

	"github.com/majorcontext/gatekeeper/proxy"
)

// CA subject for the daemon's TLS-interception CA. A CA created by an earlier
// moat keeps the subject it was created with.
const (
	CAOrganization = "Moat"
	CACommonName   = "Moat CA"
)

// Branding is the client-visible wording the daemon's proxy uses for blocked
// responses and relay errors. Gatekeeper's defaults are neutral; moat keeps
// the wording (and the MCP stub prefix) it has always used, and points the
// hints at moat.yaml and moat commands.
//
// MCPStubPrefix must match the placeholder moat writes into agent MCP configs
// (see run.mcpStubPrefix): the relay only swaps in the real credential when
// the client sends a value with this prefix.
func Branding() proxy.Branding {
	return proxy.Branding{
		Name:          "Moat",
		ErrorPrefix:   "MOAT",
		MCPStubPrefix: MCPStubPrefix,
		PolicyScheme:  "Moat-Policy",
		NetworkHint:   "To allow this request, update network.rules in moat.yaml.",
		HostHint: func(port int) string {
			return fmt.Sprintf("To allow port %d on the host, add to moat.yaml:\n\n  network:\n    host:\n      - %d", port, port)
		},
		GrantHint: func(grant string) string {
			return "Run: moat grant " + grantToCommand(grant)
		},
	}
}

// MCPStubPrefix starts the placeholder credential moat writes into agent MCP
// configs; the proxy replaces it with the real credential.
const MCPStubPrefix = "moat-stub-"

// grantToCommand converts a grant name to the arguments of `moat grant`:
// "oauth:notion" → "oauth notion", "mcp-context7" → "mcp context7".
func grantToCommand(grant string) string {
	if kind, name, ok := strings.Cut(grant, ":"); ok {
		return kind + " " + name
	}
	if after, ok := strings.CutPrefix(grant, "mcp-"); ok {
		return "mcp " + after
	}
	return grant
}
