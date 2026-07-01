// Package pi implements the Pi coding agent provider.
//
// Pi (https://github.com/earendil-works/pi) is a model-agnostic, BYOK terminal
// coding agent. Moat runs it in an isolated container with transparent
// credential injection.
//
// # No credential of its own
//
// Unlike the claude/codex/gemini providers, Pi has no dedicated credential. It
// runs against whichever backend the user's grant provides. v1 supports two
// backends:
//
//   - anthropic (x-api-key injection on api.anthropic.com)
//   - openai    (Bearer injection on api.openai.com)
//
// Every other Pi backend fails hard as future work. The backend is chosen by
// resolvePiProvider: pi.provider / --provider override, otherwise inferred from
// the single configured grant; ambiguous or missing configurations are hard
// errors.
//
// # Credential injection
//
// Injection is delegated entirely to the anthropic/openai credential providers
// (Pi honors HTTP_PROXY, verified). Pi's own ConfigureProxy/ContainerEnv are
// no-ops. The backend grant provider sets a placeholder API-key env var that Pi
// reads; the real key is injected by the proxy at the network layer and never
// touches the container filesystem.
//
// # Runtime context
//
// The Moat runtime-context markdown is injected via Pi's --append-system-prompt
// flag pointing at a staged file, so it augments Pi's system prompt without
// clobbering the user's own AGENTS.md / CLAUDE.md.
package pi
