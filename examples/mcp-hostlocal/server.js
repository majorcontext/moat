#!/usr/bin/env node
// Minimal host-local MCP server (zero dependencies).
//
// Simulates corp tooling that authenticates with a credential only available
// on the host — here, a "token" read from the host environment. The agent in
// the sandbox can call this server's tools through Moat's proxy relay, but the
// credential never leaves the host: it is not injected into the container, not
// written to any container config, and never returned in a tool result.
//
// Speaks the MCP streamable-HTTP transport well enough for a basic client:
// POST JSON-RPC, single JSON response. Listens on 127.0.0.1 only.

const http = require("http");

const PORT = parseInt(process.env.PORT || "9123", 10);

// A host-only secret. In real corp tooling this would come from a credential
// process, the OS keychain, or a VPN-gated CLI — none reachable from inside
// the sandbox. We only ever expose facts *derived* from it, never the value.
const CORP_TOKEN = process.env.CORP_TOKEN || "s3cr3t-corp-token-do-not-leak";
const CORP_USER = process.env.CORP_USER || "alice@corp.example";

const TOOLS = [
  {
    name: "whoami",
    description:
      "Report the corp identity this host server authenticates as. " +
      "Uses the host-only credential without revealing it.",
    inputSchema: { type: "object", properties: {}, additionalProperties: false },
  },
];

function handle(msg) {
  switch (msg.method) {
    case "initialize":
      return {
        protocolVersion: "2024-11-05",
        capabilities: { tools: {} },
        serverInfo: { name: "corp-hostlocal", version: "0.1.0" },
      };
    case "tools/list":
      return { tools: TOOLS };
    case "tools/call":
      if (msg.params && msg.params.name === "whoami") {
        // Derived fact only — the token value is never returned.
        return {
          content: [
            {
              type: "text",
              text:
                `Authenticated to corp as ${CORP_USER} ` +
                `(credential present on host, length ${CORP_TOKEN.length}).`,
            },
          ],
        };
      }
      throw { code: -32602, message: `unknown tool: ${msg.params && msg.params.name}` };
    default:
      throw { code: -32601, message: `method not found: ${msg.method}` };
  }
}

const server = http.createServer((req, res) => {
  if (req.method !== "POST") {
    res.writeHead(405).end();
    return;
  }
  let body = "";
  req.on("data", (c) => (body += c));
  req.on("end", () => {
    let msg;
    try {
      msg = JSON.parse(body);
    } catch {
      res.writeHead(400).end();
      return;
    }
    console.error(`[host-mcp] ${msg.method} (id=${msg.id ?? "-"})`);

    // Notifications (no id) get an empty 202 — no response body.
    if (msg.id === undefined || msg.id === null) {
      res.writeHead(202).end();
      return;
    }

    let result, error;
    try {
      result = handle(msg);
    } catch (e) {
      error = e.code ? e : { code: -32603, message: String(e) };
    }
    const payload = { jsonrpc: "2.0", id: msg.id };
    if (error) payload.error = error;
    else payload.result = result;

    res.writeHead(200, { "Content-Type": "application/json" });
    res.end(JSON.stringify(payload));
  });
});

server.listen(PORT, "127.0.0.1", () => {
  console.error(`[host-mcp] listening on http://127.0.0.1:${PORT} (corp token hidden from sandbox)`);
});
