# Keep Policy Example

Run Claude Code with a Keep LLM gateway that enforces a read-only policy. Claude can read files but cannot edit, write, or run shell commands.

## Prerequisites

```bash
moat grant claude
```

## Try it

The default command asks Claude to edit `moat.yaml`. The gateway blocks it:

```bash
moat run examples/policy
```

The Keep LLM gateway intercepts Claude's `Edit` tool call and denies it:

```
File editing is blocked by policy.
```

Claude sees the denial and reports that it cannot make the change.

Override the command to try a read operation (allowed):

```bash
moat run examples/policy -- claude -p "read moat.yaml and summarize it"
```

## How it works

The `claude.llm-gateway` field configures the Moat proxy to evaluate [Keep](https://github.com/majorcontext/keep) rules against tool calls in Anthropic API responses before they reach the container.

```yaml
claude:
  llm-gateway:
    policy: .keep/read-only.yaml
```

The rules file uses Keep's native YAML format. Keep supports three rule actions: `deny`, `log`, and `redact`. Unmatched operations are implicitly allowed.

```yaml
# .keep/read-only.yaml
scope: llm-gateway
mode: enforce
rules:
  - name: deny-edit
    match:
      operation: "llm.tool_use"
      when: "params.name == 'edit'"
    action: deny
    message: "File editing is blocked by policy."

  - name: deny-write
    match:
      operation: "llm.tool_use"
      when: "params.name == 'write'"
    action: deny
    message: "File writing is blocked by policy."

  - name: deny-bash
    match:
      operation: "llm.tool_use"
      when: "params.name == 'bash'"
    action: deny
    message: "Shell commands are blocked by policy."
```

## Audit mode

Set `mode: audit` in the rules file to log decisions without blocking. Review what the agent attempted:

```bash
moat audit <run-id>
```

Switch back to `mode: enforce` once you are confident in the rules.

## See also

- [MCP policy enforcement](https://majorcontext.com/moat/guides/mcp#policy-enforcement)
- [moat.yaml reference](https://majorcontext.com/moat/reference/moat-yaml)
