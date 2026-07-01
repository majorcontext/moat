package pi

// PiInitMountPath is where the Pi staging directory is mounted in containers.
const PiInitMountPath = "/moat/pi-init"

// ContextFileName is the staged runtime-context file. It is injected into Pi's
// system prompt via --append-system-prompt (so it never clobbers a user's own
// AGENTS.md / CLAUDE.md).
const ContextFileName = "moat-context.md"
