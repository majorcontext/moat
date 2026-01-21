package run

// AWSCredentialHelperScript is a shell script that fetches AWS credentials
// from the AgentOps proxy. It implements the AWS credential_process interface.
//
// This requires curl, which is always installed as a base package in containers
// built with the dependency system (see internal/deps/dockerfile.go). Since
// --grant aws requires the aws dependency for the AWS CLI, curl is guaranteed
// to be present in any container using AWS credentials.
const AWSCredentialHelperScript = `#!/bin/sh
set -e
if [ -z "$AGENTOPS_CREDENTIAL_URL" ]; then
  echo "AGENTOPS_CREDENTIAL_URL not set" >&2
  exit 1
fi
if [ -n "$AGENTOPS_CREDENTIAL_TOKEN" ]; then
  exec curl -sf -m 10 -H "Authorization: Bearer $AGENTOPS_CREDENTIAL_TOKEN" "$AGENTOPS_CREDENTIAL_URL"
else
  exec curl -sf -m 10 "$AGENTOPS_CREDENTIAL_URL"
fi
`

// GetAWSCredentialHelper returns the credential helper script as bytes.
func GetAWSCredentialHelper() []byte {
	return []byte(AWSCredentialHelperScript)
}
