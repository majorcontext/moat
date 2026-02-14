# OpenClaw Internals: Docker & Headless Mode

Research notes for mapping OpenClaw's Docker setup into Moat's container model.

## How the Official Docker Setup Works

### Two-Container Architecture

Docker Compose runs two containers sharing volumes:

```yaml
# Gateway: long-running server
openclaw-gateway:
  command: ["node", "dist/index.js", "gateway", "--bind", "${OPENCLAW_GATEWAY_BIND:-lan}", "--port", "18789"]
  environment:
    OPENCLAW_GATEWAY_TOKEN: ${OPENCLAW_GATEWAY_TOKEN}
  volumes:
    - ${OPENCLAW_CONFIG_DIR}:/home/node/.openclaw
    - ${OPENCLAW_WORKSPACE_DIR}:/home/node/.openclaw/workspace
  ports:
    - "${OPENCLAW_GATEWAY_PORT:-18789}:18789"

# CLI: interactive, ephemeral
openclaw-cli:
  entrypoint: ["node", "dist/index.js"]
  environment:
    OPENCLAW_GATEWAY_TOKEN: ${OPENCLAW_GATEWAY_TOKEN}
    BROWSER: echo                # prevent browser launch in headless
  volumes:
    - ${OPENCLAW_CONFIG_DIR}:/home/node/.openclaw
    - ${OPENCLAW_WORKSPACE_DIR}:/home/node/.openclaw/workspace
```

Key design choices:
- `--bind lan` passed as **CLI flag to the gateway command**, NOT in `openclaw.json`
- Token passed as **env var** (`OPENCLAW_GATEWAY_TOKEN`), NOT in config file
- Both containers share the same config volume (`~/.openclaw`)

### Token Generation

`docker-setup.sh` auto-generates a 64-character hex token (32 random bytes):

```bash
OPENCLAW_GATEWAY_TOKEN="$(openssl rand -hex 32)"
export OPENCLAW_GATEWAY_TOKEN
```

### Onboard Command (Docker-Specific)

```bash
docker compose run --rm openclaw-cli onboard \
  --non-interactive \
  --accept-risk \
  --gateway-bind lan \
  --gateway-auth token \
  --gateway-token "$OPENCLAW_GATEWAY_TOKEN" \
  --no-install-daemon
```

Flags:
- `--no-install-daemon` — skip systemd/launchd service install (not needed in containers)
- `--gateway-bind lan` — sets bind mode in config for onboarding awareness
- `--gateway-auth token` — configure token auth mode
- `--gateway-token` — pass token to onboarding

## CLI ↔ Gateway Connection Resolution

This is the critical piece. The CLI determines the gateway WebSocket URL in
`src/gateway/call.ts:buildGatewayConnectionDetails()`:

```typescript
const bindMode = config.gateway?.bind ?? "loopback";   // reads from openclaw.json
const preferLan = bindMode === "lan";
const lanIPv4 = preferLan ? pickPrimaryLanIPv4() : undefined;

const localUrl =
  preferTailnet && tailnetIPv4 ? `${scheme}://${tailnetIPv4}:${localPort}`
  : preferLan && lanIPv4 ? `${scheme}://${lanIPv4}:${localPort}`
  : `${scheme}://127.0.0.1:${localPort}`;              // default: localhost
