package moatinit

import "strings"

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
