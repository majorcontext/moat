package keep

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// PolicyConfig represents a Keep policy parsed from moat.yaml.
// It accepts three shapes:
//   - Starter pack name: plain string without "/" or ".yaml" suffix
//   - File path: string containing "/" or ending in ".yaml"
//   - Inline rules: YAML mapping with deny/mode fields
type PolicyConfig struct {
	Pack string   `yaml:"-"`
	File string   `yaml:"-"`
	Deny []string `yaml:"deny,omitempty"`
	Mode string   `yaml:"mode,omitempty"`
}

func (p *PolicyConfig) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		s := node.Value
		if strings.Contains(s, "/") || strings.HasSuffix(s, ".yaml") || strings.HasSuffix(s, ".yml") {
			p.File = s
		} else {
			p.Pack = s
		}
		return nil
	case yaml.MappingNode:
		type policyAlias PolicyConfig
		var alias policyAlias
		if err := node.Decode(&alias); err != nil {
			return fmt.Errorf("invalid inline policy: %w", err)
		}
		if !ValidModes[alias.Mode] {
			return fmt.Errorf("invalid policy mode %q: must be \"enforce\" or \"audit\"", alias.Mode)
		}
		if len(alias.Deny) == 0 {
			return fmt.Errorf("inline policy must have at least one deny rule; use a file path or pack name for file-based policies")
		}
		*p = PolicyConfig(alias)
		return nil
	default:
		return fmt.Errorf("policy must be a string (file path or pack name) or mapping (inline rules), got %v", node.Kind)
	}
}

func (p *PolicyConfig) IsInline() bool {
	return p.Pack == "" && p.File == "" && len(p.Deny) > 0
}

func (p *PolicyConfig) IsFile() bool {
	return p.File != ""
}

func (p *PolicyConfig) IsPack() bool {
	return p.Pack != ""
}

// ValidModes lists the accepted values for PolicyConfig.Mode.
var ValidModes = map[string]bool{
	"":        true,
	"enforce": true,
	"audit":   true,
}

// ToKeepYAML converts inline rules to Keep's native YAML rule format.
// Listed deny operations get deny rules; everything else is implicitly
// allowed (Keep's default behavior for unmatched calls).
func (p *PolicyConfig) ToKeepYAML(scope string) ([]byte, error) {
	if !p.IsInline() {
		return nil, fmt.Errorf("ToKeepYAML called on non-inline policy")
	}

	mode := p.Mode
	if mode == "" {
		mode = "enforce"
	}
	// Keep uses "audit_only", moat.yaml uses "audit" for brevity.
	if mode == "audit" {
		mode = "audit_only"
	}

	var rules []keepRule

	for _, op := range p.Deny {
		rules = append(rules, keepRule{
			Name:    "deny-" + op,
			Match:   keepMatch{Operation: op},
			Action:  "deny",
			Message: "Operation blocked by policy.",
		})
	}

	doc := keepRuleDoc{
		Scope: scope,
		Mode:  mode,
		Rules: rules,
	}

	return yaml.Marshal(doc)
}

type keepRuleDoc struct {
	Scope string     `yaml:"scope"`
	Mode  string     `yaml:"mode"`
	Rules []keepRule `yaml:"rules"`
}

type keepRule struct {
	Name    string    `yaml:"name"`
	Match   keepMatch `yaml:"match"`
	Action  string    `yaml:"action"`
	Message string    `yaml:"message,omitempty"`
}

type keepMatch struct {
	Operation string `yaml:"operation"`
}

// ResolvePolicyYAML resolves a PolicyConfig into raw YAML bytes
// suitable for keep.LoadFromBytes(). baseDir is used to resolve
// relative file paths; if empty, paths are used as-is.
func ResolvePolicyYAML(pc *PolicyConfig, scope, baseDir string) ([]byte, error) {
	switch {
	case pc.IsInline():
		return pc.ToKeepYAML(scope)
	case pc.IsFile():
		path := pc.File
		if baseDir != "" && !filepath.IsAbs(path) {
			path = filepath.Join(baseDir, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("policy file not found: %s: %w", pc.File, err)
		}
		return data, nil
	case pc.IsPack():
		data, err := GetStarterPack(pc.Pack)
		if err != nil {
			return nil, err
		}
		// Rewrite scope to match how the engine will be keyed and evaluated.
		var doc map[string]any
		if unmarshalErr := yaml.Unmarshal(data, &doc); unmarshalErr != nil {
			return nil, fmt.Errorf("failed to parse starter pack %q: %w", pc.Pack, unmarshalErr)
		}
		doc["scope"] = scope
		rewritten, marshalErr := yaml.Marshal(doc)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to rewrite starter pack %q scope: %w", pc.Pack, marshalErr)
		}
		return rewritten, nil
	default:
		return nil, fmt.Errorf("empty policy config")
	}
}
