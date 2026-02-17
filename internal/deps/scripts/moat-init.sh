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

# Configuration constants
SSH_SOCKET_WAIT_ITERS=20     # iterations * 0.1s = 2 second timeout for SSH socket
DIND_TIMEOUT_SECONDS=30      # timeout for Docker daemon startup in dind mode

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
    # Wait for socket to be created (SSH_SOCKET_WAIT_ITERS * 0.1s timeout)
    i=0
    while [ "$i" -lt "$SSH_SOCKET_WAIT_ITERS" ]; do
      [ -S /run/moat/ssh/agent.sock ] && break
      sleep 0.1
      i=$((i + 1))
    done
    # Verify socat is still running and socket was created
    if ! kill -0 "$SOCAT_PID" 2>/dev/null; then
      echo "Warning: SSH agent bridge (socat) failed to start" >&2
    elif [ ! -S /run/moat/ssh/agent.sock ]; then
      echo "Warning: SSH agent socket was not created after 2s" >&2
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

  # Note: Plugins are now installed during image build via `claude plugin install`
  # commands in the Dockerfile. The claude CLI creates and manages installed_plugins.json
  # itself during installation. Runtime plugin staging (the old approach) is no longer used.

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

# Codex CLI Setup
# When MOAT_CODEX_INIT is set to the staging directory path, copy files
# from the staging area to their final locations (~/.codex).
if [ -n "$MOAT_CODEX_INIT" ] && [ -d "$MOAT_CODEX_INIT" ]; then
  # Determine target home directory
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    TARGET_HOME="/home/moatuser"
  else
    TARGET_HOME="$HOME"
  fi

  # Create ~/.codex directory
  mkdir -p "$TARGET_HOME/.codex"

  # Copy config.toml if present (preserve permissions)
  [ -f "$MOAT_CODEX_INIT/config.toml" ] && \
    cp -p "$MOAT_CODEX_INIT/config.toml" "$TARGET_HOME/.codex/"

  # Copy auth.json if present (ensure restricted permissions for security)
  if [ -f "$MOAT_CODEX_INIT/auth.json" ]; then
    cp -p "$MOAT_CODEX_INIT/auth.json" "$TARGET_HOME/.codex/"
    chmod 600 "$TARGET_HOME/.codex/auth.json"
  fi

  # Ensure moatuser owns all the files if we're running as root
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    chown -R moatuser:moatuser "$TARGET_HOME/.codex" 2>/dev/null || true
  fi
fi

# Gemini CLI Setup
# When MOAT_GEMINI_INIT is set to the staging directory path, copy files
# from the staging area to their final locations (~/.gemini).
if [ -n "$MOAT_GEMINI_INIT" ] && [ -d "$MOAT_GEMINI_INIT" ]; then
  # Determine target home directory
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    TARGET_HOME="/home/moatuser"
  else
    TARGET_HOME="$HOME"
  fi

  # Create ~/.gemini directory
  mkdir -p "$TARGET_HOME/.gemini"

  # Copy settings.json if present (preserve permissions)
  [ -f "$MOAT_GEMINI_INIT/settings.json" ] && \
    cp -p "$MOAT_GEMINI_INIT/settings.json" "$TARGET_HOME/.gemini/"

  # Copy oauth_creds.json if present (ensure restricted permissions for security)
  if [ -f "$MOAT_GEMINI_INIT/oauth_creds.json" ]; then
    cp -p "$MOAT_GEMINI_INIT/oauth_creds.json" "$TARGET_HOME/.gemini/"
    chmod 600 "$TARGET_HOME/.gemini/oauth_creds.json"
  fi

  # Ensure moatuser owns all the files if we're running as root
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    chown -R moatuser:moatuser "$TARGET_HOME/.gemini" 2>/dev/null || true
  fi
fi

# MCP Server Setup
# Remote/host-local MCP servers are configured via .claude.json for Claude Code.
# Local process MCP servers (sandbox-local) are configured per-agent:
# - Claude: Written to .claude.json mcpServers (type: stdio) by the claude provider
# - Codex: Written to .mcp.json in workspace by the codex provider
# - Gemini: Written to .mcp.json in workspace by the gemini provider
#
# Copy .mcp.json from Codex staging if present
if [ -n "$MOAT_CODEX_INIT" ] && [ -f "$MOAT_CODEX_INIT/mcp.json" ]; then
  cp -p "$MOAT_CODEX_INIT/mcp.json" /workspace/.mcp.json
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    chown moatuser:moatuser /workspace/.mcp.json 2>/dev/null || true
  fi
