# Vision

## Principles

### Narrow Scope

Moat runs one agent in one container against one workspace.

- `moat run` starts a run
- `moat grant` stores credentials
- `moat logs`, `moat trace`, `moat audit` show what happened

Configuration is YAML. Orchestration is shell. Workflow primitives (loops, retries, dependencies) belong in the calling environment.

### Safe Defaults

Agents are untrusted code. Moat's defaults reflect that:

- **Credentials are injected at runtime.** The agent authenticates normally, but tokens never enter the container environment—they're added at the network layer by a TLS-intercepting proxy.
- **Runs execute in containers.** The workspace is mounted, but the host filesystem is otherwise inaccessible. Each run has its own container instance.
- **Network access is auditable.** Every HTTP request through the proxy is logged with method, URL, status, and timing.
- **Destruction requires intent.** `moat stop` halts execution but preserves artifacts. `moat destroy` removes them.

### Composition

Moat exits with the container's exit code. Standard streams work as expected. Combine runs with shell, Make, CI pipelines, or any tool that can invoke a command.

### Progressive Enhancement

Start with a command:

```bash
moat run -- npm test
```

Add credentials when needed:

```bash
moat grant github
moat run --grant github -- npm test
```

Add configuration when complexity grows:

```yaml
# agent.yaml
dependencies:
  - node@20
grants:
  - github
network:
  policy: strict
  allow:
    - "api.github.com"
```

Each layer is optional. A run with no `agent.yaml` uses sensible defaults (ubuntu:22.04, permissive network). Configuration captures what you'd otherwise pass as flags.

## Non-Goals

Moat does not:

- **Orchestrate multiple agents.** No job scheduler, work queue, or multi-agent coordination.
- **Manage state across runs.** Each run is independent. Shared state belongs in external storage.
- **Plan or delegate tasks.** No task decomposition, routing, or agent-to-agent communication.
- **Provide workflow primitives.** No loops, conditionals, retries, or dependencies between runs.

These concerns vary by use case and belong in the orchestration layer—shell scripts, CI systems, agent frameworks, or workflow engines. Moat provides the execution primitive they compose.
