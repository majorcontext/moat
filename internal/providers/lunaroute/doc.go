// Package lunaroute implements a credential provider for LunaRoute
// (https://lunaroute.com), a gateway that serves many models behind one key.
//
// The provider exists for the Pi agent. LunaRoute's Pi extension
// (npm:@lunaroute/pi-extension) registers a "lunaroute" provider that sends the
// key two ways:
//
//   - Authorization: Bearer <key> to gw.lunaroute.com (inference and the
//     /v1/models catalog sync)
//   - LUNAROUTE-API-KEY: <key> to mcp.lunaroute.com (web/image/convert tools)
//
// The extension reads the key from ~/.pi/agent/auth.json. Moat writes a
// placeholder there at container start and the proxy injects the real key on
// both hosts, so the key never enters the container.
//
// Claude Code reaches LunaRoute through `moat grant anthropic --base-url`
// instead; that grant injects x-api-key, which LunaRoute ignores whenever a
// Bearer header is also present — so the two are not interchangeable.
package lunaroute
