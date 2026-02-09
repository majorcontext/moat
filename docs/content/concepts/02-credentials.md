---
title: "Credential management"
navTitle: "Credentials"
description: "How Moat stores, encrypts, and injects credentials into agent runs."
keywords: ["moat", "credentials", "oauth", "tokens", "injection", "proxy", "security"]
---

# Credential management

Moat injects credentials at the network layer. Tokens are stored encrypted on your host machine and injected into HTTP requests by a TLS-intercepting proxy. The container process does not have direct access to the raw tokens. For services that cannot use HTTP header injection (like AWS CLI with `credential_process`), credentials are fetched on-demand via the proxy and never stored in the container environment.

## How credential injection works

When you run an agent with `--grant github`:

1. Moat starts a TLS-intercepting proxy on the host
2. Container traffic is routed through the proxy via `HTTP_PROXY`/`HTTPS_PROXY` environment variables
3. The proxy intercepts HTTPS connections (man-in-the-middle with a generated certificate)
4. For requests to `api.github.com`, the proxy injects an `Authorization: Bearer <token>` header
5. The request continues to GitHub with the injected header

The container sees `HTTP_PROXY` and `HTTPS_PROXY` environment variables pointing to the proxy, but it does not see the actual token. No credential-related environment variables are set inside the container.

For a detailed look at proxy internals, see [Proxy architecture](./09-proxy.md).

## The grant abstraction

A **grant** is a credential made available to a run. Each grant type targets specific hosts; the proxy only injects credentials for requests matching those hosts. Requests to other hosts pass through without modification.

| Grant | Injection target | Mechanism |
|-------|-----------------|-----------|
| `github` | `api.github.com`, `github.com` | `Authorization` header |
| `anthropic` | `api.anthropic.com` | `Authorization` header (OAuth) or `x-api-key` header |
| `openai` | `api.openai.com`, `chatgpt.com`, `*.openai.com` | `Authorization` header |
| `aws` | `*.amazonaws.com` | `credential_process` via AWS SDK |
| `ssh:<host>` | The specified host only | SSH agent proxy |
| `mcp-<name>` | Host from MCP server `url` field | Custom header injection |

Grants are configured via the `--grant` CLI flag or the `grants:` field in `agent.yaml`. Credentials from CLI flags are merged with those in `agent.yaml`. See the [Grants reference](../reference/04-grants.md) for syntax details and per-provider setup instructions.

## Credential storage

Credentials are stored in `~/.moat/credentials/`, encrypted with AES-256-GCM. The encryption key is stored in your system's keychain:

| Platform | Keychain |
|----------|----------|
| macOS | Keychain (via Security framework) |
| Linux | Secret Service (GNOME Keyring / KWallet) |
| Windows | Credential Manager |

If no system keychain is available (headless servers, CI environments), Moat falls back to file-based key storage at `~/.moat/encryption.key` with restricted permissions (`0600`).

The `moat revoke` command deletes a stored credential file. Future runs cannot use the credential until you grant it again.

## Secrets as environment variables

Some services require credentials as environment variables rather than HTTP headers. For these cases, Moat can pull secrets from external backends (such as 1Password or AWS SSM) and inject them into the container environment. Unlike grants, secrets are visible to all processes in the container via environment variables. Use grants when possible; use secrets for services that don't support header-based authentication.

See [Secrets Management](../guides/05-secrets.md) for detailed setup instructions.

## Security properties

**What credential injection protects against:**

- Credential exposure via environment variable logging
- Credential theft by dumping process environment
- Accidental credential leakage in agent output

**What it does not protect against:**

- A malicious agent that intercepts its own network traffic before the proxy
- Container escape exploits
- Credential theft if an attacker has root access to your host machine

The credential injection model assumes the agent is semi-trusted code that should not have direct credential access, but is not actively malicious and attempting to escape the sandbox.

For a full discussion of Moat's threat model and trust boundaries, see [Security model](./08-security.md).

## Related concepts

- [Sandboxing](./01-sandboxing.md) -- How container isolation works
- [Observability](./03-observability.md) -- Tracking credential usage
- [Security model](./08-security.md) -- Threat model and trust boundaries
- [Proxy architecture](./09-proxy.md) -- TLS-intercepting proxy internals

## Related references and guides

- [Grants reference](../reference/04-grants.md) -- Per-provider setup and configuration
- [SSH access guide](../guides/04-ssh.md) -- Detailed SSH setup
