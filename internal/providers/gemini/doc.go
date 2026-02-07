// Package gemini provides Google Gemini CLI integration for Moat.
//
// This package handles:
//   - Credential injection through proxy configuration
//   - Configuration file generation for Gemini CLI
//   - Session management for Gemini runs
//   - Background OAuth token refresh
//
// # Authentication
//
// Gemini supports two authentication methods:
//
//  1. API Key - Standard API access via x-goog-api-key header
//  2. OAuth - Google OAuth2 access with automatic token refresh
//
// Credentials are handled via proxy injection, never exposed to containers:
//   - Container receives placeholder credentials in ~/.gemini/
//   - The Moat proxy intercepts requests and adds real Authorization headers
//
// # Important: OAuth vs API Key Use Different API Backends
//
// Gemini CLI routes to different API backends depending on authentication:
//   - API key mode: generativelanguage.googleapis.com (Google AI SDK)
//   - OAuth mode: cloudcode-pa.googleapis.com (Cloud Code Private API)
//
// The proxy must inject credentials for the correct host based on auth type.
//
// For OAuth, the proxy also performs token substitution on
// oauth2.googleapis.com — replacing placeholder tokens in the container's
// oauth_creds.json with real tokens before requests are forwarded to Google.
package gemini
