package run

import (
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/provider"
	"github.com/majorcontext/moat/internal/providers/claude"
	"github.com/majorcontext/moat/internal/ui"
)

// claudeBaseURL is a resolved claude.base_url, split into the three forms the
// run needs: what the container gets, what the proxy matches credentials on,
// and which host port (if any) the run must be allowed to reach.
type claudeBaseURL struct {
	// ContainerURL is the value for ANTHROPIC_BASE_URL inside the container.
	ContainerURL string

	// CredentialHost is the hostname credential injection is registered under.
	// It is the host the container actually connects to, which is what the
	// proxy matches on — not necessarily the host the user wrote.
	CredentialHost string

	// HostPort is a port on the host loopback the run must be allowed to
	// reach, or 0 when the target is not host-local.
	HostPort int
}

// configureClaudeBaseURL points Claude Code at a custom LLM endpoint (a gateway
// such as LunaRoute, or a host-side proxy such as Headroom). It registers
// credential injection for the endpoint on runCtx, allows the endpoint's port
// when it is host-local, and returns the ANTHROPIC_BASE_URL env entry for the
// container. It returns "" when the run has no base_url configured.
//
// Callers must invoke this BEFORE buildRegisterRequest. That call snapshots the
// RunContext into the registration request, so a credential registered
// afterwards never reaches the daemon and nothing gets injected.
//
// cred may be nil: the endpoint still applies, because the user may be
// supplying the key themselves via env or secrets. If nothing supplies it, the
// run would fail with an opaque 401 from the endpoint, so that case warns.
//
// profileEnvVars (the active credential profile's env) and userEnv (the run's
// -e entries, "KEY=value") are needed only to tell those two cases apart.
func configureClaudeBaseURL(runCtx *daemon.RunContext, cfg *config.Config, cred *provider.Credential, profileEnvVars map[string]string, userEnv []string) (string, error) {
	raw := claudeBaseURLSource(cfg, cred)
	if raw == "" {
		return "", nil
	}

	resolved, err := resolveClaudeBaseURL(raw)
	if err != nil {
		return "", err
	}

	switch {
	case cred != nil && isGatewayCredential(cred) && resolved.CredentialHost == claude.APIHost:
		// A gateway key against Anthropic's own API: ConfigureBaseURLProxy
		// refuses to inject it, so say why rather than leaving an opaque 401.
		ui.Warnf("claude.base_url points at %s, but the active anthropic grant is a gateway key for %s — it will not be sent to Anthropic.\n"+
			"  Remove claude.base_url to use the gateway, or run without this profile to use an Anthropic key",
			claude.APIHost, cred.Metadata[credential.MetaKeyBaseURL])
		claude.ConfigureBaseURLProxy(runCtx, cred, resolved.CredentialHost)
	case cred != nil:
		claude.ConfigureBaseURLProxy(runCtx, cred, resolved.CredentialHost)
	case hasUserSuppliedAnthropicKey(cfg, profileEnvVars, userEnv):
		// The user is providing the endpoint's key themselves. Nothing to
		// inject, and nothing to warn about.
		log.Debug("claude.base_url set with a user-supplied key; no credential will be injected",
			"baseURL", raw)
	default:
		// Neither a grant nor a key of their own: the endpoint will reject
		// every request. Say so now rather than letting Claude Code retry
		// auth errors silently.
		ui.Warnf("claude.base_url is set but nothing provides its key — requests to %s will be unauthenticated.\n"+
			"  Grant one:  moat grant anthropic --base-url %s\n"+
			"  Or set ANTHROPIC_AUTH_TOKEN via env or secrets in moat.yaml",
			resolved.CredentialHost, raw)
	}

	// A host-local endpoint is only reachable if its port is allowed. base_url
	// is explicit intent, so allow it rather than making the user repeat the
	// port under network.host. Copy rather than append in place — the slice
	// aliases cfg.Network.Host.
	if resolved.HostPort != 0 && !slices.Contains(runCtx.AllowedHostPorts, resolved.HostPort) {
		runCtx.AllowedHostPorts = append(append([]int{}, runCtx.AllowedHostPorts...), resolved.HostPort)
	}

	log.Debug("configured base URL for Claude Code",
		"baseURL", raw,
		"containerURL", resolved.ContainerURL,
		"credentialHost", resolved.CredentialHost,
		"allowedHostPort", resolved.HostPort)

	return "ANTHROPIC_BASE_URL=" + resolved.ContainerURL, nil
}

