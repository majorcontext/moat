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

Run multiple agents with endpoints on the same ports. Each agent gets its own hostname namespace.

### Configuring ports

Declare endpoint ports in `agent.yaml`:

```yaml
name: my-agent

ports:
  web: 3000
  api: 8080
```

When you run an agent with ports configured, the routing proxy starts automatically on port 8080 (or the port specified by `MOAT_PROXY_PORT`).

### Accessing endpoints

With the routing proxy running, endpoints are accessible via hostnames:

```
https://<endpoint>.<agent-name>.localhost:<proxy-port>
```

Example with two agents:

```bash
$ moat run --name dark-mode ./my-app &
$ moat run --name checkout ./my-app &

$ moat list
NAME        RUN ID              STATE    ENDPOINTS
dark-mode   run_a1b2c3d4e5f6   running  web, api
checkout    run_d4e5f6a1b2c3   running  web, api
```

Access each agent's endpoints:

```
https://web.dark-mode.localhost:8080  → dark-mode container:3000
https://api.dark-mode.localhost:8080  → dark-mode container:8080
https://web.checkout.localhost:8080   → checkout container:3000
https://api.checkout.localhost:8080   → checkout container:8080
```

Both agents expose the same internal ports, but the routing proxy directs traffic based on the hostname.

### Environment variables

Inside each container, environment variables point to its own endpoints:

```bash
# In dark-mode container:
MOAT_URL_WEB=http://web.dark-mode.localhost:8080
MOAT_URL_API=http://api.dark-mode.localhost:8080

# In checkout container:
MOAT_URL_WEB=http://web.checkout.localhost:8080
MOAT_URL_API=http://api.checkout.localhost:8080
```

Use these variables for inter-endpoint communication or OAuth callbacks.

### Running on privileged ports (80/443)

By default, the routing proxy listens on port 8080. To use privileged ports (below 1024), manually start the proxy with `sudo`:

```bash
$ sudo moat proxy start --port 80

Proxy listening on port 80 (HTTP and HTTPS)
Access services at:
  http://<service>.<agent>.localhost:80
  https://<service>.<agent>.localhost:80
```

The proxy runs in the foreground. Keep it running in a separate terminal or use a process manager.

When the proxy is already running, `moat run` will detect and use it. All agents will be accessible on port 80:

```bash
# In another terminal:
$ moat run --name my-agent ./workspace

# Access at:
https://web.my-agent.localhost  # Port 80, no port number needed in URL
```

You can also set `MOAT_PROXY_PORT=80` in your environment, but you must still use `sudo moat proxy start` to bind to the privileged port—`moat run` cannot bind to privileged ports automatically.

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

### Proxy status and management

Check if the proxy is running:

```bash
$ moat proxy status

Proxy running on port 8080 (pid 12345)
Supports HTTP and HTTPS on the same port

Registered agents:
  - https://my-agent.localhost:8080
```

Stop a manually started proxy:

```bash
moat proxy stop
```

The proxy stops automatically when all agents with ports exit. If you manually started it with `moat proxy start`, use `moat proxy stop` or press Ctrl+C in the proxy terminal.

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
- [Exposing ports](../guides/06-exposing-ports.md) — Access services running inside containers
