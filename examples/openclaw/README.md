# OpenClaw Example

Run [OpenClaw](https://openclaw.ai) in an isolated container with credential injection. OpenClaw is a self-hosted AI gateway that connects messaging platforms to LLM agents.

This example demonstrates two credential isolation mechanisms working together:

- **Anthropic API key** — injected at the network layer by moat's proxy. OpenClaw never sees the real key.
- **Gateway token** — injected from 1Password via moat's secrets. Protects the OpenClaw Control UI.

## Prerequisites

1. An Anthropic API key ([console.anthropic.com](https://console.anthropic.com/))
2. [1Password CLI](https://1password.com/downloads/command-line/) installed and signed in

## Setup

### 1. Grant Anthropic credentials

```bash
# From environment variable
export ANTHROPIC_API_KEY="sk-ant-api03-..."
moat grant anthropic

# Or interactively
moat grant anthropic
```

### 2. Create the gateway token in 1Password

Generate a random token and store it in 1Password:

```bash
op item create \
  --category=password \
  --title="OpenClaw Demo" \
  --vault="Private" \
  password="$(openssl rand -hex 32)"
```

To retrieve it later:

```bash
op read "op://Private/OpenClaw Demo/password"
```

### 3. Run

```bash
moat run examples/openclaw
```

Moat will:
1. Build a container with Node.js 22 and OpenClaw
2. Resolve the gateway token from 1Password
3. Start the TLS-intercepting proxy for Anthropic API injection
4. Launch an interactive shell with the OpenClaw gateway running in the background

### 4. Access the Control UI

Open the URL shown by moat for the `gateway` port. To get a direct link with the auth token:

```bash
# Inside the container
openclaw dashboard --no-open
```

## How It Works

```
┌─────────────────────────────────────────────────────────┐
│  Container                                              │
│                                                         │
│  ┌──────────────┐     ┌──────────────────────────────┐  │
│  │  OpenClaw    │     │  OPENCLAW_GATEWAY_TOKEN       │  │
│  │  Gateway     │◄────│  (env var from 1Password)     │  │
│  │  :18789      │     │                               │  │
│  └──────┬───────┘     └──────────────────────────────┘  │
│         │                                               │
│         │ POST api.anthropic.com/v1/messages             │
│         │ x-api-key: moat-proxy-injected                │
└─────────┼───────────────────────────────────────────────┘
          │
    ┌─────▼─────┐
    │   Moat    │  Replaces stub key with real Anthropic API key
    │  (proxy)  │
    └─────┬─────┘
          │
          │ x-api-key: sk-ant-api03-...
          ▼
    ┌───────────┐
    │ Anthropic │
    │    API    │
    └───────────┘
```

Two credential flows:

1. **Anthropic API key**: The onboard script registers a stub key (`moat-proxy-injected`) with OpenClaw's auth system. When OpenClaw makes API requests, moat's proxy intercepts and replaces the stub with the real key before forwarding to Anthropic.

2. **Gateway token**: Moat resolves `op://Private/OpenClaw Demo/password` from 1Password at launch and injects the value as `OPENCLAW_GATEWAY_TOKEN`. The gateway reads the token from this env var at startup — it never touches the config file.

### Why `--bind lan` is a runtime flag

OpenClaw's CLI determines the gateway WebSocket URL by reading `gateway.bind` from `openclaw.json`. If `bind: "lan"` is in the config, the CLI connects to the container's LAN IP instead of localhost, which triggers device pairing requirements.

The fix (mirroring OpenClaw's official Docker setup): pass `--bind lan` as a CLI flag to `openclaw gateway run` so the gateway listens on all interfaces, while keeping the config file clean so the CLI defaults to `ws://127.0.0.1:18789`.

## Viewing Logs

After a session, inspect the captured data:

```bash
# List runs
moat list

# View all API calls to Anthropic (proxied by moat)
cat ~/.moat/runs/<run-id>/network.jsonl | jq .
```

Every LLM request is logged with method, URL, status code, and timing — without exposing the API key.

## Troubleshooting

### "invalid token (401 Unauthorized)" during grant

Your Anthropic API key may be invalid or expired. Verify it works:

```bash
curl -s https://api.anthropic.com/v1/messages \
  -H "x-api-key: $ANTHROPIC_API_KEY" \
  -H "anthropic-version: 2023-06-01" \
  -H "content-type: application/json" \
  -d '{"model":"claude-sonnet-4-5-20250929","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}' \
  | jq .type
# Should print: "message"
```

### 1Password errors

Ensure you're signed in:

```bash
eval $(op signin)
```

Verify the secret exists:

```bash
op read "op://Private/OpenClaw Demo/password"
```

### Gateway won't start

Check that port 18789 isn't already in use on the host. If it is, the port mapping will fail at container start.

### CLI commands fail with "pairing required"

If you see pairing errors when running CLI commands inside the container, the gateway token may not be resolving from the environment. Check:

```bash
echo $OPENCLAW_GATEWAY_TOKEN
```

The token should be set. If empty, the 1Password secret may not have resolved correctly.
