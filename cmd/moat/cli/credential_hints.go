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
		hint:  "GitHub rejected the injected credential — the stored token may be expired. Run `moat grant github` to refresh it.",
	},
}

// credentialRejectionHints scans a finished run's network log for auth
// rejections (401/403) on hosts tied to one of the run's grants, returning an
// actionable re-grant hint per affected grant. An expired stored token
// otherwise surfaces only as an opaque downstream error (e.g. git's
// "could not read Username"), so this points the user at the real fix.
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
		if hasAuthRejection(reqs, ga.hosts) {
			hints = append(hints, ga.hint)
		}
	}
	return hints
}

// hasAuthRejection reports whether any request to one of hosts was rejected with
// a 401 or 403.
func hasAuthRejection(reqs []storage.NetworkRequest, hosts []string) bool {
	for _, req := range reqs {
		if req.StatusCode != 401 && req.StatusCode != 403 {
			continue
		}
		host := requestHost(req.URL)
		if host == "" {
			continue
		}
		for _, h := range hosts {
			if host == h || strings.HasSuffix(host, "."+h) {
				return true
			}
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
