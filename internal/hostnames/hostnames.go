// Package hostnames defines synthetic hostnames that moat injects into
// container networking. Using constants here prevents drift between the
// container-side /etc/hosts entries, the proxy URL env vars, and the
// proxy's in-process host-gateway checks.
package hostnames

// Proxy is the hostname containers use to reach the moat proxy. It MUST
// appear in NO_PROXY so that direct tunnel requests to the proxy itself
// are not proxied (which would loop forever).
const Proxy = "moat-proxy"

// HostGateway is the hostname containers use to reach services on the
// host. It is intentionally NOT in NO_PROXY so that host-service requests
// flow through the proxy for policy enforcement (network.host rules).
const HostGateway = "moat-host"
