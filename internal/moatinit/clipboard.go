package moatinit

// clipboardPhase mirrors the clipboard bridging block: when MOAT_CLIPBOARD
// is exactly "1", start a headless X server for clipboard operations (the
// host writes clipboard data and uses xclip to set the X selection) and
// export DISPLAY=:99.
//
// Xvfb is fire-and-forget: no readiness wait, output discarded, and DISPLAY
// is exported even if the spawn failed (GIT-CLIP-02) — it persists into the
// exec'd user command and later `moat` exec sessions. The child must stay
// alive for the container's lifetime, exactly like socat/dockerd.
func clipboardPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.Clipboard != "1" {
		return nil
	}
	_, _ = sys.StartDetached(Cmd{Argv: []string{"Xvfb", ":99", "-screen", "0", "1x1x8"}}) // >/dev/null 2>&1 &
	sys.Setenv("DISPLAY", ":99")
	return nil
}
