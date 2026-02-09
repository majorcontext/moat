---
title: "Proxy architecture"
navTitle: "Proxy"
description: "How Moat's TLS-intercepting proxy handles credential injection, MCP relay, network policies, and traffic observability."
keywords: ["moat", "proxy", "tls", "credential injection", "mcp relay", "network policy"]
---

# Proxy architecture

Moat routes all container HTTP and HTTPS traffic through a TLS-intercepting proxy on the host. The proxy sits between the container and the internet, inspecting and modifying requests before they reach upstream servers. It is the mechanism behind credential injection, MCP relay, network policy enforcement, and request logging.

The container never communicates directly with external services. Every outbound HTTP request passes through the proxy, giving Moat a single control point for security and observability.

## What the proxy does

The proxy serves four functions:

- **Credential injection** -- Matches outbound requests by hostname and injects Authorization headers (or other configured headers) into the request. The container process does not have access to the raw tokens.
- **MCP relay** -- Relays requests from the container to remote MCP servers, injecting credentials along the way. This works around HTTP clients that do not respect `HTTP_PROXY` settings.
- **Network policy enforcement** -- In strict mode, blocks requests to hosts not on the allow list. In permissive mode (the default), all hosts are reachable.
- **Request logging** -- Captures HTTP method, URL, status code, timing, headers, and body snippets (up to 8 KB) for every proxied request. This data is written to `~/.moat/runs/<run-id>/network.jsonl`.

## How traffic flows

Moat sets `HTTP_PROXY` and `HTTPS_PROXY` environment variables inside the container, pointing to the proxy on the host. Most HTTP clients -- including `curl`, Python `requests`, Node.js `fetch`, and Go's `net/http` -- respect these variables automatically.

```text
Container process
  → HTTP_PROXY / HTTPS_PROXY (set by Moat)
  → Moat proxy on host
  → TLS interception (CONNECT + dynamic cert)
  → Network policy check
  → Credential injection for matching hosts
  → Request forwarded to upstream server
```

For HTTPS, the client sends a `CONNECT` request to the proxy. The proxy checks the network policy, then performs TLS interception: it generates a certificate for the target host on the fly, signed by a per-run CA. The proxy terminates the client's TLS connection, inspects (and optionally modifies) the request, then opens a new TLS connection to the upstream server and forwards the request.

For plain HTTP, the proxy forwards the request directly, injecting credentials if the host matches.

Requests to `localhost`, `127.0.0.1`, and other addresses listed in `NO_PROXY` bypass the proxy entirely. Moat sets `NO_PROXY` automatically to exclude local addresses and internal endpoints like the MCP relay.

## TLS interception

The proxy generates a CA certificate and key, stored at `~/.moat/proxy/ca/`. When a container makes an HTTPS request, the proxy:

1. Receives the `CONNECT` request with the target hostname
2. Generates a TLS certificate for that hostname, signed by the CA
3. Caches the generated certificate for reuse within the same session
4. Terminates the client's TLS connection using the generated certificate
5. Opens a separate TLS connection to the upstream server, verifying its real certificate
6. Forwards the request, injecting credentials if configured for that host

The CA certificate is mounted into the container's trust store at `/etc/ssl/certs/` so the container's HTTP clients accept the proxy's certificates without errors. On the host, the CA files are stored at `~/.moat/proxy/ca/ca.crt` and `~/.moat/proxy/ca/ca.key`.

All HTTPS traffic is intercepted, not just traffic to credential-injected hosts. This is intentional -- network traces capture every request for full observability. Applications with certificate pinning will fail, since they reject the proxy's generated certificates.

