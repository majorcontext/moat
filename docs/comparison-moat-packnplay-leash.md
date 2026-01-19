# AI Agent Sandboxing Tools: Moat vs Pack'n'Play vs Leash

A neutral comparison of three tools for running AI coding agents in isolated containers.

## Executive Summary

| Aspect | Moat | Pack'n'Play | Leash |
|--------|------|-------------|-------|
| **Primary Focus** | Credential security + observability | Developer convenience | Policy enforcement + monitoring |
| **Credential Model** | Network-layer injection (agent never sees tokens) | Mount host credentials into container | Forward environment variables |
| **Policy Enforcement** | Allow-list network policy | None (isolation only) | Cedar policy language |
| **Monitoring** | Network requests + audit logs | None | Syscall-level + MCP tool calls |
| **Configuration** | Declarative `agent.yaml` | `devcontainer.json` + CLI flags | `config.toml` + Cedar policies |
| **Target User** | Security-conscious teams | Developers wanting quick sandboxing | Enterprises needing governance |

## Philosophical Differences

### Moat: "Run-Centric Security"

Moat treats each execution as a sealed unit called a "run"—code, dependencies, credentials, and observability bundled together. The core philosophy is **credential isolation**: tokens are never exposed to the agent, even in environment variables. Instead, a TLS-intercepting proxy injects authentication headers at the network layer.

Key beliefs:
- Agents shouldn't need raw credentials to use APIs
- Every action should be auditable with cryptographic proof
- Infrastructure complexity should be invisible to users

### Pack'n'Play: "Frictionless Sandboxing"

Pack'n'Play prioritizes developer experience and compatibility. It aims to get agents running in containers with minimal setup, supporting devcontainer specifications and preserving host paths exactly. The philosophy is **pragmatic isolation**: run agents in containers to limit blast radius, but don't fundamentally change how credentials work.

Key beliefs:
- Developers shouldn't need Docker expertise
- Existing credential workflows (SSH keys, AWS SSO, gh CLI) should just work
- Containers provide sufficient isolation for most use cases

### Leash: "Observable Governance"

Leash treats agent execution as a governance problem. It captures every filesystem access and network connection, enabling real-time policy enforcement using Cedar. The philosophy is **controlled autonomy**: let agents operate, but with fine-grained rules about what they can do and complete visibility into what they did.

Key beliefs:
- Agents need guardrails, not just isolation
- Policies should be declarative and auditable
- Complete telemetry is essential for trust

## Feature Comparison

### Container Isolation

| Feature | Moat | Pack'n'Play | Leash |
|---------|------|-------------|-------|
| Docker support | Yes | Yes | Yes |
| Podman/OrbStack | No | No | Yes |
| Apple containers (macOS) | Yes (auto-detected) | No | No |
| Devcontainer support | No | Yes (100% compatible) | Partial |
| Custom images | Via dependencies | Full devcontainer spec | Yes (Dockerfile.coder) |
| Path preservation | Yes (same paths in container) | Yes | Yes |

### Credential Management

