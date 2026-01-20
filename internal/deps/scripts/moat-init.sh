#!/bin/sh
# moat-init.sh - Container initialization script
# This script runs before the user's command to set up moat features.
# Features are enabled via environment variables.

set -e

# SSH Agent Bridge
# When MOAT_SSH_TCP_ADDR is set, create a Unix socket that bridges to the
# TCP-based SSH agent proxy running on the host. This is needed for Docker
# on macOS where Unix sockets can't be shared via bind mounts.
if [ -n "$MOAT_SSH_TCP_ADDR" ]; then
  mkdir -p /run/moat/ssh
  socat UNIX-LISTEN:/run/moat/ssh/agent.sock,fork TCP:"$MOAT_SSH_TCP_ADDR" &
  # Wait for socket to be created
  for i in 1 2 3 4 5; do
    [ -S /run/moat/ssh/agent.sock ] && break
    sleep 0.1
  done
fi

# Execute the user's command
exec "$@"
