// Package aws implements the AWS credential provider for moat.
//
// Unlike other providers that inject credentials via proxy headers, AWS uses
// a credential endpoint pattern. The provider exposes an HTTP endpoint that
// returns temporary credentials from STS AssumeRole in ECS container format.
//
// The container is configured with AWS_CONTAINER_CREDENTIALS_FULL_URI pointing
// to the proxy's credential endpoint, allowing AWS SDKs to automatically fetch
// credentials when needed.
//
// Grant flow:
//  1. User provides IAM role ARN via `moat grant aws`
//  2. ARN is validated and tested with STS AssumeRole
//  3. Role ARN stored in Credential.Token, region/duration in Metadata
//
// Runtime flow:
//  1. Container makes AWS API call
//  2. AWS SDK detects AWS_CONTAINER_CREDENTIALS_FULL_URI
//  3. SDK fetches credentials from proxy endpoint
//  4. Proxy calls STS AssumeRole and returns temporary credentials
//  5. SDK uses credentials for the API call
package aws
