package support

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// UpdateRegistry edits only default/version lines, preserving comments and all
// installer metadata. A default advances only after its upstream line expires.
func UpdateRegistry(data []byte, catalog Catalog, now time.Time) ([]byte, error) {
	var specs map[string]struct {
		Default string `yaml:"default"`
	}
	if err := yaml.Unmarshal(data, &specs); err != nil {
		return nil, err
	}
	defaults := map[string]string{}
	for name := range catalog.Dependencies {
		spec, ok := specs[name]
		if !ok {
			return nil, fmt.Errorf("catalog dependency %q is absent from registry", name)
		}
		value, err := catalog.Default(name, spec.Default, now)
		if err != nil {
			return nil, err
		}
		defaults[name] = value
	}
	var out []string
	current := ""
	emitted := map[string]int{}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "#") && strings.HasSuffix(line, ":") {
			current = strings.TrimSuffix(line, ":")
		}
		product, tracked := catalog.Dependencies[current]
		if tracked && strings.HasPrefix(line, "  versions:") {
			continue
		}
		if tracked && strings.HasPrefix(line, "  default:") {
			emitted[current]++
			encoded, _ := json.Marshal(defaults[current])
			out = append(out, "  default: "+string(encoded))
			if !product.PublishedOnly {
				versions, _ := json.Marshal(product.Versions(now))
				out = append(out, "  versions: "+string(versions))
			}
			continue
		}
		out = append(out, line)
	}
	result := []byte(strings.Join(out, "\n"))
	var check map[string]struct {
		Default  string   `yaml:"default"`
		Versions []string `yaml:"versions"`
	}
	if err := yaml.Unmarshal(result, &check); err != nil {
		return nil, fmt.Errorf("generated registry is invalid: %w", err)
	}
	for name, product := range catalog.Dependencies {
		if emitted[name] != 1 || check[name].Default != defaults[name] || strings.Join(check[name].Versions, ",") != strings.Join(product.Versions(now), ",") {
			return nil, fmt.Errorf("could not safely update %s default/version fields", name)
		}
	}
	return result, nil
}
