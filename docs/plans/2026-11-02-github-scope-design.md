# GitHub workspace scope — design

Status: implemented (moat, keep, gatekeeper branches `claude/moat-github-access-policy-b43vj7`)

## Problem

The `github` grant injects the user's GitHub token, which usually comes from `gh auth token` and can push to every repository the user can. An agent working in one project should not be able to change other repositories, even though the token allows it.

## Goals

- Writes with the github grant succeed only for the project's repositories.
- The repository set is inferred from the workspace, with no manual list required.
- Workspaces with several cloned repositories work.
- An agent cannot widen its own access across runs, by cloning a repository, re-pointing a remote, or editing `moat.yaml`.
- Optional read access to other repositories.

## Non-goals

- A hard confidentiality boundary for reads. GraphQL queries can name any repository, and the policy allows all queries.
- Remote MCP servers using the github grant, and Copilot runs. Both fail fast rather than run unscoped.
- GitHub Enterprise hosts.

## UX

```yaml
grants: [github]
github:
  scope: workspace        # any (default) | workspace
  other_repos: read       # read (default) | none
  extra_repos: [org/repo] # approved like discovered repositories
```

Configuration lives only in `moat.yaml`. There is no global setting and no CLI flag. `moat github {repos,allow,forget,policy}` act on the current project: the nearest ancestor directory containing `moat.yaml`.

| Situation | Behavior |
|-----------|----------|
| First scoped run | Approve every repository found, lock the project, print the list |
| Later run, new repository | Interactive: `[y/N]` prompt per repository. Non-interactive: read-only with a `moat github allow` hint |
| `moat.yaml` drops `github.scope` | Still scoped (sticky lock), with a note; `moat github forget --all` unlocks |
| Blocked push | git prints `remote: Push blocked: this run can only push to …` |

## Decisions

1. **`other_repos` defaults to `read`.** `none` blocks public dependencies fetched from github.com, and reads leak through GraphQL anyway.
2. **GraphQL mutations that identify their target only by a node ID are allowed only if they are on a fixed list of discussion mutations** (comments, reactions, labels, assignees, edits, non-approving reviews). `gh pr comment` and `gh pr edit` keep working. Everything else is denied, failing closed for branch protection, rulesets, deletions, and mutations GitHub adds later. That includes code-changing mutations (`mergePullRequest`, `updateRef`, …), whose deny message names a REST alternative. Approving reviews are denied. This follows from code review: a denylist of code mutations let `deleteBranchProtectionRule` through. A follow-up could resolve node IDs to repositories with a lookup.
3. **The lock is sticky.** `moat.yaml` is writable by the agent, so removing the setting must not lift the scope.
4. **The config is a `github:` block**, not a `network.keep_policy` pack name: discovery and prompts should not be hidden behind a pack.
5. **Copilot, any `ssh:` grant, and github-granted MCP servers fail fast** instead of running without the scope. The SSH check covers every SSH grant because moat's SSH agent proxy never learns the destination host (`SetCurrentHost` is not called), so a key granted for one host signs connections to any host.
6. **Moat moves to gatekeeper main.** The body-scoping change lives there.

## Design

### Discovery (`internal/githubscope/discover.go`)

- Walk the workspace on the host, to depth 4, skipping dependency and build directories (`node_modules`, `vendor`, `.venv`, `target`, `.build`, `.terraform`, `_deps`, …).
- A directory with a `.git` dir, or a `.git` file (`gitdir:`, with `commondir` for worktrees), is a checkout. Symlinked `.git` entries are ignored.
- Parse the config file directly, never running `git`, since hooks or fsmonitor could execute on the host. Read the `url` and `pushurl` of every remote. Ignore `include` and `insteadOf`.
- Accept github.com URLs over HTTPS, git, SSH, and scp-like syntax; lowercase `owner/repo`.

Discovery can only suggest repositories. Missing one, by skipping a directory or stopping at the entry cap, narrows access.

### Approval store (`internal/githubscope/store.go`, `plan.go`)

- `~/.moat/github-repos.json`, keyed by the project's absolute, symlink-resolved path. Cross-process `flock`; atomic write-then-rename.
- `BuildPlan` is the single source of truth, used by the CLI pre-flight (print and prompt) and by `Create` (enforce). It yields Granted / Pending / Missing.
- `CommitRun` records the first run's approvals and `locked_at`, the last explicit `other_repos` (used by sticky runs), and cached repository IDs. `Create` calls it only after the policy has been built and merged, so a rejected run doesn't lock the project.
- `moat github allow` approves without locking. Repositories it approves are granted even when not checked out; discovered ones are granted only while present.
- `moat github` commands use the nearest ancestor with a stored record, else the nearest `moat.yaml`, so a nested `moat.yaml` doesn't split approvals.

