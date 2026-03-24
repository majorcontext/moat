# Notion MCP server with OAuth

Give Claude Code access to your Notion workspace through the remote Notion MCP server.

## What this demonstrates

- OAuth credential granting with auto-discovery (`moat grant oauth notion`)
- Remote MCP server configuration with credential injection via the proxy relay
- Automatic token refresh for OAuth credentials

## Setup

Grant OAuth credentials (one-time, opens a browser):

```bash
moat grant oauth notion
```

## Run

```bash
moat claude examples/mcp-notion
```

Claude Code can now search, read, and update pages in your Notion workspace.
