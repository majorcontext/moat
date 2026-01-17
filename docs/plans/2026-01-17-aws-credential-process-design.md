# AWS Dynamic Credential Refresh via credential_process

## Problem

AWS credentials injected as static environment variables expire after the session duration (default 15m, max 12h). Agent runs can last multiple days, so we need dynamic credential refresh.

## Solution

Use AWS SDK's `credential_process` feature. A shell script inside the container fetches fresh credentials from our proxy on demand. The SDK handles caching and automatic refresh.

## Architecture

```
Container                              Host
┌──────────────────────────┐          ┌──────────────────────────┐
│                          │          │                          │
│  AWS SDK                 │          │  AgentOps Proxy          │
│    │                     │          │    │                     │
│    ▼                     │          │    ▼                     │
│  credential_process      │   HTTP   │  /_aws/credentials       │
│    │                     │ ───────> │    │                     │
│    ▼                     │          │    ▼                     │
│  /agentops/aws-creds     │          │  STS AssumeRole          │
│  (shell script)          │          │  (with caching)          │
│                          │          │                          │
└──────────────────────────┘          └──────────────────────────┘
```

## credential_process Format

The script must output JSON:
```json
{
  "Version": 1,
  "AccessKeyId": "ASIA...",
  "SecretAccessKey": "...",
  "SessionToken": "...",
  "Expiration": "2024-01-16T12:00:00Z"
}
```

The SDK caches credentials and calls the script again ~5 minutes before expiry.

## Implementation

### 1. Update proxy endpoint response format

Change `internal/proxy/aws.go` to return credential_process format:
```go
resp := map[string]interface{}{
    "Version":         1,
    "AccessKeyId":     creds.AccessKeyID,
    "SecretAccessKey": creds.SecretAccessKey,
    "SessionToken":    creds.SessionToken,
    "Expiration":      creds.Expiration.Format(time.RFC3339),
}
```

### 2. Create credential helper binary

A small static Go binary that:
- Reads `AGENTOPS_CREDENTIAL_URL` and `AGENTOPS_CREDENTIAL_TOKEN` from env
- Makes HTTP request to the proxy endpoint
- Outputs the JSON response to stdout
- Exits non-zero on error

**Cross-compilation strategy:**
- Build helper for linux/amd64 and linux/arm64 at build time
- Embed both binaries using `go:embed`
- At runtime, select based on `runtime.GOARCH` (matches container architecture)

**Build process (Makefile or go generate):**
```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o internal/run/helpers/aws-credential-helper-amd64 ./cmd/aws-credential-helper
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o internal/run/helpers/aws-credential-helper-arm64 ./cmd/aws-credential-helper
```

**Embedding:**
```go
// internal/run/credential_helper.go
//go:embed helpers/aws-credential-helper-amd64
var awsCredentialHelperAmd64 []byte

//go:embed helpers/aws-credential-helper-arm64
var awsCredentialHelperArm64 []byte

func GetAWSCredentialHelper() []byte {
    if runtime.GOARCH == "arm64" {
        return awsCredentialHelperArm64
    }
    return awsCredentialHelperAmd64
}
```

This ensures the credential helper works regardless of what's installed in the container image.

### 3. Create AWS config file

Also written to temp dir at runtime:
```ini
[default]
credential_process = /agentops/aws/credentials
region = {region}
```

### 4. Mount and configure in manager.go

- Create temp directory with script (executable) and config
- Mount at `/agentops/aws` (read-only)
- Set environment variables:
  - `AWS_CONFIG_FILE=/agentops/aws/config`
  - `AGENTOPS_CREDENTIAL_URL=http://{proxy_host}:{port}/_aws/credentials`
  - `AGENTOPS_CREDENTIAL_TOKEN={token}` (if auth required)
  - `NO_PROXY=.amazonaws.com,.aws.amazon.com` (AWS API calls bypass proxy)
  - `AWS_REGION={region}`
- Remove static credential env vars (AWS_ACCESS_KEY_ID, etc.)

## Endpoint Reachability

| Platform | Proxy Host | Auth Required |
|----------|------------|---------------|
| Linux (host network) | 127.0.0.1 | No |
| Linux (bridge) | host.docker.internal | Yes |
| macOS/Windows | host.docker.internal | Yes |

## Error Handling

- Script has 10-second timeout to fail fast
- Non-zero exit on curl failure
- AWS SDK retries and surfaces errors to user
- If proxy dies, existing credentials work until expiry

## Files Modified

- `internal/proxy/aws.go` - Response format change
- `internal/proxy/aws_test.go` - Update test expectations
- `internal/run/manager.go` - Mount binary/config, set env vars

## New Files

- `cmd/aws-credential-helper/main.go` - Credential helper binary
- `internal/run/credential_helper.go` - Embedded binary and setup logic

## Testing

1. Unit test: Verify endpoint returns credential_process format
2. E2E test: Run container, verify `aws sts get-caller-identity` works
3. E2E test: Verify credentials refresh (mock short expiry)