fi

# Copy .mcp.json from Gemini staging if present
if [ -n "$MOAT_GEMINI_INIT" ] && [ -f "$MOAT_GEMINI_INIT/mcp.json" ]; then
  cp -p "$MOAT_GEMINI_INIT/mcp.json" /workspace/.mcp.json
  if [ "$(id -u)" = "0" ] && id moatuser >/dev/null 2>&1; then
    chown moatuser:moatuser /workspace/.mcp.json 2>/dev/null || true
  fi
fi

# Git Configuration
# 1. Safe directory: The workspace is mounted from the host with different
#    ownership than the container user. Git 2.35.2+ rejects operations on
#    directories owned by other users unless explicitly marked safe.
# 2. Identity: When the host has git user.name/user.email configured, moat
#    passes them via MOAT_GIT_USER_NAME and MOAT_GIT_USER_EMAIL. Set them as
#    system-level git config so commits inside the container use the host's
#    identity.
if command -v git >/dev/null 2>&1; then
  git config --system --add safe.directory /workspace 2>/dev/null || true
  if [ -n "$MOAT_GIT_USER_NAME" ]; then
    git config --system user.name "$MOAT_GIT_USER_NAME" 2>/dev/null || true
  fi
  if [ -n "$MOAT_GIT_USER_EMAIL" ]; then
    git config --system user.email "$MOAT_GIT_USER_EMAIL" 2>/dev/null || true
  fi
fi

# Docker Access Setup
# Two mutually exclusive modes:
# 1. MOAT_DOCKER_GID (host mode): Docker socket mounted from host, just need group access
# 2. MOAT_DOCKER_DIND (dind mode): Start dockerd inside the container

if [ -n "$MOAT_DOCKER_DIND" ] && [ -n "$MOAT_DOCKER_GID" ]; then
  echo "Error: MOAT_DOCKER_DIND and MOAT_DOCKER_GID are mutually exclusive" >&2
  echo "Use MOAT_DOCKER_GID when mounting host's docker socket" >&2
  echo "Use MOAT_DOCKER_DIND when running Docker-in-Docker" >&2
  exit 1
fi

# Docker-in-Docker Mode
# When MOAT_DOCKER_DIND=1, start dockerd inside the container.
# This requires the container to be run with --privileged or appropriate capabilities.
if [ "$MOAT_DOCKER_DIND" = "1" ] && [ "$(id -u)" = "0" ]; then
  echo "Starting Docker daemon (dind mode)..." >&2

  # Create docker run directory if it doesn't exist
  mkdir -p /var/run

  # Start dockerd in the background with vfs storage driver (most compatible for nested containers)
  # Use vfs by default as it works without special kernel requirements
  # overlay2 may work if the outer container has it available
  dockerd --storage-driver=vfs --log-level=warn >/var/log/dockerd.log 2>&1 &
  DOCKERD_PID=$!

  # Wait for dockerd to be ready (up to DIND_TIMEOUT_SECONDS)
  # Check for socket file AND docker info since socket must exist for non-root users
  DIND_WAITED=0
  echo "Waiting for Docker daemon to be ready..." >&2
  while [ "$DIND_WAITED" -lt "$DIND_TIMEOUT_SECONDS" ]; do
    # Check both socket exists AND daemon responds
    if [ -S /var/run/docker.sock ] && docker info >/dev/null 2>&1; then
      echo "Docker daemon is ready (took ${DIND_WAITED}s)" >&2
      break
    fi
    # Check if dockerd is still running
    if ! kill -0 "$DOCKERD_PID" 2>/dev/null; then
      echo "Error: Docker daemon failed to start" >&2
      echo "Check /var/log/dockerd.log for details:" >&2
      tail -20 /var/log/dockerd.log 2>/dev/null || true
      exit 1
    fi
    sleep 1
    DIND_WAITED=$((DIND_WAITED + 1))
  done

  if [ "$DIND_WAITED" -ge "$DIND_TIMEOUT_SECONDS" ]; then
    echo "Error: Docker daemon did not become ready within ${DIND_TIMEOUT_SECONDS} seconds" >&2
    echo "Socket exists: $([ -S /var/run/docker.sock ] && echo yes || echo no)" >&2
    echo "Check /var/log/dockerd.log for details:" >&2
    tail -20 /var/log/dockerd.log 2>/dev/null || true
    exit 1
  fi

  # Add moatuser to docker group so they can use docker without sudo
  if id moatuser >/dev/null 2>&1; then
    # Ensure docker group exists (dockerd creates it, but be safe)
    if ! getent group docker >/dev/null 2>&1; then
      groupadd docker 2>/dev/null || true
    fi
    usermod -aG docker moatuser 2>/dev/null || true
  fi