// claudeBaseURLSource picks the endpoint for a run.
//
// moat.yaml wins over the credential: a project that names an endpoint means
// it, and a gateway credential is a default for "wherever I run this key", not
// an override of an explicit setting.
//
// The credential's endpoint comes from `moat grant anthropic --base-url`, which
// is how a gateway key stays out of moat.yaml and out of the container.
func claudeBaseURLSource(cfg *config.Config, cred *provider.Credential) string {
	if cfg != nil && cfg.Claude.BaseURL != "" {
		return cfg.Claude.BaseURL
	}
	if cred != nil {
		return cred.Metadata[credential.MetaKeyBaseURL]
	}
	return ""
}

// resolveClaudeBaseURL rewrites a claude.base_url into the form the container
// and the proxy each need.
//
// A loopback URL names a service on the host (a local LLM proxy such as
// Headroom), which the container cannot reach at its own 127.0.0.1. Those are
// rewritten to the synthetic host-gateway hostname, and the port is returned so
// the caller can add it to the run's allowed host ports. The host gateway is
// deliberately absent from NO_PROXY, so the rewritten request still goes
// through the proxy: it gets its credential injected and stays subject to
// network policy.
//
// Any other host is returned unchanged. The proxy sees an ordinary request and
// injects on the way through, so no rewrite is needed.
//
// The URL is re-checked here rather than trusted. config.Load validates a
// moat.yaml value and grant validates a --base-url one, but a credential's
// recorded endpoint is read back from the store, so this is the one place both
// sources pass through.
func resolveClaudeBaseURL(raw string) (claudeBaseURL, error) {
	u, normalized, err := config.ValidateHTTPURL(raw)
	if err != nil {
		return claudeBaseURL{}, err
	}

	host := u.Hostname()
	if !isLoopbackHost(host) {
		// The normalized form, not raw: an endpoint from a credential or a
		// moat.yaml with a trailing slash must reach the container in the same
		// shape either way.
		return claudeBaseURL{ContainerURL: normalized, CredentialHost: host}, nil
	}

	port := u.Port()
	if port == "" {
		// A loopback URL with no explicit port still needs one: the rewritten
		// URL must name the port the host service listens on, and the run must
		// allow that exact port.
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	portNum, err := strconv.Atoi(port)
	if err != nil {
		return claudeBaseURL{}, fmt.Errorf("invalid port %q in %q: %w", port, raw, err)
	}

	rewritten := *u
	rewritten.Host = net.JoinHostPort(syntheticHostGateway, port)
	return claudeBaseURL{
		ContainerURL:   rewritten.String(),
		CredentialHost: syntheticHostGateway,
		HostPort:       portNum,
	}, nil
}

// isLoopbackHost reports whether host names the machine the CLI runs on.
// "localhost" is matched by name because it is what users write; everything
// else is decided by the parsed IP so that 127.0.0.2 and ::1 are covered too.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// hasUserSuppliedAnthropicKey reports whether the run provides an Anthropic
// credential of its own, through any of the places a user can set one: the
// active profile's env, moat.yaml env, moat.yaml secrets, or a -e flag. Used
// only to decide whether a base_url with no grant is a mistake or a deliberate
// bring-your-own-key run.
//
// Every env layer has to be checked here. Missing one produces a warning that
// contradicts a working setup, which is worse than no warning at all.
func hasUserSuppliedAnthropicKey(cfg *config.Config, profileEnvVars map[string]string, userEnv []string) bool {
	names := []string{"ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_API_KEY"}

	for _, n := range names {
		if _, ok := profileEnvVars[n]; ok {
			return true
		}
	}

	if cfg != nil {
		for _, n := range names {
			if _, ok := cfg.Env[n]; ok {
				return true
			}
			if _, ok := cfg.Secrets[n]; ok {
				return true
			}
		}
	}

	for _, e := range userEnv {
		name, _, found := strings.Cut(e, "=")
		if !found {
			continue
		}
		if slices.Contains(names, name) {
			return true
		}
	}
	return false
}

// isGatewayCredential reports whether cred authenticates against a third-party
// endpoint rather than Anthropic — i.e. it came from
// `moat grant anthropic --base-url`.
func isGatewayCredential(cred *provider.Credential) bool {
	return cred != nil && cred.Metadata[credential.MetaKeyBaseURL] != ""
}
