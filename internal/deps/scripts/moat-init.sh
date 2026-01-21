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

# Claude Code Setup
# When MOAT_CLAUDE_INIT is set to the staging directory path, copy files
# from the staging area to their final locations. This is needed because:
# 1. Apple containers only support directory mounts, not file mounts
# 2. We need ~/.claude to be a real directory so projects/ can be mounted inside it
if [ -n "$MOAT_CLAUDE_INIT" ] && [ -d "$MOAT_CLAUDE_INIT" ]; then
  # Create ~/.claude directory
  mkdir -p "$HOME/.claude"

  # Copy settings.json if present
  [ -f "$MOAT_CLAUDE_INIT/settings.json" ] && \
    cp "$MOAT_CLAUDE_INIT/settings.json" "$HOME/.claude/"

  # Copy credentials if present
  [ -f "$MOAT_CLAUDE_INIT/.credentials.json" ] && \
    cp "$MOAT_CLAUDE_INIT/.credentials.json" "$HOME/.claude/"

  # Copy statsig directory if present (feature flags)
  [ -d "$MOAT_CLAUDE_INIT/statsig" ] && \
    cp -r "$MOAT_CLAUDE_INIT/statsig" "$HOME/.claude/"

  # Copy stats-cache.json if present (usage stats)
  [ -f "$MOAT_CLAUDE_INIT/stats-cache.json" ] && \
    cp "$MOAT_CLAUDE_INIT/stats-cache.json" "$HOME/.claude/"

  # Copy .claude.json to home directory (onboarding state)
  [ -f "$MOAT_CLAUDE_INIT/.claude.json" ] && \
    cp "$MOAT_CLAUDE_INIT/.claude.json" "$HOME/"
fi

# Execute the user's command
exec "$@"
