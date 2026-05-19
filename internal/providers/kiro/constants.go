package kiro

// KiroAPIKeyPlaceholder is a syntactically plausible placeholder API key.
// kiro-cli runs in API-key mode when KIRO_API_KEY is set and sends this
// value as a Bearer token; the Moat proxy replaces it with the real token
// at the network layer. The real token never enters the container.
const KiroAPIKeyPlaceholder = "kiro-moat-proxy-injected-placeholder-000000000000000000000000000000"

// KiroInitMountPath is where the staging directory is mounted in containers.
const KiroInitMountPath = "/moat/kiro-init"

// kiroAPIHosts are the hosts the proxy injects the Kiro Bearer token for.
//
// gatekeeper v0.2.0 credential injection uses exact host-string map lookups
// (proxy.go getCredentials), not the wildcard pattern matching used by the
// network firewall. Wildcard patterns in SetCredentialWithGrant are stored
// verbatim as map keys and will never match real hostnames. Only concrete
// hostnames are effective for credential injection.
//
// Kiro CLI calls q.us-east-1.amazonaws.com (us-east-1 is the only region
// currently used in production). To support additional regions in the future,
// add their concrete hostnames here (e.g. "q.eu-west-1.amazonaws.com").
var kiroAPIHosts = []string{
	"q.us-east-1.amazonaws.com",
}

// kiroPassthroughHosts are allowlisted but receive no credential injection.
var kiroPassthroughHosts = []string{
	"cognito-identity.*.amazonaws.com",
}
