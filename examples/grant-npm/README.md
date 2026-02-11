# npm Credential Grant Example

This example demonstrates using npm registry credentials with Moat. The agent authenticates to npm registries via header injection — your token never enters the container environment.

## Quick start

### Option 1: Import from .npmrc (easiest)

If you've already run `npm login`:

```bash
# 1. Grant access (discovers tokens from ~/.npmrc)
moat grant npm

# 2. Run the example
moat run ./examples/grant-npm
```

### Option 2: Using environment variable

```bash
# 1. Set your token
export NPM_TOKEN="npm_xxxxxxxxxxxx"

# 2. Grant access
moat grant npm

# 3. Run the example
moat run ./examples/grant-npm
```

### Option 3: Enter token manually

```bash
# 1. Grant access (select "Enter token manually" when prompted)
moat grant npm

# 2. Paste your npm access token

# 3. Run the example
moat run ./examples/grant-npm
```

### Option 4: Specific registry

```bash
# 1. Grant access for a private registry
moat grant npm --host=npm.company.com

# 2. Run the example
moat run ./examples/grant-npm
```

## Expected output

```
your-username
```

## How it works

```
┌───────────────┐     ┌───────────────┐     ┌───────────────┐
│    Agent      │     │     Moat      │     │  npm Registry │
│  (container)  │     │    (proxy)    │     │               │
└───────┬───────┘     └───────┬───────┘     └───────┬───────┘
        │                     │                     │
        │ npm whoami          │                     │
        │ Authorization:      │                     │
        │   Bearer <stub>     │                     │
        │────────────────────>│                     │
        │                     │ GET /-/whoami        │
        │                     │ Authorization:      │
        │                     │   Bearer <real>     │
        │                     │────────────────────>│
        │                     │<────────────────────│
        │ your-username       │                     │
        │<────────────────────│                     │
```

1. **Grant phase**: `moat grant npm` stores your token(s) encrypted locally
2. **Run phase**: Moat generates a `.npmrc` with placeholder tokens and starts a TLS-intercepting proxy
3. **Request interception**: When npm sends a request to the registry, the proxy replaces the placeholder `Authorization` header with the real token
4. **Response**: The agent receives the response, never having seen the real token

## Multiple registries

Moat supports multiple npm registries with scope routing. Each `moat grant npm --host=<host>` adds to the existing credential:

```bash
# Add the default registry
moat grant npm

# Add a private registry for @myorg packages
moat grant npm --host=npm.company.com

# The generated .npmrc inside the container handles routing:
#   @myorg:registry=https://npm.company.com/
#   //registry.npmjs.org/:_authToken=<placeholder>
#   //npm.company.com/:_authToken=<placeholder>
```

## Verifying isolation

The real token never reaches the container:

```bash
# Check the .npmrc inside the container — only placeholder tokens
moat run --grant npm -- cat ~/.npmrc
# //registry.npmjs.org/:_authToken=npm_moatProxyInjected00000000

# But npm commands work — the proxy injects your real token at the network layer
moat run --grant npm -- npm whoami
# your-username
```
