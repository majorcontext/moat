// internal/deps/builder.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ImageTag generates a deterministic image tag for a set of dependencies.
func ImageTag(deps []Dependency) string {
	// Sort deps for deterministic ordering
	sorted := make([]string, len(deps))
	for i, d := range deps {
		v := d.Version
		if v == "" {
			spec, _ := GetSpec(d.Name)
			v = spec.Default
		}
		sorted[i] = d.Name + "@" + v
	}
	sort.Strings(sorted)

	// Hash the sorted deps list
	h := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	hash := hex.EncodeToString(h[:])[:12]

	return "moat/run:" + hash
}
