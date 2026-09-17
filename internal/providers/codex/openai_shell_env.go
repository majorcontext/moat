package codex

import (
	"fmt"
	"path"
)

const OpenAIShellEnvFileName = "openai-env.sh"

var OpenAIShellEnvPath = path.Join("$HOME", ".codex", OpenAIShellEnvFileName)

func RenderOpenAIShellEnv() string {
	return fmt.Sprintf(`# Moat: OpenAI API key scoped to shell commands.
# Preserve an independently scoped Anthropic key when both dual-auth modes are active.
[ -f "$HOME/.claude/anthropic-env.sh" ] && . "$HOME/.claude/anthropic-env.sh"
export OPENAI_API_KEY=%q

# Codex subscription auth must win over API-key billing, including nested launches.
codex() { ( unset OPENAI_API_KEY BASH_ENV; command codex "$@" ); }
`, OpenAIAPIKeyPlaceholder)
}
