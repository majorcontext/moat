package pi

import (
	"fmt"
	"net/url"
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
// Measured on Pi 0.87.1 with @lunaroute/pi-extension 0.12.1: ~1-2s.
//
// How the warm-up ends:
//   - Ready means the store parses and lists at least one LunaRoute model, so
//     a half-written file or an empty catalog never counts.
//   - RPC mode reads stdin from a FIFO this script holds open; closing it lets
//     RPC exit on its own rather than being killed mid-write.
//   - A marker file records RPC exiting, so a crash ends the wait at once
//     instead of after the 15s cap. (kill -0 can't tell: an unreaped child is
//     a zombie and still "exists".) If RPC outlives the 2s grace after its
//     stdin closes, the script moves on and removes the temp dir, so that
//     late touch is silenced rather than printing to the container's stderr.
//
// The fetch goes through the proxy like any other request, so the key is
// injected there and never enters the container.
const lunaRouteLaunchScript = `store="$HOME/.pi/agent/models-store.json"
ready() {
  node -e 'try{const m=JSON.parse(require("fs").readFileSync(process.argv[1],"utf8")).lunaroute;process.exit(m&&Array.isArray(m.models)&&m.models.length?0:1)}catch(e){process.exit(1)}' "$store"
}
if ! ready; then
  tmp=$(mktemp -d)
  mkfifo "$tmp/in"
  { pi --mode rpc <"$tmp/in" >/dev/null 2>&1; touch "$tmp/exited" 2>/dev/null; } &
  exec 3>"$tmp/in"
  i=0
  while [ "$i" -lt 30 ] && [ ! -e "$tmp/exited" ] && ! ready; do sleep 0.5; i=$((i+1)); done
  exec 3>&-
  i=0
  while [ "$i" -lt 10 ] && [ ! -e "$tmp/exited" ]; do sleep 0.2; i=$((i+1)); done
  rm -rf "$tmp"
  ready || echo "moat: could not load LunaRoute's model list from gw.lunaroute.com — check the network policy and 'moat grant lunaroute'" >&2
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

// anthropicGatewayErr explains why Pi can't run on an anthropic gateway key
// (`moat grant anthropic --base-url`): that key is never sent to
// api.anthropic.com, and Pi's anthropic backend talks to nothing else, so every
// request would fail with an opaque auth error.
//
// Only a LunaRoute endpoint gets pointed at `moat grant lunaroute`; for any
// other gateway that advice would be wrong, so the message says what Pi can
// use instead.
func anthropicGatewayErr(baseURL string) error {
	if isLunaRouteURL(baseURL) {
		return fmt.Errorf(
			"pi: the anthropic grant is a gateway key for %s, which Pi's anthropic backend cannot use\n"+
				"Grant the same key for Pi and select that backend:\n"+
				"  Run: moat grant lunaroute\n"+
				"  Then: moat pi --provider lunaroute\n"+
				"(use the same --profile for both if the gateway key lives in a profile)",
			baseURL)
	}
	return fmt.Errorf(
		"pi: the anthropic grant is a gateway key for %s, which Pi's anthropic backend cannot use —\n"+
			"Pi's anthropic backend only talks to api.anthropic.com, where a gateway key is never sent.\n"+
			"Use one of the backends Pi supports instead:\n"+
			"  - an Anthropic API key: moat grant anthropic (without --base-url; use --profile to keep it apart from the gateway key)\n"+
			"  - moat grant openai\n"+
			"  - moat grant lunaroute",
		baseURL)
}

// isLunaRouteURL reports whether raw points at a LunaRoute host.
func isLunaRouteURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == "lunaroute.com" || strings.HasSuffix(host, ".lunaroute.com")
}
