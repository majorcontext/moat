package claude

import (
	"fmt"
	"path"
)

// AnthropicShellEnvFileName is the name of the shell env file staged into the
// Claude init directory when the Anthropic API key is scoped to shell commands.
const AnthropicShellEnvFileName = "anthropic-env.sh"

// AnthropicShellEnvPath is the in-container path of the shell env file, after
// moat-init.sh copies it out of the staging mount.
//
// The staging mount itself is not usable here. It is a bind of a host directory
// at mode 0700, and the shell that sources this file may run as a different UID
// than the host user who owns it (volume-mode runs start as root and drop to
// moatuser, and Apple containers do not remap bind-mount ownership). A
// non-owner cannot traverse a 0700 directory, and bash treats an unreadable
// BASH_ENV as a silent no-op — the key would simply never arrive. Every other
// staged file avoids this by being copied out as root before the privilege
// drop; this one does the same.
//
// $HOME is left unexpanded deliberately: bash expands BASH_ENV's value before
// using it as a path, so this resolves to the runtime user's home whether
// moat-init.sh targeted /home/moatuser or an alternate $HOME.
var AnthropicShellEnvPath = path.Join("$HOME", ".claude", AnthropicShellEnvFileName)

// AnthropicShellEnvStagedPath is the path of the file inside the staging mount,
// where moat-init.sh reads it from.
var AnthropicShellEnvStagedPath = path.Join(ClaudeInitMountPath, AnthropicShellEnvFileName)

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
