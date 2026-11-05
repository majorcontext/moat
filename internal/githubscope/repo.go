// Package githubscope limits what a run's github grant can change on GitHub to
// the repositories that belong to the project being worked on.
//
// It has three parts:
//
//   - Discovery finds the GitHub repositories checked out in a workspace by
//     reading git config files directly (it never runs git).
//   - The approval Store records, outside the workspace, which repositories
//     the user has approved for each project. A repository that appears after
//     the first run is not trusted until the user approves it, so an agent
//     cannot widen its own access by cloning a repository or rewriting a
//     remote and waiting for the next run.
//   - Policy renders a Keep rule file for the proxy's "http" scope that denies
//     writes to any other repository.
package githubscope

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// GitHub's own limits: owners are 1-39 alphanumerics or single hyphens;
// repository names are 1-100 of [A-Za-z0-9._-]. Both are case-insensitive.
var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// NormalizeRepo validates an "owner/repo" name and returns it lowercased.
// GitHub treats owner and repository names case-insensitively, so all
// comparisons in this package use the lowercased form.
func NormalizeRepo(s string) (string, error) {
	owner, name, ok := strings.Cut(strings.TrimSpace(s), "/")
	if !ok || strings.Contains(name, "/") {
		return "", fmt.Errorf("invalid repository %q: use the form owner/repo (e.g. majorcontext/moat)", s)
	}
	name = strings.TrimSuffix(name, ".git")
	if !ownerPattern.MatchString(owner) {
		return "", fmt.Errorf("invalid repository %q: %q is not a valid GitHub owner name", s, owner)
	}
	if !namePattern.MatchString(name) || name == "." || name == ".." {
		return "", fmt.Errorf("invalid repository %q: %q is not a valid GitHub repository name", s, name)
	}
	return strings.ToLower(owner + "/" + name), nil
}

// ParseRemoteURL returns the lowercased "owner/repo" a git remote URL points
// at, or ok=false when the URL is not a github.com repository.
//
// Accepted forms:
//
//	https://github.com/owner/repo(.git)   (also http://, git://, with user@ or :port)
//	ssh://git@github.com(:22)/owner/repo(.git)   (also git+ssh://, ssh+git://)
//	git@github.com:owner/repo(.git)       (scp-like; any user)
//
// insteadOf rewrites are not applied: the proxy enforces the policy on the
// request GitHub actually receives, so a rewrite can only change which
// repositories discovery suggests, never what a push can reach.
func ParseRemoteURL(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}

	var host, path string
	if i := strings.Index(raw, "://"); i >= 0 {
		u, err := url.Parse(raw)
		if err != nil {
			return "", false
		}
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "git", "ssh", "git+ssh", "ssh+git":
		default:
			return "", false
		}
		host, path = u.Hostname(), u.Path
	} else {
		// scp-like syntax: [user@]host:path. Git only treats it as such when
		// there is no slash before the first colon.
		colon := strings.IndexByte(raw, ':')
		if colon < 0 || strings.Contains(raw[:colon], "/") {
			return "", false
		}
		host = raw[:colon]
		if at := strings.LastIndexByte(host, '@'); at >= 0 {
			host = host[at+1:]
		}
		path = raw[colon+1:]
	}

	if !strings.EqualFold(strings.TrimSuffix(host, "."), "github.com") {
		return "", false
	}
	path = strings.Trim(path, "/")
	repo, err := NormalizeRepo(path)
	if err != nil {
		return "", false
	}
	return repo, true
}
