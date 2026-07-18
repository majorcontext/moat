package moatinit

import (
	"fmt"
	"strings"
)

// Plan returns the ordered actions the entrypoint would take for the
// current environment and identity, without performing any of them — a
// side-effect-free dry-run (only read-only stats and lookups). It is the
// debugging affordance the shell script could never offer, and the release
// pipeline's positive functional gate: a regenerated-but-defective binary
// that lost the privilege-drop or scrub phases fails the gate regardless of
// its checksum.
//
// One line per decision, "<phase>: <action>". Wording here is NOT part of
// the parity contract (the script has no equivalent); the fatal stderr
// wordings pinned by the golden tests are.
func Plan(ctx *Context) []string {
	cfg, sys := ctx.Cfg, ctx.Sys
	euid := sys.Geteuid()
	moat := moatuserExists(sys)
	home := targetHome(euid, moat, cfg.Home)

	var out []string
	add := func(format string, args ...any) { out = append(out, fmt.Sprintf(format, args...)) }

	if cfg.ExtraHosts == "" {
		add("extra-hosts: skip (MOAT_EXTRA_HOSTS unset)")
	} else {
		for _, tok := range splitExtraHosts(cfg.ExtraHosts) {
			e := parseHostEntry(tok)
			if e.skip() {
				add("extra-hosts: skip malformed entry %q", tok)
				continue
			}
			if hostname, resolve := e.resolveTarget(); resolve {
				add("extra-hosts: resolve %q (IPv4 preferred, ~5s budget) and append to /etc/hosts as %q — fatal if unresolvable", hostname, e.name)
			} else {
				add("extra-hosts: append %q -> %q to /etc/hosts — fatal if unwritable", e.target, e.name)
			}
		}
	}

	if cfg.SSHTCPAddr != "" {
		add("ssh-agent-bridge: start socat %s (0660) <-> TCP:%s as a long-lived child", sshSocketPath, cfg.SSHTCPAddr)
	} else {
		add("ssh-agent-bridge: skip (MOAT_SSH_TCP_ADDR unset)")
	}

	agents := []struct{ name, staging, dir string }{
		{"claude-staging", cfg.ClaudeInit, ".claude"},
		{"codex-staging", cfg.CodexInit, ".codex"},
		{"gemini-staging", cfg.GeminiInit, ".gemini"},
		{"copilot-staging", cfg.CopilotInit, ".copilot"},
	}
	for _, a := range agents {
		switch {
		case a.staging == "":
			add("%s: skip (staging var unset)", a.name)
		case !isDir(sys, a.staging):
			add("%s: skip (%s is not a directory)", a.name, a.staging)
		default:
			add("%s: copy allowlisted files from %s into %s/%s (credential files forced 0600)", a.name, a.staging, home, a.dir)
		}
	}

	if cfg.InitFiles == "" {
		add("init-files: skip (MOAT_INIT_FILES unset)")
	} else {
		n := 0
		for _, rec := range parseInitFiles(cfg.InitFiles) {
			if rec.path != "" {
				n++
			}
		}
		add("init-files: write %d file(s) at 0600 (parents 0755), then scrub MOAT_INIT_FILES from the environment", n)
	}

	if cfg.Clipboard == "1" {
		add("clipboard: start Xvfb :99 as a long-lived child and export DISPLAY=:99")
	} else {
		add("clipboard: skip (MOAT_CLIPBOARD != 1)")
	}

	if _, err := sys.LookPath("git"); err != nil {
		add("git-config: skip (no git binary on PATH)")
	} else {
		for _, argv := range gitConfigCommands(cfg) {
			add("git-config: %s (best-effort)", strings.Join(argv, " "))
		}
	}

	switch {
	case dockerMutexViolated(cfg.DockerDIND, cfg.DockerGID):
		add("docker: FATAL — MOAT_DOCKER_DIND and MOAT_DOCKER_GID are mutually exclusive")
	case dindActive(cfg.DockerDIND, euid):
		add("docker: start dockerd (dind, vfs) as a long-lived child, wait up to %ds, add moatuser to the docker group", dindTimeoutSeconds)
	case hostGIDActive(cfg.DockerGID, euid, isSocket(sys, dockerSocketPath)):
		add("docker: detect %s group inside the container and add moatuser to it", dockerSocketPath)
	default:
		add("docker: skip")
	}

	if paths := volumeChownPaths(cfg.VolumeChown); len(paths) > 0 && euid == 0 && moat {
		add("named-volume-chown: chown %s to moatuser (non-recursive, best-effort)", strings.Join(paths, " "))
	} else {
		add("named-volume-chown: skip")
	}

	if workspaceVolumeEnabled(cfg.WorkspaceVolume) {
		n := 0
		if cfg.WorkspaceExcludes != "" {
			n = len(strings.Split(cfg.WorkspaceExcludes, "\n"))
		}
		add("populate-workspace-volume: tar-copy %s -> /workspace (%d exclude pattern(s), both pipe exit codes checked), then chown -R moatuser — requires root, fatal otherwise", stagingDir(cfg.WorkspaceStaging), n)
	} else {
		add("populate-workspace-volume: skip (MOAT_WORKSPACE_VOLUME != 1)")
	}

	switch {
	case cfg.CodexInit != "" && isFile(sys, cfg.CodexInit+"/mcp.json"):
		add("workspace-mcp-json: copy %s/mcp.json -> /workspace/.mcp.json", cfg.CodexInit)
	case cfg.GeminiInit != "" && isFile(sys, cfg.GeminiInit+"/mcp.json"):
		add("workspace-mcp-json: copy %s/mcp.json -> /workspace/.mcp.json", cfg.GeminiInit)
	default:
		add("workspace-mcp-json: skip (no staged mcp.json)")
	}

	if cfg.PreRun == "" {
		add("pre-run-hook: skip (MOAT_PRE_RUN unset)")
	} else {
		switch {
		case euid != 0:
			add("pre-run-hook: run %q via sh -c in /workspace (already non-root); a non-zero exit aborts with that code", cfg.PreRun)
		case moat:
			add("pre-run-hook: run %q via gosu moatuser sh -c in /workspace; a non-zero exit aborts with that code", cfg.PreRun)
		default:
			add("pre-run-hook: skip (root without moatuser)")
		}
	}

	cmd := strings.Join(ctx.Argv, " ")
	switch {
	case euid != 0:
		add("privilege drop: exec %q directly (already non-root; MOAT_INIT_FILES scrubbed from the exec environment)", cmd)
	case moat:
		add("privilege drop: exec gosu moatuser %q (MOAT_INIT_FILES scrubbed from the exec environment)", cmd)
	default:
		add("privilege drop: FATAL — running as root but the moatuser account does not exist")
	}

	return out
}
