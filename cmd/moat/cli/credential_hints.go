package cli

import (
	"net/url"
	"strings"

	"github.com/majorcontext/moat/internal/storage"
)

// grantAuth maps a credential grant to the hosts whose auth rejections imply a
// stale/invalid stored credential, plus the actionable hint to surface.
type grantAuth struct {
	grant string
	hosts []string
	hint  string
}

// grantAuths drives post-run credential-rejection hints. Add an entry per grant
// whose injected credential can be rejected with a 401/403.
var grantAuths = []grantAuth{
	{
		grant: "github",
		hosts: []string{"github.com", "api.github.com"},
		hint:  "GitHub rejected the injected credential — the stored token may be expired or missing a required scope. Run `moat grant github` to refresh it.",
	},
}

// credentialRejectionHints scans a finished run's network log for auth
// rejections (401/403) on hosts tied to one of the run's grants, returning an
// actionable re-grant hint per affected grant. An expired stored token
// otherwise surfaces only as an opaque downstream error (e.g. git's
// "could not read Username"), so this points the user at the real fix.
//
// A host is only flagged when it had a rejection and no subsequent success —
// so a request that recovered (e.g. retried and got a 2xx, or fell back to SSH
// and the run completed) does not produce a spurious warning. Callers should
// additionally only surface these on a failed run.
func credentialRejectionHints(reqs []storage.NetworkRequest, grants []string) []string {
	granted := make(map[string]bool, len(grants))
	for _, g := range grants {
		granted[g] = true
	}

	var hints []string
	for _, ga := range grantAuths {
		if !granted[ga.grant] {
			continue
		}
		if hasUnrecoveredRejection(reqs, ga.hosts) {
			hints = append(hints, ga.hint)
		}
	}
	return hints
}

// hasUnrecoveredRejection reports whether any of hosts ended on a 401/403
// rejection that was not followed by a successful (2xx) request to that same
// host. Requests are processed in chronological order (network.jsonl is appended
// as requests complete): a later rejection un-recovers an earlier success, and a
// later success recovers an earlier rejection. So [200, 401] flags (the auth
// failure came last) while [401, 200] and [200, 401, 200] do not.
func hasUnrecoveredRejection(reqs []storage.NetworkRequest, hosts []string) bool {
	rejected := make(map[string]bool)
	succeeded := make(map[string]bool)
	for _, req := range reqs {
		host := requestHost(req.URL)
		if host == "" || !hostMatches(host, hosts) {
			continue
		}
		switch {
		case req.StatusCode == 401 || req.StatusCode == 403:
			rejected[host] = true
			delete(succeeded, host) // a later rejection un-recovers a prior success
		case req.StatusCode >= 200 && req.StatusCode < 300:
			succeeded[host] = true
		}
	}
	for host := range rejected {
		if !succeeded[host] {
			return true
		}
	}
	return false
}

// hostMatches reports whether host equals or is a subdomain of any of hosts.
func hostMatches(host string, hosts []string) bool {
	for _, h := range hosts {
		if host == h || strings.HasSuffix(host, "."+h) {
			return true
		}
	}
	return false
}

// requestHost extracts the hostname from a request URL, dropping any port.
func requestHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return u.Hostname()
}
