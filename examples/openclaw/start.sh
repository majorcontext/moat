#!/usr/bin/env bash
set -euo pipefail

# Create workspace directory
mkdir -p ~/.openclaw/workspace

# Register a stub API key with OpenClaw's auth system so it attempts
# requests. The real credential is injected by moat's proxy at the
# network layer — OpenClaw never sees the actual key.
#
# --no-install-daemon: skip systemd/launchd service (not needed in container)
# --gateway-auth token: configure token auth mode
openclaw onboard \
  --non-interactive \
  --accept-risk \
  --no-install-daemon \
  --anthropic-api-key "moat-proxy-injected" \
  --gateway-auth token \
  || true

# Write config AFTER onboard (which overwrites openclaw.json).
#
# Gateway token: resolves from OPENCLAW_GATEWAY_TOKEN env var (injected by
# moat from 1Password), not written to config.
#
# bind: "lan" — listen on 0.0.0.0 so Docker port mapping works. The --bind
# lan CLI flag is silently ignored in v2026.2.13, so config is the only way.
# This makes the CLI connect via the container's LAN IP, but
# dangerouslyDisableDeviceAuth bypasses the pairing that would trigger.
#
# trustedProxies: moat's routing proxy terminates TLS and forwards requests
# with X-Forwarded-For/Proto headers. We trust both the host IP
# (host.docker.internal) and the bridge gateway (.1 on the same subnet)
# since they can differ (e.g., host=172.29.0.254, gateway=172.29.0.1).
# CIDR notation is not supported — exact IPs only.
#
# dangerouslyDisableDeviceAuth: skip device pairing for the Control UI.
# Safe here: moat's proxy binds to localhost only, the container is
# ephemeral, and token auth is still active.

# Collect all IPs that moat's routing proxy might connect from:
# 1. host.docker.internal (host IP on the Docker network)
# 2. Bridge gateway (.1 on the same subnet — Docker's convention)
# Both are needed because the host IP and bridge gateway can differ
# (e.g., host=172.29.0.254, gateway=172.29.0.1).
HOST_IP=$(getent ahostsv4 host.docker.internal 2>/dev/null | awk 'NR==1{print $1}')
if [ -n "$HOST_IP" ]; then
  BRIDGE_GW="${HOST_IP%.*}.1"
  TRUSTED_PROXIES="\"${HOST_IP}\",\"${BRIDGE_GW}\""
else
  TRUSTED_PROXIES="\"172.17.0.1\""
fi

cat > ~/.openclaw/openclaw.json << EOF
{
  "gateway": {
    "mode": "local",
    "bind": "lan",
    "trustedProxies": [${TRUSTED_PROXIES}],
    "controlUi": {
      "dangerouslyDisableDeviceAuth": true
    }
  },
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-sonnet-4-5"
      }
    }
  }
}
EOF

# Run security audit
openclaw security audit || true

# Set a placeholder API key so OpenClaw's auth store check passes.
# The real credential is injected by moat's proxy at the HTTP layer.
export ANTHROPIC_API_KEY=moat-proxy-injected

# Start the gateway. Auth token comes from OPENCLAW_GATEWAY_TOKEN env var.
openclaw gateway run &
GATEWAY_PID=$!

# Give the gateway a moment to start
sleep 2

echo ""
echo "=== OpenClaw Gateway ==="
echo "Control UI: open the URL shown by moat for the 'gateway' port"
echo ""
echo "To get a dashboard link with auth token:"
echo "  openclaw dashboard --no-open"
echo ""

# Drop into interactive shell — gateway runs in background
exec bash
