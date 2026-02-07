package gemini

import "github.com/majorcontext/moat/internal/credential"

const (
	// GeminiInitMountPath is where the staging directory is mounted in containers.
	GeminiInitMountPath = "/moat/gemini-init"

	// GeminiAPIHost is the API endpoint used by Gemini CLI in OAuth mode.
	// OAuth mode uses the Cloud Code Private API, NOT generativelanguage.googleapis.com.
	GeminiAPIHost = "cloudcode-pa.googleapis.com"

	// GeminiAPIKeyHost is the API endpoint used in API key mode.
	GeminiAPIKeyHost = "generativelanguage.googleapis.com"

	// GeminiOAuthHost is Google's OAuth2 endpoint.
	GeminiOAuthHost = "oauth2.googleapis.com"

	// ProxyInjectedPlaceholder is the placeholder for proxy-injected credentials.
	ProxyInjectedPlaceholder = credential.ProxyInjectedPlaceholder

	// OAuthClientID is the public OAuth client ID used by Gemini CLI.
	//
	// Source: @google/gemini-cli-core npm package, code_assist/oauth2.ts
	//   npm pack @google/gemini-cli-core && tar xzf *.tgz
	//   grep "OAUTH_CLIENT_ID" package/dist/src/code_assist/oauth2.js
	//
	// This is an installed/desktop OAuth application. Per Google's OAuth2 docs:
	// https://developers.google.com/identity/protocols/oauth2#installed
	// "The process results in a client ID and, in some cases, a client secret,
	// which you embed in the source code of your application."
	OAuthClientID = "681255809395-oo8ft2oprdrnp9e3aqf6av3hmdib135j.apps.googleusercontent.com"

	// OAuthClientSecret is the public OAuth client secret used by Gemini CLI.
	//
	// Source: same file as OAuthClientID above (code_assist/oauth2.ts).
	// The Gemini CLI source includes this comment:
	//   "It's ok to save this in git because this is an installed application [...]
	//    In this context, the client secret is obviously not treated as a secret."
	//
	// See: https://developers.google.com/identity/protocols/oauth2#installed
	OAuthClientSecret = "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"

	// OAuthTokenURL is Google's OAuth2 token endpoint.
	OAuthTokenURL = "https://oauth2.googleapis.com/token"

	// ModelsURL is the Gemini API models endpoint for key validation.
	ModelsURL = "https://generativelanguage.googleapis.com/v1beta/models"

	// CredentialsFile is the path to Gemini CLI's OAuth credentials relative to home.
	CredentialsFile = ".gemini/oauth_creds.json"
)
