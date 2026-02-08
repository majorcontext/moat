---
title: "Secrets management"
navTitle: "Secrets"
description: "Pull secrets from 1Password and AWS SSM into container environment variables."
keywords: ["moat", "secrets", "1password", "aws ssm", "environment variables"]
---

# Secrets management

This guide covers pulling secrets from external backends into container environment variables. Moat supports 1Password and AWS Systems Manager Parameter Store (SSM).

## Secrets vs. credentials

Moat distinguishes between **secrets** and **credentials**:

| | Secrets | Credentials |
|---|---------|-------------|
| **Delivery** | Environment variables | Network-layer injection* |
| **Visibility** | Visible to all processes in container | Not visible in environment |
| **Use case** | Signing keys, database URLs, services without grant support | GitHub, Anthropic, OpenAI, AWS, SSH |
| **Configuration** | `secrets:` in agent.yaml | `grants:` in agent.yaml |

*AWS uses `credential_process` instead of network-layer injection, making temporary credentials accessible in the container. See [AWS credentials](../concepts/02-credentials.md#aws) for details.

Use credentials (grants) when available. Use secrets for services that don't have dedicated grant support.

## 1Password

### Prerequisites

1. Install the 1Password CLI:
   ```bash
   brew install 1password-cli
   ```

2. Sign in:
   ```bash
   op signin
   ```

### Configuration

Reference 1Password items using the `op://` URL format:

```yaml
secrets:
  JWT_SIGNING_KEY: op://Dev/Auth/jwt-signing-key
  DATABASE_URL: op://Production/Database/connection-string
  STRIPE_WEBHOOK_SECRET: op://Dev/Stripe/webhook-secret
```

Format: `op://<vault>/<item>/<field>`

### How it works

When the run starts:

1. Moat parses the `secrets` field
2. For each `op://` reference, Moat calls `op read <reference>`
3. The resolved value is set as an environment variable in the container

### Finding your item reference

Use the 1Password CLI to find item details:

```bash
# List vaults
op vault list

# List items in a vault
op item list --vault Dev

# Get item details
op item get "OpenAI" --vault Dev

# Copy the reference path from the output
```

Or use the 1Password desktop app: right-click a field and select "Copy Secret Reference".

### Example

```yaml
name: my-agent

dependencies:
  - python@3.11

secrets:
  JWT_SIGNING_KEY: op://Dev/Auth/jwt-signing-key

command: ["python", "server.py"]
```

```python
# server.py
import os
import jwt

signing_key = os.environ["JWT_SIGNING_KEY"]
# Use the signing key to create/verify JWTs...
token = jwt.encode({"user_id": 123}, signing_key, algorithm="HS256")
```

```bash
$ moat run

# JWT_SIGNING_KEY is available in the container
```

## AWS SSM Parameter Store

### Prerequisites

1. Install the AWS CLI:
   ```bash
   brew install awscli
   ```

2. Configure credentials:
   ```bash
   aws configure
   ```

   Or use environment variables:
   ```bash
   export AWS_ACCESS_KEY_ID="..."
   export AWS_SECRET_ACCESS_KEY="..."
   export AWS_REGION="us-east-1"
   ```

### Configuration

Reference SSM parameters using the `ssm://` URL format:

```yaml
secrets:
  DATABASE_URL: ssm:///production/database/url
  API_KEY: ssm:///production/api-key
```

Format: `ssm:///<parameter-path>` or `ssm://<region>/<parameter-path>`

### With explicit region

Specify the AWS region in the URL:

```yaml
secrets:
  DATABASE_URL: ssm://us-west-2/production/database/url
```

Without a region, Moat uses the default from your AWS configuration.

### How it works

When the run starts:

1. Moat parses the `secrets` field
2. For each `ssm://` reference, Moat calls `aws ssm get-parameter --name <path> --with-decryption`
3. The resolved value is set as an environment variable in the container

### SecureString parameters

SSM supports encrypted parameters (SecureString). Moat automatically requests decryption with `--with-decryption`. Ensure your AWS credentials have permission to decrypt the parameter.

Required IAM permissions:

```json
{
  "Effect": "Allow",
  "Action": [
    "ssm:GetParameter"
  ],
  "Resource": "arn:aws:ssm:*:*:parameter/production/*"
}
```

For encrypted parameters, also add:

```json
{
  "Effect": "Allow",
  "Action": [
    "kms:Decrypt"
  ],
  "Resource": "arn:aws:kms:*:*:key/<key-id>"
}
```

### Example

```yaml
name: my-agent

dependencies:
  - node@20

secrets:
  DATABASE_URL: ssm:///production/database/url
  REDIS_URL: ssm:///production/redis/url

command: ["node", "server.js"]
```

```bash
$ moat run

# DATABASE_URL and REDIS_URL are available in the container
```

## Combining multiple backends

Use secrets from different backends in the same configuration:

```yaml
secrets:
  # From 1Password
  JWT_SIGNING_KEY: op://Dev/Auth/jwt-signing-key

  # From AWS SSM
  DATABASE_URL: ssm:///production/database/url

  # Plain value (not recommended for sensitive data)
  LOG_LEVEL: debug
```

**Note:** For services with dedicated grants (GitHub, Anthropic, OpenAI, AWS), use `grants:` instead of `secrets:`. Grants provide better security by injecting credentials at the network layer.

## Security considerations

**Secrets are environment variables.** They are visible to:

- All processes in the container
- Code that reads `/proc/*/environ`
- Logging that dumps environment variables

For sensitive credentials like OAuth tokens, use grants instead of secrets. Grants inject credentials at the network layer where they're not visible in the environment.

**Secrets are resolved on your host machine.** The 1Password CLI and AWS CLI run on your host, not in the container. Your host must have access to the secret backends.

**Secrets are logged in the audit trail.** Secret resolution events (which secrets were resolved, not their values) are recorded in the audit log.

## Troubleshooting

### "op: command not found"

Install the 1Password CLI:

```bash
brew install 1password-cli
```

### "You are not currently signed in"

Sign in to 1Password:

```bash
op signin
```

### "aws: command not found"

Install the AWS CLI:

```bash
brew install awscli
```

### "Unable to locate credentials"

Configure AWS credentials:

```bash
aws configure
```

Or set environment variables:

```bash
export AWS_ACCESS_KEY_ID="..."
export AWS_SECRET_ACCESS_KEY="..."
```

### "ParameterNotFound"

Verify the parameter exists:

```bash
aws ssm get-parameter --name /production/database/url
```

Check the path format—SSM paths start with `/`.

### "AccessDeniedException"

Your AWS credentials lack permission to read the parameter. Check IAM policies.

## Related guides

- [Credential management](../concepts/02-credentials.md) — Network-layer credential injection
- [Running Claude Code](./01-running-claude-code.md) — Use secrets with Claude Code
