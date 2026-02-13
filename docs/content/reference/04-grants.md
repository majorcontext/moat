---
title: "Grants reference"
navTitle: "Grants"
description: "Complete reference for Moat grant types: supported providers, host matching, credential sources, and configuration."
keywords: ["moat", "grants", "credentials", "github", "anthropic", "aws", "ssh", "openai", "npm", "gitlab", "brave-search", "elevenlabs", "linear", "vercel", "sentry", "datadog"]
---

# Grants reference

Grants provide credentials to container runs. Each grant type injects authentication for specific hosts. Credentials are stored encrypted on your host machine and injected at the network layer by Moat's TLS-intercepting proxy. The container process does not have direct access to raw tokens.

Store a credential with `moat grant <provider>`, then use it in runs with `--grant <provider>` or in `agent.yaml`.

## Grant types

| Grant | Hosts matched | Header injected | Credential source |
|-------|---------------|-----------------|-------------------|
| `github` | `api.github.com`, `github.com` | `Authorization: Bearer ...` | gh CLI, `GITHUB_TOKEN`/`GH_TOKEN`, or PAT prompt |
| `anthropic` | `api.anthropic.com` | `Authorization: Bearer ...` (OAuth) or `x-api-key: ...` (API key) | `claude setup-token`, API key, or imported OAuth |
| `openai` | `api.openai.com`, `chatgpt.com`, `*.openai.com` | `Authorization: Bearer ...` | `OPENAI_API_KEY` or prompt |
| `gemini` | `generativelanguage.googleapis.com` (API key) or `cloudcode-pa.googleapis.com` (OAuth) | `x-goog-api-key: ...` (API key) or `Authorization: Bearer ...` (OAuth) | Gemini CLI OAuth, `GEMINI_API_KEY`, or prompt |
| `npm` | Per-registry (e.g., `registry.npmjs.org`, `npm.company.com`) | `Authorization: Bearer ...` | `.npmrc`, `NPM_TOKEN`, or manual |
| `aws` | All AWS service endpoints | AWS `credential_process` (STS temporary credentials) | IAM role assumption via STS |
| `ssh:<host>` | Specified host only | SSH agent forwarding (not HTTP) | Host SSH agent (`SSH_AUTH_SOCK`) |
| `mcp-<name>` | Host from MCP server `url` field | Configured per-server header | Interactive prompt |
| `gitlab` | `gitlab.com`, `*.gitlab.com` | `PRIVATE-TOKEN: ...` | `GITLAB_TOKEN`, `GL_TOKEN`, or prompt |
| `brave-search` | `api.search.brave.com` | `X-Subscription-Token: ...` | `BRAVE_API_KEY`, `BRAVE_SEARCH_API_KEY`, or prompt |
| `elevenlabs` | `api.elevenlabs.io` | `xi-api-key: ...` | `ELEVENLABS_API_KEY` or prompt |
| `linear` | `api.linear.app` | `Authorization: ...` | `LINEAR_API_KEY` or prompt |
| `vercel` | `api.vercel.com`, `*.vercel.com` | `Authorization: Bearer ...` | `VERCEL_TOKEN` or prompt |
| `sentry` | `sentry.io`, `*.sentry.io` | `Authorization: Bearer ...` | `SENTRY_AUTH_TOKEN` or prompt |
| `datadog` | `*.datadoghq.com` | `DD-API-KEY: ...` | `DD_API_KEY`, `DATADOG_API_KEY`, or prompt |

