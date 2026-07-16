package moatinit

import (
	"fmt"
	"net"
	"strings"
	"time"
)

// dnsWaitIters mirrors MOAT_DNS_WAIT_ITERS: iterations * 0.2s = 5 second
// timeout for container DNS to answer an '@'-form target.
const dnsWaitIters = 25

// hostEntry is one parsed MOAT_EXTRA_HOSTS token ("name:target").
type hostEntry struct {
	name   string
	target string
}

// splitExtraHosts mirrors the script's unquoted `for entry in
// $MOAT_EXTRA_HOSTS` (HOSTS-02): word-splitting on default IFS — space, tab,
// and newline — with runs of separators collapsing so no empty tokens are
// produced.
func splitExtraHosts(v string) []string {
	return strings.Fields(v)
}

// parseHostEntry splits a token on its FIRST colon (HOSTS-03):
// name=${entry%%:*} (everything before the first colon) and
// target=${entry#*:} (everything after it). A colon-less token leaves both
// parameter expansions unchanged, so name == target == token — which the
// skip rule then drops.
func parseHostEntry(tok string) hostEntry {
	idx := strings.IndexByte(tok, ':')
	if idx < 0 {
		return hostEntry{name: tok, target: tok}
	}
	return hostEntry{name: tok[:idx], target: tok[idx+1:]}
}

// skip mirrors the script's continue (HOSTS-04): drop malformed entries
// ("name:", ":target") and colon-less tokens (name == target).
func (e hostEntry) skip() bool {
	return e.name == "" || e.target == "" || e.name == e.target
}

// resolveTarget discriminates the target (HOSTS-05): a leading '@' marks a
// hostname to resolve via the container's DNS ("@host.docker.internal");
// anything else is a literal IP written verbatim (including odd values like
// "a@b" or "::1" — no validation, parity with the script's `case` patterns).
func (e hostEntry) resolveTarget() (hostname string, resolve bool) {
	if strings.HasPrefix(e.target, "@") {
		return strings.TrimPrefix(e.target, "@"), true
	}
	return "", false
}

// extraHostsPhase appends synthetic entries to /etc/hosts (HOSTS region).
// It must be the FIRST phase: everything after it may resolve
// moat-proxy/moat-host (HOSTS-11).
//
// Fail-closed: an unresolvable '@'-target or an /etc/hosts write failure
// aborts the entrypoint with the script's exact three-line errors. Silent
// failure would leave moat-proxy unresolvable, HTTP_PROXY broken, and
// network policy silently degraded.
func extraHostsPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.ExtraHosts == "" {
		return nil // HOSTS-01: unset/empty is a complete no-op
	}
	for _, tok := range splitExtraHosts(cfg.ExtraHosts) {
		e := parseHostEntry(tok)
		if e.skip() {
			continue
		}

		var ip string
		if hostname, resolve := e.resolveTarget(); resolve {
			// Prefer IPv4 (getent ahostsv4) because the host is reached via
			// Docker Desktop's IPv4-only mapping; an IPv6 entry like "::1"
			// would resolve to the container's own loopback and silently
			// not reach the host. Fall back to any address (getent hosts)
			// if the name has only IPv6 records. Retried because Docker
			// Desktop's embedded DNS may not be ready the instant the
			// ENTRYPOINT runs (HOSTS-06/HOSTS-14: 25 × 0.2s ≈ 5s budget;
			// each lookup attempt is itself bounded so a hanging resolver
			// cannot blow the loop budget).
			for i := 0; i < dnsWaitIters; i++ {
				candidate := sys.ResolveIPv4First(hostname)
				if candidate == "" {
					candidate = sys.ResolveAnyFirst(hostname)
				}
				if candidate != "" {
					ip = candidate
					break
				}
				sys.Sleep(200 * time.Millisecond)
			}
			if ip == "" {
				fmt.Fprintf(ctx.Stderr, "Error: moat-init.sh could not resolve '%s' for /etc/hosts entry '%s'.\n", hostname, e.name)
				fmt.Fprintln(ctx.Stderr, "The container's DNS should answer this name. On Docker Desktop, verify that")
				fmt.Fprintf(ctx.Stderr, "'getent hosts %s' works inside this container.\n", hostname)
				return exitError{code: 1}
			}
			// Sanctioned addition (plan risk register P1): the IPv6/loopback
			// fallback is reachable only when the name had no A record; the
			// resulting entry very likely cannot reach the host-side proxy,
			// so say so instead of degrading silently. Warning only — the
			// entry is still written, byte-for-byte like the script.
			if parsed := net.ParseIP(ip); parsed != nil && (parsed.To4() == nil || parsed.IsLoopback()) {
				fmt.Fprintf(ctx.Stderr, "Warning: /etc/hosts entry '%s' resolved to '%s' (IPv6 or loopback); the moat proxy may not be reachable through it\n", e.name, ip)
			}
		} else {
			ip = e.target
		}

		// Append "<ip> <name>" (HOSTS-09). A write failure is fatal
		// (HOSTS-10) — typically the entrypoint is not root and lacks
		// permission.
		if err := sys.AppendFile("/etc/hosts", []byte(ip+" "+e.name+"\n")); err != nil {
			fmt.Fprintf(ctx.Stderr, "Error: moat-init.sh cannot write %s to /etc/hosts (required for moat proxy resolution).\n", e.name)
			fmt.Fprintf(ctx.Stderr, "The container user (UID %d) lacks permission to modify /etc/hosts.\n", sys.Geteuid())
			fmt.Fprintln(ctx.Stderr, "Rebuild the base image so moat-init.sh runs as root, or grant CAP_DAC_OVERRIDE.")
			return exitError{code: 1}
		}
	}
	return nil
}
