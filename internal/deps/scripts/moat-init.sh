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
    # Set directory permissions so moatuser can access it
    chmod 755 /run/moat/ssh 2>/dev/null || true
    if id moatuser >/dev/null 2>&1; then
      chown moatuser:moatuser /run/moat/ssh 2>/dev/null || true
    fi
    # Start socat to bridge TCP to Unix socket
    # Socket created with mode 0660 - accessible by owner and group only
    socat UNIX-LISTEN:/run/moat/ssh/agent.sock,fork,mode=0660 TCP:"$MOAT_SSH_TCP_ADDR" &
    SOCAT_PID=$!
    # Wait for socket to be created
    for i in 1 2 3 4 5; do
      [ -S /run/moat/ssh/agent.sock ] && break
      sleep 0.1
    done
    # Verify socat is still running and socket was created
    if ! kill -0 "$SOCAT_PID" 2>/dev/null; then
      echo "Warning: SSH agent bridge (socat) failed to start" >&2
    elif [ ! -S /run/moat/ssh/agent.sock ]; then
      echo "Warning: SSH agent socket was not created" >&2
    else
      # Ensure socket is owned by moatuser if it exists
      if id moatuser >/dev/null 2>&1; then
        chown moatuser:moatuser /run/moat/ssh/agent.sock 2>/dev/null || true
      fi
    fi
  fi
fi

# Claude Code Setup
# When MOAT_CLAUDE_INIT is set to the staging directory path, copy files
# from the staging area to their final locations. This is needed because:
# 1. Apple containers only support directory mounts, not file mounts
# 2. We need ~/.claude to be a real directory so projects/ can be mounted inside it
#
# IMPORTANT: We determine the target home directory based on whether we'll drop
# privileges to moatuser. If running as root with moatuser available, files go
# to /home/moatuser. Otherwise, files go to the current $HOME.
if [ -n "$MOAT_CLAUDE_INIT" ] && [ -d "$MOAT_CLAUDE_INIT" ]; then
  # Determine target home directory
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    TARGET_HOME="/home/moatuser"
  else
    TARGET_HOME="$HOME"
  fi

  # Create ~/.claude directory
  mkdir -p "$TARGET_HOME/.claude"

  # Copy settings.json if present (preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/settings.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/settings.json" "$TARGET_HOME/.claude/"

  # Copy credentials if present (ensure restricted permissions for security)
  if [ -f "$MOAT_CLAUDE_INIT/.credentials.json" ]; then
    cp -p "$MOAT_CLAUDE_INIT/.credentials.json" "$TARGET_HOME/.claude/"
    chmod 600 "$TARGET_HOME/.claude/.credentials.json"
  fi

  # Copy statsig directory if present (feature flags, preserve permissions)
  [ -d "$MOAT_CLAUDE_INIT/statsig" ] && \
    cp -rp "$MOAT_CLAUDE_INIT/statsig" "$TARGET_HOME/.claude/"

  # Copy stats-cache.json if present (usage stats, preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/stats-cache.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/stats-cache.json" "$TARGET_HOME/.claude/"

  # Copy .claude.json to home directory (onboarding state, preserve permissions)
  [ -f "$MOAT_CLAUDE_INIT/.claude.json" ] && \
    cp -p "$MOAT_CLAUDE_INIT/.claude.json" "$TARGET_HOME/"

  # Ensure moatuser owns all the files if we're running as root
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    chown -R moatuser:moatuser "$TARGET_HOME/.claude" 2>/dev/null || true
    [ -f "$TARGET_HOME/.claude.json" ] && chown moatuser:moatuser "$TARGET_HOME/.claude.json" 2>/dev/null || true
  fi
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
