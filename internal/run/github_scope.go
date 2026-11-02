package run

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/daemon"
	"github.com/majorcontext/moat/internal/githubscope"
	"github.com/majorcontext/moat/internal/log"
	"github.com/majorcontext/moat/internal/ui"
	"gopkg.in/yaml.v3"
)

// githubScopeDocsURL is the guide linked from github.scope errors.
const githubScopeDocsURL = "https://majorcontext.com/moat/guides/github-scope"

// githubScopeStorePath returns the approval store location. Tests override it.
var githubScopeStorePath = func() string {
	return githubscope.DefaultStorePath(config.GlobalConfigDir())
}

// githubLookupRepos fetches repository identity from GitHub. Tests override it.
var githubLookupRepos = func(ctx context.Context, token string, repos []string) (map[string]githubscope.RepoMeta, map[string]error) {
	return githubscope.LookupRepos(ctx, &http.Client{Timeout: 10 * time.Second}, githubscope.DefaultAPIURL, token, repos)
}

// OpenGitHubScopeStore opens the per-project approval store.
func OpenGitHubScopeStore() (*githubscope.Store, error) {
	return githubscope.OpenStore(githubScopeStorePath())
}

// PlanGitHubScope decides what a run in workspace may change on GitHub. It is
// the single source of truth for the CLI pre-flight (which prints the plan
// and asks about pending repositories) and Create (which enforces it).
//
// The plan is disabled when the run has no github grant: with no token in the
// proxy there is nothing to scope.
func PlanGitHubScope(store *githubscope.Store, workspace string, grants []string, cfg *config.Config) (*githubscope.Plan, error) {
	if !hasGrant(normalizeCopilotGrantNames(grants), "github") {
		return &githubscope.Plan{}, nil
	}
	key, err := githubscope.ProjectKey(workspace)
	if err != nil {
		return nil, err
	}
	existing, err := store.Get(key)
	if err != nil {
		return nil, err
	}
	var settings githubscope.Settings
	if cfg != nil {
		settings = cfg.GitHub.Settings()
	}
	return githubscope.BuildPlan(workspace, settings, existing)
}

// checkGitHubScopeConflicts fails fast on configurations that would let a
// run change GitHub repositories without passing through the policy. The
// scope is only useful if every path to GitHub with the user's credentials
// goes through the proxy's HTTP policy; each case below is a path that does
// not.
func checkGitHubScopeConflicts(plan *githubscope.Plan, grants []string, cfg *config.Config) error {
	why := "github.scope: workspace is enabled"
	if plan.Sticky {
		why = fmt.Sprintf("this project was locked to its GitHub repositories on %s (github.scope: workspace)", plan.LockedAt.Format("2006-01-02"))
	}
	unlock := "Remove it, or turn the scope off"
	if plan.Sticky {
		unlock = "Remove it, or unlock the project with `moat github forget --all`"
	}

	// Any SSH grant, not only ssh:github.com: the SSH agent bridge cannot
	// tell which host a signature is for, so a key granted for one host
	// (ssh:gitlab.com) also signs a connection to github.com or
	// ssh.github.com:443, and git push over SSH never reaches the HTTPS policy.
	for _, g := range grants {
		if strings.HasPrefix(g, "ssh:") {
			return fmt.Errorf("%s, but the run also has the %s grant.\n"+
				"  git push over SSH bypasses the HTTPS policy that enforces the scope, and moat's SSH agent\n"+
				"  cannot limit a key to one destination host — any granted key that GitHub accepts could push.\n"+
				"  Remove SSH grants from this run; HTTPS remotes use the github grant automatically.\n"+
				"  If the workspace uses SSH remotes (git@github.com:...), switch them to https://github.com/... .\n"+
				"  See %s", why, g, githubScopeDocsURL)
		}
	}
	if cfg != nil {
		for _, m := range cfg.MCP {
			if m.Auth != nil && strings.Split(m.Auth.Grant, ":")[0] == "github" {
				return fmt.Errorf("%s, but MCP server %q uses the github grant.\n"+
					"  Remote MCP servers are not covered by the GitHub scope policy, so its tools could change any repository.\n"+
					"  %s, or use the gh CLI inside the container instead. See %s", why, m.Name, unlock, githubScopeDocsURL)
			}
		}
	}
	if configUsesCopilotCLI(cfg) {
		return fmt.Errorf("%s, but this is a GitHub Copilot run.\n"+
			"  Copilot reaches GitHub through Copilot API hosts that the scope policy does not cover yet.\n"+
			"  %s to run Copilot. See %s", why, unlock, githubScopeDocsURL)
	}
	return nil
}

