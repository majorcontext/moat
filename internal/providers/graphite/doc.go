// Package graphite implements a credential provider for the Graphite CLI (gt).
//
// Graphite is a stacked-PR workflow tool. The CLI stores its auth token in
// ~/.config/graphite/user_config and sends it as "Authorization: token <token>"
// to api.graphite.com. All GitHub operations are proxied through Graphite's server.
//
// The provider mounts a config file with a placeholder token into the container.
// The proxy intercepts requests to api.graphite.com and replaces the placeholder
// with the real token.
package graphite
