package run

import (
	"sort"
	"strings"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/deps"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
)

// providerCodex is the provider registry name for the Codex/OpenAI agent.
// The provider registry resolves "openai" → "codex" via alias
// (see internal/providers/codex/provider.go), but credentials are stored
// under credential.ProviderOpenAI — not under this name.
const providerCodex = "codex"

// imageNeeds holds the results of grant/dependency analysis for image building.
type imageNeeds struct {
	initProviders []string
	initFiles     bool
}

// resolveImageNeeds determines which agent init steps and features are needed
// based on granted credentials and declared dependencies. It opens the
// credential store at most once for the init-detection phase (the
// PrepareContainer phase in manager.Create still opens its own stores).
func resolveImageNeeds(grants []string, depList []deps.Dependency) imageNeeds {
	// Open credential store once (best-effort; if it fails we skip
	// credential-dependent checks — same behavior as before).
	var store credential.Store
	key, keyErr := credential.DefaultEncryptionKey()
	if keyErr != nil {
		log.Debug("failed to derive encryption key", "error", keyErr)
	} else if s, storeErr := credential.NewFileStore(credential.DefaultStoreDir(), key); storeErr != nil {
		log.Debug("failed to open credential store", "error", storeErr)
	} else {
		store = s
	}
	return resolveImageNeedsWithStore(grants, depList, store)
}

// resolveImageNeedsWithStore is the testable core of resolveImageNeeds.
// It accepts an explicit credential store (which may be nil).
func resolveImageNeedsWithStore(grants []string, depList []deps.Dependency, store credential.Store) imageNeeds {
	var needs imageNeeds
	initSet := make(map[string]bool)

	for _, grant := range grants {
		grantName := strings.Split(grant, ":")[0]

		// canonical is the provider registry name (e.g. "codex" for an
		// "openai" grant). This is NOT a credential store key — each case
		// uses the appropriate credential.Provider* constant for store lookups.
		canonical := provider.ResolveName(grantName)

		switch canonical {
		case "claude":
			initSet["claude"] = true

		case "anthropic":
			// Legacy: only needs Claude init if the stored token is OAuth.
			if store != nil {
				if cred, err := store.Get(credential.ProviderAnthropic); err == nil {
					if credential.IsOAuthToken(cred.Token) {
						initSet["claude"] = true
					}
				}
			}

		case providerCodex:
			// The "openai" grant resolves to "codex" via provider alias.
			// Credentials are stored under ProviderOpenAI.
			if store != nil {
				if _, err := store.Get(credential.ProviderOpenAI); err == nil {
					initSet["codex"] = true
				}
			}

		case "gemini":
			if store != nil {
				if _, err := store.Get(credential.ProviderGemini); err == nil {
					initSet["gemini"] = true
				}
			}
		}

		// Check InitFileProvider interface using the original grant name
		// (not canonical), since provider.Get handles alias resolution.
		if prov := provider.Get(grantName); prov != nil {
			if _, ok := prov.(provider.InitFileProvider); ok {
				needs.initFiles = true
			}
		}
	}

	// Dependency fallbacks: some agents can run without credential injection.
	if !initSet["claude"] && hasDep(depList, "claude-code") {
		initSet["claude"] = true
	}
	if !initSet["gemini"] && hasDep(depList, "gemini-cli") {
		initSet["gemini"] = true
	}

	for name := range initSet {
		needs.initProviders = append(needs.initProviders, name)
	}
	sort.Strings(needs.initProviders)
	return needs
}

// credentialStoreKey maps a grant name to the credential store key.
// Most providers store credentials under a key matching the resolved provider
// name, but the codex provider is an exception: the provider registry name is
// "codex" (aliased from "openai"), but credentials are stored under "openai".
func credentialStoreKey(grantName string) credential.Provider {
	canonical := provider.ResolveName(grantName)
	if canonical == providerCodex {
		return credential.ProviderOpenAI
	}
	return credential.Provider(canonical)
}

func hasDep(depList []deps.Dependency, name string) bool {
	for _, d := range depList {
		if d.Name == name {
			return true
		}
	}
	return false
}
