---
title: "Credential management"
description: "How Moat stores, encrypts, and injects credentials into agent runs."
keywords: ["moat", "credentials", "oauth", "tokens", "injection", "proxy", "security"]
---

# Credential management

Moat injects credentials at the network layer. Tokens are stored encrypted on your host machine and injected into HTTP requests by a TLS-intercepting proxy. The container process does not have direct access to the raw tokens. For services that cannot use HTTP header injection (like AWS CLI with credential_process), credentials are fetched on-demand via the proxy and never stored in the container environment.

## How credential injection works

When you run an agent with `--grant github`:

1. Moat starts a TLS-intercepting proxy on the host
2. Container traffic is routed through the proxy via `HTTP_PROXY`/`HTTPS_PROXY` environment variables
3. The proxy intercepts HTTPS connections (man-in-the-middle with a generated certificate)
4. For requests to `api.github.com`, the proxy injects an `Authorization: Bearer <token>` header
5. The request continues to GitHub with the injected header

The container sees `HTTP_PROXY` and `HTTPS_PROXY` environment variables pointing to the proxy, but it does not see the actual token.

```bash
$ moat run --grant github -- env | grep -i proxy
HTTP_PROXY=http://127.0.0.1:54321
HTTPS_PROXY=http://127.0.0.1:54321

$ moat run --grant github -- env | grep -i token
# (no output)
```

## Credential storage

Credentials are stored in `~/.moat/credentials/`, encrypted with AES-256-GCM.

```
~/.moat/credentials/
  github.enc
  anthropic.enc
```

The encryption key is stored in your system's keychain:

| Platform | Keychain |
|----------|----------|
| macOS | Keychain (via Security framework) |
| Linux | Secret Service (GNOME Keyring / KWallet) |
| Windows | Credential Manager |

If no system keychain is available (headless servers, CI environments), Moat falls back to file-based key storage at `~/.moat/encryption.key` with restricted permissions (`0600`).

## GitHub

GitHub credentials are obtained from multiple sources, in order of preference:

