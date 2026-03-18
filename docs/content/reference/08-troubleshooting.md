---
title: "Troubleshooting"
description: "Error-to-fix lookup table for common proxy, authentication, credential, and runtime errors."
keywords: ["moat", "troubleshooting", "errors", "proxy", "authentication", "credentials", "docker", "TLS"]
---

# Troubleshooting

Common errors, their causes, and fixes.

---

## Proxy errors

### `Proxy authentication required` (407)

```
Proxy authentication required
```

**Cause:** The container sent a request without a valid proxy auth token, or the token does not match any registered run.

**Fix:**

- The proxy daemon may have restarted while the container was still running. Stop and re-run the agent:

      moat stop <run-id>
      moat run --grant github ./my-project

- If the error persists, check that the daemon is running:

      moat proxy status

### `Invalid proxy token` (407)

```
Invalid proxy token
```

**Cause:** The container's proxy auth token does not match any run registered with the daemon. This happens when the daemon restarts and loses run registrations, or when a stale container is still running after its run was unregistered.

**Fix:** Stop and re-run the agent:

    moat stop <run-id>
    moat run --grant github ./my-project

### `request blocked by network policy` (407)

```
Moat: request blocked by network policy.
Host "api.example.com" is not in the allow list.
Add it to network.rules in moat.yaml or use policy: permissive.
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

### `proxy port mismatch`

```
proxy port mismatch: running on 8080, requested 9000. Either unset MOAT_PROXY_PORT, or stop all agents to restart the proxy
```

**Cause:** `MOAT_PROXY_PORT` is set to a port that differs from the port the already-running proxy is using. A single proxy serves all runs, and its port cannot change while active.

**Fix:** Either unset the variable to use the running proxy's port:

    unset MOAT_PROXY_PORT

Or stop all running agents so the proxy shuts down, then start again with the new port:

    moat stop --all

---

## TLS and certificate errors

### `certificate verify failed` / `x509: certificate signed by unknown authority`

**Cause:** Moat's TLS-intercepting proxy uses a per-session CA certificate. If a tool inside the container does not trust this CA, TLS verification fails.

**Fix:** Moat mounts the CA certificate and sets `SSL_CERT_FILE` / `NODE_EXTRA_CA_CERTS` automatically. If a tool ignores these variables:

1. Check the CA cert is mounted inside the container:

       ls /etc/ssl/certs/moat-ca/ca.crt

2. Point the tool at the CA cert. For example, with `curl`:

       curl --cacert /etc/ssl/certs/moat-ca/ca.crt https://api.github.com

3. For applications with certificate pinning, TLS interception is expected to fail. These applications cannot use the proxy for credential injection.

### HTTP client ignoring proxy

**Cause:** Some HTTP clients (including Claude Code's MCP client) do not respect `HTTP_PROXY` / `HTTPS_PROXY` environment variables.

**Fix:** For MCP servers, Moat works around this with a relay pattern. Define MCP servers in `moat.yaml` under the top-level `mcp:` key rather than configuring them directly in the agent. Moat generates relay URLs that route through the proxy without requiring the client to respect proxy settings.

For other tools, verify the tool supports proxy environment variables or configure its proxy settings directly.

---

## Authentication and credential errors

### `credential not found: <provider>`

```
credential not found: github
```

**Cause:** The grant has not been configured yet, or credentials were stored under a different profile.

**Fix:** Run the grant command for the missing provider:

    moat grant github

If using profiles, ensure the correct profile is active:

    export MOAT_PROFILE=work
    moat grant github

### `missing grants`

```
missing grants:
  - github: not configured
    Run: moat grant github
  - claude: encryption key changed
    Run: moat grant claude

Configure the grants above, then run again.
```

**Cause:** One or more grants required by the run are missing or cannot be decrypted.

**Fix:** Follow the instructions in the error output. Run `moat grant <provider>` for each listed provider.

### `decrypting credential for <provider>`

```
decrypting credential for github: cipher: message authentication failed
  This may indicate the encryption key has changed.
  If you recently upgraded moat, your credentials may have been encrypted with the old key.
  To re-authenticate: moat grant github
