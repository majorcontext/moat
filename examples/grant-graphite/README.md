# Graphite Credential Grant Example

This example demonstrates using Graphite credentials with Moat. The agent authenticates to the Graphite API via header injection — your token never enters the container environment.

## Quick start

### Option 1: Using environment variable

```bash
# 1. Set your token
export GRAPHITE_TOKEN="your-graphite-token"

# 2. Grant access
moat grant graphite

# 3. Run the example
moat run ./examples/grant-graphite
```

### Option 2: Interactive prompt

```bash
# 1. Grant access (will prompt for token)
moat grant graphite

# 2. Enter your token when prompted

# 3. Run the example
moat run ./examples/grant-graphite
```

## Getting a Graphite token

1. Visit [app.graphite.com/activate](https://app.graphite.com/activate)
2. Sign in with your GitHub account
3. Copy the auth token

## Expected output

```
=== Graphite Auth Status ===
Authenticated as: your-username

=== Graphite CLI Version ===
1.x.x

=== Git Version ===
git version 2.x.x

=== Graphite Config (in container) ===
{"authToken":"moat-proxy-injected"}

Token above is a placeholder — the real token is
injected by the proxy at the network layer.
```

## How it works

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Agent      │     │     Moat      │     │ Graphite API  │
│  (container)  │     │    (proxy)    │     │               │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │                     │
        │ gt auth             │                     │
        │ Authorization:      │                     │
        │   token <stub>      │                     │
        │────────────────────>│                     │
        │                     │ POST /check-auth    │
        │                     │ Authorization:      │
        │                     │   token <real>      │
        │                     │────────────────────>│
        │                     │<────────────────────│
        │ Authenticated as:   │                     │
        │   your-username     │                     │
        │<────────────────────│                     │
```

1. **Grant phase**: `moat grant graphite` stores your token encrypted locally
2. **Run phase**: Moat writes a config file with a placeholder token at container startup and routes traffic through a TLS-intercepting proxy
3. **Request interception**: When the Graphite CLI sends a request to `api.graphite.com`, the proxy replaces the `Authorization: token <stub>` header with the real token
4. **Response**: The agent receives the response, never having seen the real token

## Verifying isolation

The real token never reaches the container:

```bash
# Check the config inside the container — only placeholder token
moat run --grant graphite ./my-project -- cat ~/.config/graphite/user_config
# {"authToken":"moat-proxy-injected"}

# But gt commands work — the proxy injects your real token at the network layer
moat run --grant graphite ./my-project -- gt auth
# Authenticated as: your-username
```

## Revoking access

To remove the stored credential:

```bash
moat revoke graphite
```

This deletes the encrypted token from `~/.moat/credentials/graphite.enc`. The original token remains valid — revoke it separately at [app.graphite.com](https://app.graphite.com) if needed.
