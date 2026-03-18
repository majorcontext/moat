---
title: "Troubleshooting"
description: "Common errors and fixes for proxy, TLS, and credential issues."
keywords: ["moat", "troubleshooting", "errors", "proxy", "authentication", "credentials", "TLS"]
---

# Troubleshooting

Common errors, their causes, and fixes.

---

## Proxy errors

### `Proxy authentication required` / `Invalid proxy token` (407)

**Cause:** The container's proxy auth token does not match any run registered with the daemon. Runs normally re-register automatically after a daemon restart, so this typically indicates a stale container from a previous run that was not properly stopped.

**Fix:** Stop the stale container and start a new run:

```bash
moat stop <run-id>
moat run --grant github ./my-project
```

### `request blocked by network policy` (407)

```
Moat: request blocked by network policy.
Host "api.example.com" is not in the allow list.
```

**Cause:** The run uses a strict network policy and the target host is not in the allow list.

**Fix:** Add the host to `network.rules` in `moat.yaml`:

```yaml
network:
  policy: strict
  rules:
    - host: api.example.com
      allow: true
```

Or switch to a permissive policy:

```yaml
network:
  policy: permissive
```

---

## TLS and certificate errors

### `certificate verify failed` / `x509: certificate signed by unknown authority`

**Cause:** Moat's TLS-intercepting proxy uses a per-session CA certificate. If a tool inside the container does not trust this CA, TLS verification fails.

**Fix:** Moat mounts the CA certificate and sets `SSL_CERT_FILE` / `NODE_EXTRA_CA_CERTS` automatically. If a tool ignores these variables, point it at the CA cert directly:

```bash
curl --cacert /etc/ssl/certs/moat-ca/ca.crt https://api.github.com
```

Applications with certificate pinning cannot use the proxy for credential injection.

### HTTP client ignoring proxy

**Cause:** Some HTTP clients (including Claude Code's MCP client) do not respect `HTTP_PROXY` / `HTTPS_PROXY` environment variables.

**Fix:** For MCP servers, define them in `moat.yaml` under the top-level `mcp:` key. Moat generates relay URLs that route through the proxy without requiring the client to respect proxy settings.

For other tools, configure their proxy settings directly.

---

## Authentication and credential errors

### `credential not found: <provider>`

**Cause:** The grant has not been configured yet.

**Fix:** Run the grant command for the missing provider:

```bash
moat grant github
```

### `missing grants`

**Cause:** One or more grants required by the run are missing or cannot be decrypted.

**Fix:** Follow the instructions in the error output. Run `moat grant <provider>` for each listed provider.

### `Claude Code token has expired` / `invalid API key` / `invalid token (401)`

**Cause:** The stored credential is expired, invalid, or revoked.

**Fix:** Re-grant the affected provider:

```bash
moat grant claude
moat grant anthropic
moat grant github
```

---

## General tips

- Add `--verbose` to any `moat` command to see debug logs on stderr.
- Check `~/.moat/debug/` for structured JSON debug logs.
- Use `moat doctor` to check system configuration.
- Run storage (logs, network traces, audit data) is at `~/.moat/runs/<run-id>/`.

## Error not listed?

If you hit an error not covered here, please [file an issue](https://github.com/majorcontext/moat/issues/new) with the full error message and the command you ran. This helps us expand this page.
