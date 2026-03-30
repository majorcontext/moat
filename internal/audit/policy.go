package audit

// EntryPolicy is the entry type for Keep policy evaluation decisions.
const EntryPolicy EntryType = "policy"

// PolicyDecisionData holds Keep policy evaluation entry data.
type PolicyDecisionData struct {
	Scope     string `json:"scope"`
	Operation string `json:"operation"`
	Decision  string `json:"decision"`
	Rule      string `json:"rule,omitempty"`
	Message   string `json:"message,omitempty"`
}

// AppendPolicy adds a policy decision entry.
func (s *Store) AppendPolicy(data PolicyDecisionData) (*Entry, error) {
	return s.Append(EntryPolicy, &data)
}

// AppendPolicyEntry is a convenience method for logging policy decisions.
func (s *Store) AppendPolicyEntry(scope, operation, decision, rule, message string) error {
	_, err := s.AppendPolicy(PolicyDecisionData{
		Scope:     scope,
		Operation: operation,
		Decision:  decision,
		Rule:      rule,
		Message:   message,
	})
	return err
}