fi

# Docker Socket Group (host mode)
# When MOAT_DOCKER_GID is set, the docker socket is mounted and we need to
# give moatuser access. We detect the socket's GID inside the container
# (not from the host) because Docker Desktop on macOS translates ownership.
# Note: Uses GNU stat -c format (Linux-specific, but containers are always Linux).
if [ -n "$MOAT_DOCKER_GID" ] && [ "$(id -u)" = "0" ] && [ -S /var/run/docker.sock ]; then
  # Get the actual GID of the socket as seen inside the container
  SOCKET_GID=$(stat -c '%g' /var/run/docker.sock 2>/dev/null) || true
  if [ -z "$SOCKET_GID" ]; then
    echo "Warning: Failed to detect docker socket GID, docker access may not work" >&2
  elif [ -n "$SOCKET_GID" ]; then
    # Check if a group with this GID already exists
    if ! getent group "$SOCKET_GID" >/dev/null 2>&1; then
      # Create a group with the docker socket GID
      groupadd -g "$SOCKET_GID" moat-docker 2>/dev/null || true
    fi
    # Add moatuser to the group
    DOCKER_GROUP=$(getent group "$SOCKET_GID" | cut -d: -f1)
    if [ -n "$DOCKER_GROUP" ] && id moatuser >/dev/null 2>&1; then
      usermod -aG "$DOCKER_GROUP" moatuser 2>/dev/null || true
    fi
  fi
fi

# Pre-run Hook
# When MOAT_PRE_RUN is set, run the command as moatuser in /workspace before
# executing the main command. This runs on every container start (not cached).
# Use for workspace-level setup that needs project files (e.g., "npm install").
run_pre_run_hook() {
  if [ -z "$MOAT_PRE_RUN" ]; then
    return
  fi
  if [ "$(id -u)" != "0" ]; then
    # Already non-root, run directly
    cd /workspace && sh -c "$MOAT_PRE_RUN"
  elif id moatuser >/dev/null 2>&1; then
    # Drop to moatuser for the hook
    gosu moatuser sh -c "cd /workspace && $MOAT_PRE_RUN"
  fi
}

# Execute the user's command
# First run the pre_run hook (if set), then exec the main command.
# If we're already running as a non-root user (UID != 0), just exec directly.
# This happens when Docker is started with --user to match host UID on Linux.
# If we're root and moatuser exists, drop privileges with gosu.
# If moatuser doesn't exist, fail - running as root defeats the security model.
run_pre_run_hook
if [ "$(id -u)" != "0" ]; then
  # Already non-root (e.g., --user was passed to docker run)
  exec "$@"
elif id moatuser >/dev/null 2>&1; then
  # Running as root, moatuser exists - drop privileges
  exec gosu moatuser "$@"
else
  # Running as root, no moatuser - fail with clear error
  # Running as root defeats the container security model
  echo "Error: Container started as root but moatuser does not exist." >&2
  echo "This is a security issue - running as root defeats container isolation." >&2
  echo "" >&2
  echo "If you're using a custom image, ensure it creates a 'moatuser' account:" >&2
  echo "  RUN useradd -m -u 5000 -s /bin/bash moatuser" >&2
  echo "" >&2
  echo "Or run the container with a non-root user:" >&2
  echo "  docker run --user 1000:1000 ..." >&2
  exit 1
fi
