// Package support tracks upstream release-line lifecycles, not patch allowlists.
package support

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Catalog struct {
	Dependencies map[string]Product `json:"dependencies"`
}
type Product struct {
	Source string `json:"source"`
	// Published-only products have no upstream EOL policy; age alone is not evidence of unsupported releases.
	PublishedOnly bool   `json:"published_only,omitempty"`
	Lines         []Line `json:"lines"`
}
type Line struct {
	Cycle    string `json:"cycle"`
	Released string `json:"released"`
	End      string `json:"end,omitempty"`
	Ended    bool   `json:"ended,omitempty"`
}

func parts(version string) []int {
	version = strings.TrimPrefix(version, "v")
	var result []int
	for _, part := range strings.Split(version, ".") {
		end := 0
		for end < len(part) && part[end] >= '0' && part[end] <= '9' {
			end++
		}
		if end == 0 {
			return result
		}
		number, err := strconv.Atoi(part[:end])
		if err != nil {
			return nil
		}
		result = append(result, number)
		if end != len(part) {
			break
		}
	}
	return result
}

func Compare(a, b string) int {
	ap, bp := parts(a), parts(b)
	for i := 0; i < len(ap) || i < len(bp); i++ {
		av, bv := 0, 0
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		if av < bv {
			return -1
		}
		if av > bv {
			return 1
		}
	}
	return 0
}

func compatible(a, b []int) bool {
	if len(a) == 0 || len(b) == 0 {
		return false
	}
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (l Line) active(now time.Time) bool {
	start, err := time.Parse(time.DateOnly, l.Released)
	if err != nil || now.Before(start) {
		return false
	}
	if l.Ended {
		return false
	}
	if l.End == "" {
		return true
	}
	end, err := time.Parse(time.DateOnly, l.End)
	return err == nil && now.Before(end.AddDate(0, 0, 1))
}

func (p Product) Versions(now time.Time) []string {
	var versions []string
	for _, line := range p.Lines {
		if p.PublishedOnly || line.active(now) {
			versions = append(versions, line.Cycle)
		}
	}
	sort.Slice(versions, func(i, j int) bool { return Compare(versions[i], versions[j]) < 0 })
	if p.PublishedOnly && len(versions) > 8 {
		versions = versions[len(versions)-8:]
	}
	return versions
}

// Check rejects known EOL lines. A patch within a maintained line and a newly
// published line newer than the catalog are not blocked by stale metadata.
// Actual artifact availability remains the upstream installer's responsibility.
func (c Catalog) Check(name, version string, now time.Time) error {
	product, ok := c.Dependencies[name]
	if !ok || product.PublishedOnly || version == "" {
		return nil
	}
	requested := parts(version)
	if len(requested) == 0 {
		return nil
	}
	var matched *Line
	for i := range product.Lines {
		line := &product.Lines[i]
		if compatible(requested, parts(line.Cycle)) && (matched == nil || Compare(line.Cycle, matched.Cycle) > 0) {
			matched = line
		}
	}
	if matched != nil && matched.active(now) {
		return nil
	}
	versions := product.Versions(now)
	if matched == nil {
		// Unknown future releases must not require a new Moat binary. Unknown older
		// lines below the maintained floor cannot be a supported release either.
		if len(versions) == 0 || Compare(version, versions[0]) >= 0 {
			return nil
		}
	}
	reason := "is no longer supported upstream"
	if matched != nil {
		start, _ := time.Parse(time.DateOnly, matched.Released)
		if now.Before(start) {
			reason = "has not been released upstream"
		} else if matched.End != "" {
			reason = "reached upstream end of support on " + matched.End
		}
	}
	advice := "upgrade Moat to refresh support metadata"
	if len(versions) > 0 {
		advice = "use a maintained release line (" + strings.Join(versions, ", ") + ") or " + advice
	}
	return fmt.Errorf("%s@%s %s; %s. Source: %s", name, version, reason, advice, product.Source)
}

// Default preserves a maintained default. After EOL, choose a maintained
// successor rather than making unversioned configurations fail with age.
// Prefer the oldest successor with at least 90 days left; Go uses the newest
// supported toolchain so current Go tools can build.
func (c Catalog) Default(name, current string, now time.Time) (string, error) {
	if err := c.Check(name, current, now); err == nil {
		return current, nil
	}
	product := c.Dependencies[name]
	versions := product.Versions(now)
	if name == "go" && len(versions) > 0 {
		return versions[len(versions)-1], nil
	}
	fallback := ""
	for _, version := range versions {
		if Compare(version, current) <= 0 {
			continue
		}
		if fallback == "" {
			fallback = version
		}
		for _, line := range product.Lines {
			if line.Cycle != version {
				continue
			}
			end, _ := time.Parse(time.DateOnly, line.End)
			if line.End == "" || end.After(now.AddDate(0, 0, 90)) {
				return version, nil
			}
		}
	}
	if fallback != "" {
		return fallback, nil
	}
	return "", fmt.Errorf("%s has no maintained default successor; upgrade Moat to refresh support metadata", name)
}

func (c Catalog) Validate() error {
	if len(c.Dependencies) == 0 {
		return fmt.Errorf("empty dependency support catalog")
	}
	for name, product := range c.Dependencies {
		if product.Source == "" || (len(product.Lines) == 0 && !product.PublishedOnly) {
			return fmt.Errorf("%s: missing source or release lines", name)
		}
		seen := map[string]bool{}
		for _, line := range product.Lines {
			numbers := parts(line.Cycle)
			if len(numbers) == 0 || strings.Trim(line.Cycle, "0123456789.") != "" || strings.HasSuffix(line.Cycle, ".") || strings.Contains(line.Cycle, "..") || seen[line.Cycle] {
				return fmt.Errorf("%s: invalid or duplicate cycle %q", name, line.Cycle)
			}
			seen[line.Cycle] = true
			start, err := time.Parse(time.DateOnly, line.Released)
			if err != nil {
				return fmt.Errorf("%s: invalid release date: %w", name, err)
			}
			if line.End != "" {
				end, err := time.Parse(time.DateOnly, line.End)
				if err != nil || end.Before(start) {
					return fmt.Errorf("%s: invalid end date %q", name, line.End)
				}
			}
		}
	}
	return nil
}
