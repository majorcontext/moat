package run

import (
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/providers/pi"
)

// missingLunaRouteExtension reports whether a run installs Pi and holds the
// lunaroute grant, may use the lunaroute backend, and will not install
// LunaRoute's Pi extension. `moat pi` adds the extension itself (and loads the
// model catalog before Pi starts); `moat run` with a Pi command does neither,
// and without the extension Pi has no LunaRoute models. Only a warning: a user
// may have configured the provider by hand instead.
//
// piBackend is pi.provider. `moat pi` always sets it to the resolved backend,
// so a `moat pi --provider anthropic` run that happens to hold the lunaroute
// grant is not warned about an extension it doesn't need.
func missingLunaRouteExtension(hasPi bool, grants []string, piBackend string, piPackages []string) bool {
	lunaroute := string(credential.ProviderLunaRoute)
	return hasPi &&
		hasGrant(grants, lunaroute) &&
		(piBackend == "" || piBackend == lunaroute) &&
		!pi.HasLunaRouteExtension(piPackages)
}