```

**URL resolution priority:**
1. `--url` CLI flag (explicit override)
2. `gateway.remote.url` config (remote mode only)
3. Auto-calculated from `gateway.bind` in config:
   - `bind=tailnet` → Tailnet IPv4
   - `bind=lan` → container's LAN IPv4 (e.g. `172.17.0.5`)
   - anything else → `127.0.0.1`

**This is our bug.** When `bind: "lan"` is in `openclaw.json`, the CLI resolves
the gateway at the container's LAN IP, triggering pairing requirements. Docker
avoids this by passing `--bind lan` as a **runtime flag** to the gateway process,
keeping the config file clean so the CLI defaults to `127.0.0.1`.

## Gateway Bind Modes

`src/gateway/net.ts:resolveGatewayBindHost()`:

| Mode       | Binds to              | Notes                                        |
|------------|-----------------------|----------------------------------------------|
| `loopback` | `127.0.0.1`          | Default. Falls back to `0.0.0.0` if can't.   |
| `lan`      | `0.0.0.0`            | All interfaces. No fallback.                  |
| `auto`     | `127.0.0.1`          | Prefers loopback, falls back to `0.0.0.0`.   |
| `tailnet`  | Tailscale IPv4        | Falls back to `127.0.0.1` then `0.0.0.0`.   |
| `custom`   | User-specified IPv4   | Falls back to `0.0.0.0`.                     |

Security: non-loopback bind **requires** a shared secret (token or password).

## Authentication

`src/gateway/auth.ts:resolveGatewayAuth()`:

```typescript
const token = authConfig.token ?? env.OPENCLAW_GATEWAY_TOKEN ?? env.CLAWDBOT_GATEWAY_TOKEN;
const password = authConfig.password ?? env.OPENCLAW_GATEWAY_PASSWORD;
const mode = authConfig.mode ?? (password ? "password" : "token");
```

**Resolution order:** config file → env var → legacy env var

Auth methods:
- **token** (default): shared secret token, hex string
- **password**: shared secret password
- **device-token**: per-device tokens issued after pairing
- **tailscale**: auto-allowed for loopback via Tailscale Serve

When is pairing required?
- NOT required when shared-secret auth (token/password) is configured
- NOT required for loopback connections with shared-secret
- Required for non-loopback connections without shared-secret

## Environment Variables (Relevant to Moat)

### Gateway Configuration
| Variable                     | Default      | Description                              |
|------------------------------|--------------|------------------------------------------|
| `OPENCLAW_GATEWAY_TOKEN`     | (generated)  | Auth token for gateway access            |
| `OPENCLAW_GATEWAY_PASSWORD`  | (none)       | Alternative: password auth               |
| `OPENCLAW_GATEWAY_PORT`      | `18789`      | Gateway WebSocket port                   |
| `OPENCLAW_GATEWAY_BIND`      | `lan`*       | Bind mode (docker default)               |
| `OPENCLAW_STATE_DIR`         | `~/.openclaw`| Base directory for config/state          |
| `OPENCLAW_CONFIG_PATH`       | (auto)       | Path to `openclaw.json`                  |

*Note: `OPENCLAW_GATEWAY_BIND` is used by `docker-setup.sh` and passed as
`--bind` CLI flag. It is NOT read as an env var by the gateway process itself.

### Feature Flags (Skip Subsystems)
| Variable                              | Description                       |
|---------------------------------------|-----------------------------------|
| `OPENCLAW_SKIP_CHANNELS`              | Skip channel initialization       |
| `OPENCLAW_SKIP_GMAIL_WATCHER`         | Skip Gmail Pub/Sub watcher        |
| `OPENCLAW_SKIP_CRON`                  | Skip cron scheduler               |
| `OPENCLAW_SKIP_CANVAS_HOST`           | Skip Canvas/A2UI host server      |
| `OPENCLAW_SKIP_BROWSER_CONTROL_SERVER`| Skip browser control server       |
| `OPENCLAW_SKIP_PROVIDERS`             | Skip provider initialization      |

### Networking & Discovery
| Variable                     | Default | Description                              |
|------------------------------|---------|------------------------------------------|
| `OPENCLAW_BRIDGE_PORT`       | `18790` | Bridge server port (mobile pairing)      |
| `OPENCLAW_DISABLE_BONJOUR`   | (off)   | Disable mDNS/Bonjour discovery           |
| `OPENCLAW_MDNS_HOSTNAME`     | (auto)  | Custom mDNS hostname                     |

### Model Provider Keys
| Variable              | Description                    |
|-----------------------|--------------------------------|
| `ANTHROPIC_API_KEY`   | Claude API key (`sk-ant-...`)  |
| `OPENAI_API_KEY`      | OpenAI API key                 |
| `GEMINI_API_KEY`      | Google Gemini API key          |
| `OPENROUTER_API_KEY`  | OpenRouter API key             |

### Misc
| Variable                     | Description                              |
|------------------------------|------------------------------------------|
| `OPENCLAW_PROFILE`           | `dev` enables verbose logging            |
| `OPENCLAW_LOAD_SHELL_ENV`    | Load env from login shell profile        |
| `OPENCLAW_HIDE_BANNER`       | Hide startup banner                      |
| `OPENCLAW_NO_RESPAWN`        | Prevent daemon respawn                   |

## The Fix for Moat

**Problem:** Our `openclaw.json` contains `"bind": "lan"`, which makes the CLI
resolve the gateway at the container's LAN IP instead of localhost, triggering
device pairing.

**Solution:** Mirror Docker's approach:
1. Do NOT put `bind` in `openclaw.json`
2. Pass `--bind lan` as a CLI flag to `openclaw gateway run`
3. Let the token resolve from `OPENCLAW_GATEWAY_TOKEN` env var (already injected by moat)
4. The CLI reads config, sees no `bind`, defaults to `ws://127.0.0.1:18789`
5. Gateway listens on `0.0.0.0:18789` (from `--bind lan`), so localhost works

**Minimal `openclaw.json`:**
```json
{
  "agents": {
    "defaults": {
      "model": {
        "primary": "anthropic/claude-sonnet-4-5"
      },
      "workspace": "~/.openclaw/workspace"
    }
  }
}
```

**Gateway start command:**
```bash
openclaw gateway run --bind lan &
```

Auth token comes from `OPENCLAW_GATEWAY_TOKEN` env var (injected by moat from
1Password). No need to write it into the config file.
