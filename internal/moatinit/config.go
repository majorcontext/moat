package moatinit

// Config is the entrypoint's environment contract, read once at startup and
// frozen — no MOAT_* control variable is re-read after a user-controlled
// phase (e.g. the pre_run hook) has executed.
//
// One deliberate exception mirrors the script: MOAT_INIT_FILES is also
// removed from the live process environment right after the init-files phase
// (the script's `unset MOAT_INIT_FILES`), so children spawned by later
// phases never see it.
type Config struct {
	ExtraHosts string // MOAT_EXTRA_HOSTS: space-separated name:target pairs for /etc/hosts
	SSHTCPAddr string // MOAT_SSH_TCP_ADDR: TCP address of the host-side SSH agent proxy

	ClaudeInit  string // MOAT_CLAUDE_INIT: staging dir for ~/.claude
	CodexInit   string // MOAT_CODEX_INIT: staging dir for ~/.codex
	GeminiInit  string // MOAT_GEMINI_INIT: staging dir for ~/.gemini
	CopilotInit string // MOAT_COPILOT_INIT: staging dir for ~/.copilot

	InitFiles string // MOAT_INIT_FILES: tab-delimited <path>\t<base64> records

	Clipboard string // MOAT_CLIPBOARD: "1" starts Xvfb :99 and exports DISPLAY

	GitUserName  string // MOAT_GIT_USER_NAME
	GitUserEmail string // MOAT_GIT_USER_EMAIL
	GitSSHGitHub string // MOAT_GIT_SSH_GITHUB: "1" sets the github.com insteadOf rewrite

	DockerDIND string // MOAT_DOCKER_DIND: "1" starts dockerd (dind mode)
	DockerGID  string // MOAT_DOCKER_GID: non-empty enables host-socket group mode

	WorkspaceVolume   string // MOAT_WORKSPACE_VOLUME: "1" populates /workspace from staging
	WorkspaceStaging  string // MOAT_WORKSPACE_STAGING: staging source (default /mnt/host-workspace)
	WorkspaceExcludes string // MOAT_WORKSPACE_EXCLUDES: newline-delimited ./-prefixed patterns

	VolumeChown string // MOAT_VOLUME_CHOWN: space-separated named-volume mount roots

	PreRun string // MOAT_PRE_RUN: hook command run in /workspace before the main command

	Home string // HOME of the entrypoint process (target home on the non-root path)
}

// LoadConfig snapshots the entrypoint environment from sys.
func LoadConfig(sys Sys) *Config {
	return &Config{
		ExtraHosts:        sys.Getenv("MOAT_EXTRA_HOSTS"),
		SSHTCPAddr:        sys.Getenv("MOAT_SSH_TCP_ADDR"),
		ClaudeInit:        sys.Getenv("MOAT_CLAUDE_INIT"),
		CodexInit:         sys.Getenv("MOAT_CODEX_INIT"),
		GeminiInit:        sys.Getenv("MOAT_GEMINI_INIT"),
		CopilotInit:       sys.Getenv("MOAT_COPILOT_INIT"),
		InitFiles:         sys.Getenv("MOAT_INIT_FILES"),
		Clipboard:         sys.Getenv("MOAT_CLIPBOARD"),
		GitUserName:       sys.Getenv("MOAT_GIT_USER_NAME"),
		GitUserEmail:      sys.Getenv("MOAT_GIT_USER_EMAIL"),
		GitSSHGitHub:      sys.Getenv("MOAT_GIT_SSH_GITHUB"),
		DockerDIND:        sys.Getenv("MOAT_DOCKER_DIND"),
		DockerGID:         sys.Getenv("MOAT_DOCKER_GID"),
		WorkspaceVolume:   sys.Getenv("MOAT_WORKSPACE_VOLUME"),
		WorkspaceStaging:  sys.Getenv("MOAT_WORKSPACE_STAGING"),
		WorkspaceExcludes: sys.Getenv("MOAT_WORKSPACE_EXCLUDES"),
		VolumeChown:       sys.Getenv("MOAT_VOLUME_CHOWN"),
		PreRun:            sys.Getenv("MOAT_PRE_RUN"),
		Home:              sys.Getenv("HOME"),
	}
}