1. **gh CLI** — If you have the [GitHub CLI](https://cli.github.com/) installed and authenticated, Moat uses your existing token
2. **Environment variable** — Falls back to `GITHUB_TOKEN` or `GH_TOKEN` if set
3. **Personal Access Token** — Interactive prompt for manual PAT entry

```bash
$ moat grant github

Found gh CLI authentication
Use token from gh CLI? [Y/n]: y
Validating token...
Authenticated as: octocat
GitHub credential saved
```

Or without gh CLI:

```bash
$ moat grant github

Enter a GitHub Personal Access Token.
To create one: visit https://github.com/settings/tokens
Token: ••••••••
Validating token...
Authenticated as: octocat
GitHub credential saved
```

The token is injected for requests to `api.github.com` and `github.com`.

`GH_TOKEN` is set in the container environment so the gh CLI works inside the container. This environment variable contains a format-valid placeholder value—the actual token is injected at the network layer by the proxy and never appears in the container environment.

## Anthropic

The `moat grant anthropic` command offers three authentication options:

**1. Claude subscription (recommended)** — Uses `claude setup-token` to obtain a long-lived OAuth token. Requires Claude Pro or Max subscription and the Claude CLI installed:

```bash
$ moat grant anthropic

Choose authentication method:

  1. Claude subscription (recommended)
     Uses 'claude setup-token' to get a long-lived OAuth token.
     Requires a Claude Pro/Max subscription.

  2. Anthropic API key
     Use an API key from console.anthropic.com
     Billed per token to your API account.

  3. Import existing Claude Code credentials
     Use OAuth tokens from your local Claude Code installation.

Enter choice [1, 2, or 3]: 1

Running 'claude setup-token' to obtain authentication token...
This may open a browser for authentication.

Anthropic credential saved to ~/.moat/credentials/anthropic.enc

You can now run 'moat claude' to start Claude Code.
```

**2. API key** — Enter an API key directly or set `ANTHROPIC_API_KEY`:

```bash
$ export ANTHROPIC_API_KEY="sk-ant-api..."
$ moat grant anthropic

Choose authentication method:
...
Enter choice [1, 2, or 3]: 2

Using API key from ANTHROPIC_API_KEY environment variable
Validate API key with a test request? This makes a small API call. [Y/n]: y

Validating API key...
API key is valid.
Anthropic API key saved to ~/.moat/credentials/anthropic.enc
```

**3. Import existing credentials** — If you already have Claude Code installed and logged in locally, Moat can import your existing OAuth credentials:

```bash
$ moat grant anthropic

Choose authentication method:
...
Enter choice [1, 2, or 3]: 3

Found Claude Code credentials.
  Subscription: claude_pro
  Expires: 2026-02-15T10:30:00Z

Claude Code credentials imported to ~/.moat/credentials/anthropic.enc
```

Note: Imported tokens do not auto-refresh. When the token expires, run a Claude Code session on your host machine to refresh it, then run `moat grant anthropic` again to import the new token.

OAuth tokens (identified by the `sk-ant-oat` prefix) use the `CLAUDE_CODE_OAUTH_TOKEN` environment variable. API keys use `ANTHROPIC_API_KEY`. In both cases, the environment variable contains a placeholder value—the actual credential is never in the container environment. The proxy intercepts requests to `api.anthropic.com` and injects the real token at the network layer.

The credential is injected for requests to `api.anthropic.com`.

## OpenAI

OpenAI credentials are obtained from the `OPENAI_API_KEY` environment variable or via interactive prompt.

```bash
$ moat grant openai
Enter your OpenAI API key.
You can find or create one at: https://platform.openai.com/api-keys

API Key: ••••••••
Validating API key...
API key is valid.

OpenAI API key saved to ~/.moat/credentials/openai.enc
```

Or from environment variable:

```bash
$ export OPENAI_API_KEY="sk-..."
$ moat grant openai
Using API key from OPENAI_API_KEY environment variable
Validating API key...
API key is valid.
OpenAI API key saved to ~/.moat/credentials/openai.enc
```

The token is injected for requests to `api.openai.com`, `chatgpt.com`, and `*.openai.com`.

`OPENAI_API_KEY` is set in the container environment so OpenAI SDKs work inside the container. This environment variable contains a format-valid placeholder value—the actual API key is injected at the network layer by the proxy and never appears in the container environment.

This credential is used by `moat codex` to run the Codex CLI with OpenAI API access.

## AWS

AWS credentials use IAM role assumption to provide temporary credentials that automatically refresh.

```bash
$ moat grant aws --role arn:aws:iam::123456789012:role/AgentRole

Assuming role: arn:aws:iam::123456789012:role/AgentRole
Session duration: 15m0s
Role assumed successfully
AWS credential saved
```

**Available options:**

| Flag | Description | Default |
|------|-------------|---------|
| `--role` | IAM role ARN to assume (required) | — |
| `--region` | AWS region | From AWS config |
| `--session-duration` | Session duration (e.g., `1h`, `30m`) | 15 minutes |
| `--external-id` | External ID for role assumption | — |

**With explicit region and duration:**

```bash
$ moat grant aws \
    --role arn:aws:iam::123456789012:role/AgentRole \
    --region us-west-2 \
    --session-duration 2h

Assuming role: arn:aws:iam::123456789012:role/AgentRole
Region: us-west-2
Session duration: 2h0m0s
Role assumed successfully
AWS credential saved
```

**How it works:**

1. Moat uses your host AWS credentials to call `sts:AssumeRole`
2. The role ARN and configuration are stored (not the temporary credentials)
3. When a run starts, Moat configures `AWS_CONFIG_FILE` in the container with a [`credential_process`](https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html)—an AWS SDK feature that executes an external command to fetch credentials on demand. The command calls back to Moat's proxy, which assumes the role and returns fresh temporary credentials.
4. The AWS SDK automatically calls the credential process whenever credentials expire, providing seamless refresh during long-running tasks

This approach means credentials are never stored long-term and automatically refresh during long-running tasks.

**Requirements:**

- Your host machine must have valid AWS credentials (via `aws configure`, environment variables, or instance profile)
- The IAM role must have a trust policy allowing your host identity to assume it

**Example trust policy:**

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Principal": {
        "AWS": "arn:aws:iam::123456789012:user/developer"
      },
      "Action": "sts:AssumeRole"
    }
  ]
}
```

The credential is injected for all AWS API requests via the standard SDK credential chain.

## SSH

SSH credentials work differently—Moat proxies SSH agent requests rather than injecting tokens into HTTP headers.

```bash
$ moat grant ssh --host github.com

