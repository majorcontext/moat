package githubscope

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"time"
)

// Scope values for github.scope in moat.yaml.
const (
	ScopeAny       = "any"
	ScopeWorkspace = "workspace"
)

// OtherRepos values for github.other_repos in moat.yaml.
const (
	OtherReposRead = "read"
	OtherReposNone = "none"
)

// SourceExtraRepos marks repositories listed in moat.yaml's extra_repos.
const SourceExtraRepos = "moat.yaml extra_repos"

// SourceAllowCommand marks repositories approved with `moat github allow`.
const SourceAllowCommand = "moat github allow"

// Settings is the github: block of moat.yaml, already validated.
type Settings struct {
	Scope      string   // ScopeAny (or "") or ScopeWorkspace
	OtherRepos string   // OtherReposRead (or "") or OtherReposNone
	ExtraRepos []string // normalized owner/repo names
}

// Enabled reports whether moat.yaml asks for the workspace scope.
func (s Settings) Enabled() bool { return s.Scope == ScopeWorkspace }

// Candidate is a repository the run could grant write access to.
type Candidate struct {
	Repo    string
	Sources []string
}

// Plan is the GitHub scope decision for one run. It is computed by BuildPlan,
// the single source of truth for both the CLI pre-flight (which prints it and
// asks about Pending repositories) and run creation (which enforces it), so
// the two cannot disagree about what a run may change.
type Plan struct {
	// ApprovalSnapshot is the store state used to build this plan and its policy.
	ApprovalSnapshot *Project

	// Project is the store key: the workspace's absolute, symlink-resolved path.
	Project string
	// Enabled is true when the run is scoped to its workspace repositories.
	Enabled bool
	// Sticky is true when the scope is enforced only because the project was
	// locked by an earlier run; moat.yaml no longer asks for it.
	Sticky bool
	// FirstRun is true when the project has no approvals yet. Every repository
	// found is approved (trust on first use) and the project is locked.
	FirstRun bool
	// LockedAt is when the project was locked (zero on the first run).
	LockedAt time.Time
	// Granted are the repositories this run may write to.
	Granted []Candidate
	// Pending were found in the workspace but are not approved. They get the
	// same access as any other repository until the user approves them.
	Pending []Candidate
	// Missing are approved repositories that were not found this run. They
	// are not granted; they stay approved for when they come back.
	Missing []string
	// OtherRepos is the access level for every repository not in Granted.
	OtherRepos string
	// Truncated reports that discovery stopped early on a very large workspace.
	Truncated bool
}

// GrantedRepos returns the names in Granted.
func (p *Plan) GrantedRepos() []string {
	out := make([]string, 0, len(p.Granted))
	for _, c := range p.Granted {
		out = append(out, c.Repo)
	}
	return out
}

// ProjectKey returns the store key for a workspace path: absolute with
// symlinks resolved, so the same project reached by different paths shares
// one record.
func ProjectKey(workspace string) (string, error) {
	abs, err := filepath.Abs(workspace)
	if err != nil {
		return "", fmt.Errorf("resolving project path %s: %w", workspace, err)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		abs = real
	}
	return filepath.Clean(abs), nil
}

// BuildPlan decides what a run in workspace may change on GitHub. existing is
// the project's stored record, or nil when it has none.
func BuildPlan(workspace string, settings Settings, existing *Project) (*Plan, error) {
	key, err := ProjectKey(workspace)
	if err != nil {
		return nil, err
	}
	locked := existing != nil && !existing.LockedAt.IsZero()
	plan := &Plan{
		Project:          key,
		ApprovalSnapshot: existing,
		Enabled:          settings.Enabled() || locked,
		Sticky:           !settings.Enabled() && locked,
	}
	if !plan.Enabled {
		return plan, nil
	}
	plan.OtherRepos = settings.OtherRepos
	if plan.Sticky {
		plan.OtherRepos = existing.OtherRepos
	}
	if plan.OtherRepos == "" {
		plan.OtherRepos = OtherReposRead
	}
	if locked {
		plan.LockedAt = existing.LockedAt
	}

	found, err := Discover(key)
	if err != nil {
		return nil, err
	}
	plan.Truncated = found.Truncated

	present := map[string][]string{}
	for _, f := range found.Repos {
		present[f.Repo] = append(present[f.Repo], f.Sources...)
	}
	// extra_repos are only honored while moat.yaml asks for the scope; a
	// sticky run ignores the rest of the github: block too.
	if !plan.Sticky {
		for _, r := range settings.ExtraRepos {
			present[r] = append(present[r], SourceExtraRepos)
		}
	}
	// Repositories approved with `moat github allow` were named explicitly on
	// the host, so they are granted whether or not they are checked out.
	if existing != nil {
		for _, repo := range existing.RepoNames() {
			if existing.Repos[repo].Source == SourceAllowCommand {
				present[repo] = append(present[repo], SourceAllowCommand)
			}
		}
	}

	plan.FirstRun = !locked
	for _, repo := range sortedKeys(present) {
		c := Candidate{Repo: repo, Sources: present[repo]}
		if plan.FirstRun {
			plan.Granted = append(plan.Granted, c)
			continue
		}
		if _, ok := existing.Repos[repo]; ok {
			plan.Granted = append(plan.Granted, c)
		} else {
			plan.Pending = append(plan.Pending, c)
		}
	}
	if existing != nil {
		for _, repo := range existing.RepoNames() {
			if _, ok := present[repo]; !ok {
				plan.Missing = append(plan.Missing, repo)
			}
		}
	}
	return plan, nil
}

