package netrules

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// NetworkRuleEntry is the YAML representation of a single entry in network.rules.
// It handles both plain host strings and host-with-rules maps.
type NetworkRuleEntry struct {
	HostRules
}

// UnmarshalYAML handles both "host" strings and {"host": ["rule", ...]} maps.
func (e *NetworkRuleEntry) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		if value.Value == "" {
			return fmt.Errorf("network.rules entry: host cannot be empty")
		}
		e.Host = value.Value
		return nil

	case yaml.MappingNode:
		if len(value.Content) != 2 {
			return fmt.Errorf("network.rules entry must have exactly one host key, got %d", len(value.Content)/2)
		}
		e.Host = value.Content[0].Value
		if e.Host == "" {
			return fmt.Errorf("network.rules entry: host cannot be empty")
		}

		var ruleStrings []string
		if err := value.Content[1].Decode(&ruleStrings); err != nil {
			return fmt.Errorf("network.rules[%s]: %w", e.Host, err)
		}
		for _, rs := range ruleStrings {
			rule, err := ParseRule(rs)
			if err != nil {
				return fmt.Errorf("network.rules[%s]: %w", e.Host, err)
			}
			e.Rules = append(e.Rules, rule)
		}
		return nil

	default:
		return fmt.Errorf("network.rules entry must be a string or map, got %v", value.Kind)
	}
}
