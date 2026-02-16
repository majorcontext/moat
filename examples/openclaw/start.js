#!/usr/bin/env node
"use strict";

const { execFileSync, spawn } = require("child_process");
const { existsSync, mkdirSync, readFileSync, writeFileSync } = require("fs");
const { resolve } = require("path");
const { homedir } = require("os");

const OPENCLAW_DIR = resolve(homedir(), ".openclaw");
const CONFIG_PATH = resolve(OPENCLAW_DIR, "openclaw.json");
const GATEWAY_LOG = resolve(OPENCLAW_DIR, "logs", "gateway.log");

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

/** Run a command, inheriting stdio so the user sees output. */
function run(cmd, args, opts = {}) {
  return execFileSync(cmd, args, { stdio: "inherit", ...opts });
}

/** Run a command silently and return stdout, or "" on failure. */
function runQuiet(cmd, args) {
  try {
    return execFileSync(cmd, args, { encoding: "utf8", stdio: ["pipe", "pipe", "pipe"] }).trim();
  } catch {
    return "";
  }
}

/** Deep-set a dotted path on an object without clobbering siblings. */
function deepSet(obj, path, value) {
  const keys = path.split(".");
  let cur = obj;
  for (let i = 0; i < keys.length - 1; i++) {
    if (typeof cur[keys[i]] !== "object" || cur[keys[i]] === null) cur[keys[i]] = {};
    cur = cur[keys[i]];
  }
  cur[keys[keys.length - 1]] = value;
}

// ---------------------------------------------------------------------------
// 1. First-run onboard
// ---------------------------------------------------------------------------

mkdirSync(resolve(OPENCLAW_DIR, "workspace"), { recursive: true });

if (!existsSync(CONFIG_PATH)) {
  // Register a stub API key — the real credential is injected by moat's proxy.
  try {
    run("openclaw", [
      "onboard",
      "--non-interactive",
      "--accept-risk",
      "--no-install-daemon",
      "--anthropic-api-key", "moat-proxy-injected",
      "--gateway-auth", "token",
    ]);
  } catch {
    // onboard may fail on repeated attempts; continue regardless.
  }
}

// ---------------------------------------------------------------------------
// 2. Configure openclaw.json (merge, don't overwrite)
// ---------------------------------------------------------------------------

// Resolve the host IPs that moat's routing proxy connects from.
// Docker containers reach the host via host.docker.internal; we also trust
// the bridge gateway (.1 on the same subnet) since it can differ.
let trustedProxies = ["172.17.0.1"];

const hostIP = runQuiet("getent", ["ahostsv4", "host.docker.internal"]).split(/\s/)[0];
if (hostIP) {
  const bridgeGW = hostIP.replace(/\.\d+$/, ".1");
  trustedProxies = [hostIP, bridgeGW];
} else {
  console.log("Using default Docker bridge gateway (172.17.0.1) for trusted proxies");
}

let cfg = {};
try { cfg = JSON.parse(readFileSync(CONFIG_PATH, "utf8")); } catch {}

deepSet(cfg, "gateway.mode", "local");
deepSet(cfg, "gateway.bind", "lan");             // 0.0.0.0 — required for Docker port mapping
deepSet(cfg, "gateway.trustedProxies", trustedProxies);
deepSet(cfg, "gateway.controlUi.dangerouslyDisableDeviceAuth", true);
deepSet(cfg, "channels.telegram", { enabled: true, dmPolicy: "pairing" });
deepSet(cfg, "agents.defaults.model", { primary: "anthropic/claude-sonnet-4-5" });

writeFileSync(CONFIG_PATH, JSON.stringify(cfg, null, 2) + "\n");

// ---------------------------------------------------------------------------
// 3. Security audit (best-effort)
// ---------------------------------------------------------------------------

try { run("openclaw", ["security", "audit"]); } catch {}

// ---------------------------------------------------------------------------
// 4. Start gateway in background, logs to file
// ---------------------------------------------------------------------------

// Placeholder so OpenClaw's auth store check passes.
process.env.ANTHROPIC_API_KEY = "moat-proxy-injected";

mkdirSync(resolve(OPENCLAW_DIR, "logs"), { recursive: true });

const { openSync } = require("fs");
const logFd = openSync(GATEWAY_LOG, "a");
const gateway = spawn("openclaw", ["gateway", "run"], {
  stdio: ["ignore", logFd, logFd],
  detached: true,
});
gateway.unref();

// Give the gateway a moment to start.
execFileSync("sleep", ["2"]);

// ---------------------------------------------------------------------------
// 5. Print instructions and drop into interactive shell
// ---------------------------------------------------------------------------

console.log(`
=== OpenClaw Gateway ===
Control UI: open the URL shown by moat for the 'gateway' port

To get a dashboard link with auth token:
  openclaw dashboard --no-open

Gateway logs: tail -f ${GATEWAY_LOG}
`);

if (process.env.TELEGRAM_BOT_TOKEN) {
  console.log(`=== Telegram Bot ===
Telegram bot is enabled. DM your bot to get a pairing code, then approve:
  openclaw pairing list telegram
  openclaw pairing approve telegram <CODE>
`);
}

// Replace this process with an interactive bash shell.
// Using spawn + exit instead of exec so the gateway process stays alive.
const shell = spawn("bash", [], { stdio: "inherit" });
shell.on("exit", (code) => process.exit(code ?? 0));
