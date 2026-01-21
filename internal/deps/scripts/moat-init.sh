#!/bin/sh
# moat-init.sh - Container initialization script
# This script runs before the user's command to set up moat features.
# Features are enabled via environment variables.
#
# When running as root, this script performs privileged setup (SSH socket),
# then drops to moatuser for command execution. When already running as a
# non-root user (e.g., on Linux with host UID mapping), it skips privilege
# dropping since the user is already non-root.

set -e

# SSH Agent Bridge
# When MOAT_SSH_TCP_ADDR is set, create a Unix socket that bridges to the
# TCP-based SSH agent proxy running on the host. This is needed for Docker
# on macOS where Unix sockets can't be shared via bind mounts.
if [ -n "$MOAT_SSH_TCP_ADDR" ]; then
  # Create socket directory - may need root for /run
  mkdir -p /run/moat/ssh 2>/dev/null || true
  if [ -d /run/moat/ssh ]; then
    # Start socat to bridge TCP to Unix socket
    socat UNIX-LISTEN:/run/moat/ssh/agent.sock,fork,mode=0777 TCP:"$MOAT_SSH_TCP_ADDR" &
    # Wait for socket to be created
    for i in 1 2 3 4 5; do
      [ -S /run/moat/ssh/agent.sock ] && break
      sleep 0.1
    done
    # Make socket accessible to all users
    chmod 777 /run/moat/ssh/agent.sock 2>/dev/null || true
  fi
fi

# Claude Code Setup
# When MOAT_CLAUDE_INIT is set to the staging directory path, copy files
# from the staging area to their final locations. This is needed because:
# 1. Apple containers only support directory mounts, not file mounts
# 2. We need ~/.claude to be a real directory so projects/ can be mounted inside it
if [ -n "$MOAT_CLAUDE_INIT" ] && [ -d "$MOAT_CLAUDE_INIT" ]; then
  # Create ~/.claude directory
  mkdir -p "$HOME/.claude"

  # Copy settings.json if present (preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/settings.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/settings.json" "$HOME/.claude/"

  # Copy credentials if present (ensure restricted permissions for security)
  if [ -f "$MOAT_CLAUDE_INIT/.credentials.json" ]; then
    cp -p "$MOAT_CLAUDE_INIT/.credentials.json" "$HOME/.claude/"
    chmod 600 "$HOME/.claude/.credentials.json"
  fi

  # Copy statsig directory if present (feature flags, preserve permissions)
  [ -d "$MOAT_CLAUDE_INIT/statsig" ] && \
    cp -rp "$MOAT_CLAUDE_INIT/statsig" "$HOME/.claude/"

  # Copy stats-cache.json if present (usage stats, preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/stats-cache.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/stats-cache.json" "$HOME/.claude/"

  # Copy .claude.json to home directory (onboarding state, preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/.claude.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/.claude.json" "$HOME/"
fi

# Execute the user's command
# If we're already running as a non-root user (UID != 0), just exec directly.
# This happens when Docker is started with --user to match host UID on Linux.
# If we're root and moatuser exists, drop privileges with gosu.
# Otherwise, run as current user.
if [ "$(id -u)" != "0" ]; then
  # Already non-root (e.g., --user was passed to docker run)
  exec "$@"
elif id moatuser >/dev/null 2>&1; then
  # Running as root, moatuser exists - drop privileges
  exec gosu moatuser "$@"
else
  # Running as root, no moatuser - run as root (custom image)
  exec "$@"
fi
