#!/bin/sh
# moat-init-dispatch.sh - Entrypoint dispatcher for the shell->Go migration.
#
# Selects which moat-init implementation runs as PID 1:
#   /usr/local/bin/moat-init-sh  - the original shell entrypoint
#   /usr/local/bin/moat-commit  - the Go entrypoint (cmd/moat-init)
#
# MOAT_INIT_IMPL and MOAT_INIT_LEGACY are operator-only controls injected by
# the moat host binary; run.Create() rejects them in moat.yaml env and -e
# flags (they select a security-critical PID 1, so a user-settable switch
# would be an attack surface). Both are read exactly once, here, before any
# phase runs, and are never re-read after user-controlled code executes.
# They are unset before the handoff so the selected implementation and the
# user command see the same environment they would without the dispatcher.
#
# Closed enum, fatal on anything else: a typo must fail loudly, not fall
# back to an unintended entrypoint.
set -e

impl="${MOAT_INIT_IMPL:-sh}"

case "${MOAT_INIT_LEGACY:-}" in
  "") ;;
  1) impl=sh ;;
  *)
    echo "Error: invalid MOAT_INIT_LEGACY '${MOAT_INIT_LEGACY}' (expected '1' or unset)" >&2
    exit 1
    ;;
esac

case "$impl" in
  sh|go) ;;
  *)
    echo "Error: invalid MOAT_INIT_IMPL '${MOAT_INIT_IMPL}' (expected 'sh' or 'go')" >&2
    exit 1
    ;;
esac

unset MOAT_INIT_IMPL MOAT_INIT_LEGACY

if [ "$impl" = "go" ]; then
  exec /usr/local/bin/moat-commit "$@"
fi
exec /usr/local/bin/moat-init-sh "$@"