| Feature | Moat | Pack'n'Play | Leash |
|---------|------|-------------|-------|
| GitHub OAuth flow | Device flow built-in | Mount gh CLI credentials | N/A |
| Token exposure to agent | Never (network injection) | Read-only mount | Environment variables |
| SSH keys | N/A | Mount support | Mount support |
| AWS credentials | SSM secrets backend | SSO, credential_process, static | Forward env vars |
| 1Password integration | Yes (op:// URIs) | No | No |
| Per-run scoping | Yes | No | No |

### Network Control & Monitoring

| Feature | Moat | Pack'n'Play | Leash |
|---------|------|-------------|-------|
| Network policy | Allow-list (strict mode) | None | Cedar policies |
| Request logging | All HTTP/HTTPS through proxy | None | All connections |
| Syscall monitoring | No | No | Yes |
| MCP tool call tracking | No | No | Yes |
| TLS interception | Yes (for credential injection) | No | No |

### Observability & Auditing

| Feature | Moat | Pack'n'Play | Leash |
|---------|------|-------------|-------|
| Structured logs | Yes (JSONL) | No | Yes |
| Network request traces | Yes | No | Yes |
| Tamper-proof audit log | Yes (hash chain + Merkle tree) | No | No |
| Cryptographic attestations | Yes (Ed25519) | No | No |
| Web UI | No | No | Yes (localhost:18080) |
| Exportable proof bundles | Yes | No | No |

### Developer Experience

| Feature | Moat | Pack'n'Play | Leash |
|---------|------|-------------|-------|
| First-run setup | GitHub OAuth app config | Interactive credential selection | Credential mount prompts |
| Config format | agent.yaml | config.json + devcontainer.json | config.toml + Cedar |
| Port mapping | Hostname routing | Docker -p syntax | Docker -v/-p syntax |
| Multiple concurrent agents | Yes (isolated hostnames) | Via worktrees | Yes |
| Pre-installed AI agents | No | Yes (7 agents in default image) | Yes (5 agents in default image) |

## Use Case Analysis

### When to Choose Moat

**Best for:**
- Teams handling sensitive credentials (API keys, tokens)
- Regulated environments requiring audit trails
- Scenarios where agents must not see raw authentication data
- Users wanting cryptographic proof of what agents did

**Example scenarios:**
- Running agents against production APIs where token leakage is unacceptable
- Compliance requirements for audit logging
- Multi-tenant environments where runs must be provably isolated
- Security-conscious organizations

**Tradeoffs:**
- Requires GitHub OAuth app setup for credential grants
- No syscall-level monitoring (network-focused)
- Less mature devcontainer ecosystem support

### When to Choose Pack'n'Play

**Best for:**
- Individual developers wanting quick sandboxing
- Teams already using devcontainers
- Scenarios where existing credential workflows must be preserved
- Users who need multiple AI agents pre-installed

**Example scenarios:**
- Quickly sandboxing Claude Code without changing auth setup
- Projects with existing devcontainer.json configurations
- AWS SSO users who need credential_process support
- Developers testing multiple AI agents on the same codebase

**Tradeoffs:**
- No introspection or access control (stated explicitly in docs)
- Credentials mounted into container (agent can see them)
- No audit logging or network visibility

### When to Choose Leash

**Best for:**
- Enterprises requiring fine-grained policy control
- Teams needing complete visibility into agent behavior
- Organizations wanting to govern MCP tool usage
- Scenarios requiring real-time intervention capabilities

**Example scenarios:**
- Corporate environments with strict data access policies
- Auditing which files and network resources agents access
- Controlling which MCP tools agents can invoke
- Real-time monitoring via web UI

**Tradeoffs:**
- More complex policy configuration (Cedar learning curve)
- Credentials passed as environment variables (agent can see them)
- No cryptographic audit proofs
- No native macOS container support

## Security Model Comparison

### Attack Surface: Credential Theft

| Scenario | Moat | Pack'n'Play | Leash |
|----------|------|-------------|-------|
| Agent logs environment | Safe (no creds in env) | Risk (mounted creds visible) | Risk (env vars visible) |
| Agent reads credential files | Safe (no files mounted) | Risk (read-only but visible) | Risk (if mounted) |
| Agent intercepts network | Safe (TLS terminated at proxy) | Risk (full network access) | Risk (full network access) |
| Malicious exfiltration | Blocked by network policy | Not blocked | Blocked by Cedar policy |

### Attack Surface: Unauthorized Actions

| Scenario | Moat | Pack'n'Play | Leash |
|----------|------|-------------|-------|
| Unexpected API calls | Logged, can be blocked | Not detected | Logged, can be blocked |
| File system access | Container-limited | Container-limited | Cedar policy controlled |
| MCP tool abuse | Not monitored | Not monitored | Logged and policy-controlled |

## Integration & Ecosystem

### Moat
- Works with any command that uses HTTP/HTTPS
- Secret backends: 1Password, AWS SSM
- Container runtimes: Docker, Apple containers
- No AI-agent-specific integrations

### Pack'n'Play
- First-class support: Claude Code, OpenCode AI, Codex, Gemini, Copilot, Qwen, Amp
- Full devcontainer.json compatibility
- AWS credential_process support (granted.dev, aws-vault)
- macOS Keychain integration for gh CLI

### Leash
- First-class support: Claude, Codex, Gemini, Qwen, OpenCode
- MCP protocol integration for tool monitoring
- Cedar policy ecosystem
- Web UI for real-time monitoring

## Maturity & Community

| Aspect | Moat | Pack'n'Play | Leash |
|--------|------|-------------|-------|
| Primary language | Go | Go | Go, TypeScript, Swift |
| License | MIT | (Check repo) | Apache-2.0 |
| Backing | Independent | Independent | StrongDM (enterprise) |
| Documentation | README + code comments | Detailed README | Specialized guides |

## Combining Tools

These tools address different layers of the problem and could theoretically be combined:

- **Pack'n'Play + Leash**: Pack'n'Play's credential mounting with Leash's policy enforcement
- **Moat for production, Pack'n'Play for development**: Strict credential isolation for production API access, convenient mounting for local development

However, currently no integrations exist between them.

## Recommendations by Persona

### Security Engineer
**Recommendation: Moat**
- Network-layer credential injection prevents entire classes of attacks
- Cryptographic audit logs provide compliance evidence
- Allow-list network policies limit blast radius

### Solo Developer
**Recommendation: Pack'n'Play**
- Fastest path to sandboxed agents
- Works with existing credential setup
- Pre-installed agents reduce friction

### Enterprise Platform Team
**Recommendation: Leash**
- Cedar policies enable fine-grained governance
- Complete telemetry supports audit requirements
- Web UI enables real-time oversight
- StrongDM backing suggests enterprise support

### Compliance-Heavy Environment
**Recommendation: Moat**
- Tamper-proof audit logs with Merkle tree verification
- Ed25519 attestations for cryptographic proof
- Exportable proof bundles for offline verification

## Conclusion

All three tools recognize that running AI agents requires isolation, but they prioritize different aspects:

- **Moat** prioritizes credential security and auditability, ensuring agents can use APIs without ever seeing tokens
- **Pack'n'Play** prioritizes developer convenience, making container sandboxing invisible while preserving existing workflows
- **Leash** prioritizes policy enforcement and monitoring, giving organizations control over agent behavior

The "right" choice depends on your threat model, compliance requirements, and developer experience priorities. For most security-conscious production use cases, Moat's credential isolation model offers the strongest guarantees. For quick local development, Pack'n'Play's frictionless approach wins. For enterprise governance, Leash's policy framework provides the most flexibility.
