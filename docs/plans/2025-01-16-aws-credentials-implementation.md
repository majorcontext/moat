# AWS Credential Support Implementation Plan

> **Status:** IMPLEMENTED - This plan has been completed. See implementation notes below.

**Goal:** Add AWS credential support via IAM role assumption with credential_process.

**Architecture:** Store role ARN config (not secrets). At runtime, proxy serves `/_aws/credentials` endpoint that calls STS AssumeRole using host AWS credentials. AWS SDKs inside the container use `credential_process` to fetch credentials on demand via an embedded helper binary.

**Tech Stack:** Go, AWS SDK v2 (`github.com/aws/aws-sdk-go-v2`), existing proxy infrastructure.

---

## Implementation Summary

The following tasks were completed to implement AWS credential support:

### Task 1: Add AWS SDK Dependency ✓

**Files modified:**
- `go.mod`
- `go.sum`

AWS SDK v2 packages added:
- `github.com/aws/aws-sdk-go-v2`
- `github.com/aws/aws-sdk-go-v2/config`
- `github.com/aws/aws-sdk-go-v2/service/sts`

---

### Task 2: Create AWS Credential Types ✓

**Files modified:**
- `internal/credential/types.go` - Added `AWSConfig` struct with `SessionDuration()` method
- `internal/credential/aws.go` - Added `ParseRoleARN()` function
- `internal/credential/aws_test.go` - Tests for ARN parsing and session duration validation

The `AWSConfig` type stores:
- `RoleARN` - The IAM role ARN to assume
- `Region` - AWS region (defaults to us-east-1)
- `SessionDurationStr` - Session duration string (15m-12h)
- `ExternalID` - Optional external ID for role assumption

---

### Task 3: Implement AWS Grant Command ✓

**Files modified:**
- `cmd/agent/cli/grant.go`

Added:
- `grantAWS()` function that validates role ARN, tests AssumeRole, and stores config
- AWS-specific flags: `--role`, `--region`, `--session-duration`, `--external-id`
- Stores config in Credential struct with Token=roleARN and Scopes=[region, sessionDuration, externalID]

Usage:
```bash
agent grant aws --role=arn:aws:iam::123456789012:role/AgentRole
agent grant aws --role=arn:aws:iam::123456789012:role/AgentRole \
  --region=us-west-2 --session-duration=1h
```

---

### Task 4: Create AWS Credential Provider for Proxy ✓

**Files modified:**
- `internal/proxy/aws.go`
- `internal/proxy/aws_test.go`

Implemented:
- `AWSCredentials` struct for temporary credentials
- `AWSCredentialHandler` HTTP handler serving credentials in credential_process format
- `AWSCredentialProvider` with caching (5-minute refresh buffer) and STS AssumeRole calls
- `STSAssumeRoler` interface for testing
- `SetAuthToken()` for authorization header verification
- `Region()` and `RoleARN()` getter methods

**Response format (credential_process):**
```json
{
  "Version": 1,
  "AccessKeyId": "AKIA...",
  "SecretAccessKey": "...",
  "SessionToken": "...",
  "Expiration": "2025-01-16T12:00:00Z"
}
```

---

### Task 5: Integrate AWS Credentials into Proxy ✓

**Files modified:**
- `internal/proxy/proxy.go`

Added:
- `awsHandler http.Handler` field to Proxy struct
- `SetAWSHandler(h http.Handler)` method
- Request routing for `/_aws/credentials` endpoint

---

### Task 6: Add AWS to Allowed Hosts ✓

**Files modified:**
- `internal/proxy/hosts.go`

Added AWS hosts to `grantHosts` map:
```go
"aws": {
    "sts.amazonaws.com",
    "sts.*.amazonaws.com",
    "*.amazonaws.com",
},
```

---

### Task 7: Integrate into Run Manager ✓

**Files modified:**
- `internal/run/manager.go`
- `internal/run/run.go`

Implemented:
- Parse AWS grant from stored credential config
- Create `AWSCredentialProvider` and set auth token
- Mount embedded credential helper binary to `/agentops/aws/credentials`
- Write AWS config file with `credential_process` directive
- Set environment variables:
  - `AWS_CONFIG_FILE=/agentops/aws/config`
  - `AGENTOPS_CREDENTIAL_URL=http://host:port/_aws/credentials`
  - `AGENTOPS_CREDENTIAL_TOKEN=<auth-token>` (if proxy auth enabled)
  - `AWS_REGION=<region>`
  - `AWS_CA_BUNDLE=<ca-cert-path>` (for MITM SSL)
  - `AWS_PAGER=` (disable pager)

