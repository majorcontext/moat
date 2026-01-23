// internal/deps/builder.go
package deps

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
)

// ImageTagOptions configures image tag generation.
type ImageTagOptions struct {
	// NeedsSSH indicates the image needs SSH packages and init script.
	NeedsSSH bool

	// NeedsClaudeInit indicates the image needs the init script for Claude setup.
	NeedsClaudeInit bool

	// NeedsCodexInit indicates the image needs the init script for Codex setup.
	NeedsCodexInit bool
}

// ImageTag generates a deterministic image tag for a set of dependencies.
func ImageTag(deps []Dependency, opts *ImageTagOptions) string {
	if opts == nil {
		opts = &ImageTagOptions{}
	}

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

	// Build the hash input
	hashInput := strings.Join(sorted, ",")
	if opts.NeedsSSH {
		hashInput += ",ssh:agent"
	}
	if opts.NeedsClaudeInit {
		hashInput += ",claude:init"
	}
	if opts.NeedsCodexInit {
		hashInput += ",codex:init"
	}

	// Hash the combined input
	h := sha256.Sum256([]byte(hashInput))
	hash := hex.EncodeToString(h[:])[:12]

	return "moat/run:" + hash
}
