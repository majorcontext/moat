# Sandbox-local MCP server

Run an MCP server as a child process inside the container.

## What this demonstrates

- The `claude.mcp` section in `moat.yaml` configures a stdio-based MCP server
- The server runs inside the sandbox alongside Claude Code
- No network access or credentials are needed for the MCP server itself

## Run

```bash
moat claude examples/mcp-local
```

## Verify config generation

To check that the `.claude.json` is generated correctly without needing a grant:

```bash
moat run --no-sandbox examples/mcp-local -- cat /moat/claude-init/.claude.json
```

The output should include an `mcpServers` section with the filesystem server configured as `type: "stdio"`.
