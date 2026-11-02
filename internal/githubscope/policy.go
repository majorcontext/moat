package githubscope

import (
	"bytes"
	_ "embed"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

//go:embed github-workspace.yaml
var policyTemplate []byte

// PolicyTemplate returns the unrendered Keep rule file. Its defs hold example
// values, so it loads and can be tested with `keep test` as-is.
func PolicyTemplate() []byte {
	out := make([]byte, len(policyTemplate))
	copy(out, policyTemplate)
	return out
}

// PolicyScope is the Keep scope the proxy evaluates HTTP requests in.
const PolicyScope = "http"

// PolicyParams are the per-run values rendered into the policy.
type PolicyParams struct {
	// Repos are the lowercased owner/repo names the run may write to,
	// including canonical names of renamed repositories.
	Repos []string
	// RepoIDs are GitHub's numeric repository IDs for Repos.
	RepoIDs []int64
	// NodeIDs are GraphQL node IDs for Repos (both formats).
	NodeIDs []string
	// OtherRepos is OtherReposRead or OtherReposNone.
	OtherRepos string
}

// neverMatch is an RE2 pattern that matches no string, used when a list is
// empty so the policy treats nothing as an approved repository.
const neverMatch = `[^\s\S]`

// maxDefLen is Keep's limit on a def value's length.
const maxDefLen = 2048

// maxMessageRepos caps how many repository names a deny message lists.
const maxMessageRepos = 8

// nodeIDPattern bounds node IDs to the characters GitHub uses (base64 and
// the next-gen prefix underscore) so they can be embedded in a CEL string
// literal without escaping.
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_=+/-]{1,128}$`)

// PlanPolicyParams builds the policy parameters for a plan. GitHub identity
// comes from fresh (this run's lookups) when present, else from the identity
// cached on the project's approvals.
func PlanPolicyParams(plan *Plan, project *Project, fresh map[string]RepoMeta) PolicyParams {
	p := PolicyParams{OtherRepos: plan.OtherRepos}
	seen := map[string]bool{}
	addRepo := func(r string) {
		if r != "" && !seen[r] {
			seen[r] = true
			p.Repos = append(p.Repos, r)
		}
	}
	for _, c := range plan.Granted {
		addRepo(c.Repo)
		m, ok := fresh[c.Repo]
		if !ok && project != nil {
			if a, cached := project.Repos[c.Repo]; cached {
				m, ok = RepoMeta{FullName: a.FullName, ID: a.ID, NodeIDs: a.NodeIDs}, true
			}
		}
		if !ok {
			continue
		}
		addRepo(strings.ToLower(m.FullName))
		if m.ID > 0 {
			p.RepoIDs = append(p.RepoIDs, m.ID)
		}
		p.NodeIDs = append(p.NodeIDs, m.NodeIDs...)
	}
	return p
}

// RenderPolicy fills the template's defs and messages for one run.
func RenderPolicy(p PolicyParams) ([]byte, error) {
	repos := uniqueSorted(p.Repos)
	for _, r := range repos {
		if _, err := NormalizeRepo(r); err != nil {
			return nil, err
		}
	}
	var nodeIDs []string
	for _, id := range uniqueSorted(p.NodeIDs) {
		if !nodeIDPattern.MatchString(id) {
			return nil, fmt.Errorf("unexpected GitHub node ID %q", id)
		}
		nodeIDs = append(nodeIDs, id)
	}
	ids := make([]string, 0, len(p.RepoIDs))
	seenID := map[int64]bool{}
	for _, id := range p.RepoIDs {
		if id > 0 && !seenID[id] {
			seenID[id] = true
			ids = append(ids, strconv.FormatInt(id, 10))
		}
	}
	sort.Strings(ids)

	otherRead := "true"
	switch p.OtherRepos {
	case "", OtherReposRead:
	case OtherReposNone:
		otherRead = "false"
	default:
		return nil, fmt.Errorf("invalid github.other_repos %q", p.OtherRepos)
	}

	quoted := make([]string, len(repos))
	alts := make([]string, len(repos))
	for i, r := range repos {
		quoted[i] = strconv.Quote(r)
		alts[i] = regexp.QuoteMeta(r)
	}
	repoAlt := strings.Join(alts, "|")
	gitPath, apiPath, idPath := neverMatch, neverMatch, neverMatch
	if len(repos) > 0 {
		gitPath = `^/(` + repoAlt + `)(\.git)?(/|$)`
		apiPath = `^/repos/(` + repoAlt + `)(/|$)`
	}
	if len(ids) > 0 {
		idPath = `^/repositories/(` + strings.Join(ids, "|") + `)(/|$)`
	}
	quotedNodes := make([]string, len(nodeIDs))
	for i, id := range nodeIDs {
		quotedNodes[i] = strconv.Quote(id)
	}

	defs := map[string]string{
		"gh_ws_repos":    "[" + strings.Join(quoted, ", ") + "]",
		"gh_ws_git_path": "r'" + gitPath + "'",
		"gh_ws_api_path": "r'" + apiPath + "'",
		"gh_ws_id_path":  "r'" + idPath + "'",
		"gh_ws_node_ids": "[" + strings.Join(quotedNodes, ", ") + "]",
		"gh_other_read":  otherRead,
	}

	for name, value := range defs {
		if len(value) > maxDefLen {
			return nil, fmt.Errorf("too many approved GitHub repositories for one policy (%d; the %s list exceeds Keep's %d-character limit)\n"+
				"  Narrow the workspace or remove unnecessary checkouts and github.extra_repos entries; for a locked project, `moat github forget OWNER/REPO` also removes its approval", len(repos), name, maxDefLen)
		}
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(policyTemplate, &doc); err != nil {
		return nil, fmt.Errorf("parsing github policy template: %w", err)
	}
	root := doc.Content[0]
	defsNode := mappingValue(root, "defs")
	if defsNode == nil {
		return nil, fmt.Errorf("github policy template has no defs")
	}
	for name, value := range defs {
		v := mappingValue(defsNode, name)
		if v == nil {
			return nil, fmt.Errorf("github policy template has no def %q", name)
		}
		v.Value = value
		// Pick the style that needs no escaping, so the rendered file reads
		// like the CEL it holds.
		switch {
		case strings.ContainsAny(value, `\`):
			v.Style = yaml.LiteralStyle
		case strings.Contains(value, "'"):
			v.Style = yaml.DoubleQuotedStyle
		default:
			v.Style = yaml.SingleQuotedStyle
		}
		v.LineComment = ""
	}

	repoList := "no repositories"
	if len(repos) > 0 {
		shown := repos
		if len(shown) > maxMessageRepos {
			shown = shown[:maxMessageRepos]
		}
		repoList = strings.Join(shown, ", ")
		if extra := len(repos) - len(shown); extra > 0 {
			repoList += fmt.Sprintf(" and %d more", extra)
		}
	}
	if rules := mappingValue(root, "rules"); rules != nil {
		for _, rule := range rules.Content {
			if msg := mappingValue(rule, "message"); msg != nil {
				msg.Value = strings.ReplaceAll(msg.Value, "$REPOS", repoList)
			}
		}
	}

	out, err := marshalYAML(&doc)
	if err != nil {
		return nil, fmt.Errorf("rendering github policy: %w", err)
	}
	return out, nil
}

// marshalYAML encodes with two-space indentation, matching hand-written
// Keep rule files.
func marshalYAML(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// MergeHTTPPolicy appends the rules and defs of a user's http-scope Keep
// policy (network.keep_policy) to a rendered github policy. Keep evaluates one
// rule file per scope, so the two must agree on the scope-wide settings the
// github rules depend on: enforce mode, closed error handling, and
// case-insensitive matching.
func MergeHTTPPolicy(github, user []byte) ([]byte, error) {
	var gdoc, udoc yaml.Node
	if err := yaml.Unmarshal(github, &gdoc); err != nil {
		return nil, fmt.Errorf("parsing github policy: %w", err)
	}
	// Expand aliases and YAML merge keys before inspecting names/actions.
	// Otherwise a raw node named << could decode into a protected def later.
	var userFields map[string]any
	if err := yaml.Unmarshal(user, &userFields); err != nil {
		return nil, fmt.Errorf("network.keep_policy: %w", err)
	}
	normalizedUser, normalizeErr := yaml.Marshal(userFields)
	if normalizeErr != nil {
		return nil, fmt.Errorf("network.keep_policy: %w", normalizeErr)
	}
	if err := yaml.Unmarshal(normalizedUser, &udoc); err != nil {
		return nil, fmt.Errorf("network.keep_policy: %w", err)
	}
	if len(udoc.Content) == 0 || udoc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("network.keep_policy: expected a Keep rule file (a YAML mapping)")
	}
	groot, uroot := gdoc.Content[0], udoc.Content[0]

	// Check every scope-wide field before copying rules; dropping an unknown
	// setting would silently change the meaning of the user's policy.
	for i := 0; i+1 < len(uroot.Content); i += 2 {
		key, value := uroot.Content[i].Value, uroot.Content[i+1].Value
		switch key {
		case "scope":
			if value != PolicyScope {
				return nil, fmt.Errorf("network.keep_policy: expected scope: http, got %q", value)
			}
		case "version":
			if value != "v1" {
				return nil, fmt.Errorf("network.keep_policy: unsupported version %q", value)
			}
		case "mode", "on_error", "case_sensitive", "defs", "rules":
		default:
			return nil, fmt.Errorf("network.keep_policy: unsupported top-level field %q when github.scope is workspace", key)
		}
	}

	const fix = "\n  github.scope: workspace adds its rules to the same http scope, and Keep applies these settings to every rule in a scope."
	mode := "audit_only" // Keep's default when a rule file omits mode
	if v := mappingValue(uroot, "mode"); v != nil {
		mode = v.Value
	}
	if mode != "enforce" {
		return nil, fmt.Errorf("github.scope: workspace requires enforced HTTP policies; set network.keep_policy mode: enforce instead of %q (audit mode is called audit_only in Keep files)%s", mode, fix)
	}
	if v := mappingValue(uroot, "on_error"); v != nil && v.Value != "closed" {
		return nil, fmt.Errorf("network.keep_policy must not set on_error: %s when github.scope is workspace%s", v.Value, fix)
	}
	if v := mappingValue(uroot, "case_sensitive"); v != nil && v.Value == "true" {
		return nil, fmt.Errorf("network.keep_policy must not set case_sensitive: true when github.scope is workspace%s", fix)
	}

	// Keep expands defs before compiling and lets redact rules mutate params.
	// User policy must never rewrite the expressions or input of scope rules.
	protected := map[string]bool{}
	identifiers := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
	for _, rule := range mappingValue(groot, "rules").Content {
		if when := mappingValue(mappingValue(rule, "match"), "when"); when != nil {
			for _, name := range identifiers.FindAllString(when.Value, -1) {
				protected[name] = true
			}
		}
	}

	for _, field := range []struct {
		name string
		kind yaml.Kind
	}{{"defs", yaml.MappingNode}, {"rules", yaml.SequenceNode}} {
		if node := mappingValue(uroot, field.name); node != nil && node.Tag != "!!null" && node.Kind != field.kind {
			return nil, fmt.Errorf("network.keep_policy: invalid %s structure", field.name)
		}
	}

	if udefs := mappingValue(uroot, "defs"); udefs != nil && len(udefs.Content) > 0 {
		gdefs := mappingValue(groot, "defs")
		for i := 0; i+1 < len(udefs.Content); i += 2 {
			name := udefs.Content[i].Value
			if mappingValue(gdefs, name) != nil || protected[name] {
				return nil, fmt.Errorf("network.keep_policy: def %q conflicts with a github.scope identifier; rename it", name)
			}
			gdefs.Content = append(gdefs.Content, udefs.Content[i], udefs.Content[i+1])
		}
	}

	grules := mappingValue(groot, "rules")
	names := map[string]bool{}
	for _, r := range grules.Content {
		if n := mappingValue(r, "name"); n != nil {
			names[n.Value] = true
		}
	}
	if urules := mappingValue(uroot, "rules"); urules != nil {
		for _, r := range urules.Content {
			action := mappingValue(r, "action")
			if action == nil || (action.Value != "deny" && action.Value != "log") {
				return nil, fmt.Errorf("network.keep_policy: only deny and log actions are supported with github.scope: workspace; other actions can alter scope enforcement")
			}
			if n := mappingValue(r, "name"); n != nil && names[n.Value] {
				return nil, fmt.Errorf("network.keep_policy: rule name %q conflicts with a github.scope rule; rename it", n.Value)
			}
			grules.Content = append(grules.Content, r)
		}
	}

	out, err := marshalYAML(&gdoc)
	if err != nil {
		return nil, fmt.Errorf("merging network.keep_policy into the github policy: %w", err)
	}
	return out, nil
}

// mappingValue returns the value node for key in a YAML mapping node.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
