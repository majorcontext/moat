package daemon

import (
	"fmt"

	keeplib "github.com/majorcontext/keep"
	"github.com/majorcontext/moat/internal/log"
)

// Registration and restart restoration must compile identical policy inputs.
func compilePolicies(yamls map[string][]byte, specs []PolicyRuleSetSpec) (map[string]*keeplib.Engine, error) {
	if len(yamls)+len(specs) == 0 {
		return nil, nil
	}
	engines := make(map[string]*keeplib.Engine)
	cleanup := func() {
		for _, e := range engines {
			e.Close()
		}
	}
	auditHook := func(scope string) func(keeplib.AuditEntry) {
		return func(e keeplib.AuditEntry) {
			fields := []any{"scope", scope, "op", e.Operation, "decision", string(e.Decision), "rule", e.Rule, "message", e.Message}
			if e.Decision == "deny" {
				log.Warn("keep policy deny", fields...)
			} else {
				log.Info("keep policy evaluation", fields...)
			}
		}
	}
	for scope, data := range yamls {
		eng, err := keeplib.LoadFromBytes(data, keeplib.WithAuditHook(auditHook(scope)))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("keep: compile scope %q: %w", scope, err)
		}
		engines[scope] = eng
	}
	for _, spec := range specs {
		if _, exists := engines[spec.Scope]; exists {
			cleanup()
			return nil, fmt.Errorf("keep: multiple policies for scope %q", spec.Scope)
		}
		rs := keeplib.NewRuleSet(spec.Scope, spec.Mode)
		rs.Deny(spec.Deny...)
		eng, err := rs.Compile(keeplib.WithAuditHook(auditHook(spec.Scope)))
		if err != nil {
			cleanup()
			return nil, fmt.Errorf("keep: compile ruleset %q: %w", spec.Scope, err)
		}
		engines[spec.Scope] = eng
	}
	return engines, nil
}
