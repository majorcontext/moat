# Langfuse MCP server

Give Claude Code access to Langfuse's MCP server for LLM observability, tracing, and evaluation.

## What this demonstrates

- API-key credential granting with Basic auth (`moat grant mcp langfuse`)
- Remote MCP server shorthand — `langfuse-us` resolves URL and auth from the built-in catalog
- Per-region server selection

## Regions

Pick the entry that matches your Langfuse project:

| Name | Host |
|------|------|
| `langfuse-eu` | cloud.langfuse.com |
| `langfuse-us` | us.cloud.langfuse.com |
| `langfuse-jp` | jp.cloud.langfuse.com |
| `langfuse-hipaa` | hipaa.cloud.langfuse.com |

Edit `moat.yaml` to replace `langfuse-us` with the correct region before running.

## Setup

### 1. Build the credential

Langfuse uses HTTP Basic auth. The credential is `Basic <base64>` where the base64 encodes
`public-key:secret-key`:

```bash
echo -n "pk-lf-your-public-key:sk-lf-your-secret-key" | base64
```

Prefix the result with `Basic ` (note the trailing space before the base64 string).

### 2. Grant the credential

```bash
moat grant mcp langfuse
Credential: Basic <paste the value from step 1>
```

The credential is stored encrypted under grant name `mcp-langfuse` and injected by the
proxy for all `langfuse-*` servers (they share the same grant).

## Run

```bash
moat claude examples/mcp-langfuse
```

## Self-hosted Langfuse

For a self-hosted instance, use the full map form instead of the shorthand name:

```yaml
mcp:
  - name: langfuse
    url: https://langfuse.internal.acme.com/api/public/mcp
    auth:
      grant: mcp-langfuse
      header: Authorization
```
