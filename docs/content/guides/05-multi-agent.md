---
title: "Running multiple agents"
description: "Run multiple agents simultaneously with hostname routing."
keywords: ["moat", "multi-agent", "hostname routing", "parallel", "services"]
---

# Running multiple agents

This guide covers running multiple agents simultaneously on the same codebase. Hostname routing gives each agent its own URL namespace, avoiding port conflicts.

## Use case

You want to test multiple features in parallel:

- Agent A is implementing dark mode
- Agent B is implementing checkout flow
- Both need to run web servers on port 3000

Without hostname routing, you'd have port conflicts. With hostname routing, each agent gets its own URLs:

```
https://web.dark-mode.localhost:8080
https://web.checkout.localhost:8080
```

## Setting up hostname routing

### 1. Configure ports in agent.yaml

```yaml
name: my-app

dependencies:
  - node@20

ports:
  web: 3000
  api: 8080

command: ["npm", "start"]
```

### 2. Start the routing proxy

```bash
$ moat proxy start

Routing proxy started on :8080
CA certificate: ~/.moat/proxy/ca/ca.crt
```

The proxy runs in the background. It routes requests based on hostname.

### 3. Trust the CA certificate (one time)

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

### 4. Start agents with names

```bash
moat run --name dark-mode ./my-app &
moat run --name checkout ./my-app &
```

Each agent must have a unique name.

### 5. Access via hostnames

```
https://web.dark-mode.localhost:8080   → dark-mode container:3000
https://api.dark-mode.localhost:8080   → dark-mode container:8080
https://web.checkout.localhost:8080    → checkout container:3000
https://api.checkout.localhost:8080    → checkout container:8080
```

## Managing multiple agents

### List running agents

```bash
$ moat list

NAME        RUN ID              STATE    SERVICES
dark-mode   run_a1b2c3d4e5f6   running  web, api
checkout    run_d4e5f6a1b2c3   running  web, api
```

### View logs for a specific agent

```bash
moat logs run_a1b2c3d4e5f6
```

### Stop a specific agent

```bash
moat stop run_a1b2c3d4e5f6
```

Or stop by name (if unique):

```bash
moat stop dark-mode
```

### Stop all agents

```bash
moat stop --all
```

## Environment variables

Inside each container, environment variables point to its own services:

```bash
# In dark-mode container:
MOAT_URL_WEB=http://web.dark-mode.localhost:8080
MOAT_URL_API=http://api.dark-mode.localhost:8080

# In checkout container:
MOAT_URL_WEB=http://web.checkout.localhost:8080
MOAT_URL_API=http://api.checkout.localhost:8080
```

Use these for:

- Service-to-service communication within the agent
- OAuth callback URLs
- Webhook endpoints

## Example: Parallel feature development

### Directory structure

```
my-app/
  agent.yaml
  package.json
  src/
    ...
```

### agent.yaml

```yaml
name: my-app

dependencies:
  - node@20

grants:
  - github
  - anthropic

ports:
  web: 3000

snapshots:
  triggers:
    disable_pre_run: false
```

### Start multiple Claude Code sessions

Terminal 1:
```bash
moat claude --name dark-mode ./my-app
```

Terminal 2:
```bash
moat claude --name checkout ./my-app
```

Each Claude Code session:
- Has its own container
- Has its own credential injection proxy
- Can access its services at unique hostnames
- Has independent snapshots

### Access the web servers

Open in browser:
- `https://web.dark-mode.localhost:8080`
- `https://web.checkout.localhost:8080`

Test both features side by side.

## Hostname format

The hostname format is:

```
<service>.<agent-name>.localhost:<proxy-port>
```

- `service` — From the `ports` map in agent.yaml
- `agent-name` — From `--name` flag or `name` in agent.yaml
- `proxy-port` — Default 8080, or as specified with `moat proxy start --port`

### Naming constraints

Agent names must be:
- Lowercase alphanumeric with hyphens
- Valid DNS label (no underscores, no leading/trailing hyphens)
- Unique among running agents

## Proxy management

### Check proxy status

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

### Use a different port

```bash
moat proxy stop
moat proxy start --port 9000
```

Update URLs accordingly: `https://web.dark-mode.localhost:9000`

### Stop the proxy

```bash
moat proxy stop
```

Running agents continue to work, but hostname routing is unavailable until the proxy restarts.

## Without hostname routing

If you don't need hostname routing, agents can still run in parallel:

```bash
moat run --name agent-1 ./my-app &
moat run --name agent-2 ./my-app &
```

Services won't be externally accessible via hostnames, but agents can still:
- Run their code
- Make outbound requests
- Use credential injection

## Troubleshooting

### "Name already in use"

An agent with that name is already running:

```bash
$ moat list
$ moat stop existing-agent
```

### Browser shows certificate error

Trust the CA certificate (see step 3 above), or the certificate may have been regenerated. Re-trust:

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

3. Check the service name matches:
   ```bash
   # URL uses "web" but agent.yaml has "frontend"
   # Fix: use https://frontend.agent.localhost:8080
   ```

### Agents interfering with each other

Each agent has an isolated filesystem, but they share the same workspace on your host. Changes by one agent are visible to others.

For complete isolation, use separate workspace directories:

```bash
cp -r ./my-app ./my-app-dark-mode
cp -r ./my-app ./my-app-checkout

moat run --name dark-mode ./my-app-dark-mode &
moat run --name checkout ./my-app-checkout &
```

## Related guides

- [Networking](../concepts/05-networking.md) — How hostname routing works
- [Running Claude Code](./01-running-claude-code.md) — Use multiple Claude Code sessions
- [Snapshots](./06-snapshots.md) — Independent snapshots per agent
