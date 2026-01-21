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

Run multiple agents with services on the same ports. Each agent gets its own hostname namespace.

### Configuring ports

Declare service ports in `agent.yaml`:

```yaml
name: my-agent

ports:
  web: 3000
  api: 8080
```

### Starting the routing proxy

The hostname routing proxy is separate from the credential injection proxy:

```bash
$ moat proxy start

Routing proxy started on :8080
CA certificate: ~/.moat/proxy/ca/ca.crt
```

The proxy listens on port 8080 by default. Change with `--port`:

```bash
moat proxy start --port 9000
```

### Accessing services

With the routing proxy running, services are accessible via hostnames:

```
https://<service>.<agent-name>.localhost:<proxy-port>
```

Example with two agents:

```bash
$ moat run --name dark-mode ./my-app &
$ moat run --name checkout ./my-app &

$ moat list
NAME        RUN ID              STATE    SERVICES
dark-mode   run_a1b2c3d4e5f6   running  web, api
checkout    run_d4e5f6a1b2c3   running  web, api
```

Access each agent's services:

```
https://web.dark-mode.localhost:8080  → dark-mode container:3000
https://api.dark-mode.localhost:8080  → dark-mode container:8080
https://web.checkout.localhost:8080   → checkout container:3000
https://api.checkout.localhost:8080   → checkout container:8080
```

Both agents expose the same internal ports, but the routing proxy directs traffic based on the hostname.

### Environment variables

Inside each container, environment variables point to its own services:

```bash
# In dark-mode container:
MOAT_URL_WEB=http://web.dark-mode.localhost:8080
MOAT_URL_API=http://api.dark-mode.localhost:8080

# In checkout container:
MOAT_URL_WEB=http://web.checkout.localhost:8080
MOAT_URL_API=http://api.checkout.localhost:8080
```

Use these variables for service-to-service communication or OAuth callbacks.

### Trusting the CA certificate

The routing proxy uses HTTPS with a self-signed CA certificate. To avoid browser warnings, trust the CA:

**macOS:**
```bash
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain \
  ~/.moat/proxy/ca/ca.crt
```

**Linux:**
```bash
sudo cp ~/.moat/proxy/ca/ca.crt /usr/local/share/ca-certificates/moat.crt
sudo update-ca-certificates
```

### Stopping the proxy

```bash
moat proxy stop
```

### Proxy status

```bash
$ moat proxy status

Routing Proxy
=============
Status: running
Port: 8080
CA: ~/.moat/proxy/ca/ca.crt

Registered Agents:
  dark-mode   web:3000, api:8080
  checkout    web:3000, api:8080
```

## Non-HTTP traffic

The proxy handles HTTP and HTTPS traffic. Other protocols behave differently depending on the network policy:

### Permissive mode

Non-HTTP traffic has direct network access:

- **Raw TCP** — Connects directly (bypasses proxy)
- **UDP** — Connects directly (bypasses proxy)
- **SSH** — Uses the SSH agent proxy for granted hosts (see [SSH access](../guides/02-ssh-access.md))

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

## Debugging network issues

### Check proxy status

```bash
moat status
```

Shows whether the proxy is running and any errors.

### View network traces

```bash
moat trace --network
moat trace --network -v  # With headers and bodies
```

### Test connectivity from container

```bash
moat run -- curl -v https://api.github.com/
```

The `-v` flag shows the connection details, including proxy negotiation.

### Check policy violations

If requests are blocked by network policy, they appear in logs with the block reason:

```bash
$ moat logs | grep "blocked by network policy"

[10:23:45] Moat: request blocked by network policy. Host "example.com" is not in the allow list.
```

## Related concepts

- [Credential management](./02-credentials.md) — How credentials are injected via the proxy
- [Sandboxing](./01-sandboxing.md) — Container network isolation
- [Multi-agent guide](../guides/04-multi-agent.md) — Running multiple agents with hostname routing
