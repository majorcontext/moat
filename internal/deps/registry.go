// internal/deps/registry.go
package deps

import (
	_ "embed"
	"time"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

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
	return applySupport(name, spec, time.Now().UTC()), ok
}

func applySupport(name string, spec DepSpec, now time.Time) DepSpec {
	if product, tracked := releaseSupport.Dependencies[name]; tracked {
		spec.Versions = product.Versions(now)
		if value, err := releaseSupport.Default(name, spec.Default, now); err == nil {
			spec.Default = value
		}
	}
	return spec
}

// AllSpecs returns a copy of the registry map.
func AllSpecs() map[string]DepSpec {
	result := make(map[string]DepSpec, len(registry))
	for k := range registry {
		result[k], _ = GetSpec(k)
	}
	return result
}