### Policy (`internal/githubscope/github-workspace.yaml`)

The policy is a Keep rule file in the proxy's `http` scope. Moat fills its `defs` per run: approved names, path regexes, numeric IDs, node IDs, and `other_repos`. It is merged with any `network.keep_policy`, which must use `mode: enforce`, closed errors, case-insensitive matching, and pinned body rules.

| Rule | Denies |
|------|--------|
| `github-path-hygiene` | `.`, `..`, empty segments, or backslashes in GitHub paths |
| `github-new-repository` | REST fork or generate |
| `github-git-push-start` | `GET …/info/refs?service=git-receive-pack` outside the workspace. Git shows this request's error body; it does not show the body for the pack POST |
| `github-git-write` | Non-read github.com requests outside the workspace. `/{o}/{r}/git-upload-pack` and `/{o}/{r}/info/lfs/objects/batch` (exact shape) count as reads |
| `github-lfs-upload` | LFS batch requests outside the workspace whose body is not `operation: download`. The rule is pinned to the batch operation so only these bodies are read |
| `github-api-write` | Non-GET api.github.com requests outside `/repos/{ws}` and `/repositories/{ws-id}`, except `POST /graphql` and `/markdown` |
| `github-other-repo-read` | With `other_repos: none`: other repositories over git and REST, and code and commit search |
| `github-graphql-*` | Unparseable bodies; `createRepository` and `cloneTemplateRepository`; `input.repositoryId` not in the workspace; `branch.repositoryNameWithOwner` not in the workspace; unattributable code mutations (specific message); any other unattributable mutation not on `gh_unattributed_ok`; approving reviews |

Each nested GraphQL argument access is guarded with `has()`, because Keep turns missing-key errors into a non-match.

Repository identity (canonical name, numeric ID, and node IDs in both legacy and next-gen formats) is fetched with the run's token only for granted repositories that have none cached, then cached in the store.

### Keep changes

- `graphql(body)` CEL function. It parses with gqlparser, reports all operations, expands root fragments, substitutes variables and their defaults, and parses the original-case body.
- `Engine.RequiresBodyFor(scope, call)`: a body rule counts only if its `operation` glob matches the call.
- Fix: `ValidateRuleBytes` now substitutes `defs`; it previously rejected valid files.

### Gatekeeper changes

- `buildHTTPCall` uses `RequiresBodyFor`, so the GraphQL body rule no longer makes `git push` (non-JSON pack bodies) fail closed.
- Calls carry `params.query`, which the push-start rule needs.

### Moat consequences of the gatekeeper jump

These changes come from the move off v0.23.2, not from the feature itself:

- The daemon sets `Branding` to keep moat's wording, `moat-stub-` MCP placeholders, and the `Moat-Policy` scheme. New CAs keep the "Moat CA" subject.
- Gatekeeper now refuses credential injection over plain HTTP. The daemon allows it for the host-gateway name (localhost `base_url`) and loopback MCP URLs. Remote `http://` endpoints now get a 403.
- Go 1.27.3 is required. CI's golangci-lint moves to v2.15.0, built with `install-mode: goinstall`. New gosec rules that fire on existing code are excluded with the same rationale as existing exclusions.
- The `keep-scoped-body` daemon capability is required for scoped runs; an older daemon fails with "run `moat proxy restart`".

## Testing

- Policy tables run keeplib the way gatekeeper calls it, covering both outcomes for each rule: git, REST, GraphQL (variables, defaults, fragments, aliases, `operationName` tricks, case variants), `none` mode, and the empty set.
- An end-to-end test runs the real gatekeeper proxy with TLS interception: a real `git push` to a `git-http-backend` posing as github.com (allowed and blocked, including the message git shows) and GraphQL requests.
- Discovery tests cover nested clones, worktrees, submodules, skipped directories, depth, symlinks, and malformed configs. Plan and store tests cover first run, pending, sticky, `allow`/`forget`/`reset`, and concurrent writers.
- CLI pre-flight and `repos` output, the run gate, conflict checks, and policy merging (file and inline, with parity against Keep's `RuleSet`) are also tested.

## Follow-ups

- Resolve GraphQL node IDs to repositories, for full write confinement.
- Optionally protect workspace repositories from admin operations (delete, transfer, settings, branch protection).
- Copilot support, once Copilot's GitHub-facing endpoints are mapped.
- Release keep v0.7.0 and gatekeeper with these changes, and replace moat's pseudo-versions.
