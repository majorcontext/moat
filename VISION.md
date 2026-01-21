# Vision and Design Philosophy

## Positioning

**Moat is the execution substrate. Orchestration lives above it.**

Moat provides a single, well-defined primitive: the isolated run. Higher-level concerns—parallel execution, multi-agent coordination, task planning—are intentionally left to other tools. This creates a clear separation of responsibilities and enables composition.

## Core Purpose

Moat is a **single-run execution environment** for agents or processes, designed primarily for **local execution**.

The typical workflow:
- You're exploring a new codebase or starting a project
- You want to run an agent with proper isolation and credential security
- Dependencies are packaged automatically—no manual Docker configuration
- You run `moat claude ./workspace` and stay attached, watching output in real-time
- Full observability is captured: logs, network requests, audit trail

**One command. One agent. One workspace.**

- Each run is isolated, auditable, and reproducible
- Credentials are injected securely without exposure to the agent
- By default, you're attached and monitoring—detach is optional for background work

This focus on the single-run primitive keeps moat simple, predictable, and composable.

## Execution Model

### Runs are First-Class

Each invocation of `moat run` or `moat claude` creates a **run**—an isolated execution environment with:

- A unique run ID
- Mounted workspace (your code)
- Injected credentials (if granted)
- Captured logs, network requests, and audit trail
- Independent lifecycle (running, stopped, destroyed)

Runs are independent and self-contained. They do not share state, communicate with each other, or depend on external coordination.

### Default Behavior: Attached Monitoring

By default, `moat run` and `moat claude` **attach** to the container—you see output in real-time and can interact if needed.

```bash
moat claude ./workspace  # Attached by default, you see everything
```

This is the primary workflow for local development. You stay connected, monitor progress, and see exactly what's happening.

### Optional: Detach for Background Execution

Use `-d` to start detached and let the run continue in the background:

```bash
moat claude -d ./workspace  # Runs in background
moat attach <run-id>         # Reattach later to see output
```

### Lifecycle Operations

Runs support these operations:

- **Attach** (default): Stay connected to see output in real-time
- **Detach**: Disconnect without stopping (run continues in background)
- **Stop**: Halt execution (container stopped, artifacts preserved)
- **Destroy**: Remove the run and its artifacts
- **Replay**: Re-run with the same configuration (future capability)

Critically: **Detach ≠ Stop**. Detaching leaves the run active. This enables background execution and reattachment when needed.

## Explicit Non-Goals

### Moat Does Not Orchestrate

Moat does not:

- **Coordinate multiple agents**: No built-in multi-agent workflows
- **Manage parallel runs**: No job scheduler or work queue
- **Plan or delegate tasks**: No task decomposition or routing
- **Share state across runs**: Each run is isolated

**Rationale**: These are higher-level concerns that vary by use case. A shell script needs different orchestration than a Kubernetes operator. By staying focused on the single-run primitive, moat remains flexible enough to support any orchestration model.

### Moat Does Not Provide Workflow Primitives

No loops, conditionals, retries, or dependencies between runs. These belong in the orchestration layer.

**Example of what moat does not do:**

```yaml
# This is NOT moat's job
workflow:
  - run: agent-1
    depends_on: []
  - run: agent-2
    depends_on: [agent-1]
    when: agent-1.status == "success"
```

Instead, orchestrate with shell, Make, or a proper workflow engine:

```bash
# Shell orchestration
moat run ./agent-1 && moat run ./agent-2
```

```makefile
# Make orchestration
agent-2: agent-1
	moat run ./agent-2
```

## Composition Philosophy

Moat is designed to be **composed**, not extended.

### With Shell Scripts

```bash
# Run multiple agents in parallel
moat run ./agent-1 &
moat run ./agent-2 &
wait

# Fan-out execution across workspaces
for workspace in projects/*; do
  moat claude "$workspace" -p "run tests" &
done
wait

# Retry on failure
until moat run ./flaky-agent; do
  echo "Retrying..."
  sleep 5
done
```

### With External Orchestrators

- **GitHub Actions**: Each workflow step runs `moat run` or `moat claude`
- **Jenkins/CircleCI**: Build pipeline stages invoke moat
- **Kubernetes CronJobs**: Scheduled jobs execute moat commands
- **Temporal/Airflow**: Workflow tasks shell out to moat

### With Agent Frameworks

Agent frameworks can use moat as their execution backend:

```python
# Hypothetical agent framework
framework.run_agent(
    agent_config="./my-agent",
    executor=lambda: subprocess.run(["moat", "run", "./my-agent"])
)
```

The framework handles planning, delegation, and state. Moat handles isolation, credentials, and observability.

## Design Principles

### Small Surface Area

**One command, one run, clear semantics.**

Moat provides a minimal set of primitives:
- Run an agent (`moat run`, `moat claude`)
- Manage credentials (`moat grant`, `moat revoke`)
- Observe runs (`moat logs`, `moat trace`, `moat audit`)
- Control lifecycle (`moat attach`, `moat stop`, `moat destroy`)

No plugins, no extensions, no templating language. The scope is intentionally constrained.

**Why**: A small surface area is easier to understand, test, and maintain. It reduces the chance of surprising interactions or edge cases.

### Predictable Behavior

**Runs are independent and self-contained.**

Each run has:
- Isolated filesystem (container)
- No shared state with other runs
- Deterministic configuration (from `agent.yaml` or flags)
- Reproducible environment (same image, same mounts, same credentials)

