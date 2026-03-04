package quickstart

import "strings"

// BuildPrompt assembles the full quickstart prompt from a workspace scan,
// schema reference, deps catalog, examples, and instructions. The returned
// string is suitable for use as a system prompt when asking an AI agent to
// generate a moat.yaml for a project.
func BuildPrompt(workspace string) string {
	var b strings.Builder

	// Role setting.
	b.WriteString("You are a Moat configuration expert. Moat runs AI agents in isolated containers.\n\n")

	// Task.
	b.WriteString("Your task: analyze the project at /workspace and generate a moat.yaml configuration file.\n\n")

	// Workspace scan results (deterministic, pre-computed).
	b.WriteString(ScanWorkspace(workspace))

	// Schema reference.
	b.WriteString(GenerateSchemaReference())
	b.WriteString("\n")

	// Deps reference.
	b.WriteString(GenerateDepsReference())
	b.WriteString("\n")

	// Examples.
	b.WriteString("## Examples\n\n")

	b.WriteString("```yaml\n")
	b.WriteString("# Example 1: Node.js web app with PostgreSQL\n")
	b.WriteString("name: my-app\n")
	b.WriteString("dependencies: [node@20, postgres@17, psql, git]\n")
	b.WriteString("grants: [github]\n")
	b.WriteString("hooks:\n")
	b.WriteString("  pre_run: \"npm install\"\n")
	b.WriteString("ports:\n")
	b.WriteString("  web: 3000\n")
	b.WriteString("```\n\n")

	b.WriteString("```yaml\n")
	b.WriteString("# Example 2: Python ML project\n")
	b.WriteString("name: ml-project\n")
	b.WriteString("dependencies: [python@3.11, uv, git]\n")
	b.WriteString("grants: [github]\n")
	b.WriteString("hooks:\n")
	b.WriteString("  pre_run: \"uv sync\"\n")
	b.WriteString("```\n\n")

	b.WriteString("```yaml\n")
	b.WriteString("# Example 3: Go service with Redis\n")
	b.WriteString("name: api-service\n")
	b.WriteString("dependencies: [go@1.25, redis@7, git]\n")
	b.WriteString("grants: [github]\n")
	b.WriteString("ports:\n")
	b.WriteString("  api: 8080\n")
	b.WriteString("```\n\n")

	// Instructions.
	b.WriteString("## Instructions\n\n")
	b.WriteString("1. Read manifest files: package.json, go.mod, pyproject.toml, Gemfile, requirements.txt, Cargo.toml, etc.\n")
	b.WriteString("2. Check for docker-compose.yml or docker-compose.yaml for service hints (databases, caches).\n")
	b.WriteString("3. Check .env.example, .env.sample, or README for environment variable and credential hints.\n")
	b.WriteString("4. Detect which runtime and version the project needs.\n")
	b.WriteString("5. Detect database or cache dependencies (postgres, mysql, redis).\n")
	b.WriteString("6. Read Makefile, Taskfile, CI configs (.github/workflows/), and test scripts to detect ALL tools used (test runners, linters, secondary runtimes like python or bats).\n")
	b.WriteString("7. If the project uses Docker (Dockerfile, docker-compose, Docker SDK, or docker CLI commands), add `docker:host` or `docker:dind` and set `runtime: docker`.\n")
	b.WriteString("8. Only include grants if there is evidence the project uses that service.\n")
	b.WriteString("9. Keep the config minimal — only include what the project actually needs, but don't miss dependencies used by tests or build scripts.\n")
	b.WriteString("10. Use pre_run hooks for dependency installation (npm install, pip install, etc.).\n")
	b.WriteString("11. Use post_build_root hooks only for system packages not available as dependencies.\n")
	b.WriteString("12. Output only valid YAML, nothing else. No markdown fences, no explanation.\n")

	return b.String()
}
