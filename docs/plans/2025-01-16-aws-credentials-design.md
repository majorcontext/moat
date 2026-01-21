# AWS Credential Support Design

## Overview

Add AWS credential support to agentops, allowing agents to make AWS API calls without direct access to long-lived credentials. Uses IAM role assumption with short-lived credentials served via the ECS container credential protocol.

## Design Principles

1. **Role-only** — No credential passthrough. Users must specify an IAM role to assume.
2. **Short-lived credentials** — 15-minute default session duration, auto-refreshed by SDK.
3. **No agent access to secrets** — Host credentials never enter the container.

## User Experience

### Grant Command

```bash
# Minimal — just the role ARN
agent grant aws --role=arn:aws:iam::123456789012:role/AgentRole

# With options
agent grant aws \
  --role=arn:aws:iam::123456789012:role/AgentRole \
  --region=us-west-2 \
  --session-duration=30m \
  --external-id=my-external-id
```

**Grant-time behavior:**
1. Parse and validate the role ARN format
2. Detect host AWS credentials (env vars → `~/.aws/credentials` → `~/.aws/config`)
3. Test `sts:AssumeRole` to verify the role is assumable
4. On success, store the grant config

**Success output:**
```
✓ Found AWS credentials (profile: default)
✓ Successfully assumed role: arn:aws:iam::123456789012:role/AgentRole
✓ AWS grant saved

Role:             arn:aws:iam::123456789012:role/AgentRole
Region:           us-east-1 (from role ARN)
Session duration: 15m

Use with: agent run --grant aws <agent>
```

**Failure outputs:**
```
✗ No AWS credentials found

Set credentials via:
  • AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY environment variables
  • aws configure
  • aws sso login
```

```
✗ Cannot assume role: AccessDenied

The role arn:aws:iam::123456789012:role/AgentRole cannot be assumed
with your current credentials. Check that:
  • The role's trust policy allows your IAM principal
  • You have sts:AssumeRole permission
```

### Run Command

```bash
agent run --grant aws my-agent/
```

No changes to run command syntax — AWS works like other grants.

### What the Agent Sees

```bash
# Inside container
$ env | grep AWS
AWS_CONTAINER_CREDENTIALS_FULL_URI=http://host.docker.internal:8080/_aws/credentials
AWS_REGION=us-east-1

# No access keys visible
$ echo $AWS_ACCESS_KEY_ID
(empty)

# But AWS CLI works transparently
$ aws s3 ls
2024-01-01 bucket-1
2024-01-02 bucket-2
```

## Architecture

### Credential Flow

```
┌─────────────┐         ┌─────────────┐         ┌─────────────┐
│   AWS SDK   │         │    Proxy    │         │     STS     │
│ (in container)        │  (on host)  │         │   (AWS)     │
└──────┬──────┘         └──────┬──────┘         └──────┬──────┘
       │                       │                       │
       │ GET /_aws/credentials │                       │
       │──────────────────────>│                       │
       │                       │                       │
       │                       │ AssumeRole(           │
       │                       │   RoleArn,            │
       │                       │   SessionName,        │
       │                       │   DurationSeconds)    │
       │                       │──────────────────────>│
       │                       │                       │
       │                       │   {AccessKeyId,       │
       │                       │    SecretAccessKey,   │
       │                       │    SessionToken,      │
       │                       │    Expiration}        │
       │                       │<──────────────────────│
       │                       │                       │
       │ {AccessKeyId,         │                       │
       │  SecretAccessKey,     │                       │
       │  Token,               │                       │
       │  Expiration}          │                       │
       │<──────────────────────│                       │
       │                       │                       │
       │  [SDK caches creds,   │                       │
       │   auto-refreshes      │                       │
       │   before expiry]      │                       │
```

### Storage Format

`~/.agentops/credentials/aws.enc`:
```json
{
  "provider": "aws",
  "role_arn": "arn:aws:iam::123456789012:role/AgentRole",
  "region": "us-east-1",
  "session_duration": "15m",
  "external_id": "",
  "created_at": "2024-01-15T10:00:00Z"
}
```