Added `AWSCredentialProvider` field to `Run` struct in `internal/run/run.go`.

---

### Task 8: Create Embedded Credential Helper ✓

**Files created:**
- `cmd/aws-credential-helper/main.go` - Standalone binary that fetches credentials from proxy
- `internal/run/credential_helper.go` - Embeds pre-built Linux binaries
- `internal/run/helpers/aws-credential-helper-linux-amd64` - AMD64 binary
- `internal/run/helpers/aws-credential-helper-linux-arm64` - ARM64 binary

The credential helper:
- Reads `AGENTOPS_CREDENTIAL_URL` and `AGENTOPS_CREDENTIAL_TOKEN` from environment
- Fetches credentials from proxy endpoint with Authorization header
- Outputs credential_process format to stdout

Build command:
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o internal/run/helpers/aws-credential-helper-linux-amd64 ./cmd/aws-credential-helper
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" \
  -o internal/run/helpers/aws-credential-helper-linux-arm64 ./cmd/aws-credential-helper
```

---

## Architecture Notes

### Problem: Credential Expiration

AWS credentials injected as static environment variables expire after the session duration (default 15m, max 12h). Agent runs can last multiple days, so we need dynamic credential refresh.

### Why credential_process Instead of AWS_CONTAINER_CREDENTIALS_FULL_URI?

The original plan proposed using `AWS_CONTAINER_CREDENTIALS_FULL_URI` (ECS-style credentials). The implementation uses `credential_process` instead because:

1. **Broader compatibility** - credential_process works with all AWS SDKs and the AWS CLI
2. **Direct injection challenges** - On macOS/Windows, injecting credentials directly into the container environment was problematic
3. **Dynamic refresh** - credential_process is called on-demand by AWS SDKs, providing automatic refresh

The SDK caches credentials and calls the helper again ~5 minutes before expiry.

### Security Model

1. **Auth token** - When proxy binds to 0.0.0.0 (Apple containers), a cryptographic token is required
2. **Token delivery** - Token passed via `AGENTOPS_CREDENTIAL_TOKEN` environment variable
3. **Authorization header** - Credential helper includes `Authorization: Bearer <token>` in requests
4. **CA bundle** - AWS traffic goes through proxy for observability; `AWS_CA_BUNDLE` trusts the MITM CA

### Endpoint Reachability by Platform

| Platform | Proxy Host | Auth Required |
|----------|------------|---------------|
| Linux (host network) | 127.0.0.1 | No |
| Linux (bridge) | host.docker.internal | Yes |
| macOS/Windows | host.docker.internal | Yes |
| Apple containers | gateway IP | Yes |

### Error Handling

- Credential helper has timeout to fail fast
- Non-zero exit on HTTP failure
- AWS SDK retries and surfaces errors to user
- If proxy dies, existing credentials work until expiry

### Credential Flow

```
Container                    Host Proxy                 AWS STS
    |                            |                         |
    | credential_process called  |                         |
    | by AWS SDK                 |                         |
    |--(helper binary)---------->|                         |
    |   GET /_aws/credentials    |                         |
    |   Authorization: Bearer X  |                         |
    |                            |--AssumeRole------------>|
    |                            |<--(temporary creds)-----|
    |<--(credential_process      |                         |
    |    JSON response)          |                         |
    |                            |                         |
    | SDK caches creds           |                         |
    | and uses for API calls     |                         |
```

---

## Files Changed (Summary)

- `go.mod`, `go.sum` - AWS SDK dependency
- `internal/credential/types.go` - AWSConfig type
- `internal/credential/aws.go` - ARN parsing
- `internal/credential/aws_test.go` - Tests
- `cmd/agent/cli/grant.go` - Grant command
- `internal/proxy/aws.go` - Credential provider
- `internal/proxy/aws_test.go` - Tests
- `internal/proxy/proxy.go` - AWS endpoint routing
- `internal/proxy/hosts.go` - Allowed hosts
- `internal/run/manager.go` - Integration
- `internal/run/run.go` - Run struct field
- `internal/run/credential_helper.go` - Embedded binaries
- `cmd/aws-credential-helper/main.go` - Helper binary source