// GitHubScopeConflict reports the configuration that would let a scoped run
// bypass the policy, or nil. The CLI pre-flight uses it to skip prompting for
// a run Create is about to reject.
func GitHubScopeConflict(plan *githubscope.Plan, grants []string, cfg *config.Config) error {
	return checkGitHubScopeConflicts(plan, normalizeCopilotGrantNames(grants), cfg)
}

// warnGitHubTokenInEnv warns when moat.yaml or -e hands a GitHub token to the
// container directly. Such a token never passes through the proxy, so the
// scope cannot restrict it.
func warnGitHubTokenInEnv(cfg *config.Config, env []string) {
	for _, name := range []string{"GITHUB_TOKEN", "GH_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_PAT"} {
		where := ""
		if cfg != nil {
			_, inEnv := cfg.Env[name]
			_, inSecrets := cfg.Secrets[name]
			if inEnv || inSecrets {
				where = "in moat.yaml"
			}
		}
		for _, kv := range env {
			if k, _, _ := strings.Cut(kv, "="); k == name {
				where = "with -e"
			}
		}
		if where != "" {
			ui.Warnf("%s is set %s. A token passed to the container directly is not limited by github.scope; only the github grant is.", name, where)
		}
	}
}

// prepareGitHubScope is Create's gate. It returns the run's plan, or nil when
// the run is not scoped. It records nothing: commitGitHubScope does that once
// the container has been created, so a run rejected later in Create does not
// lock the project.
func prepareGitHubScope(opts Options) (*githubscope.Plan, error) {
	if !hasGrant(opts.Grants, "github") {
		return nil, nil
	}
	store, err := OpenGitHubScopeStore()
	if err != nil {
		return nil, fmt.Errorf("github.scope: %w", err)
	}
	defer store.Close()

	plan, err := PlanGitHubScope(store, opts.Workspace, opts.Grants, opts.Config)
	if err != nil {
		return nil, fmt.Errorf("github.scope: %w", err)
	}
	if !plan.Enabled {
		return nil, nil
	}
	if err := checkGitHubScopeConflicts(plan, opts.Grants, opts.Config); err != nil {
		return nil, err
	}
	warnGitHubTokenInEnv(opts.Config, opts.Env)
	return plan, nil
}

// commitGitHubScope records the run in the approval store: on a first run it
// locks the project and approves what was found; it also remembers
// other_repos and caches repository identity. Create calls it after the
// run's policy is built and merged.
func commitGitHubScope(plan *githubscope.Plan, meta map[string]githubscope.RepoMeta) error {
	store, err := OpenGitHubScopeStore()
	if err != nil {
		return fmt.Errorf("github.scope: %w", err)
	}
	defer store.Close()
	if err := githubscope.CommitRun(store, plan, meta, time.Now()); err != nil {
		return fmt.Errorf("github.scope: recording approved repositories: %w", err)
	}
	return nil
}

// githubScopePolicy renders the run's Keep policy for the http scope. It
// looks up the GitHub identity (numeric and GraphQL node IDs) of granted
// repositories on every run, using the run's github credential,
// and returns what it found so commitGitHubScope can cache it.
func githubScopePolicy(ctx context.Context, plan *githubscope.Plan, token string) ([]byte, map[string]githubscope.RepoMeta, error) {
	project := plan.ApprovalSnapshot
	repos := plan.GrantedRepos()
	var meta map[string]githubscope.RepoMeta
	if len(repos) > 0 && token != "" {
		lookupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		var errs map[string]error
		meta, errs = githubLookupRepos(lookupCtx, token, repos)
		cancel()
		for repo, lookupErr := range errs {
			log.Debug("github.scope: repository lookup failed", "repo", repo, "error", lookupErr)
		}
		if len(errs) > 0 {
			failed := make([]string, 0, len(errs))
			for repo := range errs {
				failed = append(failed, fmt.Sprintf("%s (%s)", repo, githubscope.LookupFailureReason(errs[repo])))
			}
			slices.Sort(failed)
			ui.Warnf("Could not refresh %s on GitHub; using cached IDs where available. GraphQL mutations for repositories without cached IDs (e.g. gh pr create) may be blocked this run.",
				strings.Join(failed, ", "))
		}
	}

	policy, renderErr := githubscope.RenderPolicy(githubscope.PlanPolicyParams(plan, project, meta))
	if renderErr != nil {
		return nil, nil, fmt.Errorf("github.scope: %w", renderErr)
	}
	return policy, meta, nil
}

