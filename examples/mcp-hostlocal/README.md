# Host-local MCP server with host-only credentials

Run an MCP server on the **host** so it can use credentials that exist only
there — a corp credential process, the OS keychain, a VPN-gated CLI — and let
the sandboxed agent use its tools without those credentials ever entering the
container.

## What this demonstrates

- A top-level `mcp:` server with an `http://localhost` URL and **no `auth:`
  block**. The host server authenticates itself; Moat relays the agent's
  requests untouched and injects nothing.
- The proxy relay bridges the container to `localhost` on the host, so the
  agent reaches the server even though the container cannot see host `localhost`
  directly.
- The host credential stays on the host. It is never injected into the
  container, never written to container config, and (if the server is written
  carefully) never returned in a tool result.

This is the opposite trade-off from [`mcp-local`](../mcp-local), where the MCP
server — and its secrets — run *inside* the sandbox.

## Run

Start the host server first, then run the agent:

```bash
node examples/mcp-hostlocal/server.js &
moat claude examples/mcp-hostlocal
```

Ask Claude to call the `whoami` tool. It reports the corp identity without ever
seeing the underlying credential.

## How it works

The agent connects to a relay endpoint on the proxy
(`http://moat-proxy:PORT/mcp/<token>/corp-hostlocal`). The relay, running on the
host, forwards the request to `http://localhost:9123`. The host server uses its
host-only credential to do the work and returns only derived results. The
credential never crosses into the sandbox.

See the [MCP guide](../../docs/content/guides/09-mcp.md) for the full picture.
