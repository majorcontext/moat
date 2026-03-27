---
title: "Policy Enforcement"
navTitle: "Policy"
description: "Enforce operation-level allow/deny/redact rules on MCP tool calls and API requests."
keywords: ["policy", "keep", "rules", "allow", "deny", "redact", "mcp", "security"]
---

# Policy enforcement

Policy enforcement restricts what an agent can do through MCP tool calls and HTTP requests. Rules are evaluated by [Keep](https://github.com/majorcontext/keep) and applied at the proxy layer before requests reach their destination.

## Quick start with a starter pack

Starter packs are built-in policy sets for common MCP servers. Apply one by name:

```yaml
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    auth:
      grant: mcp-linear
      header: Authorization
    policy: linear-readonly
```

This restricts the Linear MCP server to read-only operations. Write operations like `create_issue` or `delete_issue` are denied.

Available starter packs:

| Name | Description |
|------|-------------|
| `linear-readonly` | Allows read operations on Linear, denies all writes |

## Inline rules

For simple policies, define rules directly in `moat.yaml`:

```yaml
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy:
      allow: [get_issue, list_issues, search_issues]
      deny: [delete_issue, update_issue]
      mode: enforce
```

When `allow` is specified, unlisted operations are denied by default. When only `deny` is specified, unlisted operations are allowed.

The `mode` field controls enforcement:

| Mode | Behavior |
|------|----------|
| `enforce` | Deny blocked operations (default) |
| `audit` | Log policy decisions without blocking |

## File-based rules

For larger policies, store rules in a separate file. The `.keep/` directory in your workspace root is the conventional location:

```yaml
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy: .keep/linear-rules.yaml
```

The file path is relative to the workspace root.

Keep rules files use Keep's native YAML format with `scope`, `mode`, and `rules`:

```yaml
# .keep/linear-rules.yaml
scope: linear
mode: enforce
rules:
  - name: allow-reads
    match:
      operation: "get_*"
    action: allow

  - name: allow-lists
    match:
      operation: "list_*"
    action: allow

  - name: allow-search
    match:
      operation: "search_*"
    action: allow

  - name: default-deny
    match:
      operation: "*"
    action: deny
    message: "Operation not allowed by policy."
```

See the [Keep documentation](https://github.com/majorcontext/keep) for the full rule format, including CEL expressions in `when` clauses.

## Audit mode

Use `mode: audit` to observe what the agent does before enforcing restrictions. Policy decisions are logged to the run's audit trail without blocking any operations:

```yaml
mcp:
  - name: linear
    url: https://mcp.linear.app/mcp
    policy:
      deny: [delete_issue]
      mode: audit
```

Run the agent, then review audit entries:

```bash
$ moat audit <run-id>
```

Once you are confident in the rules, switch to `mode: enforce`.

## Network-level HTTP policy

Apply Keep policy rules to all outbound HTTP traffic (outside of MCP) with `network.keep_policy`:

```yaml
network:
  policy: strict
  rules:
    - "api.example.com"
  keep_policy: .keep/api-rules.yaml
```

This works alongside `network.rules`. The network policy controls which hosts are reachable; `keep_policy` controls what operations are allowed on those hosts.

Inline rules work the same way:

```yaml
network:
  policy: strict
  rules:
    - "api.example.com"
  keep_policy:
    allow: [GET, HEAD]
    deny: [DELETE]
    mode: enforce
```

## LLM gateway

The LLM gateway enforces policy on Anthropic API responses. The proxy buffers each response, evaluates tool_use blocks against Keep rules, and denies responses that violate the policy before they reach the container.

```yaml
claude:
  llm-gateway:
    policy: .keep/llm-rules.yaml
```

The gateway operates at the proxy layer alongside MCP and network policies. It supports both JSON and SSE (streaming) responses, and handles gzip-compressed bodies transparently.

The LLM gateway is mutually exclusive with `claude.base_url` -- both redirect LLM traffic, so only one can be active.

## Writing custom rules

Keep rules files support additional features beyond `allow` and `deny` lists, including pattern matching and redaction. See the [Keep documentation](https://github.com/majorcontext/keep) for the full rule specification.

## Related

- [moat.yaml reference: mcp[].policy](../reference/02-moat-yaml.md#mcppolicy)
- [moat.yaml reference: network.keep_policy](../reference/02-moat-yaml.md#networkkeep_policy)
- [moat.yaml reference: claude.llm-gateway](../reference/02-moat-yaml.md#claudellm-gateway)
- [MCP servers guide](./09-mcp.md)
- [Observability guide](./11-observability.md) for reviewing audit logs
