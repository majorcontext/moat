---
title: "Quick start"
description: "Run your first AI agent with Moat in 5 minutes."
keywords: ["moat", "quick start", "tutorial", "first run"]
---

# Quick start

This tutorial walks through running an agent with credential injection. By the end, you'll understand how Moat executes code in containers, injects credentials, and captures observability data.

**Prerequisites:**
- Moat installed ([Installation](./02-installation.md))
- Docker running, or macOS 15+ with Apple Silicon
- GitHub OAuth App configured with `MOAT_GITHUB_CLIENT_ID` set

## Step 1: Grant GitHub credentials

Store a GitHub credential that Moat can inject into runs:

```bash
$ moat grant github

To authorize, visit: https://github.com/login/device
Enter code: ABCD-1234

Waiting for authorization...
GitHub credential saved successfully
```

Open the URL in your browser, enter the code, and authorize the application. The credential is encrypted and stored in `~/.moat/credentials/`.

This is a one-time setup. The credential persists across runs until you revoke it with `moat revoke github`.

## Step 2: Run a command with credential injection

Run `curl` inside a container with the GitHub credential injected:

```bash
$ moat run --grant github -- curl -s https://api.github.com/user

{
  "login": "your-username",
  "id": 1234567,
  "name": "Your Name"
}
```

What happened:
1. Moat created a container with the default `ubuntu:22.04` image
2. Started a TLS-intercepting proxy
3. Routed container traffic through the proxy
4. The proxy detected a request to `api.github.com` and injected an `Authorization: Bearer <token>` header
5. GitHub returned your user profile

## Step 3: Verify the token was not exposed

The credential was injected at the network layer, not via environment variables:

```bash
$ moat run --grant github -- env | grep -i github

# (no output)
```

```bash
$ moat run --grant github -- env | grep -i token

# (no output)
```

The container process has no access to the raw token. It can only make authenticated requests to GitHub through the proxy.

## Step 4: View the network trace

Moat records all HTTP requests made through the proxy:

```bash
$ moat trace --network

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
```

Add `-v` for headers and response bodies:

```bash
$ moat trace --network -v

[10:23:44.512] GET https://api.github.com/user 200 (89ms)
  Request Headers:
    User-Agent: curl/7.81.0
    Accept: */*
    Authorization: Bearer [REDACTED]
  Response Headers:
    Content-Type: application/json; charset=utf-8
    X-RateLimit-Remaining: 4999
  ...
```

The injected `Authorization` header is redacted in traces.

## Step 5: View container logs

Container stdout and stderr are captured with timestamps:

```bash
$ moat logs

[2025-01-21T10:23:44.512Z] {"login": "your-username", ...}
```

## Step 6: Create an agent.yaml

For repeated runs, create a configuration file. Make a new directory:

```bash
mkdir my-agent
cd my-agent
```

Create `agent.yaml`:

```yaml
name: my-agent

dependencies:
  - node@20

grants:
  - github

env:
  NODE_ENV: development
```

Create a script to test. Save as `check-repos.js`:

```javascript
const https = require('https');

const options = {
  hostname: 'api.github.com',
  path: '/user/repos?per_page=5',
  headers: { 'User-Agent': 'moat-example' }
};

https.get(options, (res) => {
  let data = '';
  res.on('data', chunk => data += chunk);
  res.on('end', () => {
    const repos = JSON.parse(data);
    repos.forEach(repo => console.log(`- ${repo.full_name}`));
  });
});
```

Run the agent:

```bash
$ moat run -- node check-repos.js

- your-username/repo-1
- your-username/repo-2
- your-username/repo-3
- your-username/repo-4
- your-username/repo-5
```

Moat:
1. Read `agent.yaml` and determined the base image (`node:20` from `node@20`)
2. Injected the GitHub credential (from `grants: [github]`)
3. Set the environment variable `NODE_ENV=development`
4. Ran `node check-repos.js`

## Step 7: List and clean up runs

List all runs:

```bash
$ moat list

NAME       RUN ID              STATE    SERVICES
my-agent   run_a1b2c3d4e5f6   stopped
my-agent   run_f6e5d4c3b2a1   stopped
(none)     run_1a2b3c4d5e6f   stopped
```

View system status including disk usage:

```bash
$ moat status

Runs:
  Active: 0
  Stopped: 3

Images:
  node:20 (245 MB)
  ubuntu:22.04 (78 MB)

Disk Usage:
  Runs: 12 MB
  Images: 323 MB
```

Clean up stopped runs:

```bash
$ moat clean

This will remove:
  - 3 stopped runs
  - 0 unused images

Proceed? [y/N]: y

Removed 3 runs
```

## Next steps

- [Sandboxing](../concepts/01-sandboxing.md) — How container isolation works
- [Credential management](../concepts/02-credentials.md) — Credential storage and injection
- [Running Claude Code](../guides/01-running-claude-code.md) — Use Moat with Claude Code
