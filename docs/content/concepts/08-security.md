---
title: "Security model"
navTitle: "Security"
description: "Moat's security model: container isolation, network-layer credential injection, and the trust boundary between agents and credentials."
keywords: ["moat", "security", "threat model", "credential injection", "container isolation"]
---

# Security model

Moat's security model combines container isolation with network-layer credential injection. Agents run in isolated containers. Credentials are injected into HTTP requests by a TLS-intercepting proxy on the host. The agent process never has direct access to raw tokens.

This page describes what Moat protects against, what it does not protect against, and how to layer additional controls for higher-security workloads.

## Why it matters

AI agents need credentials to clone repositories, call APIs, and deploy infrastructure. Giving an agent raw tokens (via environment variables or config files) creates risk: the agent could log them, leak them in output, or store them where other processes can read them.

Moat separates credential storage from credential use. The agent makes normal HTTP requests; the proxy adds the `Authorization` header transparently. If the agent dumps its environment variables, lists processes, or writes its configuration to disk, no tokens appear.

## What Moat protects against

Moat prevents accidental credential exposure through common leakage vectors:

- **Environment variables** -- Credentials injected by the proxy do not appear in the container's environment. Running `env` or reading `/proc/*/environ` inside the container does not reveal tokens.
- **Process listings** -- Tokens are not passed as command-line arguments, so they do not appear in `ps` output.
- **Agent output** -- Since the agent never receives the raw token, it cannot accidentally include it in logs, error messages, or generated code.
- **Container filesystem** -- No credential files are written inside the container (except for AWS `credential_process`, which returns short-lived STS tokens on demand).

Some grants set placeholder environment variables so that SDKs inside the container function correctly (for example, `GH_TOKEN` is set for the GitHub CLI). These placeholders are format-valid but do not contain the real token. The proxy replaces them at the network layer before the request leaves the host.

## What Moat does not protect against

Moat does not enforce fine-grained permissions on the actions agents take with injected credentials.

- **GitHub grant** -- An agent with GitHub access has the full permissions of that token. It could delete `.git/`, force push to main, or modify repository settings. Protection requires GitHub-level controls: branch protection rules, repository permissions, and scoped tokens.
- **AWS grant** -- An agent with an IAM role can do anything that role allows, including deleting resources. Protection requires IAM-level controls: scoped roles, explicit denies, and resource policies.
- **Filesystem** -- The workspace is mounted read-write by default. The agent can delete or modify any file in the mounted directory.
- **Network traffic interception** -- A malicious agent running as root inside the container could intercept its own traffic before it reaches the proxy, or manipulate the proxy environment variables. Container isolation makes this difficult but not impossible for a determined attacker.
- **SSH grant** -- An agent with SSH access to a host can perform any git operation on that host: clone, push, force push, delete branches. The private key never enters the container, but signing requests are forwarded for any operation the agent initiates on the granted host.

Moat controls credential delivery, not credential usage. The proxy injects whatever credentials you provide; the agent can use those credentials for any action the underlying service allows.

## Trust model

Moat treats the agent as semi-trusted code. The security model rests on three assumptions:

- **The agent is not actively malicious.** It is expected to perform its intended task honestly. It should not have direct credential access, but it is not assumed to be actively trying to escape the sandbox or exfiltrate tokens.
- **Credentials are scoped at the service level.** Permissions are controlled by the services themselves -- IAM roles, GitHub repository permissions, API key scopes -- not by Moat. Grant the minimum permissions the agent needs to do its job.
- **The container boundary prevents accidents, not attacks.** The container prevents accidental credential leakage through environment inspection, logging, and process listing. It is not designed to prevent intentional exfiltration by code that specifically targets the container runtime.

In practice, this means:

- An agent that follows instructions will never see or leak your tokens.
- An agent that deviates from its task -- whether due to a prompt injection, a bug, or malicious instructions in the repository -- can misuse credentials within the permissions of those credentials, but still cannot extract the raw tokens through normal means.
- An agent deliberately designed to escape the container or intercept proxy traffic is outside Moat's threat model. For this scenario, add VM-level isolation.

This model works well for AI coding agents running on code you control. For running code from unknown or untrusted sources, add stronger isolation -- run Moat inside a VM or on a dedicated machine. See [Sandboxing](./01-sandboxing.md) for container isolation details and limitations.

## Runtime-specific proxy security

The TLS-intercepting proxy has different security configurations depending on the container runtime.

| | Docker | Apple containers |
|---|--------|------------------|
| **Proxy bind address** | `127.0.0.1` (localhost only) | `0.0.0.0` (all interfaces) |
| **Container reaches proxy via** | `host.docker.internal` or host network | Gateway IP |
| **Authentication** | Network-level (localhost only) | Per-run cryptographic token |
| **Platform** | Linux, macOS, Windows | macOS 26+ (Apple Silicon) |

### Docker

The proxy binds to `127.0.0.1` (localhost only). Containers reach the proxy via `host.docker.internal` or host network mode. Because the proxy listens only on the loopback interface, other machines on the network cannot reach it. No additional authentication is required -- only processes on the same host can connect.

### Apple containers

