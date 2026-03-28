package aws

// CredentialHelperScript is a shell script that fetches AWS credentials
// from the moat proxy. It implements the AWS credential_process interface.
//
// This requires curl, which is always installed as a base package in containers
// built with the dependency system (see internal/deps/dockerfile.go). Since
// --grant aws requires the aws dependency for the AWS CLI, curl is guaranteed
// to be present in any container using AWS credentials.
const CredentialHelperScript = `#!/bin/sh
set -e
if [ -z "$MOAT_AWS_CREDENTIAL_URL" ]; then
  # Backwards compatibility with older daemon versions.
  if [ -n "$AGENTOPS_CREDENTIAL_URL" ]; then
    MOAT_AWS_CREDENTIAL_URL="$AGENTOPS_CREDENTIAL_URL"
    MOAT_AWS_CREDENTIAL_TOKEN="$AGENTOPS_CREDENTIAL_TOKEN"
  else
    echo "MOAT_AWS_CREDENTIAL_URL not set" >&2
    exit 1
  fi
fi
if [ -n "$MOAT_AWS_CREDENTIAL_TOKEN" ]; then
  RESPONSE=$(curl -sS -m 10 -H "Authorization: Bearer $MOAT_AWS_CREDENTIAL_TOKEN" "$MOAT_AWS_CREDENTIAL_URL" 2>&1) || {
    echo "moat: AWS credential fetch failed:" >&2
    echo "$RESPONSE" >&2
    exit 1
  }
else
  RESPONSE=$(curl -sS -m 10 "$MOAT_AWS_CREDENTIAL_URL" 2>&1) || {
    echo "moat: AWS credential fetch failed:" >&2
    echo "$RESPONSE" >&2
    exit 1
  }
fi
echo "$RESPONSE"
`

// GetCredentialHelper returns the credential helper script as bytes.
func GetCredentialHelper() []byte {
	return []byte(CredentialHelperScript)
}
