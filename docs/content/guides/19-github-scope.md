---
title: "Scoping GitHub access to the workspace"
navTitle: "GitHub scope"
description: "Limit what an agent can change on GitHub to the repositories checked out in its workspace, even when the GitHub token can write to more."
keywords: ["moat", "github", "scope", "repositories", "git push", "keep", "policy", "graphql", "gh"]
---

# Scoping GitHub access to the workspace

The `github` grant injects your GitHub token for `github.com` and `api.github.com`. A token from `gh auth token` can usually push to every repository you can. `github.scope: workspace` limits what a run can change to the repositories checked out in its workspace:

```yaml
grants:
  - github

github:
  scope: workspace
```

A push to any other repository fails:

```
$ git push https://github.com/acme/payments.git main
remote: Moat: request blocked by Keep policy.
remote: Host: github.com
remote: Push blocked: this run can only push to majorcontext/keep, majorcontext/moat. To add a repository, the user runs `moat github allow OWNER/REPO` on the host.
fatal: unable to access 'https://github.com/acme/payments.git/': The requested URL returned error: 403
```

The proxy enforces the scope with a [Keep](https://github.com/majorcontext/keep) policy on each request that carries the token, so it applies to `git`, `gh`, and any other client in the container.

## How repositories are found

When a run starts, moat scans the workspace on the host, before the container is created:

- the workspace itself, nested clones (a workspace that holds several repositories), linked git worktrees, and checked-out submodules, up to four directories deep
- every remote's `url` and `pushurl` that points at `github.com`, over HTTPS or SSH

Moat reads `.git/config` files directly and never runs `git` in the workspace, since a modified repository could run hooks on the host. It skips dependency and build directories (`node_modules`, `vendor`, `.venv`, `target`, `.build`, `.terraform`, `_deps`, and similar), where package managers leave checkouts of third-party code.

Skipping a directory can only narrow access. A repository that discovery misses gets the same access as any other repository.

## Approvals

The first run with `github.scope: workspace` approves every repository it finds and records them:

```
GitHub: this run can only push to these repositories (recorded for future runs):
  majorcontext/keep  ./keep (origin)
  majorcontext/moat  ./ (origin)
  Other repositories: read-only
```

On later runs, a repository that was not approved before is **pending**. That covers a clone the agent made, or a remote it re-pointed. On a terminal, moat asks before the run starts:

```
New since the last approved run:
  acme/payments  ./scratch/payments (origin)
Allow this run to push to acme/payments? It was found in ./scratch/payments (origin). [y/N]:
```

Without a terminal (or with `--no-prompt`), pending repositories stay read-only and moat prints the command that approves them.

Approvals are stored in `~/.moat/github-repos.json`, keyed by the project's path. The file is outside every workspace, so an agent cannot add approvals by editing files in its workspace and waiting for the next run.

### The scope stays on

Once a project has run with `github.scope: workspace`, it stays scoped even if `moat.yaml` stops asking for it. An agent can edit `moat.yaml`, so removing the setting is not enough to turn the scope off. Later runs print a note and keep enforcing it. To turn it off, run `moat github forget --all` in the project.

## Managing a project's repositories

`moat github` commands act on the current project: the nearest directory at or above the current one that contains `moat.yaml`.

```
$ moat github
Project: /Users/me/code/moat
Scope:   workspace (since 2026-11-02)
Other repositories: read-only

REPOSITORY         STATUS    FOUND IN
acme/payments      pending   ./scratch/payments (origin)
majorcontext/keep  approved  ./keep (origin)
majorcontext/moat  approved  ./ (origin)

Pending repositories are read-only. Approve one with: moat github allow OWNER/REPO
```

| Command | Effect |
|---------|--------|
| `moat github allow OWNER/REPO...` | Approve repositories. An approved repository is writable whether or not it is checked out in the workspace. |
| `moat github forget OWNER/REPO...` | Revoke repositories. The project stays scoped. |
| `moat github forget --all` | Delete the project's approvals and its lock. |
| `moat github policy` | Print the Keep rule file runs use, with the approved repositories filled in. |

To list a repository in `moat.yaml` instead, use `extra_repos`. It is approved the same way as a discovered repository: on the first run, or at the prompt afterwards.

```yaml
github:
  scope: workspace
  extra_repos:
    - majorcontext/docs
```

## What is enforced

| Request | Approved repositories | Other repositories |
|---------|----------------------|--------------------|
| `git push` (`git-receive-pack`) | allowed | denied |
| `git fetch` / `clone`, LFS download | allowed | allowed with `other_repos: read`; denied with `none` |
| REST `GET` under `/repos/{owner}/{repo}` | allowed | allowed with `read`; denied with `none` |
| REST writes under `/repos/{owner}/{repo}` and `/repositories/{id}` | allowed | denied |
| REST writes outside `/repos` (SSH keys, gists, new repositories, organization settings) | denied | denied |
| Forking or generating a repository | denied | denied |
| GraphQL queries | allowed | allowed |
| GraphQL mutations with `input.repositoryId` or `input.branch.repositoryNameWithOwner` | allowed | denied |
| GraphQL mutations that change code without naming a repository (`mergePullRequest`, `updateRef`, `deleteRef`, `enablePullRequestAutoMerge`, `createCommitOnBranch` by branch ID, ...) | denied | denied |

`gh pr create`, `gh issue create`, `gh pr view`, and `gh api repos/OWNER/REPO/...` work for approved repositories. `gh pr merge` uses the `mergePullRequest` mutation, which identifies the pull request by node ID only, so it is denied. Merge through REST instead:

```bash
gh api -X PUT repos/OWNER/REPO/pulls/NUMBER/merge
```

Moat looks up each approved repository's numeric ID and GraphQL node IDs with your token when a run starts, so it can recognize requests that address a repository by ID. If GitHub is unreachable, it uses IDs cached from earlier runs.

## Other repositories: read or none

```yaml
github:
  scope: workspace
  other_repos: none   # default: read
```

`read` (the default) lets the agent fetch and read other repositories, which public dependencies fetched from `github.com` need. `none` also denies git fetches, REST reads, and code search for repositories outside the workspace.

`none` is not a confidentiality boundary. A GraphQL query can name any repository the token can see, and the policy allows all queries. For a hard read boundary, grant a fine-grained token limited to the repositories the agent needs.

## Configurations that are refused

The scope only holds if every path to GitHub with your credentials passes through the policy. A run with `github.scope: workspace` fails before the container starts if it also has:

| Configuration | Why | Fix |
|---------------|-----|-----|
| the `ssh:github.com` grant | `git push` over SSH does not pass through the HTTPS policy | Remove the grant and use `https://github.com/...` remotes |
| an `mcp:` server with `auth.grant: github` | Remote MCP servers are not covered by the policy | Use the `gh` CLI in the container |
| a Copilot run (`moat copilot`) | Copilot reaches GitHub through Copilot API hosts the policy does not cover | Run without the scope |

A token passed to the container directly, through `env` or `secrets` (`GITHUB_TOKEN`, `GH_TOKEN`), never reaches the proxy, so the scope cannot limit it. Moat warns when it sees one.

## Limits

- **GraphQL mutations by node ID.** Mutations that identify their target only by a pull request, issue, or comment node ID (`addComment`, `updatePullRequest`, `addPullRequestReview`, ...) cannot be traced to a repository without a lookup. They are allowed unless they change code. `gh pr comment` and `gh pr edit` therefore keep working, and a comment on another repository's pull request is not blocked.
- **Workflows in approved repositories.** The agent can push changes to `.github/workflows/` in an approved repository. Those workflows run on GitHub with that repository's secrets.
- **Network policy.** The scope governs requests that carry the github grant's token. Combine it with `network.policy: strict` to limit everything else the container can reach.

## Combining with network.keep_policy

The scope adds its rules to the proxy's `http` Keep scope. A [`network.keep_policy`](../reference/02-moat-yaml.md#networkkeep_policy) is merged into the same rule file, so it must use `mode: enforce`. It must also leave `on_error` and `case_sensitive` at their defaults, and pin every request-body rule to an `operation`. An unpinned body rule would make the proxy read every request body, including `git push` pack data, which is not JSON. Moat reports any of these conflicts when the run starts.

## Upgrading

The scope needs a proxy daemon that reads request bodies only for the requests a body rule's `operation` matches. If the running daemon is older, the run fails with a message to run `moat proxy restart`.
