// Package moatinit implements the container entrypoint as testable Go.
//
// It is the Go port of internal/deps/scripts/moat-init.sh, with behavioral
// parity as the contract: same phase ordering, same fail-closed vs
// best-effort classification per operation, and verbatim user-facing
// error wording. The catalog of ported requirements lives in
// docs/plans/2026-07-01-moat-init-go-rewrite-plan.md (Appendix A/B).
//
// The package moves the entrypoint's business logic — env parsing, branch
// and mode selection, phase ordering, error classification, argument and
// config assembly, exclude computation, root/moatuser detection — into
// unit-testable Go. Mechanical, security-sensitive, or well-understood
// system operations stay delegated to the audited tools already in the
// base image, invoked as targeted subprocesses:
//
//   - gosu  — the privilege drop (Go selects the branch, gosu transitions)
//   - socat — the SSH agent TCP↔unix bridge (long-lived child)
//   - tar   — the workspace volume byte copy (Go owns excludes/args/rc checks)
//
// All identity, filesystem, subprocess, and DNS operations go through the
// Sys seam so phases can be exercised under `go test` against an injected
// temp root with no container and no root privileges.
//
// The compiled binary (cmd/moat-init) is embedded into the moat host binary
// by internal/initbin and shipped into run images as /usr/local/bin/moat-init
// (the container ENTRYPOINT) by writeEntrypoint.
package moatinit
