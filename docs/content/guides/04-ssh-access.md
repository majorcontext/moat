---
title: "SSH access"
description: "Grant agents SSH access to specific hosts without exposing private keys."
keywords: ["moat", "ssh", "git", "github", "gitlab", "private keys"]
---

# SSH access

This guide covers granting agents SSH access to specific hosts. Moat proxies SSH agent requests so agents can authenticate via SSH without your private keys entering the container.

## How it works

When you grant SSH access:

1. Moat starts an SSH agent proxy inside the container
2. The proxy connects to your host's SSH agent (`SSH_AUTH_SOCK`)
3. Key listing and signing requests are forwarded to your real SSH agent
4. Only keys mapped to granted hosts are visible to the container
5. Private keys never enter the container—only signature requests are proxied

## Prerequisites

Your SSH agent must be running with keys loaded:

```bash
# Check if SSH agent is running
echo $SSH_AUTH_SOCK

# If not set, start the agent
eval "$(ssh-agent -s)"

# Add your key
ssh-add ~/.ssh/id_ed25519
```

List loaded keys:

```bash
$ ssh-add -l

256 SHA256:abc123... user@host (ED25519)
```

## Granting SSH access

Grant access to a specific host:

```bash
$ moat grant ssh --host github.com

Using key: user@host (SHA256:abc123...)
Granted SSH access to github.com
```

Grant access to multiple hosts:

```bash
moat grant ssh --host github.com
moat grant ssh --host gitlab.com
moat grant ssh --host bitbucket.org
```

## Using SSH in runs

### Via CLI flag

```bash
moat run --grant ssh:github.com -- git clone git@github.com:org/repo.git
```

### Via agent.yaml

```yaml
grants:
  - ssh:github.com
  - ssh:gitlab.com
```

Then:

```bash
moat run -- git clone git@github.com:org/repo.git
```

## Example: Clone and work with a private repository

1. Ensure SSH agent is running with your key:
   ```bash
   ssh-add -l
   ```

2. Grant SSH access:
   ```bash
   moat grant ssh --host github.com
   ```

3. Clone a private repository:
   ```bash
   $ moat run --grant ssh:github.com -- git clone git@github.com:my-org/private-repo.git

   Cloning into 'private-repo'...
   remote: Enumerating objects: 1234, done.
   remote: Counting objects: 100% (1234/1234), done.
   ...
   ```

4. Work with the repository:
   ```bash
   moat run --grant ssh:github.com -- sh -c "cd private-repo && git pull"
   ```

## Combining SSH and HTTPS credentials

For workflows that use both SSH (for git) and HTTPS (for APIs):

```yaml
grants:
  - github         # HTTPS API access
  - ssh:github.com # SSH git access
```

```bash
moat run -- sh -c "
  git clone git@github.com:org/repo.git
  cd repo
  # Use GitHub API via HTTPS (token injected)
  curl -s https://api.github.com/repos/org/repo/pulls
"
```

## Host-specific key mapping

SSH grants are host-specific. Each grant maps your SSH key to one host:

```bash
moat grant ssh --host github.com
moat grant ssh --host gitlab.com
```

Inside the container, only keys for granted hosts are visible:

```bash
# With only github.com granted:
$ moat run --grant ssh:github.com -- ssh -T git@github.com
Hi user! You've successfully authenticated...

$ moat run --grant ssh:github.com -- ssh -T git@gitlab.com
Permission denied (publickey).
```

## Interactive SSH sessions

For interactive SSH (not just git), use interactive mode:

```bash
moat run -i --grant ssh:myserver.com -- ssh user@myserver.com
```

## Revoking SSH access

Remove SSH access for a host:

```bash
moat revoke ssh:github.com
```

## Troubleshooting

### "SSH_AUTH_SOCK not set"

Your SSH agent is not running. Start it:

```bash
eval "$(ssh-agent -s)"
ssh-add ~/.ssh/id_ed25519
```

Add to your shell profile to start automatically.

### "Permission denied (publickey)"

1. Verify the key is loaded:
   ```bash
   ssh-add -l
   ```

2. Verify the host is granted:
   ```bash
   moat run --grant ssh:github.com -- env | grep SSH
   ```

3. Test SSH from outside Moat:
   ```bash
   ssh -T git@github.com
   ```

### "Could not read from remote repository"

The SSH grant may be missing. Add it:

```bash
moat run --grant ssh:github.com -- git clone git@github.com:org/repo.git
```

Or in `agent.yaml`:

```yaml
grants:
  - ssh:github.com
```

### Key not being used

If you have multiple keys, the SSH agent proxy uses the first key that was loaded. Load your preferred key first:

```bash
ssh-add ~/.ssh/id_ed25519_github
ssh-add ~/.ssh/id_ed25519_gitlab
```

## Security considerations

**What this protects:**

- Private keys never enter the container filesystem
- Keys are only usable for granted hosts
- Signing operations are logged in the audit trail

**What this does not protect:**

- A malicious agent could make any git operation on granted hosts
- The agent has full access to repositories it can clone
- Commits are made with whatever git identity is configured

Configure git identity in your `agent.yaml` if needed:

```yaml
env:
  GIT_AUTHOR_NAME: "My Agent"
  GIT_AUTHOR_EMAIL: "agent@example.com"
  GIT_COMMITTER_NAME: "My Agent"
  GIT_COMMITTER_EMAIL: "agent@example.com"
```

## Related guides

- [Running Claude Code](./01-running-claude-code.md) — Use SSH with Claude Code
- [Credential management](../concepts/02-credentials.md) — How credential injection works
