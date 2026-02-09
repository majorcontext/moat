---
title: "Networking"
description: "Network policies, hostname routing, and proxy configuration."
keywords: ["moat", "networking", "firewall", "network policy", "hostname routing", "proxy"]
---

# Networking

Moat controls container network access through a TLS-intercepting proxy. This page covers network policies, hostname routing for multi-agent setups, and how traffic flows through the proxy.

## Traffic flow

All HTTP and HTTPS traffic from containers routes through Moat's proxy:

```
Container → HTTP_PROXY → Moat Proxy → Internet
                            ↓
                    Credential injection
                    Network policy enforcement
                    Request logging
```

The proxy is set via `HTTP_PROXY` and `HTTPS_PROXY` environment variables in the container. Most HTTP clients respect these variables automatically.

For HTTPS traffic, the proxy performs TLS interception (man-in-the-middle) using a generated CA certificate. This allows the proxy to inspect requests and inject credentials.

## Network policies

Control which hosts containers can reach with network policies.

### Permissive mode (default)

All outbound HTTP/HTTPS traffic is allowed:

```yaml
network:
  policy: permissive
```

This is the default. Containers can reach any host on the internet.

### Strict mode

Only explicitly allowed hosts are reachable:

```yaml
network:
  policy: strict
  allow:
    - "api.github.com"
    - "api.anthropic.com"
    - "*.amazonaws.com"
```

Hosts from granted credentials are automatically allowed. If you grant `github`, requests to `api.github.com` and `github.com` are allowed even if not listed.

### Blocked request behavior

When a request is blocked, the proxy returns an error response:

```
HTTP/1.1 403 Forbidden

Moat: request blocked by network policy.
Host "blocked.example.com" is not in the allow list.
Add it to network.allow in agent.yaml or use policy: permissive.
```

The error message tells the agent (and you, via logs) exactly what happened and how to fix it.

### Wildcard patterns

The `allow` list supports wildcard patterns:

| Pattern | Matches |
|---------|---------|
| `api.github.com` | Exact match only |
| `*.github.com` | Any subdomain of github.com |
| `*.*.amazonaws.com` | Two levels of subdomains |

## Hostname routing

Moat includes a routing proxy that gives each agent its own hostname namespace. When agents declare `ports` in `agent.yaml`, the proxy maps hostnames like `https://web.my-agent.localhost:8080` to the corresponding container port. This allows multiple agents to expose the same internal ports without conflicts -- the routing proxy directs traffic based on the hostname in each request. The proxy also serves HTTPS using a generated CA certificate and sets `MOAT_URL_*` environment variables inside each container for inter-service communication.

See [Exposing ports](../guides/06-ports.md) for configuration and usage. See [Proxy architecture](../concepts/09-proxy.md) for details on how the routing proxy works.

## Non-HTTP traffic

The proxy handles HTTP and HTTPS traffic. Other protocols behave differently depending on the network policy:

### Permissive mode

Non-HTTP traffic has direct network access:

- **Raw TCP** — Connects directly (bypasses proxy)
- **UDP** — Connects directly (bypasses proxy)
- **SSH** — Uses the SSH agent proxy for granted hosts (see [SSH access](../guides/04-ssh.md))

### Strict mode

When `network.policy: strict` is set, Moat configures iptables rules that block all outbound traffic except:

- Loopback (`localhost`, `127.0.0.1`)
- Established/related connections (response packets)
- DNS (UDP port 53)
- Traffic to the credential injection proxy

This means:

- **Raw TCP** — Blocked (connection refused)
- **UDP** — Blocked, except DNS
- **SSH** — Still works via the SSH agent proxy for granted hosts
- **Direct socket connections** — Blocked, even if the destination would be allowed via HTTP

The iptables firewall ensures that code cannot bypass the proxy by making direct socket connections.

## Proxy bypass

Some traffic bypasses the proxy:

- Requests to `localhost` and `127.0.0.1`
- Requests to container-internal addresses
- Hosts listed in `NO_PROXY` environment variable

The `NO_PROXY` variable is set automatically to exclude local addresses.

## Related concepts

- [Credential management](./02-credentials.md) — How credentials are injected via the proxy
- [Proxy architecture](./09-proxy.md) — TLS interception, credential injection, and proxy security model
- [Sandboxing](./01-sandboxing.md) — Container network isolation

## Related guides

- [Exposing ports](../guides/06-ports.md) — Hostname routing configuration, CA trust setup, and proxy management
