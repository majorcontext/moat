package moatinit

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
)

// chownPrune lists, per agent, the subdirectories under the agent's home that
// must NOT be recursed into during the ownership hand-off. Codex's
// ~/.codex/sessions is a bind mount of the host's session directory when
// codex.sync_logs is on, so recursing would re-own the host user's files.
var chownPrune = map[string][]string{
	"codex": {"sessions"},
}

// stagedEntry is one allowlisted item an agent staging block may copy. The
// blocks copy ONLY explicitly named files — an allowlist, never a recursive
// copy of the staging dir (AGENT-NO-EXTRANEOUS-COPY): a stray file in the
// staging mount must not leak into the home directory.
type stagedEntry struct {
	name   string // file (or dir) name inside the staging dir
	secret bool   // chmod 600 after the copy — the credential-file contract
	tree   bool   // directory copied with cp -rp instead of cp -p
	home   bool   // destination is $TARGET_HOME itself, not the agent dir
}

// claudeStagingPhase mirrors the Claude Code setup block.
func claudeStagingPhase(ctx *Context) error {
	return stageAgent(ctx, "claude", ctx.Cfg.ClaudeInit, ".claude", []stagedEntry{
		{name: "settings.json"},
		// Plugins are baked into the image at build time; settings.json
		// above carries the marketplace config (AGENT-CLAUDE-NO-PLUGINS-COPY).
		{name: ".credentials.json", secret: true},
		// Server-managed settings cache; prevents a managed-settings
		// approval prompt on every container start.
		{name: "remote-settings.json", secret: true},
		{name: "statsig", tree: true},
		{name: "stats-cache.json"},
		{name: "CLAUDE.md"},
		// Onboarding/trust state lands at the HOME ROOT, not in .claude/.
		{name: ".claude.json", home: true},
		// The BASH_ENV file is read at runtime, not only during init, so it
		// must live outside the 0700 staging mount.
		{name: "anthropic-env.sh"},
	})
}

// codexStagingPhase mirrors the Codex CLI setup block.
func codexStagingPhase(ctx *Context) error {
	if err := codexSubscriptionVersionGate(ctx); err != nil {
		return err
	}
	return stageAgent(ctx, "codex", ctx.Cfg.CodexInit, ".codex", []stagedEntry{
		{name: "config.toml"},
		{name: "auth.json", secret: true},
		{name: "AGENTS.md"},
		{name: "openai-env.sh"},
	})
}

// codexSubscriptionGateRan reports whether the subscription-auth version check
// applies: the subscription mode is set AND a Codex staging dir is present (the
// gate lives inside the staging block, so it fires only when staging would run).
func codexSubscriptionGateRan(cfg *Config, sys Sys) bool {
	return cfg.CodexSubscriptionAuth == "1" && cfg.CodexInit != "" && isDir(sys, cfg.CodexInit)
}

// codexSubscriptionVersionGate mirrors the shell's subscription-auth guard:
// the synthetic auth.json is only known to work on Codex CLI 0.146.x–0.154.x,
// so when the subscription grant is active the executable actually installed is
// re-checked. Every other mode stages files any Codex version can read and must
// not be blocked.
func codexSubscriptionVersionGate(ctx *Context) error {
	if !codexSubscriptionGateRan(ctx.Cfg, ctx.Sys) {
		return nil
	}
	sys := ctx.Sys
	if _, err := sys.LookPath("codex"); err != nil {
		fmt.Fprintln(ctx.Stderr, "Moat: the codex grant needs the codex-cli dependency; add 'codex-cli' to dependencies in moat.yaml")
		return exitError{code: 1}
	}
	var out strings.Builder
	_, _ = sys.Run(Cmd{Argv: []string{"codex", "--version"}, Stdout: &out})
	version := secondField(out.String())
	if !supportedCodexVersion(version) {
		v := version
		if v == "" {
			v = "unknown"
		}
		fmt.Fprintf(ctx.Stderr, "Moat: unsupported Codex CLI version %s for subscription auth (supported: 0.146.x through 0.154.x); pin codex-cli@0.154.0 in moat.yaml dependencies or use 'moat grant openai'\n", v)
		return exitError{code: 1}
	}
	return nil
}

// secondField mirrors `awk '{print $2}'` on the version line.
func secondField(s string) string {
	fields := strings.Fields(s)
	if len(fields) < 2 {
		return ""
	}
	return fields[1]
}

