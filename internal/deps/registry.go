// internal/deps/registry.go
package deps

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed registry.yaml
var registryData []byte

// Registry holds all available dependencies.
var Registry map[string]DepSpec

func init() {
	Registry = make(map[string]DepSpec)
	if err := yaml.Unmarshal(registryData, &Registry); err != nil {
		panic("invalid registry.yaml: " + err.Error())
	}
}