Using key: user@host (SHA256:...)
Granted SSH access to github.com
```

When a run includes `--grant ssh:github.com`:

1. Moat starts an SSH agent proxy inside the container
2. The proxy connects to your host's SSH agent (`SSH_AUTH_SOCK`)
3. Key listing and signing requests are forwarded, but only for keys mapped to the granted host
4. Private keys never enter the container

```bash
$ moat run --grant ssh:github.com -- git clone git@github.com:org/repo.git
Cloning into 'repo'...
```

**Requirement:** Your SSH agent must be running (`SSH_AUTH_SOCK` must be set).

## MCP servers

MCP (Model Context Protocol) servers can require authentication credentials. Store them with:

```bash
$ moat grant mcp context7
Enter credential for MCP server 'context7': ••••••••
MCP credential 'mcp-context7' saved
```

Configure in agent.yaml:

```yaml
mcp:
  - name: context7
    url: https://mcp.context7.com/mcp
    auth:
      grant: mcp-context7
      header: CONTEXT7_API_KEY
```

When the agent connects to the MCP server, the proxy injects the credential into the specified HTTP header. The agent never sees the raw credential.

See the [MCP servers section](../guides/01-running-claude-code.md#remote-mcp-servers) in the Claude Code guide for complete setup instructions.

## Using credentials in runs

### Via CLI flag

```bash
moat run --grant github ./my-project
moat run --grant github --grant anthropic ./my-project
moat run --grant openai ./my-project
moat run --grant ssh:github.com ./my-project
```

### Via agent.yaml

```yaml
grants:
  - github
  - anthropic
  - openai
  - ssh:github.com
```

Credentials from CLI flags are merged with those in `agent.yaml`.

## Credential scoping

Credentials are scoped by host. The proxy only injects credentials for requests to specific hosts:

| Credential | Hosts |
|------------|-------|
| `github` | `api.github.com`, `github.com` |
| `anthropic` | `api.anthropic.com` |
| `openai` | `api.openai.com`, `chatgpt.com`, `*.openai.com` |
| `aws` | `*.amazonaws.com` (all AWS service endpoints) |
| `ssh:<host>` | The specified host only |
| `mcp-<name>` | Host specified in MCP server's `url` field |

Requests to other hosts pass through the proxy without credential injection.

## Secrets as environment variables

Some services require credentials as environment variables rather than HTTP headers. For these cases, Moat can pull secrets from external backends and inject them into the container environment.

```yaml
secrets:
  OPENAI_API_KEY: op://Dev/OpenAI/api-key      # 1Password
  DATABASE_URL: ssm:///production/database/url  # AWS SSM
```

Unlike grants, secrets are visible to all processes in the container via environment variables. Use grants when possible; use secrets for services that don't support header-based authentication.

See [Secrets Management](../guides/03-secrets-management.md) for detailed setup instructions.

## Revoking credentials

Remove a stored credential:

```bash
moat revoke github
moat revoke anthropic
moat revoke openai
moat revoke ssh:github.com
moat revoke mcp-context7
```

This deletes the encrypted credential file. Future runs cannot use the credential until you grant it again.

## Security considerations

**What this protects against:**

- Credential exposure via environment variable logging
- Credential theft by dumping process environment
- Accidental credential leakage in agent output

**What this does not protect against:**

- A malicious agent that intercepts its own network traffic before the proxy
- Container escape exploits
- Credential theft if an attacker has root access to your host machine

The credential injection model assumes the agent is semi-trusted code that should not have direct credential access, but is not actively malicious and attempting to escape the sandbox.

## Troubleshooting

**"decrypting credential: cipher: message authentication failed"**

The encryption key has changed since the credential was stored. Re-authenticate:

```bash
moat grant github
```

**"SSH_AUTH_SOCK not set"**

Your SSH agent is not running. Start it:

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
```

## Related concepts

- [Sandboxing](./01-sandboxing.md) — How container isolation works
- [Audit logs](./03-audit-logs.md) — Tracking credential usage
- [SSH access guide](../guides/02-ssh-access.md) — Detailed SSH setup