```

**Cause:** The encryption key used to store the credential has changed. This can happen after an OS upgrade, keychain reset, or when the key file at `~/.moat/key` is replaced.

**Fix:** Re-grant the affected credential:

    moat grant github

### `Claude Code token has expired`

```
Claude Code token has expired
```

**Cause:** The OAuth token stored by `moat grant claude` has expired.

**Fix:** Re-authenticate:

    moat grant claude

### `invalid API key` (Anthropic)

```
invalid API key (check that the key is correct and not expired)
```

**Cause:** The stored Anthropic API key is invalid, expired, or revoked.

**Fix:** Re-grant with a valid key:

    moat grant anthropic

### `invalid token (401 Unauthorized)` (GitHub)

```
invalid token (401 Unauthorized)
```

**Cause:** The stored GitHub token is invalid or revoked.

**Fix:** Re-grant GitHub access:

    moat grant github

### `MCP server requires grant but it's not configured`

```
MCP server 'my-server' requires grant 'mcp-my-server' but it's not configured

To fix:
  moat grant mcp my-server

Then run again.
```

**Cause:** An MCP server defined in `moat.yaml` requires a grant that has not been configured.

**Fix:** Run the grant command shown in the error:

    moat grant mcp my-server

### `key file has insecure permissions`

```
key file has insecure permissions: /home/user/.moat/key has permissions 0644 (expected 0600).
```

**Cause:** The encryption key file has overly permissive file permissions.

**Fix:** Restrict permissions:

    chmod 600 ~/.moat/key

---

## SSH errors

### `SSH grants require SSH_AUTH_SOCK to be set`

```
SSH grants require SSH_AUTH_SOCK to be set

Start your SSH agent with: eval "$(ssh-agent -s)" && ssh-add
```

**Cause:** The SSH agent is not running or `SSH_AUTH_SOCK` is not set.

**Fix:**

    eval "$(ssh-agent -s)"
    ssh-add
    moat run --grant ssh:github.com ./my-project

### `no SSH keys configured for hosts`

```
no SSH keys configured for hosts: [github.com]

Grant SSH access first:
  moat grant ssh --host github.com
```

**Cause:** No SSH key mapping exists for the requested host.

**Fix:** Grant SSH access:

    moat grant ssh --host github.com

---

## Container runtime errors

### `no container runtime available`

```
no container runtime available:
  Apple containers: system not running
  Docker: docker daemon not accessible: ...

To start Apple containers manually:
  container system start

To force a specific runtime:
  moat run --runtime apple
  moat run --runtime docker
```

**Cause:** Neither Docker nor Apple containers are available.

**Fix:**

- **Docker:** Start Docker Desktop or the Docker daemon.
- **Apple containers (macOS 26+):** Start the container system:

      container system start

### `Docker runtime requested but not available`

```
Docker runtime requested (via MOAT_RUNTIME or moat.yaml) but not available: ...
```

**Cause:** `MOAT_RUNTIME=docker` is set (or `runtime: docker` in `moat.yaml`) but Docker is not running.

**Fix:** Start Docker, or remove the runtime override to use auto-detection:

    unset MOAT_RUNTIME

### `docker daemon not accessible`

```
docker daemon not accessible: ...
```

**Cause:** The Docker daemon is not running or the current user does not have permission to access it.

**Fix:**

- Start Docker Desktop or the daemon: `sudo systemctl start docker`
- Add your user to the `docker` group: `sudo usermod -aG docker $USER` (then re-login)

### `no Linux kernel configured for Apple containers`

```
no Linux kernel configured for Apple containers.

Run this command to install the recommended kernel:

  container system kernel set --recommended

Then retry your moat command
```

**Cause:** Apple containers are available but no Linux kernel is installed.

**Fix:** Install the recommended kernel:

    container system kernel set --recommended

### `gVisor (runsc) is required but not available`

```
gVisor (runsc) is required but not available
```

**Cause:** gVisor is required for sandboxed execution on Linux but is not installed.

**Fix:** Install gVisor following the instructions in the error output, or bypass the sandbox requirement:

    moat run --no-sandbox ./my-project

> **Warning:** Running without gVisor reduces container isolation.

### `agent is already running`

```
agent "my-agent" is already running. Use --name to specify a different name, or stop the existing agent first
```

**Cause:** A run with the same agent name is already active.

