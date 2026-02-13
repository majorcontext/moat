---
title: "Provider YAML reference"
navTitle: "Provider YAML"
description: "Schema reference for YAML-defined credential providers."
keywords: ["moat", "provider", "yaml", "custom", "credential"]
---

# Provider YAML reference

Credential providers can be defined as YAML files. Moat ships with several built-in YAML providers and loads custom providers from `~/.moat/providers/`.

## Schema

```yaml
# Provider identifier, used with `moat grant <name>` and `--grant <name>`.
name: gitlab

# One-line description shown in `moat grant providers`.
description: "GitLab personal access token"

# Alternate names for the provider (optional).
aliases: [gl]

# Hosts to match for credential injection.
# Supports exact matches and wildcard prefixes (*.example.com).
hosts:
  - "gitlab.com"
  - "*.gitlab.com"

# How credentials are injected into HTTP requests.
inject:
  header: "PRIVATE-TOKEN"   # HTTP header name to inject
  # prefix: "Bearer "       # Optional prefix before token value (default: none)

# Environment variables checked on the host during grant, in order (optional).
# First non-empty match is used as the token value.
source_env: [GITLAB_TOKEN, GL_TOKEN]

# Environment variable set inside the container with a placeholder value (optional).
# SDKs that read this variable will detect a configured credential.
# The real token is injected at the network layer by the proxy.
container_env: GITLAB_TOKEN

# Endpoint to validate the token (optional). Omit to skip validation.
validate:
  url: "https://gitlab.com/api/v4/user"
  # method: GET             # HTTP method (default: GET)
  # header: "PRIVATE-TOKEN" # Header for validation request (default: inject.header)
  # prefix: ""              # Prefix for validation request (default: inject.prefix)

# Text shown when prompting for interactive token entry (optional).
prompt: |
  Enter a GitLab Personal Access Token.
  Create one at: https://gitlab.com/-/user_settings/personal_access_tokens
```

## Field reference

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `name` | string | yes | Provider identifier. Must be unique across all providers. |
| `description` | string | yes | Short description for `moat grant providers` output. |
| `aliases` | list of strings | no | Alternate names for `moat grant` and `--grant`. |
| `hosts` | list of strings | yes | Hosts to inject credentials for. Supports `*.domain.com` wildcards. |
| `inject.header` | string | yes | HTTP header name to inject on matching requests. |
| `inject.prefix` | string | no | Prefix prepended to the token value (e.g., `"Bearer "`). Default: none. |
| `source_env` | list of strings | no | Environment variables checked on the host during grant. First non-empty match is used. |
| `container_env` | string | no | Environment variable set in the container with a placeholder value. |
| `validate` | object | no | Endpoint to validate the token. Omit to skip validation. |
| `validate.url` | string | yes (if `validate`) | URL to send a validation request to. |
| `validate.method` | string | no | HTTP method. Default: `GET`. |
| `validate.header` | string | no | Header name for the validation request. Default: same as `inject.header`. |
| `validate.prefix` | string | no | Prefix for the validation request. Default: same as `inject.prefix`. |
| `prompt` | string | no | Text shown when prompting for interactive token entry. |

## Example

A provider with token validation and `Bearer` prefix:

```yaml
name: vercel
description: "Vercel platform API token"

hosts:
  - "api.vercel.com"
  - "*.vercel.com"

inject:
  header: "Authorization"
  prefix: "Bearer "

source_env: [VERCEL_TOKEN]
container_env: VERCEL_TOKEN

validate:
  url: "https://api.vercel.com/v2/user"

prompt: |
  Create a Vercel API token:
  1. Go to https://vercel.com/account/tokens
  2. Click "Create Token"
  3. Copy the token value
```

```bash
moat grant vercel
moat run --grant vercel ./my-project
```

## Precedence

When multiple providers share the same name, Moat uses the first match in this order:

1. **Built-in Go providers** -- Compiled into the binary (github, anthropic, openai, gemini, npm, aws)
2. **User YAML providers** -- Files in `~/.moat/providers/`
3. **Embedded YAML providers** -- Shipped with Moat (gitlab, brave-search, elevenlabs, linear, vercel, sentry, datadog)

User YAML providers override embedded YAML providers with the same name but cannot override built-in Go providers.

## Custom providers

Create a YAML file in `~/.moat/providers/`:

```bash
mkdir -p ~/.moat/providers
```

The file name does not need to match the `name` field, but keeping them consistent is recommended.

After creating the file, verify it loads:

```bash
moat grant providers
```
