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
// VERIFICATION POINT (spec §Verification 3): if gatekeeper v0.2.0 does not
// match wildcard host patterns for credential injection, replace this slice
// with the single concrete host "q.us-east-1.amazonaws.com" and document how
// to add other regions. Confirm during implementation (see Task 10 Step "verify").
var kiroAPIHosts = []string{
	"q.*.amazonaws.com",
	"*.q.*.amazonaws.com",
}

// kiroPassthroughHosts are allowlisted but receive no credential injection.
var kiroPassthroughHosts = []string{
	"cognito-identity.*.amazonaws.com",
}
