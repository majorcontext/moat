package moatinit

import (
	"fmt"
	"path/filepath"
)

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
	})
}

// codexStagingPhase mirrors the Codex CLI setup block.
func codexStagingPhase(ctx *Context) error {
	return stageAgent(ctx, "codex", ctx.Cfg.CodexInit, ".codex", []stagedEntry{
		{name: "config.toml"},
		{name: "auth.json", secret: true},
		{name: "AGENTS.md"},
	})
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
	destDir := filepath.Join(home, agentDir)
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
			if err := sys.CopyTreePreserving(src, filepath.Join(destDir, e.name)); err != nil {
				return fatalPhaseError(ctx, "staging "+agent+" "+e.name, err)
			}
		default:
			if !isFile(sys, src) {
				continue
			}
			dst := filepath.Join(destDir, e.name)
			if e.home {
				dst = filepath.Join(home, e.name)
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
			recursiveChownBestEffort(sys, destDir, u.UID, u.GID)
			for _, e := range entries {
				if !e.home {
					continue
				}
				dst := filepath.Join(home, e.name)
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
