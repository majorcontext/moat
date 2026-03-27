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

The `claude.llm-gateway` field runs a [Keep](https://github.com/majorcontext/keep) sidecar process inside the container. It sits between Claude Code and the Anthropic API, evaluating tool calls against a policy before they execute.

```yaml
claude:
  llm-gateway:
    policy: .keep/read-only.yaml
```

The rules file uses Keep's native YAML format:

```yaml
# .keep/read-only.yaml
scope: llm
mode: enforce
rules:
  - name: allow-read
    match:
      operation: "Read"
    action: allow

  - name: deny-edit
    match:
      operation: "Edit"
    action: deny
    message: "File editing is blocked by policy."

  - name: default-deny
    match:
      operation: "*"
    action: deny
    message: "Operation not in allowlist."
```

## Audit mode

Set `mode: audit` in the rules file to log decisions without blocking. Review what the agent attempted:

```bash
moat audit <run-id>
```

Switch back to `mode: enforce` once you are confident in the rules.

## See also

- [Policy guide](https://majorcontext.com/moat/guides/policy)
- [moat.yaml reference](https://majorcontext.com/moat/reference/moat-yaml)
