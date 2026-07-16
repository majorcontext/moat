// internal/deps/registry.go
package deps

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

//go:embed scripts/moat-init.sh
var MoatInitScript string

// MoatInitDispatcher selects between the shell and Go entrypoint
// implementations during the moat-init shell->Go migration window
// (docs/plans/2026-07-01-moat-init-go-rewrite-plan.md). It is installed as
// /usr/local/bin/moat-init-dispatch (the ENTRYPOINT); the legacy script and
// the Go binary are installed next to it as moat-init.sh and moat-init.
//
//go:embed scripts/moat-init-dispatch.sh
var MoatInitDispatcher string

// registry holds all available dependencies. It is read-only after init().
var registry map[string]DepSpec

func init() {
	registry = make(map[string]DepSpec)
	if err := yaml.Unmarshal(registryData, &registry); err != nil {
		panic("invalid registry.yaml: " + err.Error())
	}
}

// GetSpec returns the DepSpec for a dependency name, or ok=false if not found.
func GetSpec(name string) (DepSpec, bool) {
	spec, ok := registry[name]
	return spec, ok
}

// AllSpecs returns a copy of the registry map.
func AllSpecs() map[string]DepSpec {
	result := make(map[string]DepSpec, len(registry))
	for k, v := range registry {
		result[k] = v
	}
	return result
}
