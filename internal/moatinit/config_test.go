package moatinit

import "testing"

// TestLoadConfigMapsEveryEnvVar is the producer↔consumer drift guard for the
// entrypoint's env contract: LoadConfig is the only place env var names map to
// Config fields, and the producer (internal/run, internal/providers) sets those
// names independently. A rename on either side silently disables a phase, so
// assert every field explicitly (companion to the unset/zero case).
func TestLoadConfigMapsEveryEnvVar(t *testing.T) {
	ts := newTestSys(t, 0, true)
	env := map[string]string{
		"MOAT_EXTRA_HOSTS":             "a:1.2.3.4",
		"MOAT_SSH_TCP_ADDR":            "127.0.0.1:5522",
		"MOAT_CLAUDE_INIT":             "/mnt/claude",
		"MOAT_CODEX_INIT":              "/mnt/codex",
		"MOAT_CODEX_SUBSCRIPTION_AUTH": "1",
		"MOAT_GEMINI_INIT":             "/mnt/gemini",
		"MOAT_COPILOT_INIT":            "/mnt/copilot",
		"MOAT_INIT_FILES":              "/home/moatuser/.x\tYWJj",
		"MOAT_CLIPBOARD":               "1",
		"MOAT_GIT_USER_NAME":           "Ada",
		"MOAT_GIT_USER_EMAIL":          "ada@example.com",
		"MOAT_GIT_SSH_GITHUB":          "1",
		"MOAT_DOCKER_DIND":             "1",
		"MOAT_DOCKER_GID":              "999",
		"MOAT_WORKSPACE_VOLUME":        "1",
		"MOAT_WORKSPACE_STAGING":       "/mnt/host-workspace",
		"MOAT_WORKSPACE_EXCLUDES":      "./node_modules\n./dist",
		"MOAT_VOLUME_CHOWN":            "/r/a /r/b",
		"MOAT_PRE_RUN":                 "npm install",
		"HOME":                         "/root",
	}
	for k, v := range env {
		ts.env[k] = v
	}

	cfg := LoadConfig(ts)
	checks := map[string]struct{ got, want string }{
		"ExtraHosts":            {cfg.ExtraHosts, env["MOAT_EXTRA_HOSTS"]},
		"SSHTCPAddr":            {cfg.SSHTCPAddr, env["MOAT_SSH_TCP_ADDR"]},
		"ClaudeInit":            {cfg.ClaudeInit, env["MOAT_CLAUDE_INIT"]},
		"CodexInit":             {cfg.CodexInit, env["MOAT_CODEX_INIT"]},
		"CodexSubscriptionAuth": {cfg.CodexSubscriptionAuth, env["MOAT_CODEX_SUBSCRIPTION_AUTH"]},
		"GeminiInit":            {cfg.GeminiInit, env["MOAT_GEMINI_INIT"]},
		"CopilotInit":           {cfg.CopilotInit, env["MOAT_COPILOT_INIT"]},
		"InitFiles":             {cfg.InitFiles, env["MOAT_INIT_FILES"]},
		"Clipboard":             {cfg.Clipboard, env["MOAT_CLIPBOARD"]},
		"GitUserName":           {cfg.GitUserName, env["MOAT_GIT_USER_NAME"]},
		"GitUserEmail":          {cfg.GitUserEmail, env["MOAT_GIT_USER_EMAIL"]},
		"GitSSHGitHub":          {cfg.GitSSHGitHub, env["MOAT_GIT_SSH_GITHUB"]},
		"DockerDIND":            {cfg.DockerDIND, env["MOAT_DOCKER_DIND"]},
		"DockerGID":             {cfg.DockerGID, env["MOAT_DOCKER_GID"]},
		"WorkspaceVolume":       {cfg.WorkspaceVolume, env["MOAT_WORKSPACE_VOLUME"]},
		"WorkspaceStaging":      {cfg.WorkspaceStaging, env["MOAT_WORKSPACE_STAGING"]},
		"WorkspaceExcludes":     {cfg.WorkspaceExcludes, env["MOAT_WORKSPACE_EXCLUDES"]},
		"VolumeChown":           {cfg.VolumeChown, env["MOAT_VOLUME_CHOWN"]},
		"PreRun":                {cfg.PreRun, env["MOAT_PRE_RUN"]},
		"Home":                  {cfg.Home, env["HOME"]},
	}
	for name, c := range checks {
		if c.got != c.want {
			t.Errorf("LoadConfig field %s = %q, want %q", name, c.got, c.want)
		}
	}

	// Companion: an empty environment yields the zero Config.
	empty := LoadConfig(newTestSys(t, 0, false))
	if *empty != (Config{}) {
		t.Errorf("LoadConfig(empty) = %+v, want zero Config", *empty)
	}
}
