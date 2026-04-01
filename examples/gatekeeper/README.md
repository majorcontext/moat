# Gate Keeper Example

Run the Gate Keeper credential-injecting proxy standalone, outside of Moat.

## Quick start

```bash
# 1. Generate a TLS CA certificate for HTTPS interception
./gen-ca.sh

# 2. Set a GitHub token for credential injection
export GITHUB_TOKEN="ghp_your_token_here"

# 3. Build and start the proxy
./run.sh

# 4. In another terminal, test it
./test.sh
```

## Manual testing with curl

Trust the example CA and point curl through the proxy:

```bash
# Health check (plain HTTP, no TLS)
curl http://127.0.0.1:9080/healthz

# HTTPS request with credential injection
export GITHUB_TOKEN="ghp_..."
curl --cacert ca.crt --proxy http://127.0.0.1:9080 https://api.github.com/user
```

## Files

| File | Purpose |
|------|---------|
| `gatekeeper.yaml` | Proxy config with credential sources |
| `gen-ca.sh` | Generate self-signed CA for TLS interception |
| `run.sh` | Build and run the proxy |
| `test.sh` | Automated curl test |