> **Note:** If you need to access the routing proxy from a browser on your host machine (for hostname routing), you need to trust the CA certificate separately. See [Exposing ports](../guides/06-ports.md#trusting-the-ca-certificate) for platform-specific instructions.

Non-HTTPS traffic (raw TCP, UDP) bypasses the proxy entirely. In strict network policy mode, iptables rules block non-proxy traffic independently of the proxy. See [Networking](./05-networking.md) for details on how non-HTTP traffic is handled in each policy mode.

## Credential injection

When a request matches a configured host, the proxy injects the credential header before forwarding. Each grant type targets specific hosts. For example, the `github` grant injects tokens for `api.github.com` and `github.com`. See [Grants reference](../reference/04-grants.md) for the complete mapping.

Requests to other hosts pass through without modification. Injected credentials are redacted in network traces, replaced with `[REDACTED]`.

Some grants also set placeholder environment variables inside the container. For example, `--grant github` sets `GH_TOKEN` to a format-valid placeholder so the `gh` CLI works, but the actual token is injected at the network layer. The environment variable never contains the real credential.

For details on how credentials are stored and granted, see [Credential management](./02-credentials.md).

## MCP relay

Some HTTP clients (including Claude Code's MCP client) do not respect `HTTP_PROXY` environment variables. To handle this, the proxy exposes a local relay endpoint at `/mcp/{server-name}`.

The flow:

1. Moat generates a `.claude.json` configuration pointing MCP server URLs to the proxy relay: `http://proxy-host:port/mcp/{name}`
2. Claude Code connects to the relay endpoint directly (no proxy needed)
3. The proxy extracts the server name from the path, looks up the real MCP server URL and credentials
4. The proxy forwards the request to the real MCP server with credentials injected
5. Responses, including SSE (Server-Sent Events) streams, are forwarded back to the client

The relay client uses `http.Transport{Proxy: nil}` to connect directly to the upstream MCP server, preventing circular proxy loops. The container's `NO_PROXY` variable is configured to exclude the relay endpoint from proxying.

## Runtime-specific behavior

The proxy binds to different interfaces depending on the container runtime:

| Runtime | Bind address | Container reaches proxy via | Authentication |
|---------|-------------|---------------------------|----------------|
| Docker | `127.0.0.1` (localhost) | `host.docker.internal` or host network mode | None required |
| Apple containers | `0.0.0.0` (all interfaces) | Host gateway IP | Per-run token |

**Docker:** The proxy binds to localhost only. Docker containers reach host services via `host.docker.internal`, which resolves to the host's loopback interface. No proxy authentication is required because only processes on the host can reach `127.0.0.1`. Firewall rules do not filter by destination IP for the proxy port, since `host.docker.internal` resolves to different IPs across environments.

**Apple containers:** Containers access the host via a gateway IP, not localhost. The proxy must bind to all interfaces (`0.0.0.0`) so the container can reach it. Security is maintained with a per-run cryptographic token: 32 bytes generated from `crypto/rand`, passed to the container in the `HTTP_PROXY` URL as `http://moat:<token>@host:port`. The proxy validates the `Proxy-Authorization` header on every request using constant-time comparison to prevent timing attacks.

## Proxy lifecycle

The proxy starts before the container and stops after the container exits. The sequence:

1. Moat generates (or loads) the CA certificate
2. Moat starts the proxy on a random high port
3. Moat creates the container with `HTTP_PROXY`, `HTTPS_PROXY`, and `NO_PROXY` set
4. The CA certificate is mounted into the container's trust store
5. The container runs, with all HTTP/HTTPS traffic routed through the proxy
6. The container exits
7. Moat stops the proxy

When multiple agents run simultaneously with port routing configured, they share the same routing proxy instance. The credential injection proxy runs per-session. The routing proxy port defaults to `8080` and can be changed with `MOAT_PROXY_PORT`.

## Network policy enforcement

The proxy enforces network policies for HTTP and HTTPS traffic. Two modes are available:

- **Permissive** (default) -- All outbound requests are allowed. The proxy forwards traffic to any host.
- **Strict** -- Only explicitly allowed hosts are reachable. All other requests are blocked.

In strict mode, the proxy checks every request against the allow list before forwarding:

- Allowed requests proceed normally
- Blocked requests receive a `403 Forbidden` response with a message explaining which host was blocked and how to allow it

The allow list supports exact hostnames and wildcard patterns (e.g., `*.amazonaws.com`). Hosts from granted credentials are automatically added to the allow list. For example, `--grant github` allows `api.github.com` and `github.com` even if they are not listed in `network.allow`.

For non-HTTP traffic in strict mode, iptables rules provide enforcement independently of the proxy. The firewall blocks all outbound traffic except loopback, DNS (UDP port 53), established connections, and traffic to the proxy itself. This prevents code from bypassing the proxy by making direct socket connections.

See [Networking](./05-networking.md) for the full network policy model, including wildcard patterns and firewall rules.

## Security boundaries

The proxy's credential injection model protects against:

- Credential exposure via environment variable logging -- tokens are not stored in env vars
- Credential theft by dumping process environment -- the container environment does not contain real tokens
- Accidental credential leakage in agent output -- the agent never has direct access to raw tokens

The model does not protect against a container that intercepts its own network traffic at the socket level before it reaches the proxy, or against container escape exploits. The proxy assumes the container is semi-trusted code that should not have direct credential access but is not actively attempting to escape the sandbox.

For a full discussion of the trust model and threat boundaries, see [Credential management: Security properties](./02-credentials.md#security-properties).

## Related concepts

- [Credential management](./02-credentials.md) -- How credentials are stored, encrypted, and scoped to hosts
- [Networking](./05-networking.md) -- Network policies, strict mode, and non-HTTP traffic handling
- [Observability](./03-observability.md) -- Network traces and request logging captured by the proxy
- [Environment variables](../reference/03-environment.md) -- `HTTP_PROXY`, `HTTPS_PROXY`, `NO_PROXY`, and `MOAT_PROXY_PORT`