On macOS 26+ with Apple Silicon, containers access the host via a gateway IP rather than `host.docker.internal`. The proxy binds to `0.0.0.0` (all interfaces) to be reachable from the container network.

Because the proxy is exposed beyond localhost, security is maintained via per-run cryptographic token authentication. Each run generates a 32-byte random token using `crypto/rand`. The token is embedded in the proxy URL passed to the container:

```
HTTP_PROXY=http://moat:<token>@<host>:<port>
```

The proxy validates the token on every request. Requests without a valid token are rejected. This ensures that only the intended container can use the proxy, even though the proxy listens on all interfaces.

## Secrets vs. credentials

Moat distinguishes between two mechanisms for providing sensitive values to agents:

| | Credentials (grants) | Secrets |
|---|----------------------|---------|
| **Delivery** | Network-layer injection by the proxy | Environment variables in the container |
| **Visibility** | Not visible to processes in the container | Visible to all processes in the container |
| **Configuration** | `grants:` in `agent.yaml` | `secrets:` in `agent.yaml` |
| **Resolution** | At request time by the proxy | At container start on the host |
| **Risk** | Lower -- agent never sees raw token | Higher -- any process can read the env var |

Use grants when a dedicated provider exists (GitHub, Anthropic, OpenAI, AWS, SSH). Use secrets for services without grant support, such as database URLs or signing keys pulled from 1Password or AWS SSM.

Secrets are resolved on the host machine before the container starts. The resolved values are set as environment variables, which means they are accessible to all processes in the container and can appear in `/proc/*/environ`. See [Secrets management](../guides/05-secrets.md) for setup details.

## Credential storage

Credentials are stored on the host machine at `~/.moat/credentials/`, encrypted with AES-256-GCM. The encryption key is held in the system keychain (macOS Keychain, GNOME Keyring, or Windows Credential Manager). On systems without a keychain, the key falls back to a file at `~/.moat/encryption.key` with `0600` permissions.

Credentials are never written inside the container. They are decrypted by the proxy process on the host at the time of injection.

See [Credential management](./02-credentials.md) for storage details and supported credential types.

## Audit trail

Every credential usage event is recorded in a tamper-proof audit log. The log uses a cryptographic hash chain -- each entry includes the SHA-256 hash of the previous entry -- so modifications, insertions, or deletions are detectable.

The audit log captures:

- When credentials are injected and for which hosts
- When secrets are resolved from external backends
- HTTP requests through the proxy (method, URL, status, timing)
- SSH agent operations (key listing, signing)

This provides an after-the-fact record of what credentials an agent used and when. It does not prevent misuse in real time, but it does provide evidence for post-incident review.

Audit logs can be exported as self-contained proof bundles for offline verification or sharing with third parties. See [Observability](./03-observability.md) for verification commands and export options.

## Defense in depth

Moat provides one layer of protection: container isolation with network-layer credential injection. For higher-security workloads, combine Moat with additional controls:

- **Service-level controls** -- Branch protection rules, scoped IAM roles with explicit denies, read-only API tokens, repository permission boundaries. These limit what an agent can do even with valid credentials.
- **Read-only mounts** -- Mount the workspace as read-only (`ro`) when the agent only needs to read code, not modify it. See [Sandboxing](./01-sandboxing.md) for mount configuration.
- **Strict network policies** -- Restrict outbound traffic to only the hosts the agent needs. See [Networking](./05-networking.md) for policy configuration.
- **Short-lived credentials** -- Use short session durations for AWS roles (the default is 15 minutes). Shorter sessions limit the window for credential misuse.
- **Agent-level policy frameworks** -- Tools like [agentsh.org](https://www.agentsh.org/) provide declarative security policies for agent actions. agentsh is complementary to Moat: Moat handles container isolation and credential delivery, while agentsh enforces action-level policies.
- **VM isolation** -- For running untrusted code, run Moat inside a virtual machine or on a dedicated machine. This adds a hardware-level isolation boundary that is significantly harder to escape than a container.

No single mechanism is sufficient. The combination of container isolation, scoped credentials, network policies, audit logging, and service-level controls provides a layered defense appropriate to the risk level of the workload.

As a starting point: scope your IAM roles tightly, enable branch protection on repositories the agent can push to, use `network.policy: strict` to limit outbound access, and review audit logs after runs that modify infrastructure or deploy code.

## Summary

Moat's security model is designed for a specific scenario: running semi-trusted AI agents that need credential access without direct token exposure. The proxy-based injection model prevents accidental leakage. Container isolation provides process and filesystem separation. Audit logging provides accountability.

Moat does not replace service-level access controls or agent-level policy enforcement. It is one layer in a defense-in-depth approach. Scope your credentials tightly, restrict network access, review audit logs, and add VM isolation or policy frameworks when the workload demands it.

## Related concepts

- [Sandboxing](./01-sandboxing.md) -- Container isolation, filesystem mounting, and runtime differences
- [Credential management](./02-credentials.md) -- How credentials are stored, encrypted, and injected
- [Observability](./03-observability.md) -- Tamper-proof logging and verification
- [Networking](./05-networking.md) -- Network policies and proxy traffic flow
