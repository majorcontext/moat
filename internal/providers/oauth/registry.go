package oauth

import (
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/mcpcatalog"
)

// LookupServerURL returns the well-known MCP server URL for a named OAuth
// grant, or "" if the name is not a catalog entry that authenticates via OAuth.
// API-key servers (e.g. context7) are deliberately excluded so they don't feed
// into OAuth auto-discovery, which would fail confusingly.
func LookupServerURL(name string) string {
	e, ok := mcpcatalog.Lookup(name)
	if !ok {
		log.Debug("no catalog entry for OAuth name", "name", name)
		return ""
	}
	if !e.OAuth {
		log.Debug("catalog entry is not OAuth-based; skipping discovery", "name", name, "grant", e.Grant)
		return ""
	}
	return e.URL
}