// RecordFirstRun locks the project and approves every granted repository when
// the plan is a first run. It is a no-op otherwise. If another process locked
// the project in the meantime, that record wins.
func RecordFirstRun(store *Store, plan *Plan, now time.Time) error {
	return commitRun(store, plan, nil, now, true)
}

// CommitRun records what a scoped run established, once the run's policy has
// been built: on a first run it locks the project and approves the granted
// repositories; when moat.yaml asked for the scope it remembers other_repos
// for later sticky runs; and it caches the repositories' GitHub identity.
// Call it only after everything that can reject the run's configuration has
// passed, so a run that never starts does not lock the project.
func CommitRun(store *Store, plan *Plan, meta map[string]RepoMeta, now time.Time) error {
	return commitRun(store, plan, meta, now, false)
}

func commitRun(store *Store, plan *Plan, meta map[string]RepoMeta, now time.Time, firstOnly bool) error {
	if !plan.Enabled {
		return nil
	}
	return store.Update(plan.Project, func(p *Project) (*Project, error) {
		if firstOnly && p != nil && !p.LockedAt.IsZero() {
			return p, nil
		}
		if !reflect.DeepEqual(p, plan.ApprovalSnapshot) {
			return nil, fmt.Errorf("GitHub approvals changed while preparing this run; retry the run to use the current approvals")
		}
		if p == nil {
			p = &Project{}
		}
		if p.Repos == nil {
			p.Repos = map[string]Approval{}
		}
		if p.LockedAt.IsZero() && plan.FirstRun {
			p.LockedAt = now
			for _, c := range plan.Granted {
				if _, ok := p.Repos[c.Repo]; !ok {
					p.Repos[c.Repo] = Approval{ApprovedAt: now, Source: firstSource(c.Sources)}
				}
			}
		}
		if p.LockedAt.IsZero() {
			// Not locked and not a first run: nothing to record. Keep an
			// existing record (approvals from `moat github allow`) as is,
			// and do not create an empty one.
			if len(p.Repos) == 0 {
				return nil, nil
			}
			return p, nil
		}
		if !plan.Sticky {
			p.OtherRepos = plan.OtherRepos
		}
		for repo, m := range meta {
			if a, ok := p.Repos[repo]; ok {
				a.FullName, a.ID, a.NodeIDs = m.FullName, m.ID, m.NodeIDs
				p.Repos[repo] = a
			}
		}
		return p, nil
	})
}

// Approve adds repositories to the project's approvals. It does not lock the
// project: a project that has never run keeps its first-run behavior, so
// approving one extra repository up front does not turn the workspace's own
// repositories into pending ones.
func Approve(store *Store, project string, cands []Candidate, now time.Time) error {
	return store.Update(project, func(p *Project) (*Project, error) {
		if p == nil {
			p = &Project{}
		}
		if p.Repos == nil {
			p.Repos = map[string]Approval{}
		}
		for _, c := range cands {
			if a, ok := p.Repos[c.Repo]; ok {
				// Explicit host approval persists even after the checkout is
				// removed. Preserve the approval time and cached identity.
				if firstSource(c.Sources) == SourceAllowCommand {
					a.Source = SourceAllowCommand
					p.Repos[c.Repo] = a
				}
				continue
			}
			p.Repos[c.Repo] = Approval{ApprovedAt: now, Source: firstSource(c.Sources)}
		}
		return p, nil
	})
}

// Forget removes repositories from the project's approvals. It returns the
// names that were not approved. The project stays locked.
func Forget(store *Store, project string, repos []string) (notFound []string, err error) {
	err = store.Update(project, func(p *Project) (*Project, error) {
		for _, r := range repos {
			if p == nil {
				notFound = append(notFound, r)
				continue
			}
			if _, ok := p.Repos[r]; !ok {
				notFound = append(notFound, r)
				continue
			}
			delete(p.Repos, r)
		}
		return p, nil
	})
	return notFound, err
}

// Reset deletes the project's record: its approvals and its lock. The next
// run with github.scope: workspace starts over as a first run.
func Reset(store *Store, project string) (existed bool, err error) {
	err = store.Update(project, func(p *Project) (*Project, error) {
		existed = p != nil
		return nil, nil
	})
	return existed, err
}

// SetRepoMeta caches GitHub's identity for approved repositories.
func SetRepoMeta(store *Store, project string, meta map[string]RepoMeta) error {
	if len(meta) == 0 {
		return nil
	}
	return store.Update(project, func(p *Project) (*Project, error) {
		if p == nil {
			return nil, nil
		}
		for repo, m := range meta {
			a, ok := p.Repos[repo]
			if !ok {
				continue
			}
			a.FullName, a.ID, a.NodeIDs = m.FullName, m.ID, m.NodeIDs
			p.Repos[repo] = a
		}
		return p, nil
	})
}

// FindProject returns the project `moat github` commands act on, walking up
// from dir: the nearest directory with a stored record (so a command run from
// a subdirectory — even one with its own moat.yaml — reaches the project its
// runs use), else the nearest directory containing moat.yaml, else dir.
func FindProject(dir string, store *Store) (string, error) {
	start, err := ProjectKey(dir)
	if err != nil {
		return "", err
	}
	nearestConfig := ""
	for d := start; ; {
		if store != nil {
			if p, getErr := store.Get(d); getErr != nil {
				return "", getErr
			} else if p != nil {
				return d, nil
			}
		}
		if nearestConfig == "" {
			if fi, statErr := os.Stat(filepath.Join(d, "moat.yaml")); statErr == nil && fi.Mode().IsRegular() {
				nearestConfig = d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	if nearestConfig != "" {
		return nearestConfig, nil
	}
	return start, nil
}

func firstSource(sources []string) string {
	if len(sources) == 0 {
		return ""
	}
	return sources[0]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
