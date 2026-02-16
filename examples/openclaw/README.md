# OpenClaw Example

Run [OpenClaw](https://openclaw.ai) in an isolated container with credential injection. OpenClaw is a self-hosted AI gateway that connects messaging platforms to LLM agents.

This example demonstrates three credential isolation mechanisms working together:

- **Anthropic API key** — injected at the network layer via header injection. OpenClaw never sees the real key.
- **Telegram bot token** — injected at the network layer via URL path token substitution. The proxy rewrites `/botmoat-<hash>/...` to `/bot<real-token>/...` so the container only sees a placeholder.
- **Gateway token** — injected from 1Password via moat's secrets. Protects the OpenClaw Control UI.

## Prerequisites

1. An Anthropic API key ([console.anthropic.com](https://console.anthropic.com/))
2. A Telegram bot token (create one via [@BotFather](https://t.me/BotFather))
3. [1Password CLI](https://1password.com/downloads/command-line/) installed and signed in

## Setup

### 1. Grant credentials

```bash
moat grant anthropic
moat grant telegram
```

Moat handles credential injection for both — Anthropic via header injection, Telegram via URL path token substitution.

### 2. Create the gateway token in 1Password

Generate a random token and store it in 1Password:

```bash
op item create \
  --category=password \
  --title="OpenClaw" \
  --vault="Private" \
  password="$(openssl rand -hex 32)"
```

To retrieve it later:

```bash
op read "op://Private/OpenClaw/password"
```

### 3. Run

```bash
moat run examples/openclaw
```

Moat will:
1. Build a container with Node.js 22 and OpenClaw
2. Resolve the gateway token from 1Password
3. Start the TLS-intercepting proxy for Anthropic and Telegram credential injection
4. Launch an interactive shell with the OpenClaw gateway running in the background

### 4. Access the Control UI

Open the URL shown by moat for the `gateway` port. To get a direct link with the auth token:

```bash
# Inside the container
openclaw dashboard --no-open
```

### 5. Pair your Telegram account

DM your bot on Telegram — you'll receive a pairing code. Approve it inside the container:

```bash
openclaw pairing list telegram
openclaw pairing approve telegram <CODE>
```

Pairing codes expire after 1 hour. After approval, you can chat with the bot directly.

## How It Works

```
┌──────────────────────────────────────────────────────────────┐
│  Container                                                   │
│                                                              │
│  ┌──────────────┐     ┌───────────────────────────────────┐  │
│  │  OpenClaw    │     │  OPENCLAW_GATEWAY_TOKEN           │  │
│  │  Gateway     │◄────│  (env var from 1Password)         │  │
│  │  :18789      │     │                                   │  │
│  └──────┬───────┘     │  TELEGRAM_BOT_TOKEN=moat-<hash>   │  │
│         │             │  (placeholder, not real token)    │  │
│         │             └───────────────────────────────────┘  │
│         │                                                    │
│         ├─► POST api.anthropic.com/v1/messages               │
│         │   x-api-key: moat-proxy-injected                   │
│         │                                                    │
│         └─► GET api.telegram.org/botmoat-<hash>/getMe        │
│                                                              │
└─────────┼────────────────────────────────────────────────────┘
          │
    ┌─────▼─────┐
    │   Moat    │  Header injection (Anthropic)
    │  (proxy)  │  URL path substitution (Telegram)
    └─────┬─────┘
          │
          ├─► x-api-key: sk-ant-api03-...  →  Anthropic API
          └─► /bot123456:ABC-DEF/getMe     →  Telegram API
```

Three credential flows:

1. **Anthropic API key** (header injection): The onboard script registers a stub key (`moat-proxy-injected`) with OpenClaw's auth system. When OpenClaw makes API requests, moat's proxy intercepts and replaces the stub with the real key before forwarding to Anthropic.

2. **Telegram bot token** (URL path substitution): The container receives `TELEGRAM_BOT_TOKEN=moat-<hash>` — a per-credential hashed placeholder. When OpenClaw makes requests to `api.telegram.org/botmoat-<hash>/...`, moat's proxy rewrites the URL path with the real bot token. Response bodies are also scrubbed to prevent the real token from leaking back.

3. **Gateway token** (1Password secret): Moat resolves `op://Private/OpenClaw/password` from 1Password at launch and injects the value as `OPENCLAW_GATEWAY_TOKEN`. The gateway reads the token from this env var at startup.

## Notes

- **Telegram in Control UI**: The Telegram config page may show "Unsupported schema node. Use Raw mode." This is an OpenClaw UI limitation — Telegram config works correctly via the config file and CLI. Use the pairing commands above to manage access.

## Viewing Logs

After a session, inspect the captured data:

```bash
# List runs
moat list

# View all API calls to Anthropic (proxied by moat)
moat trace --network --json run_id
```

Every LLM request is logged with method, URL, status code, and timing — without exposing the API key.

