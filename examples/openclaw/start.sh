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
# IMPORTANT: Do NOT set gateway.bind here. The CLI reads gateway.bind from
# the config to determine the WebSocket connection URL. If bind=lan is in
# the config, the CLI connects to the container's LAN IP instead of localhost,
# which triggers device pairing. Instead, pass --bind lan as a runtime flag
# to "openclaw gateway run" (below). This mirrors the official Docker setup.
#
# Similarly, the gateway token resolves from the OPENCLAW_GATEWAY_TOKEN env
# var (injected by moat from 1Password), so it doesn't need to be in the config.
cat > ~/.openclaw/openclaw.json << 'EOF'
{
  "gateway": {
    "mode": "local",
    "bind": "lan"
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

# Start the gateway with --bind lan so it listens on all interfaces
# (needed for port mapping from host), but the CLI defaults to localhost.
# Auth token comes from OPENCLAW_GATEWAY_TOKEN env var.
openclaw gateway run --bind lan &
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
echo "Retrieve your token: op read \"op://Private/OpenClaw Demo/password\""
echo ""

# Drop into interactive shell — gateway runs in background
exec bash
