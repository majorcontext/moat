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

Only explicitly listed hosts are reachable:

```yaml
network:
  policy: strict
  rules:
    - "api.github.com"
    - "api.anthropic.com"
    - "*.amazonaws.com"
```

Hosts from granted credentials are automatically allowed. If you grant `github`, requests to `api.github.com` and `github.com` are allowed even if not listed.

### Blocked request behavior

When a request is blocked, the proxy returns an error response:

```
HTTP/1.1 407 Proxy Authentication Required

Moat: request blocked by network policy.
Host "blocked.example.com" is not in the allow list.
Add it to network.rules in moat.yaml or use policy: permissive.
```

The error message tells the agent (and you, via logs) exactly what happened and how to fix it.

### Wildcard patterns

The `rules` list supports wildcard hostname patterns:

| Pattern | Matches |
|---------|---------|
| `api.github.com` | Exact match only |
| `*.github.com` | Any subdomain of github.com |
| `*.*.amazonaws.com` | Two levels of subdomains |

## Request-level rules

In addition to host-level access control, each host entry in `network.rules` can include per-request rules that filter by HTTP method and path.

```yaml
network:
  policy: strict
  rules:
    - "api.github.com":
        - "allow GET /repos/**"
        - "deny * /**"
    - "api.anthropic.com"
```

### Rule format

Each rule is a string: `"<allow|deny> <method> <path-pattern>"`

- `method`: HTTP method (`GET`, `POST`, `PUT`, `DELETE`, `PATCH`, etc.) or `*` for any method
- `path-pattern`: URL path where `*` matches a single path segment and `**` matches zero or more segments

> **Note:** `/prefix/**` matches paths with one or more segments after the prefix, but does not match `/prefix` itself. To block both `/admin` and everything under it, use two rules:
>
> ```yaml
> - "deny * /admin"
> - "deny * /admin/**"
> ```
>
> The root wildcard `/**` is an exception — it matches all paths including `/`.

### Evaluation order

Rules are evaluated top to bottom. The first matching rule wins. If no rule matches, the request falls through to the policy default:

- `permissive` — allows the request
- `strict` — blocks the request

### Common patterns

**Read-only access to an API:**

```yaml
network:
  policy: strict
  rules:
    - "api.example.com":
        - "allow GET /**"
        - "deny * /**"
```

The explicit `deny * /**` rule at the end ensures non-GET requests are blocked even in permissive mode. Without it, a non-matching request would fall through to the policy default.

**Block admin endpoints while allowing everything else:**

```yaml
network:
  policy: permissive
  rules:
    - "api.example.com":
        - "deny * /admin"
        - "deny * /admin/**"
        - "deny DELETE /**"
```

**Restrict to specific paths:**

```yaml
network:
  policy: strict
  rules:
    - "api.example.com":
        - "allow POST /v1/messages"
        - "allow GET /v1/models"
        - "deny * /**"
```

## Hostname routing

Moat includes a routing proxy that gives each agent its own hostname namespace. When agents declare `ports` in `moat.yaml`, the proxy maps hostnames like `https://web.my-agent.localhost:8080` to the corresponding container port. This allows multiple agents to expose the same internal ports without conflicts -- the routing proxy directs traffic based on the hostname in each request. The proxy also serves HTTPS using a generated CA certificate and sets `MOAT_URL_*` environment variables inside each container for inter-service communication.

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
