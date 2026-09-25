package run

import (
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/providers/pi"
)

// missingLunaRouteExtension reports whether a run installs Pi and holds the
// lunaroute grant but will not install LunaRoute's Pi extension. `moat pi`
// adds the extension itself (and loads the model catalog before Pi starts);
// `moat run` with a Pi command does neither, and without the extension Pi has
// no LunaRoute models. Only a warning: a user may have configured the provider
// by hand instead.
func missingLunaRouteExtension(hasPi bool, grants, piPackages []string) bool {
	return hasPi &&
		hasGrant(grants, string(credential.ProviderLunaRoute)) &&
		!pi.HasLunaRouteExtension(piPackages)
}
