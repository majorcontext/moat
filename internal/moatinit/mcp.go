package moatinit

// workspaceMCPJSONPhase mirrors setup_workspace_mcp_json: copy the
// local-process MCP config for Gemini into /workspace/.mcp.json.
//
// Ordering is load-bearing (INIT-11): it runs AFTER populate_workspace_volume
// — in volume mode populate tar-extracts the staging tree over /workspace,
// so writing .mcp.json earlier would let the user's own .mcp.json clobber
// moat's. Running it after makes moat's config win in both modes.
//
// Only Gemini writes this path: Codex reads MCP servers from
// ~/.codex/config.toml and ignores .mcp.json. An agent added later that also
// writes .mcp.json would collide, so prefer that agent's own config file.
func workspaceMCPJSONPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	staging := cfg.GeminiInit
	if staging == "" || !isFile(sys, staging+"/mcp.json") {
		return nil
	}
	// cp -p, unguarded in the function body: fatal under set -e.
	if err := sys.CopyFilePreserving(staging+"/mcp.json", "/workspace/.mcp.json"); err != nil {
		return fatalPhaseError(ctx, "copying workspace .mcp.json", err)
	}
	if chownToMoatuser(sys.Geteuid(), moatuserExists(sys)) {
		if u, ok := sys.LookupUser("moatuser"); ok {
			_ = sys.Chown("/workspace/.mcp.json", u.UID, u.GID) // best-effort
		}
	}
	return nil
}