// supportedCodexVersion mirrors the shell case arms 0.146.* .. 0.154.*.
func supportedCodexVersion(v string) bool {
	for minor := 146; minor <= 154; minor++ {
		if strings.HasPrefix(v, "0."+strconv.Itoa(minor)+".") {
			return true
		}
	}
	return false
}

// geminiStagingPhase mirrors the Gemini CLI setup block. Note settings.json
// is NOT a secret here: its source mode is preserved, only oauth_creds.json
// gets the forced 0600 (AGENT-GEMINI-CP-SETTINGS vs -CP-OAUTHCREDS).
func geminiStagingPhase(ctx *Context) error {
	return stageAgent(ctx, "gemini", ctx.Cfg.GeminiInit, ".gemini", []stagedEntry{
		{name: "settings.json"},
		{name: "oauth_creds.json", secret: true},
		{name: "GEMINI.md"},
	})
}

// copilotStagingPhase mirrors the GitHub Copilot CLI setup block (runtime
// context stays mounted in the staging dir and is referenced via
// COPILOT_CUSTOM_INSTRUCTIONS_DIRS, so only config/state files are copied).
func copilotStagingPhase(ctx *Context) error {
	return stageAgent(ctx, "copilot", ctx.Cfg.CopilotInit, ".copilot", []stagedEntry{
		{name: "config.json"},
		{name: "settings.json"},
		{name: "permissions-config.json"},
	})
}

// stageAgent is the shared body of the four agent staging blocks:
//
//   - gated on the staging env var being non-empty AND naming a directory
//   - TARGET_HOME recomputed per block via the shared root idiom
//   - mkdir -p of the agent dir (unguarded under set -e: fatal on failure)
//   - each allowlisted file that exists is copied with cp -p (fatal on
//     failure); secret files additionally chmod 600 (fatal) because cp -p
//     preserves the SOURCE mode and credentials must never stay group/world
//     readable
//   - on the root+moatuser path, a best-effort recursive chown of the agent
//     dir, then best-effort chowns of any home-root files
func stageAgent(ctx *Context, agent, staging, agentDir string, entries []stagedEntry) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if staging == "" || !isDir(sys, staging) {
		return nil
	}

	home := targetHome(sys.Geteuid(), moatuserExists(sys), cfg.Home)
	// Shell-style concatenation, not filepath.Join: the script builds
	// "$TARGET_HOME/.claude", so an empty HOME yields the root-anchored
	// "/.claude" (which then fails loudly), never a cwd-relative path.
	destDir := home + "/" + agentDir
	if err := sys.MkdirAll(destDir, 0o755); err != nil {
		return fatalPhaseError(ctx, "creating "+destDir, err)
	}

	for _, e := range entries {
		src := filepath.Join(staging, e.name)
		switch {
		case e.tree:
			if !isDir(sys, src) {
				continue
			}
			if err := sys.CopyTreePreserving(src, destDir+"/"+e.name); err != nil {
				return fatalPhaseError(ctx, "staging "+agent+" "+e.name, err)
			}
		default:
			if !isFile(sys, src) {
				continue
			}
			dst := destDir + "/" + e.name
			if e.home {
				dst = home + "/" + e.name
			}
			if err := sys.CopyFilePreserving(src, dst); err != nil {
				return fatalPhaseError(ctx, "staging "+agent+" "+e.name, err)
			}
			if e.secret {
				if err := sys.Chmod(dst, 0o600); err != nil {
					return fatalPhaseError(ctx, "restricting "+dst, err)
				}
			}
		}
	}

	// Ownership hand-off (best-effort, silent — the copies themselves are
	// the contract; a chown failure must not abort the start).
	if chownToMoatuser(sys.Geteuid(), moatuserExists(sys)) {
		if u, ok := sys.LookupUser("moatuser"); ok {
			recursiveChownBestEffortPruned(sys, destDir, u.UID, u.GID, chownPrune[agent])
			for _, e := range entries {
				if !e.home {
					continue
				}
				dst := home + "/" + e.name
				if isFile(sys, dst) {
					_ = sys.Chown(dst, u.UID, u.GID)
				}
			}
		}
	}
	return nil
}

// fatalPhaseError reports an unguarded operation failure — the Go
// equivalent of set -e aborting the script mid-block — and returns the
// exit-1 sentinel.
func fatalPhaseError(ctx *Context, op string, err error) error {
	fmt.Fprintf(ctx.Stderr, "moat-init: %s: %v\n", op, err)
	return exitError{code: 1}
}
