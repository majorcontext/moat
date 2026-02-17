---
title: "OAuth relay"
navTitle: "OAuth relay"
description: "Use the OAuth relay to share a single Google OAuth callback across multiple agent runs."
keywords: ["moat", "oauth", "google", "relay", "authentication", "local development"]
---

# OAuth relay

Google OAuth requires registering specific `host:port` combinations as authorized redirect URIs. When multiple agent runs share a single `host:port` via Moat's subdomain routing, each app cannot register its own callback URL with Google.

The OAuth relay provides a single registered callback (`oauthrelay.localhost:8080/callback`) that routes authorization codes to the correct application.

This guide covers setting up the OAuth relay, integrating it into your application, and troubleshooting common issues.

## Prerequisites

- Moat installed
- A Google Cloud project with OAuth 2.0 credentials
- An agent configured with `ports` in `agent.yaml`

## Setup

### 1. Register redirect URI with Google

In the [Google Cloud Console](https://console.cloud.google.com/apis/credentials), add this authorized redirect URI to your OAuth 2.0 client:

```
http://oauthrelay.localhost:8080/callback
```

### 2. Grant Google OAuth credentials

```bash
moat grant google-oauth
```

This prompts for your Google OAuth client ID and client secret, and stores them encrypted locally.

### 3. Enable in agent.yaml

```yaml
oauth_relay: true
ports:
  web: 3000
```

### 4. Run the agent

```bash
moat claude ./my-app
```

Moat injects three environment variables into the container:

| Variable | Description |
|----------|-------------|
| `MOAT_OAUTH_RELAY_URL` | URL to start the OAuth flow via Moat relay |
| `GOOGLE_CLIENT_ID` | Google OAuth client ID |
| `GOOGLE_CLIENT_SECRET` | Google OAuth client secret |

## Authentication flow

```
Browser             App                 Moat Relay              Google
  |                  |                      |                      |
  |-- click login -->|                      |                      |
  |<-- redirect -----|                      |                      |
  |                  |                      |                      |
  |-- GET relay/start?app=myapp ---------->|                      |
  |<-- redirect to Google -----------------|                      |
  |                                         |                      |
  |-- authorize ---------------------------------------------->|
  |<-- redirect to Moat callback --------------------------------|
  |                                         |                      |
  |-- code= -------------------------------->|                      |
  |<-- redirect to app callback + code ------|                      |
  |                                         |                      |
  |-- /__auth/callback?code= -->|          |                      |
  |                  |-- exchange code --------------------------->|
  |                  |<-- tokens ----------------------------------|
  |<-- session ------|                      |                      |
```

### Step by step

1. User clicks "Login." App redirects browser to `${MOAT_OAUTH_RELAY_URL}?app=<agent-name>`.
2. Moat relay redirects to Google's OAuth endpoint with a state parameter that tracks which app started the flow.
3. User authenticates with Google.
4. Google redirects browser to `oauthrelay.localhost:8080/callback?code=...&state=...`.
5. Moat looks up which app started the flow and redirects browser to `web.<agent-name>.localhost:8080/__auth/callback?code=...`.
6. App receives the authorization code and exchanges it for tokens using `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.
7. App establishes session. Login complete.

### Custom callback path

By default, the relay routes the authorization code to `/__auth/callback` on the app. To use a different path, pass `callback_path` when starting the flow:

```
${MOAT_OAUTH_RELAY_URL}?app=myapp&callback_path=/auth/google/callback
```

## Application integration

The app's callback handler is identical in all environments. It always:

1. Receives an authorization code.
2. Exchanges it for tokens using `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.
3. Establishes a session.

The app does not know or care whether the code arrived via Google directly or via Moat's relay.

### Example (Node.js)

```javascript
// Login route - redirect to OAuth
app.get('/login', (req, res) => {
  const relayURL = process.env.MOAT_OAUTH_RELAY_URL;
  if (relayURL) {
    // Local development: use Moat's OAuth relay
    res.redirect(`${relayURL}?app=${process.env.MOAT_HOST?.split('.')[0]}`);
  } else {
    // Production: redirect to Google directly
    res.redirect(googleAuthURL());
  }
});

// Callback route - same in both environments
app.get('/__auth/callback', async (req, res) => {
  const { code } = req.query;
  const tokens = await exchangeCodeForTokens(code);
  req.session.user = await getUserInfo(tokens.access_token);
  res.redirect('/');
});
```

## Production comparison

| | Production | Local (Moat) |
|---|---|---|
| Browser redirects to | Google directly | Moat relay, then Google |
| Google calls back to | App's registered redirect URI | Moat's registered redirect URI |
| Code delivered to app by | Google | Moat (forwarded) |
| App exchanges code for tokens | Yes | Yes |
| OAuth credentials owned by | App | Moat (injected into app) |

The app's callback handler is the same in both environments.

## Troubleshooting

### "Google OAuth credentials not found"

Run `moat grant google-oauth` to store your client ID and secret.

### "oauth_relay requires at least one port"

The OAuth relay routes authorization codes via the routing proxy. Add a `ports` entry to `agent.yaml`:

```yaml
oauth_relay: true
ports:
  web: 3000
```

### Redirect URI mismatch

Verify that `http://oauthrelay.localhost:8080/callback` is registered as an authorized redirect URI in the Google Cloud Console. The port must match your Moat proxy port (default: 8080).

### Authorization code not reaching the app

- Verify the app name in the `?app=` parameter matches the agent name from `moat list`
- Check that the app has a `web` endpoint registered in `ports`
- Use `moat trace --network` to see if the callback request reached the proxy

## Related guides

- [agent.yaml reference](../reference/02-agent-yaml.md) -- `oauth_relay` field reference
- [CLI reference](../reference/01-cli.md) -- `moat grant google-oauth` command
- [Credential management](../concepts/02-credentials.md) -- How credential injection works
