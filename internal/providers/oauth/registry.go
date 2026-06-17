package oauth

import (
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/mcpcatalog"
)

// LookupServerURL returns the well-known MCP server URL for a named OAuth
// grant, or "" if the name is not in the catalog.
func LookupServerURL(name string) string {
	e, ok := mcpcatalog.Lookup(name)
	if !ok {
		log.Debug("no catalog entry for OAuth name", "name", name)
		return ""
	}
	return e.URL
}
