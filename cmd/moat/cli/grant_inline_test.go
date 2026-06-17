// cmd/moat/cli/grant_inline_test.go
package cli

import (
	"strings"
	"testing"
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
