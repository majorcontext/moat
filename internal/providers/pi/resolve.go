package pi

import (
	"errors"
	"fmt"
)

// resolvePiProvider decides which backend Pi uses and hard-errors on every
// known bad state.
//
// providerOverride is the effective --provider / pi.provider ("" if unset);
// modelOverride is --model / pi.model ("" if unset). hasAnthropic / hasOpenAI
// report whether that grant's credential is configured in the store.
//
// Precedence: an explicit override must be a supported backend whose grant is
// configured; otherwise the backend is inferred from the single configured
// grant. Both-configured without an override is ambiguous (a hard error, by
// design — the user must choose).
func resolvePiProvider(providerOverride, modelOverride string, hasAnthropic, hasOpenAI bool) (providerName, model string, err error) {
	switch providerOverride {
	case "anthropic":
		if !hasAnthropic {
			return "", "", missingGrantErr("anthropic")
		}
		return "anthropic", modelOverride, nil
	case "openai":
		if !hasOpenAI {
			return "", "", missingGrantErr("openai")
		}
		return "openai", modelOverride, nil
	case "":
		// fall through to inference
	default:
		return "", "", fmt.Errorf(
			"pi provider %q is not supported yet (supported: anthropic, openai)\n"+
				"Other Pi backends are planned but not wired up — set pi.provider (or --provider) to a supported value.",
			providerOverride)
	}

	switch {
	case hasAnthropic && hasOpenAI:
		return "", "", errors.New(
			"pi: both the anthropic and openai grants are configured — Pi cannot pick one automatically\n" +
				"Set pi.provider in moat.yaml (or pass --provider anthropic|openai) to choose.")
	case hasAnthropic:
		return "anthropic", modelOverride, nil
	case hasOpenAI:
		return "openai", modelOverride, nil
	default:
		return "", "", errors.New(
			"pi requires a model backend, but no supported grant is configured:\n" +
				"  - anthropic\n" +
				"  - openai\n\n" +
				"Run 'moat grant anthropic' or 'moat grant openai', then run again.")
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
