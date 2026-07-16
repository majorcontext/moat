package run

import (
	"fmt"
	"strings"

	"github.com/majorcontext/moat/internal/config"
)

// reservedInitEnvVars are entrypoint-dispatcher controls injected by the moat
// host binary itself: they select which PID-1 implementation runs inside the
// container (see internal/deps/scripts/moat-init-dispatch.sh). A
// user-settable value would be an attack surface — the dispatcher chooses a
// security-critical entrypoint — so these keys are rejected outright from
// moat.yaml env and -e flags.
//
// Unlike isMoatOwnedProxyVar, this rejection is always on: it does not
// depend on whether a proxy is active (a grantless permissive run passes all
// other env through untouched), and it fails the run instead of warning and
// skipping, because silently dropping an explicit entrypoint selection would
// hide from the operator that their switch never applied.
var reservedInitEnvVars = []string{"MOAT_INIT_IMPL", "MOAT_INIT_LEGACY"}

// isReservedInitVar reports whether name is a reserved entrypoint-dispatcher
// variable. Matching is case-insensitive for consistency with
// isMoatOwnedProxyVar: only the exact-case variable influences the
// dispatcher, but allowing a case-twin through would invite confusion with
// no legitimate use.
func isReservedInitVar(name string) bool {
	upper := strings.ToUpper(name)
	for _, r := range reservedInitEnvVars {
		if upper == r {
			return true
		}
	}
	return false
}

// validateReservedEnv rejects reserved entrypoint-dispatcher variables in
// user-supplied environment sources (moat.yaml env: and -e/--env flags). An
// -e entry without '=' is a host-passthrough form and is matched on its full
// name.
func validateReservedEnv(cfg *config.Config, explicitEnv []string) error {
	if cfg != nil {
		for k := range cfg.Env {
			if isReservedInitVar(k) {
				return reservedEnvError(k, "moat.yaml env")
			}
		}
	}
	for _, e := range explicitEnv {
		name := e
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			name = e[:idx]
		}
		if isReservedInitVar(name) {
			return reservedEnvError(name, "-e flag")
		}
	}
	return nil
}

// operatorInitEnv returns the entrypoint-dispatcher variables to inject
// into the container, read from the moat PROCESS's own environment — the
// operator-only channel. Users cannot set these through moat.yaml or -e
// (validateReservedEnv rejects them); an operator exports them on the host
// to select the entrypoint implementation: the parity harness drives both
// legs this way, and MOAT_INIT_LEGACY=1 is the one-release rollback lever
// after the Go cutover.
func operatorInitEnv(getenv func(string) string) []string {
	var env []string
	for _, key := range reservedInitEnvVars {
		if v := getenv(key); v != "" {
			env = append(env, key+"="+v)
		}
	}
	return env
}

func reservedEnvError(name, source string) error {
	return fmt.Errorf("%s is reserved for moat's entrypoint dispatcher and cannot be set via %s.\n"+
		"It selects which container entrypoint implementation runs and is managed by moat itself.\n"+
		"Remove %s from your configuration and re-run", name, source, name)
}
