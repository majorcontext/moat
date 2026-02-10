// Package provider defines interfaces for credential and agent providers.
//
// All providers implement CredentialProvider for credential acquisition,
// proxy configuration, and container setup. Agent providers (Claude, Codex, Gemini)
// additionally implement AgentProvider for container preparation and CLI commands.
// Endpoint providers (AWS) implement EndpointProvider to expose HTTP endpoints.
//
// Providers are registered explicitly via Register() and looked up via Get().
package provider