**Fix:** Stop the existing run first, or use a different name:

    moat stop my-agent
    moat run ./my-project

    # Or use a different name:
    moat run --name my-agent-2 ./my-project

---

## Daemon errors

### `daemon did not start within 5 seconds`

```
daemon did not start within 5 seconds
```

**Cause:** The proxy daemon process failed to start or become healthy.

**Fix:**

1. Check for a stale lock file:

       cat ~/.moat/proxy/daemon.lock

2. If the PID in the lock file is not running, remove the lock and retry:

       rm ~/.moat/proxy/daemon.lock
       moat run --grant github ./my-project

3. Check daemon logs:

       ls ~/.moat/debug/

### `connecting to daemon`

```
connecting to daemon: ...
```

**Cause:** The CLI cannot connect to the daemon's Unix socket at `~/.moat/proxy/daemon.sock`.

**Fix:**

- The daemon may have crashed. Remove stale state and retry:

      rm -f ~/.moat/proxy/daemon.lock ~/.moat/proxy/daemon.sock
      moat run --grant github ./my-project

- Check if another process is using the socket path.

### `registering run with proxy daemon`

```
registering run with proxy daemon: ...
```

**Cause:** The daemon is running but rejected the run registration. This can happen if the daemon is from a different Moat version with an incompatible API.

**Fix:** Stop all runs so the daemon shuts down (auto-shutdown after 5 minutes idle), then retry:

    moat stop --all

Wait for the daemon to shut down (or remove the lock file), then start a new run.

---

## Build errors

### `building image with dependencies`

```
building image with dependencies [node@20, python@3.12]: ...
```

**Cause:** The Docker or Apple container image build failed. Common causes: network issues pulling base images, invalid dependency versions, or syntax errors in custom Dockerfile instructions.

**Fix:**

1. Check your internet connection and Docker Hub access.
2. Verify dependency versions in `moat.yaml` are valid:

   ```yaml
   dependencies:
     - node@20
     - python@3.12
   ```

3. Run with `--verbose` to see the full build log:

       moat run --verbose ./my-project

### `BuildKit requires Docker runtime`

```
BuildKit requires Docker runtime (networks not supported by apple)
```

**Cause:** BuildKit sidecars require Docker for network support. Apple containers do not support this.

**Fix:** Use Docker runtime for builds that require BuildKit:

    MOAT_RUNTIME=docker moat run ./my-project

Or unset `BUILDKIT_HOST` to use the legacy builder:

    unset BUILDKIT_HOST

---

## Service dependency errors

### `service failed to become ready`

```
postgres service failed to become ready: ...

Check:
  - Is the service image available?
  - Is the readiness command correct?
```

**Cause:** A service dependency (e.g., PostgreSQL, Redis) defined in `moat.yaml` did not pass its readiness check within the timeout.

**Fix:**

1. Verify the service image exists and can be pulled.
2. Check the readiness check command in `moat.yaml`.
3. Increase the readiness timeout if the service needs more startup time.

### `service provisioning failed`

```
postgres service provisioning failed: ...
```

**Cause:** A provisioning command for a service dependency failed after the service became ready.

**Fix:** Check the provisioning commands in `moat.yaml` and verify they work against the service image you specified.

---

## Firewall errors

### `firewall setup failed`

```
firewall setup failed (required for strict network policy): ...
```

**Cause:** `iptables` is not available inside the container, but a strict network policy requires firewall rules.

**Fix:**

- Ensure the container image includes `iptables`. Moat's default images include it.
- If using a custom image, install `iptables`:

  ```yaml
  dependencies:
    - node@20
  ```

  Using Moat's dependency system ensures `iptables` is available.

---

## General tips

- **Verbose output:** Add `--verbose` to any `moat` command to see debug logs on stderr.
- **Debug logs:** Check `~/.moat/debug/` for structured JSON debug logs.
- **Run diagnostics:** Use `moat doctor` to check system configuration.
- **Daemon state:** Check `~/.moat/proxy/daemon.lock` for daemon PID and port info.
- **Run storage:** Logs, network traces, and audit data for each run are stored in `~/.moat/runs/<run-id>/`.

## Error not listed?

If you hit an error not covered here, please [file an issue](https://github.com/majorcontext/moat/issues/new) with the full error message and the command you ran. This helps us expand this page.
