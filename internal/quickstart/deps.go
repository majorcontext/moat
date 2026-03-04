package quickstart

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/deps"
)

// GenerateDepsReference produces a markdown reference of all available dependencies.
func GenerateDepsReference() string {
	var b strings.Builder
	b.WriteString("## Available Dependencies\n\n")

	for _, name := range deps.List() {
		spec, ok := deps.GetSpec(name)
		if !ok {
			continue
		}

		// Format: - `name` (default: X) [versions: A, B, C] — Description
		fmt.Fprintf(&b, "- `%s`", name)
		if spec.Default != "" {
			fmt.Fprintf(&b, " (default: %s)", spec.Default)
		}
		if len(spec.Versions) > 0 {
			fmt.Fprintf(&b, " [versions: %s]", strings.Join(spec.Versions, ", "))
		}
		if spec.Description != "" {
			fmt.Fprintf(&b, " — %s", spec.Description)
		}
		b.WriteString("\n")
	}

	b.WriteString("\n## Dynamic Dependencies\n\n")
	b.WriteString("For packages not in the registry, use prefix syntax:\n")
	b.WriteString("- `npm:<package>` — Install npm package globally (requires node)\n")
	b.WriteString("- `pip:<package>` — Install pip package (requires python)\n")
	b.WriteString("- `uv:<package>` — Install uv tool (requires uv)\n")
	b.WriteString("- `cargo:<package>` — Install cargo crate (requires rust)\n")
	b.WriteString("- `go:<package>` — Install Go binary (requires go)\n")
	b.WriteString("\nFor system packages not available through any prefix, use lifecycle hooks:\n")
	b.WriteString("```yaml\nhooks:\n  post_build_root: \"apt-get update && apt-get install -y <package>\"\n```\n")

	b.WriteString("\n## Docker Dependency\n\n")
	b.WriteString("Docker requires an explicit mode — bare `docker` is not allowed:\n")
	b.WriteString("- `docker:host` — Docker CLI only, uses host Docker socket (for running docker commands)\n")
	b.WriteString("- `docker:dind` — Full Docker-in-Docker (for building/running containers inside the sandbox)\n\n")
	b.WriteString("IMPORTANT: Docker dependencies require `runtime: docker` in the config.\n")
	b.WriteString("```yaml\nruntime: docker\ndependencies: [docker:dind]\n```\n")

	return b.String()
}
