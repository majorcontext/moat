# Pi + LunaRoute Example

Run the [Pi coding agent](https://github.com/earendil-works/pi) against [LunaRoute](https://lunaroute.com) with the key injected by the Moat proxy.

## Setup (one-time)

```bash
export LUNAROUTE_API_KEY="lr_..."
moat grant lunaroute
```

Moat validates the key against `gw.lunaroute.com` and stores it encrypted. The container only gets a placeholder.

## Run

```bash
moat pi examples/agent-pi-lunaroute
moat pi examples/agent-pi-lunaroute -p "fix the bug in main.py"
```

Pi starts on the first model LunaRoute lists; switch with `/model` inside Pi.

## What Moat does for the lunaroute backend

- Installs `npm:@lunaroute/pi-extension` into the image.
- Writes `~/.pi/agent/auth.json` with a placeholder login. The proxy swaps in the real key on `gw.lunaroute.com` (`Authorization: Bearer`) and `mcp.lunaroute.com` (`LUNAROUTE-API-KEY`), and nowhere else.
- Loads LunaRoute's model list before Pi starts (about 1–2 seconds). Pi picks its model before it fetches any list, so without this step a new container fails with `Unknown provider "lunaroute"`.

## Recommended packages

`moat.yaml` also installs the Pi packages the LunaRoute team recommends (September 2026), pinned to the versions tested with this example. None are required, and each is third-party code that runs inside Pi, so trim the list to what you want. Together they add a few seconds to startup.

## Common mistakes

- **Using `moat grant anthropic --base-url https://gw.lunaroute.com` for Pi.** That grant is for Claude Code. `moat pi` refuses to start with it and tells you to run `moat grant lunaroute`.
- **Starting Pi with `moat run`.** It skips the extension and the model-list step, so Pi has no LunaRoute models. Use `moat pi`.

See [Running Pi](../../docs/content/guides/16-pi.md#lunaroute) for details.
