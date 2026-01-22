# GitHub Credential Grant Example

This example demonstrates using GitHub credentials with Moat. The agent receives authenticated API access via header injection—your token never enters the container environment.

## Quick Start

### Option 1: Using gh CLI (easiest)

If you have the [GitHub CLI](https://cli.github.com/) installed and authenticated:

```bash
# 1. Grant access (uses your existing gh authentication)
moat grant github

# 2. Run the example
moat run ./examples/grant-github
```

### Option 2: Using Environment Variable

```bash
# 1. Set your token
export GITHUB_TOKEN="ghp_xxxxxxxxxxxx"

# 2. Grant access
moat grant github

# 3. Run the example
moat run ./examples/grant-github
```

### Option 3: Interactive Prompt

```bash
# 1. Grant access (will prompt for token)
moat grant github

# 2. Enter your Personal Access Token when prompted

# 3. Run the example
moat run ./examples/grant-github
```

## Expected Output

```json
{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name",
  "email": "you@example.com"
}
```

## Creating a Personal Access Token

If you don't have the gh CLI, create a Personal Access Token:

1. Visit [github.com/settings/tokens](https://github.com/settings/tokens)
2. Click **Generate new token** → **Fine-grained token** (recommended)
3. Set an expiration date
4. Select which repositories to grant access to
5. Under **Repository permissions**, grant **Contents** read/write access
6. Click **Generate token** and copy it

Fine-grained tokens are recommended because they:
- Can be scoped to specific repositories
- Have granular permissions
- Can be required by organization policies

## How It Works

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Agent      │     │     Moat      │     │  GitHub API   │
│  (container)  │     │    (proxy)    │     │               │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │                     │
        │ curl api.github.com │                     │
        │ (no auth header)    │                     │
        │────────────────────>│                     │
        │                     │ GET /user           │
        │                     │ Authorization:      │
        │                     │   Bearer <token>    │
        │                     │────────────────────>│
        │                     │<────────────────────│
        │ {user JSON}         │                     │
        │<────────────────────│                     │
```

1. **Grant phase**: `moat grant github` stores your token encrypted locally
2. **Run phase**: Moat starts a TLS-intercepting proxy
3. **Request interception**: When the agent makes a request to `api.github.com` or `github.com`, the proxy injects the `Authorization: Bearer <token>` header
4. **Response**: The agent receives the response, never having seen the token

## Verifying Isolation

The real token is never exposed to the container—only a stub value is set for gh CLI compatibility:

```bash
# Check environment - only stub token visible (not your real token)
moat run --grant github -- env | grep -i token
# GH_TOKEN=moat-proxy-injected

# But API calls work - the proxy injects your real token at the network layer
moat run --grant github -- curl -s https://api.github.com/user | jq .login
# "your-username"
```

## Common Use Cases

### Using the GitHub CLI (gh)

The `gh` CLI works inside the container because moat injects authentication at the network layer:

```bash
# List your top 3 pending pull requests
moat run --grant github -- gh pr list --author @me --state open --limit 3

# View PR details
moat run --grant github -- gh pr view 123

# Check workflow runs
moat run --grant github -- gh run list --limit 5

# Create a release
moat run --grant github -- gh release create v1.0.0 --notes "Initial release"
```

### Clone a Private Repository

```bash
moat run --grant github -- git clone https://github.com/org/private-repo.git
```

### Using curl with the API

```bash
# List your repositories
moat run --grant github -- curl -s https://api.github.com/user/repos | jq '.[].full_name'

# Create an issue
moat run --grant github -- curl -s -X POST \
  https://api.github.com/repos/owner/repo/issues \
  -d '{"title":"Bug report","body":"Description here"}'

# Check rate limits
moat run --grant github -- curl -s https://api.github.com/rate_limit | jq .rate
```

## Revoking Access

To remove the stored credential:

```bash
moat revoke github
```

This deletes the encrypted token from `~/.moat/credentials/github.enc`. The original token (in gh CLI or your PAT) remains valid—revoke it separately if needed.

## Troubleshooting

### "invalid token (401 Unauthorized)"

Your token may have expired or been revoked. Generate a new one:

```bash
# If using gh CLI
gh auth refresh

# Then re-grant
moat grant github
```

### "no token provided"

When prompted interactively, make sure to paste your token. The input is hidden for security.

### Token works in curl but not in moat

Ensure the grant is active:

```bash
# Check stored credentials
ls ~/.moat/credentials/
# Should show: github.enc

# Re-grant if needed
moat grant github
```
