---
title: "Exposing ports"
description: "Access web servers and services running inside agent containers."
keywords: ["moat", "ports", "endpoints", "hostname routing", "web server", "preview"]
---

# Exposing ports

Access web servers and services running inside agent containers from your browser or local tools.

## Single agent

### 1. Declare ports in agent.yaml

```yaml
name: my-app

dependencies:
  - node@20

ports:
  web: 3000

command: ["npm", "start"]
```

The `ports` map defines endpoints. Each key is an endpoint name that becomes part of the URL.

### 2. Start the agent

```bash
moat run --name my-app ./my-app
```

Moat starts a routing proxy automatically when `ports` are configured.

### 3. Open in your browser

```
https://web.my-app.localhost:8080
```

The URL format is `https://<endpoint>.<agent-name>.localhost:<proxy-port>`. The proxy terminates TLS and forwards to the container's port 3000.

### Multiple ports

Declare as many endpoints as your application needs:

```yaml
ports:
  web: 3000
  api: 8080
  docs: 4000
```

Each gets its own URL:

```
https://web.my-app.localhost:8080   → container:3000
https://api.my-app.localhost:8080   → container:8080
https://docs.my-app.localhost:8080  → container:4000
```

## Multiple agents

When two agents expose the same internal port, hostname routing keeps them separate. Each agent's name creates a distinct URL namespace.

```bash
moat run --name dark-mode ./my-app &
moat run --name checkout ./my-app &
```

Both agents run web servers on port 3000, but each is accessible at its own hostname:

```
https://web.dark-mode.localhost:8080  → dark-mode container:3000
https://web.checkout.localhost:8080   → checkout container:3000
```

### List running agents

```bash
$ moat list

NAME        RUN ID              STATE    ENDPOINTS
dark-mode   run_a1b2c3d4e5f6   running  web
checkout    run_d4e5f6a1b2c3   running  web
```

### Stop agents

By name:

```bash
moat stop dark-mode
```

By run ID:

```bash
moat stop run_a1b2c3d4e5f6
```

All at once:

```bash
moat stop --all
```

## Trusting the CA certificate

The routing proxy serves HTTPS using a self-signed CA. Browsers show a certificate warning until you trust the CA. This is a one-time setup.

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

## Environment variables

Inside each container, `MOAT_URL_*` environment variables point to its own endpoints:

```bash
# In the dark-mode container:
MOAT_URL_WEB=http://web.dark-mode.localhost:8080

# In the checkout container:
MOAT_URL_WEB=http://web.checkout.localhost:8080
```

Use these for OAuth callback URLs, webhook endpoints, or inter-service communication within the agent.

## Naming constraints

Agent names must be:

- Lowercase alphanumeric with hyphens
- Valid DNS labels (no underscores, no leading/trailing hyphens)
- Unique among running agents

The name comes from `--name` on the CLI or `name` in `agent.yaml`.

## Proxy management

### Check status

```bash
$ moat proxy status

Routing Proxy
=============
Status: running
Port: 8080
CA: ~/.moat/proxy/ca/ca.crt

Registered Agents:
  dark-mode   web:3000
  checkout    web:3000
```

### Use a different port

```bash
moat proxy stop
moat proxy start --port 9000
```

URLs change accordingly: `https://web.dark-mode.localhost:9000`

You can also set `MOAT_PROXY_PORT=9000` in your environment.

### Use port 80 or 443

Privileged ports require `sudo`:

```bash
sudo moat proxy start --port 80
```

With port 80, URLs don't need a port number: `https://web.my-app.localhost`

When a proxy is already running, `moat run` detects and uses it automatically.

### Stop the proxy

```bash
moat proxy stop
```

The proxy stops automatically when all agents with ports exit. If you started it manually with `moat proxy start`, use `moat proxy stop` or Ctrl+C.

## Without port access

Agents work without `ports` configured. They can still:

- Run code
- Make outbound HTTP requests
- Use credential injection

Port configuration is only needed when you want to access services running inside the container from outside.

## Troubleshooting

### "Name already in use"

An agent with that name is already running:

```bash
$ moat list
$ moat stop existing-agent
```

### Browser shows certificate error

Trust the CA certificate (see [Trusting the CA certificate](#trusting-the-ca-certificate)), or re-trust if the certificate was regenerated:

```bash
# macOS
sudo security delete-certificate -c "Moat CA"
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain \
  ~/.moat/proxy/ca/ca.crt
```

### "Connection refused" on hostname

1. Check the proxy is running:
   ```bash
   moat proxy status
   ```

2. Check the agent is running:
   ```bash
   moat list
   ```

3. Verify the endpoint name matches `agent.yaml`:
   ```bash
   # URL uses "web" but agent.yaml has "frontend"
   # Fix: use https://frontend.my-app.localhost:8080
   ```

### Agents sharing a workspace

Each agent has an isolated container filesystem, but they share the same workspace directory on your host. Changes by one agent are visible to others.

For complete isolation, point each agent at a separate directory. Git worktrees work well for this — each agent gets its own working tree from the same repository:

```bash
moat run --name dark-mode .worktrees/dark-mode &
moat run --name checkout .worktrees/checkout &
```

## Related

- [Networking](../concepts/05-networking.md) — How the routing proxy works, network policies, and traffic flow
- [Running Claude Code](./01-running-claude-code.md) — Use port access with Claude Code sessions
- [Snapshots](./06-snapshots.md) — Independent snapshots per agent
