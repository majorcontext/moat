package configprovider

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"

	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/proxy"
)

//go:embed defaults/*.yaml
var defaultsFS embed.FS

var registerOnce sync.Once

// RegisterAll loads and registers all config-driven providers.
// Embedded defaults are loaded first, then user-defined providers from
// ~/.moat/providers/. Go providers (registered via init()) take precedence
// over config-driven providers with the same name.
// Safe to call multiple times; only the first call has any effect.
func RegisterAll() {
	registerOnce.Do(func() {
		defs := loadEmbedded()
		userNames := loadUserDir(defs)
		registerAll(defs, userNames)
	})
}

// loadEmbedded loads provider definitions from the embedded defaults directory.
func loadEmbedded() map[string]ProviderDef {
	defs := make(map[string]ProviderDef)

	entries, err := defaultsFS.ReadDir("defaults")
	if err != nil {
		log.Debug("reading embedded defaults", "error", err)
		return defs
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		data, err := defaultsFS.ReadFile("defaults/" + entry.Name())
		if err != nil {
			log.Debug("reading embedded provider", "file", entry.Name(), "error", err)
			continue
		}

		def, err := parseProviderDef(data)
		if err != nil {
			log.Warn("parsing embedded provider", "file", entry.Name(), "error", err)
			continue
		}

		defs[def.Name] = def
	}

	return defs
}

// loadUserDir loads provider definitions from ~/.moat/providers/.
// User definitions override embedded defaults with the same name.
// Returns the set of provider names that came from the user directory.
func loadUserDir(defs map[string]ProviderDef) map[string]bool {
	userNames := make(map[string]bool)

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return userNames
	}

	userDir := filepath.Join(homeDir, ".moat", "providers")
	entries, err := os.ReadDir(userDir)
	if err != nil {
		// Directory doesn't exist — normal case
		return userNames
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".yaml") {
			continue
		}

		data, err := os.ReadFile(filepath.Join(userDir, entry.Name()))
		if err != nil {
			log.Debug("reading user provider", "file", entry.Name(), "error", err)
			continue
		}

		def, err := parseProviderDef(data)
		if err != nil {
			log.Debug("parsing user provider", "file", entry.Name(), "error", err)
			continue
		}

		defs[def.Name] = def
		userNames[def.Name] = true
	}

	return userNames
}

// registerAll registers all loaded provider definitions with the provider registry.
// userNames is the set of provider names loaded from the user directory.
func registerAll(defs map[string]ProviderDef, userNames map[string]bool) {
	for _, def := range defs {
		// Skip if a Go provider already registered with this name
		if provider.Get(def.Name) != nil {
			log.Debug("skipping config provider (Go provider registered)", "name", def.Name)
			continue
		}

		source := "builtin"
		if userNames[def.Name] {
			source = "custom"
		}

		cp := NewConfigProvider(def, source)
		provider.Register(cp)

		// Register aliases
		for _, alias := range def.Aliases {
			provider.RegisterAlias(alias, def.Name)
		}

		// Register hosts for network policy
		proxy.RegisterGrantHosts(def.Name, def.Hosts)

		// Register as a known credential provider
		credential.RegisterDynamicProvider(credential.Provider(def.Name))

		log.Debug("registered config provider", "name", def.Name, "source", source)
	}
}

// parseProviderDef parses and validates a YAML provider definition.
func parseProviderDef(data []byte) (ProviderDef, error) {
	var def ProviderDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return ProviderDef{}, err
	}
	if def.Name == "" {
		return ProviderDef{}, fmt.Errorf("provider name is required")
	}
	if len(def.Hosts) == 0 {
		return ProviderDef{}, fmt.Errorf("provider %q: at least one host is required", def.Name)
	}
	if def.Description == "" {
		return ProviderDef{}, fmt.Errorf("provider %q: description is required", def.Name)
	}
	if def.Inject.Header == "" {
		return ProviderDef{}, fmt.Errorf("provider %q: inject.header is required", def.Name)
	}
	return def, nil
}
