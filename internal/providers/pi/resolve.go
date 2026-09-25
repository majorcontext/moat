package pi

import (
	"fmt"
	"slices"
	"strings"
)

// Supported Pi backends, in the order they are listed in messages.
const (
	backendAnthropic = "anthropic"
	backendOpenAI    = "openai"
	backendLunaRoute = "lunaroute"
)

var supportedBackends = []string{backendAnthropic, backendOpenAI, backendLunaRoute}

// piGrants reports which backend grants are configured in the credential store.
type piGrants struct {
	Anthropic bool
	OpenAI    bool
	LunaRoute bool
}

func (g piGrants) has(backend string) bool {
	switch backend {
	case backendAnthropic:
		return g.Anthropic
	case backendOpenAI:
		return g.OpenAI
	case backendLunaRoute:
		return g.LunaRoute
	}
	return false
}

// resolvePiProvider decides which backend Pi uses and hard-errors on every
// known bad state.
//
// providerOverride is the effective --provider / pi.provider ("" if unset);
// modelOverride is --model / pi.model ("" if unset).
//
// Precedence: an explicit override must be a supported backend whose grant is
// configured; otherwise the backend is inferred from the single configured
// grant. More than one configured without an override is ambiguous (a hard
// error, by design — the user must choose).
func resolvePiProvider(providerOverride, modelOverride string, grants piGrants) (providerName, model string, err error) {
	if providerOverride != "" {
		if !slices.Contains(supportedBackends, providerOverride) {
			return "", "", fmt.Errorf(
				"pi provider %q is not supported yet (supported: %s)\n"+
					"Other Pi backends are planned but not wired up — set pi.provider (or --provider) to a supported value.",
				providerOverride, strings.Join(supportedBackends, ", "))
		}
		if !grants.has(providerOverride) {
			return "", "", missingGrantErr(providerOverride)
		}
		return providerOverride, modelOverride, nil
	}

	var configured []string
	for _, b := range supportedBackends {
		if grants.has(b) {
			configured = append(configured, b)
		}
	}

	switch len(configured) {
	case 1:
		return configured[0], modelOverride, nil
	case 0:
		var b strings.Builder
		b.WriteString("pi requires a model backend, but no supported grant is configured:\n")
		for _, name := range supportedBackends {
			fmt.Fprintf(&b, "  - %s\n", name)
		}
		b.WriteString("\nRun 'moat grant anthropic', 'moat grant openai', or 'moat grant lunaroute', then run again.")
		return "", "", fmt.Errorf("%s", b.String())
	default:
		return "", "", fmt.Errorf(
			"pi: several model backend grants are configured (%s) — Pi cannot pick one automatically\n"+
				"Set pi.provider in moat.yaml (or pass --provider %s) to choose.",
			strings.Join(configured, ", "), strings.Join(configured, "|"))
	}
}

// missingGrantErr reports a pi.provider whose backing grant isn't configured,
// matching the validateGrants message style.
func missingGrantErr(name string) error {
	return fmt.Errorf(
		"pi.provider is %q but that grant isn't configured\n"+
			"  - %s: not configured\n"+
			"    Run: moat grant %s",
		name, name, name)
}