Note: Stores config, not secrets. Host AWS credentials used at runtime.

### Container Environment

```bash
# Set by agentops at container start
AWS_CONTAINER_CREDENTIALS_FULL_URI=http://host.docker.internal:8080/_aws/credentials
AWS_REGION=us-east-1

# Explicitly unset to prevent leakage
AWS_ACCESS_KEY_ID=
AWS_SECRET_ACCESS_KEY=
AWS_SESSION_TOKEN=
```

### Proxy Endpoint

Path-based routing on existing proxy:
```
GET http://proxy:8080/_aws/credentials
```

**Authentication:**
- Docker: localhost-only binding, no auth needed
- Apple containers: Uses existing basic auth format
  ```
  http://agentops:<token>@host:8080/_aws/credentials
  ```

**Response format (ECS container credential protocol):**
```json
{
  "AccessKeyId": "ASIA...",
  "SecretAccessKey": "...",
  "Token": "...",
  "Expiration": "2024-01-15T10:20:00Z"
}
```

### Credential Caching

Proxy caches credentials and refreshes 5 minutes before expiration:

```go
type awsCredentialCache struct {
    mu         sync.RWMutex
    creds      *sts.Credentials
    expiration time.Time
}

func (c *awsCredentialCache) get(ctx context.Context, provider *AWSCredentialProvider) (*sts.Credentials, error) {
    c.mu.RLock()
    if c.creds != nil && time.Now().Add(5*time.Minute).Before(c.expiration) {
        defer c.mu.RUnlock()
        return c.creds, nil
    }
    c.mu.RUnlock()

    c.mu.Lock()
    defer c.mu.Unlock()

    // Double-check after acquiring write lock
    if c.creds != nil && time.Now().Add(5*time.Minute).Before(c.expiration) {
        return c.creds, nil
    }

    fresh, err := provider.assumeRole(ctx)
    if err != nil {
        return nil, err
    }

    c.creds = fresh
    c.expiration = *fresh.Expiration
    return fresh, nil
}
```

### Audit Logging

Each credential fetch logged:
```json
{
  "type": "aws_credential_fetch",
  "timestamp": "2024-01-15T10:05:00Z",
  "role_arn": "arn:aws:iam::123456789012:role/AgentRole",
  "session_name": "agentops-run-abc123",
  "expiration": "2024-01-15T10:20:00Z"
}
```

## Implementation Components

### New Files
- `internal/credential/aws.go` — AWS grant logic, role validation
- `internal/proxy/aws.go` — Credential endpoint handler, STS client

### Modified Files
- `cmd/agent/cli/grant.go` — Add `grantAWS()` function
- `internal/credential/types.go` — Add AWS-specific credential fields
- `internal/proxy/proxy.go` — Route `/_aws/` paths to AWS handler
- `internal/run/manager.go` — Set AWS env vars, configure credential endpoint

## Configuration Options

| Flag | Default | Description |
|------|---------|-------------|
| `--role` | (required) | IAM role ARN to assume |
| `--region` | from ARN | AWS region for STS calls |
| `--session-duration` | 15m | Session credential lifetime (15m-12h) |
| `--external-id` | (none) | External ID for role assumption |

## Security Considerations

1. **Role-only policy** — No `--inherit-caller-identity` option. Forces least-privilege.
2. **Short sessions** — 15-minute default limits credential exposure window.
3. **No credential storage** — Only role ARN stored; host credentials used at runtime.
4. **Audit trail** — CloudTrail shows `assumed-role/RoleName/agentops-<runid>`.
5. **Proxy auth** — Apple containers use token auth for credential endpoint.

## Not Supported (Intentionally)

- `--scope` flag — IAM roles already define permissions; scopes would be redundant/confusing
- Credential passthrough — Users must create a role for permission boundaries
- Multiple AWS grants — One role per run (simplicity; can revisit if needed)
