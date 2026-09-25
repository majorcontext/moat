package pi

import (
	"fmt"
	"slices"
	"strings"
)

// LunaRouteExtensionPackage is LunaRoute's Pi extension. It registers the
// "lunaroute" provider and syncs the model catalog from the gateway; without
// it Pi has no LunaRoute models to offer.
const LunaRouteExtensionPackage = "npm:@lunaroute/pi-extension"

// lunaRouteLaunchScript loads LunaRoute's model catalog, then execs pi with the
// script's arguments.
//
// Pi resolves --provider/--model before it refreshes any catalog over the
// network, and in -p mode it never refreshes at all, so on a fresh container
// `pi --provider lunaroute` fails with "Unknown provider". The extension seeds
// its models from ~/.pi/agent/models-store.json when it registers, so the
// script fills that file first: `pi --mode rpc` starts a background catalog
// refresh with extensions loaded, and the extension persists what it fetched.
// Closing RPC mode's stdin ends it once the file names lunaroute, or after 15s.
// Measured on Pi 0.87.1 with @lunaroute/pi-extension 0.12.1: ~1-2s.
//
// The fetch goes through the proxy like any other request, so the key is
// injected there and never enters the container.
const lunaRouteLaunchScript = `store="$HOME/.pi/agent/models-store.json"
if ! grep -q '"lunaroute"' "$store" 2>/dev/null; then
  ( i=0; while [ "$i" -lt 30 ]; do grep -q '"lunaroute"' "$store" 2>/dev/null && break; sleep 0.5; i=$((i+1)); done ) | pi --mode rpc >/dev/null 2>&1
  grep -q '"lunaroute"' "$store" 2>/dev/null || echo "moat: could not load LunaRoute's model list from gw.lunaroute.com — check the network policy and 'moat grant lunaroute'" >&2
fi
exec pi "$@"
`

// HasLunaRouteExtension reports whether pkgs installs the LunaRoute extension,
// either unpinned or pinned to a version (npm:@lunaroute/pi-extension@x.y.z).
func HasLunaRouteExtension(pkgs []string) bool {
	return slices.ContainsFunc(pkgs, func(p string) bool {
		return p == LunaRouteExtensionPackage || strings.HasPrefix(p, LunaRouteExtensionPackage+"@")
	})
}

// withLunaRouteExtension returns pkgs with the LunaRoute extension appended,
// unless it is already listed. The input slice is not modified.
func withLunaRouteExtension(pkgs []string) []string {
	if HasLunaRouteExtension(pkgs) {
		return pkgs
	}
	return append(slices.Clone(pkgs), LunaRouteExtensionPackage)
}

// checkAnthropicGatewayKey rejects running Pi's anthropic backend on a gateway
// key (`moat grant anthropic --base-url`). That key is never sent to
// api.anthropic.com, and Pi's anthropic backend talks to nothing else, so
// every request would fail with an opaque auth error. baseURL is the endpoint
// recorded on the anthropic credential ("" for a plain Anthropic key).
func checkAnthropicGatewayKey(backend, baseURL string) error {
	if backend != backendAnthropic || baseURL == "" {
		return nil
	}
	return fmt.Errorf(
		"pi: the anthropic grant is a gateway key for %s, which Pi's anthropic backend cannot use\n"+
			"For LunaRoute, grant the key for Pi and select that backend:\n"+
			"  Run: moat grant lunaroute\n"+
			"  Then: moat pi --provider lunaroute\n"+
			"(use the same --profile for both if the gateway key lives in a profile)",
		baseURL)
}