// mergeGitHubScopePolicy folds the run's github policy into the http-scope
// policy. Keep evaluates one rule file per scope, so a network.keep_policy
// — either file/pack YAML in policyYAML["http"] or an inline deny list in
// policyRuleSets — is merged into the same file, github rules first.
func mergeGitHubScopePolicy(github []byte, policyYAML map[string][]byte, policyRuleSets []daemon.PolicyRuleSetSpec) (map[string][]byte, []daemon.PolicyRuleSetSpec, error) {
	merged := github
	if user, ok := policyYAML[githubscope.PolicyScope]; ok {
		m, err := githubscope.MergeHTTPPolicy(merged, user)
		if err != nil {
			return nil, nil, err
		}
		merged = m
	}
	var rest []daemon.PolicyRuleSetSpec
	for _, rs := range policyRuleSets {
		if rs.Scope != githubscope.PolicyScope {
			rest = append(rest, rs)
			continue
		}
		inline, err := ruleSetYAML(rs)
		if err != nil {
			return nil, nil, fmt.Errorf("network.keep_policy: %w", err)
		}
		m, err := githubscope.MergeHTTPPolicy(merged, inline)
		if err != nil {
			return nil, nil, err
		}
		merged = m
	}

	if err := keeplib.ValidateRuleBytes(merged); err != nil {
		return nil, nil, fmt.Errorf("github.scope policy validation: %w", err)
	}
	if err := checkGitBodiesNotInspected(merged); err != nil {
		return nil, nil, err
	}
	if policyYAML == nil {
		policyYAML = map[string][]byte{}
	}
	policyYAML[githubscope.PolicyScope] = merged
	return policyYAML, rest, nil
}

// ruleSetYAML renders an inline deny list as a Keep rule file with the same
// rules keep.RuleSet builds for it (one deny rule per operation glob, in
// order), but with rule names that are valid for any operation string —
// RuleSet derives names from the operation, which an HTTP operation such as
// "DELETE api.example.com/*" cannot be.
func ruleSetYAML(rs daemon.PolicyRuleSetSpec) ([]byte, error) {
	mode := rs.Mode
	switch mode {
	case "", "enforce":
		mode = "enforce"
	case "audit":
		mode = "audit_only"
	default:
		return nil, fmt.Errorf("network.keep_policy: invalid mode %q (expected enforce or audit)", mode)
	}
	type match struct {
		Operation string `yaml:"operation"`
	}
	type rule struct {
		Name    string `yaml:"name"`
		Match   match  `yaml:"match"`
		Action  string `yaml:"action"`
		Message string `yaml:"message"`
	}
	doc := struct {
		Scope string `yaml:"scope"`
		Mode  string `yaml:"mode"`
		Rules []rule `yaml:"rules"`
	}{Scope: rs.Scope, Mode: mode}
	for i, op := range rs.Deny {
		doc.Rules = append(doc.Rules, rule{
			Name:    fmt.Sprintf("network-keep-policy-deny-%d", i+1),
			Match:   match{Operation: op},
			Action:  "deny",
			Message: op + " is not allowed",
		})
	}
	return yaml.Marshal(doc)
}

// checkGitBodiesNotInspected rejects a merged policy that would read git's
// request bodies. A network.keep_policy body rule without an operation applies
// to every request, and the proxy denies non-JSON bodies once it inspects
// them — which would block every git fetch and push over HTTPS.
func checkGitBodiesNotInspected(policy []byte) error {
	eng, err := keeplib.LoadFromBytes(policy)
	if err != nil {
		return fmt.Errorf("github.scope policy: %w", err)
	}
	defer eng.Close()
	push := keeplib.NewHTTPCall("POST", "github.com", "/owner/repo.git/git-receive-pack")
	if eng.RequiresBodyFor(githubscope.PolicyScope, push) {
		return fmt.Errorf("network.keep_policy has a rule that reads params.body without an operation, which would block git over HTTPS when github.scope is workspace.\n" +
			"  Pin each body rule to the requests it inspects, e.g.\n" +
			"    match:\n      operation: \"post api.example.com/*\"\n" +
			"  See https://majorcontext.com/moat/reference/moat-yaml")
	}
	return nil
}

// checkGitHubScopeCapability verifies the daemon reads request bodies per
// rule operation. An older daemon would inspect git's non-JSON bodies and
// deny every push and fetch.
func checkGitHubScopeCapability(daemonCapabilities []string) error {
	if !slices.Contains(daemonCapabilities, daemon.CapKeepScopedBody) {
		return fmt.Errorf("proxy daemon is too old for github.scope: workspace (missing '%s' capability); run 'moat proxy restart' to upgrade", daemon.CapKeepScopedBody)
	}
	return nil
}