Run `moat grant providers` to list all providers, including any [custom providers](#custom-providers) you've added.

## GitHub

### CLI command

```bash
moat grant github
```

No flags. The command automatically detects your credential source.

### Credential sources (in order of preference)

1. **gh CLI** -- Uses the token from `gh auth token` if the GitHub CLI is installed and authenticated
2. **Environment variable** -- Falls back to `GITHUB_TOKEN` or `GH_TOKEN` if set
3. **Personal Access Token** -- Interactive prompt for manual PAT entry

### What it injects

The proxy injects an `Authorization: Bearer <token>` header for requests to:

- `api.github.com`
- `github.com`

The container receives `GH_TOKEN` set to a format-valid placeholder so the gh CLI works without prompting.

### Refresh behavior

Tokens sourced from `gh auth token` or environment variables are refreshed every 30 minutes. PATs entered manually are static.

### agent.yaml

```yaml
grants:
  - github
```

### Example

```bash
$ moat grant github

Found gh CLI authentication
Use token from gh CLI? [Y/n]: y
Validating token...
Authenticated as: octocat
GitHub credential saved

$ moat run --grant github ./my-project
```

## Anthropic

### CLI command

```bash
moat grant anthropic
```

No flags. The command presents a menu with three authentication options.

### Credential sources

1. **Claude subscription (recommended)** -- Runs `claude setup-token` to obtain a long-lived OAuth token. Requires a Claude Pro or Max subscription and the Claude CLI installed.
2. **API key** -- Enter an API key directly or set `ANTHROPIC_API_KEY` in your environment. Billed per token to your API account.
3. **Import existing credentials** -- Imports OAuth tokens from your local Claude Code installation. Imported tokens do not auto-refresh; re-run `moat grant anthropic` when they expire.

### What it injects

The proxy injects credentials for requests to `api.anthropic.com`:

- **OAuth tokens** (`sk-ant-oat` prefix): `Authorization: Bearer <token>`
- **API keys** (`sk-ant-api` prefix): `x-api-key: <key>`

The container receives either `CLAUDE_CODE_OAUTH_TOKEN` or `ANTHROPIC_API_KEY` set to a placeholder value.

### Refresh behavior

OAuth tokens imported from a local Claude Code installation do not auto-refresh. When the token expires, run a Claude Code session on your host to refresh it, then re-import with `moat grant anthropic`.

API keys do not expire.

### agent.yaml

```yaml
grants:
  - anthropic
```

### Example

```bash
$ moat grant anthropic

Choose authentication method:

  1. Claude subscription (recommended)
  2. Anthropic API key
  3. Import existing Claude Code credentials

Enter choice [1, 2, or 3]: 1

Running 'claude setup-token' to obtain authentication token...
Anthropic credential saved to ~/.moat/credentials/anthropic.enc

$ moat claude ./my-project
```

## OpenAI

### CLI command

```bash
moat grant openai
```

No flags.

### Credential sources

1. **Environment variable** -- Uses `OPENAI_API_KEY` if set
2. **Interactive prompt** -- Prompts for an API key

### What it injects

The proxy injects an `Authorization: Bearer <token>` header for requests to `api.openai.com`, `chatgpt.com`, and `*.openai.com`.

The container receives `OPENAI_API_KEY` set to a format-valid placeholder so OpenAI SDKs work without prompting.

### Refresh behavior

API keys do not expire or refresh.

### agent.yaml

```yaml
grants:
  - openai
```

### Example

```bash
$ moat grant openai
Enter your OpenAI API key.
You can find or create one at: https://platform.openai.com/api-keys

API Key: ••••••••
Validating API key...
API key is valid.

OpenAI API key saved to ~/.moat/credentials/openai.enc

$ moat codex ./my-project
```

## Gemini

### CLI command

```bash
moat grant gemini
```

No flags. The command detects whether Gemini CLI is installed and presents options accordingly.

### Credential sources

1. **Gemini CLI OAuth (recommended)** -- Imports refresh tokens from a local Gemini CLI installation. Requires Gemini CLI installed and authenticated.
2. **API key** -- Enter an API key directly or set `GEMINI_API_KEY` in your environment.

### What it injects

Gemini routes to different API backends depending on authentication method:

- **API key mode**: The proxy injects an `x-goog-api-key: <key>` header for requests to `generativelanguage.googleapis.com`. The container receives `GEMINI_API_KEY` set to a placeholder value.
- **OAuth mode**: The proxy injects `Authorization: Bearer <token>` for requests to `cloudcode-pa.googleapis.com` and handles token substitution for `oauth2.googleapis.com`. The container receives a placeholder `oauth_creds.json` in `~/.gemini/`.

### Refresh behavior

OAuth tokens are automatically refreshed by the proxy. Google OAuth tokens expire after 1 hour; Moat refreshes 15 minutes before expiry (every 45 minutes).

API keys do not expire.

### agent.yaml

```yaml
grants:
  - gemini
```

### Example

```bash
$ moat grant gemini

Choose authentication method:

  1. Import Gemini CLI credentials (recommended)
  2. Gemini API key

Enter choice [1 or 2]: 1

Found Gemini CLI credentials.
Validating refresh token...
Refresh token is valid.

Gemini credential saved to ~/.moat/credentials/gemini.enc

$ moat gemini ./my-project
```

## npm

### CLI command

```bash
moat grant npm
moat grant npm --host=<registry-host>
```

### Flags

| Flag | Description |
|------|-------------|
| `--host HOSTNAME` | Specific registry host (e.g., `npm.company.com`) |

Without `--host`, the command auto-discovers registries from `~/.npmrc` and the `NPM_TOKEN` environment variable.

### Credential sources

1. **`.npmrc` file** -- Parses `~/.npmrc` for `//host/:_authToken=` entries and `@scope:registry=` routing
2. **Environment variable** -- Falls back to `NPM_TOKEN` for the default registry (`registry.npmjs.org`)
3. **Manual entry** -- Interactive prompt for a token

### What it injects

The proxy injects an `Authorization: Bearer <token>` header for requests to each registered npm registry host. Each host gets its own credential — multiple registries are supported in a single grant.

The container receives a generated `.npmrc` at `~/.npmrc` with:
- Real scope-to-registry routing (npm needs this to resolve scoped packages)
- Placeholder tokens (`npm_moatProxyInjected00000000`) — the proxy replaces `Authorization` headers at the network layer

### Refresh behavior

npm tokens are static and do not refresh. If a token expires, revoke and re-grant.

### Stacking

Multiple `moat grant npm --host=<host>` calls merge into a single credential. Each call adds or replaces the entry for that host. All registries are injected together at runtime.

### agent.yaml

```yaml
grants:
  - npm
```

### Example

```bash
$ moat grant npm

Choose authentication method:

  1. Import from .npmrc / environment
     Found registries: registry.npmjs.org (default), npm.company.com (@myorg)
     To import a single registry, use: moat grant npm --host=<host>

  2. Enter token manually

Enter choice [1-2]: 1
Validating...
  ✓ registry.npmjs.org — authenticated as "jsmith"
  ✓ npm.company.com — authenticated as "jsmith"
Credential saved to ~/.moat/credentials/npm.enc

$ moat run --grant npm -- npm whoami
jsmith
```

## AWS

### CLI command

```bash
moat grant aws --role <ARN> [flags]
```

### Flags

| Flag | Description | Default |
|------|-------------|---------|
| `--role ARN` | IAM role ARN to assume (required) | -- |
| `--region REGION` | AWS region for API calls | From AWS config |
| `--session-duration DURATION` | Session duration (e.g., `1h`, `30m`, `15m`) | `15m` |
| `--external-id ID` | External ID for cross-account role assumption | -- |

### Credential source

Moat uses your host AWS credentials to call `sts:AssumeRole`. Your host must have valid AWS credentials (via `aws configure`, environment variables, or instance profile), and the target role must have a trust policy allowing your host identity to assume it.

### What it injects

AWS credentials use `credential_process` rather than HTTP header injection:

1. The role ARN and configuration are stored (not temporary credentials)
2. When a run starts, Moat configures `AWS_CONFIG_FILE` in the container with a `credential_process` entry
3. The `credential_process` command calls back to the proxy, which assumes the role and returns fresh temporary credentials
4. The AWS SDK automatically calls the credential process when credentials expire

> **Note:** The `credential_process` mechanism is accessible inside the container. Credentials are temporary (STS), sessions are short (default 15 minutes), and permissions are scoped to the assumed role.

### Refresh behavior

The AWS SDK handles credential refresh automatically via `credential_process`. Each call to the process assumes a fresh role session with the configured duration.

### agent.yaml

```yaml
grants:
  - aws
```

> **Note:** AWS-specific options (role, region, session duration, external ID) are configured at grant time with `moat grant aws`, not in `agent.yaml`. The `agent.yaml` grants field only specifies which grant types to use for a run.

### Example

```bash
$ moat grant aws \
    --role arn:aws:iam::123456789012:role/AgentRole \
    --region us-west-2 \
    --session-duration 30m

Assuming role: arn:aws:iam::123456789012:role/AgentRole
Region: us-west-2
Session duration: 30m0s
Role assumed successfully
AWS credential saved

$ moat run --grant aws ./my-project
```

## SSH

### CLI command

```bash
moat grant ssh --host <hostname>
```

### Flags

| Flag | Description |
|------|-------------|
| `--host HOSTNAME` | Host to grant SSH access to (required) |

### Credential source

Uses your host's SSH agent (`SSH_AUTH_SOCK`). The SSH agent must be running with keys loaded.

### What it injects

SSH grants work differently from other grants. Instead of injecting HTTP headers, Moat proxies SSH agent requests:

1. An SSH agent proxy starts inside the container
2. The proxy connects to your host's SSH agent
3. Key listing and signing requests are forwarded, but only for keys mapped to the granted host
4. Private keys never enter the container

### Refresh behavior

SSH agent requests are forwarded in real time. No refresh mechanism is needed.

### agent.yaml

```yaml
grants:
  - ssh:github.com
```

The host name is part of the grant identifier, separated by a colon.

### Example

```bash
$ moat grant ssh --host github.com

Using key: user@host (SHA256:...)
Granted SSH access to github.com

$ moat run --grant ssh:github.com -- git clone git@github.com:my-org/my-project.git
```

## MCP

### CLI command

```bash
moat grant mcp <name>
```

The `<name>` argument matches the MCP server name in `agent.yaml`. The credential is stored as `mcp-<name>`.

### Credential source

Interactive prompt for a credential value (hidden input).

### What it injects

The proxy injects the credential into the HTTP header specified by `auth.header` in the MCP server configuration. Injection occurs for requests matching the host in the MCP server's `url` field.

### Refresh behavior

MCP credentials are static. Revoke and re-grant to update them.

### agent.yaml

MCP grants are referenced in the top-level `mcp:` field, not in `grants:`:

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

### Example

```bash
$ moat grant mcp context7
Enter credential for MCP server 'context7': ••••••••
MCP credential 'mcp-context7' saved

$ moat claude ./my-project
```

## Using grants in runs

### Via CLI flags

Pass `--grant` one or more times:

```bash
moat run --grant github ./my-project
moat run --grant github --grant anthropic ./my-project
moat run --grant ssh:github.com ./my-project
```

### Via agent.yaml

List grants in the `grants` field:

```yaml
grants:
  - github
  - anthropic
  - openai
  - npm
  - ssh:github.com
```

Grants from CLI flags are merged with those in `agent.yaml`.

### Multiple grants

Combine any number of grants in a single run:

```yaml
grants:
  - anthropic
  - github
  - ssh:github.com
  - aws
```

```bash
moat claude --grant github --grant ssh:github.com ./my-project
```

Each grant type injects credentials independently. The proxy matches requests by host and injects the appropriate headers.

## Managing grants

### List stored grants

```bash
moat grant list
moat grant list --json
```

### Revoke a grant

```bash
moat revoke github
moat revoke anthropic
moat revoke npm
moat revoke ssh:github.com
moat revoke mcp-context7
```

This deletes the encrypted credential file. Future runs cannot use the credential until you grant it again.

## Credential storage

Credentials are stored encrypted in `~/.moat/credentials/`. See [Credential management](../concepts/02-credentials.md) for encryption and storage details.

## Config-driven providers

The providers from `gitlab` through `datadog` in the [grant types table](#grant-types) are defined as YAML configurations shipped with the binary. They work the same as the Go-implemented providers -- credentials are stored encrypted and injected at the network layer -- but are defined declaratively.

Use them the same way:

```bash
moat grant gitlab
moat run --grant gitlab ./my-project
```

### Custom providers

Add your own providers by creating YAML files in `~/.moat/providers/`. Each file defines a provider with host matching rules, header injection, and credential sources. See [Provider YAML reference](./provider-yaml) for the full schema.

### Listing providers

List all available providers (built-in, packaged, and custom) with:

```bash
moat grant providers
moat grant providers --json
```

## Related pages

- [Credential management](../concepts/02-credentials.md) -- How credential injection works conceptually
- [Security model](../concepts/08-security.md) -- Threat model and security properties
- [CLI reference](./01-cli.md) -- Full CLI command reference, including `moat grant` subcommands
- [agent.yaml reference](./02-agent-yaml.md) -- All `agent.yaml` fields, including `grants` and `mcp`
- [Provider YAML reference](./provider-yaml) -- Schema for YAML-defined credential providers
