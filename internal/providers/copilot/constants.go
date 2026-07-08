package copilot

const (
	// CopilotInitMountPath is where the Copilot staging directory is mounted.
	CopilotInitMountPath = "/moat/copilot-init"

	// ContextFileName is loaded through COPILOT_CUSTOM_INSTRUCTIONS_DIRS so it
	// augments repository instructions without writing into /workspace.
	ContextFileName = "moat-context.instructions.md"

	copilotAPIHost      = "api.github.com"
	copilotGitHost      = "github.com"
	copilotProxyHost    = "copilot-proxy.githubusercontent.com"
	copilotChatAPIHost  = "api.githubcopilot.com"
	copilotBusinessHost = "api.business.githubcopilot.com"
	copilotMCPHost      = "api.mcp.github.com"
	copilotTelemetry    = "telemetry.business.githubcopilot.com"
	copilotProviderName = "copilot"
)
