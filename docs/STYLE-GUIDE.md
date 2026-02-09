# Documentation Style Guide

This guide establishes the voice, tone, and conventions for Moat documentation. Follow these guidelines to ensure consistency across all pages.

## Voice and Tone

### Be Objective
State facts. Avoid hyperbole, marketing language, and subjective claims.

| Avoid | Prefer |
|-------|--------|
| "Moat makes credential management incredibly easy" | "Moat injects credentials at the network layer" |
| "The blazingly fast proxy" | "The proxy adds ~2ms latency per request" |
| "Finally, a solution that actually works" | (Just describe what it does) |
| "Unlike other tools that get this wrong..." | (Describe Moat's approach without comparison) |

Don't use words like: revolutionary, game-changing, seamless, effortless, simple (as a claim), easy (as a claim), powerful, robust, elegant, beautiful, magic/magical.

### Be Respectful
Acknowledge that other tools exist and serve their purposes. Avoid dismissive comparisons.

| Avoid | Prefer |
|-------|--------|
| "Docker Compose is overkill for this" | "Moat manages container lifecycle automatically" |
| "Unlike traditional approaches that expose tokens..." | "Tokens are injected at the network layer, not stored in environment variables" |
| "It's not a container orchestrator, it's a run manager" | "Moat manages individual runs rather than multi-container deployments" |
| "Stop copying tokens into .env files" | "Credentials are injected without manual configuration" |

When comparing approaches, describe what Moat does and let readers draw their own conclusions. Don't tell them what's wrong with their current workflow.

### Be Factual
Make specific, verifiable claims. Avoid generalizations and euphemisms.

| Avoid | Prefer |
|-------|--------|
| "Credentials are kept secure" | "Credentials are encrypted with AES-256-GCM and stored in the system keychain" |
| "Full visibility into what happened" | "Logs capture stdout/stderr with timestamps; network traces record HTTP method, URL, status, and timing" |
| "Isolated environment" | "Each run executes in a separate container with its own filesystem namespace" |
| "Enterprise-grade security" | (Describe the specific security properties) |

If you can't point to a specific mechanism, the claim is too vague.

### Be Direct
Write in active voice. State what things do, not what they "can" or "may" do.

| Avoid | Prefer |
|-------|--------|
| "You can use the `--grant` flag to inject credentials" | "The `--grant` flag injects credentials" |
| "Moat may automatically detect your container runtime" | "Moat detects your container runtime automatically" |
| "It is possible to run multiple agents" | "Run multiple agents simultaneously" |

### Be Concise
Eliminate filler words. Every sentence should convey information.

| Avoid | Prefer |
|-------|--------|
| "In order to start a run, you need to..." | "To start a run..." |
| "It's important to note that tokens are never..." | "Tokens are never..." |
| "Basically, what happens is that the proxy..." | "The proxy..." |

### Be Precise
Use specific terms consistently. Avoid synonyms that create ambiguity.

| Term | Definition | Don't use |
|------|------------|-----------|
| **run** | A container execution with its associated artifacts | session, instance, job |
| **grant** | A credential made available to a run | permission, access, auth |
| **workspace** | The mounted directory containing agent code | project, directory, folder |
| **inject** | Add credentials at the network layer | pass, provide, supply |

### Be Practical
Lead with what users need to do, not theory. Show working examples first, explain after.

```markdown
<!-- Avoid: Theory first -->
Moat uses a TLS-intercepting proxy to inject credentials. The proxy
sits between the container and the internet, inspecting requests and
adding Authorization headers. To use this feature:

<!-- Prefer: Action first -->
Grant GitHub access to your agent:

    moat run --grant github ./my-project

The token is injected at the network layer—it never appears in the
container environment.
```

### Be Honest About Limitations
Document what Moat doesn't do, edge cases, and known issues. Users trust documentation that acknowledges limitations.

```markdown
<!-- Good: Acknowledges limitation -->
> **Note:** The `--follow` flag for `moat logs` is not yet implemented.
> Use `moat attach` to see live output from a running container.

<!-- Good: States trade-off -->
Network policy enforcement adds a firewall rule per allowed host.
For policies with many hosts (>100), this may increase container
startup time.
```

## Formatting Conventions

### Headings
- Use sentence case: "Getting started" not "Getting Started"
- Keep headings short (under 6 words when possible)
- Don't skip levels (h2 → h4)

### Code Blocks
Always specify the language for syntax highlighting:

````markdown
```bash
moat run --grant github
```

```yaml
grants:
  - github
```

```go
func main() {
    // ...
}
```
````

### Command Examples
Show the command, then the output. Use `$` prefix for commands:

```markdown
    $ moat grant github

    To authorize, visit: https://github.com/login/device
    Enter code: ABCD-1234

    Waiting for authorization...
    GitHub credential saved successfully
```

For commands without meaningful output, omit the output section.

### Inline Code
Use backticks for:
- Commands: `moat run`
- Flags: `--grant`
- File names: `agent.yaml`
- Environment variables: `MOAT_PROXY_PORT`
- Values: `true`, `strict`, `node@20`

Don't use backticks for:
- Product names: Moat, Docker, GitHub
- General concepts: credential injection, audit logging

### File Paths
- Use relative paths when referring to project files: `./agent.yaml`
- Use `~/.moat/` for Moat's data directory
- Use absolute paths only when necessary for system paths

### Lists
Use bullet lists for unordered items. Use numbered lists only for sequential steps.

```markdown
<!-- Unordered: features, options, notes -->
Supported backends:
- 1Password (`op://vault/item/field`)
- AWS SSM (`ssm:///path`)

<!-- Ordered: steps that must be followed in sequence -->
1. Install Moat
2. Verify Docker is running
3. Grant GitHub credentials with `moat grant github`
```

### Tables
Use tables for structured comparisons. Keep cells concise.

```markdown
| Platform | Runtime | Notes |
|----------|---------|-------|
| macOS 26+ (Apple Silicon) | Apple containers | Preferred |
| macOS (Intel) | Docker | Requires Docker Desktop |
| Linux | Docker | Native Docker support |
```

### Admonitions
Use blockquotes with bold labels for callouts:

```markdown
> **Note:** Additional context that's helpful but not critical.

> **Warning:** Something that could cause problems if ignored.

> **Tip:** A useful shortcut or best practice.
```

## Content Guidelines

### Show Real Output
When documenting commands, use realistic output that matches what users will see. Test commands before documenting them.

### Explain the "Why"
Don't just show what to do—briefly explain why it matters:

```markdown
<!-- Just what -->
Use `--grant github` to inject GitHub credentials.

<!-- What + why -->
Use `--grant github` to inject GitHub credentials. The token is
injected at the network layer, so it never appears in the container
environment—even if the agent logs all environment variables.
```

### Link to Related Content
Cross-reference related pages. Use relative links:

```markdown
See [Credential Management](../concepts/02-credentials.md) for details
on how token injection works.
```

### Use Generic Examples
Use placeholder names that don't imply specific technologies or products:

| Avoid | Prefer |
|-------|--------|
| `acme-corp/billing-service` | `my-org/my-project` |
| `openai-agent` | `my-agent` |
| `claude-assistant` | `my-assistant` |

### Error Messages
When documenting errors, show the full error message and explain how to resolve it:

```markdown
If you see:

    Error: proxy port mismatch: running on 8080, requested 9000

The proxy is already running on a different port. Either unset MOAT_PROXY_PORT, or stop all agents to restart the proxy:

    unset MOAT_PROXY_PORT
    moat run ...
```

## Section Definitions

The documentation has four sections. Each serves a distinct purpose. Content that doesn't fit a section's purpose belongs elsewhere.

### Getting Started

**Purpose:** Onboard new users from install to first successful run.

**Audience:** Someone who has never used Moat.

**Contains:** Installation instructions, a guided walkthrough, and orientation material. Pages are sequential -- each builds on the previous one.

**Does not contain:** Deep explanations, exhaustive configuration options, or advanced workflows.

### Concepts

**Purpose:** Explain *how things work* and *why they are designed that way*. Build mental models.

**Audience:** Someone who wants to understand the system, not accomplish a specific task.

**Contains:** Architecture, design decisions, trade-offs, threat models, data flow diagrams. Describes mechanisms and explains rationale. May include brief examples to illustrate a point, but examples serve understanding, not task completion.

**Does not contain:** Step-by-step instructions, command output examples, configuration syntax tables, or troubleshooting steps. If a reader needs to *do* something, that content belongs in a guide. If a reader needs to *look up* syntax or options, that belongs in reference.

**Test:** If you removed all code blocks and the page still makes sense, it's a concept page.

### Guides

**Purpose:** Help users accomplish specific tasks. Answer "how do I do X?"

**Audience:** Someone who has a goal and needs steps to reach it.

**Contains:** Prerequisites, step-by-step instructions, working examples with expected output, verification steps, and troubleshooting. May include brief "how it works" context (3-5 sentences) to orient the reader, but the bulk of the page is procedural.

**Does not contain:** Deep architectural explanations, design rationale, or exhaustive option tables. Link to concept pages for "why" and reference pages for "all options."

**Test:** The page should read as a recipe. A reader should be able to follow it start-to-finish and achieve a result.

### Reference

**Purpose:** Provide complete, structured specifications. Answer "what are all the options?"

**Audience:** Someone who knows what they want to do and needs exact syntax, flags, fields, or values.

**Contains:** CLI commands with all flags, configuration file schemas with all fields, environment variable tables, format specifications. Organized for lookup, not reading. Every option documented with type, default, and description.

**Does not contain:** Extended explanations of why things work the way they do, or guided workflows. Brief notes clarifying behavior are fine; multi-paragraph explanations belong in concepts.

**Test:** The page should work as a lookup table. A reader should be able to find any option in under 10 seconds.

## Page Structure

### Getting started pages
1. Brief intro (1-2 sentences)
2. What you'll accomplish
3. Prerequisites (if any)
4. Step-by-step instructions
5. Next steps / related pages

### Concept pages
1. What it is (1-2 paragraphs)
2. Why it matters
3. How it works (with diagrams if helpful)
4. Key details / edge cases
5. Related concepts

### Guide pages
1. What you'll accomplish
2. Prerequisites
3. Step-by-step walkthrough
4. Verification / testing
5. Troubleshooting common issues
6. Related guides

### Reference pages
1. Brief description
2. Complete specification
3. Examples for each option
4. Notes and caveats

## Terminology

### Capitalize
- Moat (the product)
- Docker
- Apple containers (not "Apple Containers")
- GitHub, GitLab
- macOS, Linux, Windows

### Don't Capitalize
- container, runtime
- credential, token, grant
- proxy, firewall
- audit log, trace

### Abbreviations
Spell out on first use, then use abbreviation:

- TLS (Transport Layer Security)
- CLI (command-line interface)
- API (application programming interface)
- SSH (Secure Shell)

Common abbreviations that don't need expansion:
- URL, HTTP, HTTPS
- JSON, YAML
- ID (identifier)

## Frontmatter Template

Every documentation page should start with this frontmatter:

```yaml
---
title: "Page Title"
description: "One sentence description for SEO and link previews."
keywords: ["moat", "relevant", "keywords"]
---
```

Optional fields:
```yaml
draft: true  # Exclude from production builds
```

The following are inferred from the file path and don't need to be specified:
- `slug` — From filename (e.g., `01-introduction.md` → `introduction`)
- `section` — From parent directory
- `order` — From numeric prefix
- `prev`/`next` — From adjacent files