**Why**: Predictability builds trust. You should be able to reason about what a run will do without consulting documentation or inspecting global state.

### Unix Composability

**Compose moat with existing tools.**

Moat follows Unix philosophy:
- Do one thing well (isolated runs)
- Plain text output (JSON logs, parseable traces)
- Zero-configuration defaults
- Composable via shell pipelines and scripts

**Why**: Reinventing orchestration, scripting, or workflow engines adds complexity and limits adoption. Moat integrates into existing toolchains instead of replacing them.

### Clear Trust Boundaries

**Explicit about what has access to credentials.**

Moat's security model:
- Credentials stored encrypted on host
- Injected at network layer via TLS-intercepting proxy
- Never exposed as environment variables
- Scoped per-run (binding discarded when run ends)
- Full audit trail of usage

**Why**: Agents are untrusted. Credentials must never enter the container environment where they could be logged, exfiltrated, or misused.

## CLI Contract

### Canonical Usage

```bash
moat claude ./path/to/workspace
moat run ./path/to/workspace
```

One command, one workspace, one run.

### Re-Running and Fan-Out

Re-running or executing across multiple workspaces is done **externally**:

```bash
# Re-run the same workspace
moat run ./workspace  # First run
moat run ./workspace  # Second run (new run ID)

# Fan-out across workspaces
for ws in workspaces/*; do
  moat run "$ws"
done
```

**Why**: Shell scripting is universal, well-understood, and powerful. Moat doesn't need to reimplement `for` loops.

### Flags vs. Configuration

- **Common options**: Available as CLI flags (`--grant`, `--name`, `-d`)
- **Complex configuration**: Use `agent.yaml` for dependencies, mounts, network policy
- **Precedence**: CLI flags override `agent.yaml`

**Why**: Flags are convenient for ad-hoc runs. `agent.yaml` is better for repeatable, versioned configurations.

## Examples: What Moat Enables

### Exploring a New Codebase

You clone a repository and want to understand it:

```bash
git clone https://github.com/example/new-project
cd new-project

# Run Claude Code with GitHub access, stay attached, see everything
moat claude --grant github
```

Moat automatically:
- Detects dependencies (Node, Python, Go, etc.)
- Pulls the appropriate base image
- Injects your GitHub credentials securely
- Captures all API calls for audit

You see Claude's work in real-time. No Docker configuration needed.

### Packaging Dependencies Easily

You have a Python project with complex dependencies:

```bash
# Create agent.yaml
cat > agent.yaml <<EOF
dependencies:
  - python@3.11
grants:
  - github
EOF

# Run with automatic dependency packaging
moat claude
```

Moat handles the container setup. You focus on the task.

### Isolated Development Environments

```bash
# Work on authentication feature
moat claude ./app --name auth-work

# Separately, work on checkout flow (same repo, different container)
moat claude ./app --name checkout-work
```

No conflicts, no shared state. Each run is independent.

### Credential Isolation for Security Testing

```bash
# Grant limited GitHub access
moat grant github:repo

# Run an untrusted agent with scoped credentials
moat run ./untrusted-agent --grant github

# After completion, audit exactly what it accessed
moat audit
moat trace --network
```

The agent gets scoped access. Full observability shows exactly what it did.

### Parallel Testing (Optional Advanced Use)

For users who need it, moat composes with shell for parallel execution:

```bash
# Fan out across branches (detached for parallel execution)
for branch in feature-1 feature-2 feature-3; do
  git worktree add /tmp/$branch $branch
  moat run /tmp/$branch --name "test-$branch" -d -- npm test
done

moat list  # See all tests running
```

This is advanced usage—most users stay attached for interactive, local work.

## Comparison to Other Tools

| Tool | Scope | Orchestration | Moat's Position |
|------|-------|---------------|-----------------|
| **Docker** | Container runtime | None | Moat uses Docker (or Apple containers) as its runtime |
| **Docker Compose** | Multi-container apps | Declarative service coordination | Moat runs one container per invocation; compose with shell |
| **Kubernetes** | Container orchestration | Cluster-scale orchestration | Moat is for local, single-run execution |
| **Agent frameworks** | Agent logic | Task planning, delegation | Moat provides the execution substrate |
| **CI/CD systems** | Build/test automation | Workflow pipelines | Moat runs as a step in CI/CD pipelines |

Moat occupies the layer **between** container runtimes and orchestration systems. It makes running a single agent simple, secure, and observable. Higher-level tools compose moat to build workflows.

## Future Direction

Moat's scope is intentionally constrained. Future development will focus on:

- **Replay**: Re-run a previous run with the same configuration
- **Snapshots**: Capture workspace state for reproducibility
- **Network simulation**: Test agents under different network conditions
- **Enhanced audit**: Richer cryptographic proofs and verification

We will **not** add:
- Multi-agent orchestration
- Workflow engines
- Task planning
- Shared state management

These belong in the orchestration layer, not the execution substrate.

## Summary

Moat is a **single-run execution environment** designed for **local development**:

**Primary use case:**
- Exploring new codebases
- Easy dependency packaging
- Running agents with full observability
- Attached by default—you see output in real-time

**Clear scope:**
- ✅ Single-run isolation, auditability, reproducibility, safe credentials
- ✅ Composable with shell, orchestrators, and frameworks
- ❌ No orchestration, coordination, or planning

**One command. One agent. One workspace.**

Detach to background if needed. Compose with shell for parallel execution. The rest is composition.
