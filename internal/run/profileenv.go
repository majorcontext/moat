package run

import (
	"sort"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
)

// profileEnv returns the "KEY=value" entries configured for a credential
// profile in ~/.moat/config.yaml, for a run using that profile.
//
// A profile is an identity: a set of credentials plus the configuration that
// goes with them. Something like the model an LLM gateway serves belongs to
// the identity holding that gateway's key, not to each project that uses it.
//
// Proxy variables moat owns are filtered when the run has a proxy, exactly as
// they are for moat.yaml env and -e flags — a profile is user-writable
// configuration and must not be a way around network policy.
//
// The result is sorted so a run's environment does not depend on map order.
// Returns nil for an empty profile name, an unreadable global config, or a
// profile with no env.
func profileEnv(profile string, needsProxy bool) []string {
	if profile == "" {
		return nil
	}

	globalCfg, err := config.LoadGlobal()
	if err != nil {
		// LoadGlobal already warns the user about a malformed config; a run
		// should not fail because of an unrelated global setting.
		log.Debug("could not load global config for profile env", "profile", profile, "error", err)
		return nil
	}

	env := globalCfg.ProfileEnv(profile)
	if len(env) == 0 {
		return nil
	}

	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := make([]string, 0, len(keys))
	for _, k := range keys {
		if needsProxy && isMoatOwnedProxyVar(k) {
			ui.Warnf("ignoring %s in profile %q env — overriding proxy settings would bypass network policy enforcement", k, profile)
			continue
		}
		result = append(result, k+"="+env[k])
	}
	return result
}
