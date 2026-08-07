package claude

import (
	"fmt"
	"path"
)

// AnthropicShellEnvFileName is the name of the shell env file staged into the
// Claude init directory when the Anthropic API key is scoped to shell commands.
const AnthropicShellEnvFileName = "anthropic-env.sh"

// AnthropicShellEnvPath is the in-container path of the shell env file.
var AnthropicShellEnvPath = path.Join(ClaudeInitMountPath, AnthropicShellEnvFileName)

// RenderAnthropicShellEnv returns the contents of the BASH_ENV file that
// exports the Anthropic API key to shell commands only.
//
// # Why this exists
//
// When a run holds both the "claude" (OAuth) and "anthropic" (API key) grants,
// exporting ANTHROPIC_API_KEY into the container environment breaks Claude Code:
// the API key takes precedence over its OAuth login, silently moving the session
// off the subscription and onto API billing.
//
// Claude Code ships as a native binary, so it ignores BASH_ENV. Non-interactive
// bash — which is exactly how Claude Code's Bash tool runs commands
// (`bash -c '...'`) — sources it on every invocation. Staging the key here gives
// agent-authored scripts a real API key while keeping it out of Claude Code's
// own environment.
func RenderAnthropicShellEnv() string {
	return fmt.Sprintf(`# Moat: Anthropic API key scoped to shell commands.
#
# Sourced by every non-interactive bash shell via BASH_ENV. The key is exported
# here rather than in the container environment so that Claude Code never sees
# it: ANTHROPIC_API_KEY takes precedence over Claude Code's OAuth login and
# would silently switch the session onto API billing.
#
# The value is a placeholder. The real key is injected by the moat proxy at the
# network layer, so send it explicitly (-H "x-api-key: $ANTHROPIC_API_KEY") or
# use an SDK that reads the variable.
export ANTHROPIC_API_KEY=%q

# Keep nested "claude" invocations on OAuth. Unsetting BASH_ENV as well is
# required, not incidental: a shell-script launcher would otherwise re-source
# this file and re-export the key, undoing the unset.
claude() { ( unset ANTHROPIC_API_KEY BASH_ENV; command claude "$@" ); }
`, ProxyInjectedPlaceholder)
}

// AnthropicShellEnvVars returns the container environment variables that
// activate the staged shell env file.
func AnthropicShellEnvVars() []string {
	return []string{"BASH_ENV=" + AnthropicShellEnvPath}
}
