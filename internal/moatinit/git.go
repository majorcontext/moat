package moatinit

// gitConfigCommands assembles the `git config --system` invocations for the
// git-configuration phase, in script order (GIT-02..GIT-06):
//
//  1. safe.directory /workspace — always (whitelists exactly /workspace,
//     nothing else; do not broaden or narrow)
//  2. user.name — only when MOAT_GIT_USER_NAME is non-empty
//  3. user.email — only when MOAT_GIT_USER_EMAIL is non-empty (independent
//     of user.name; either may be set without the other)
//  4. http.proxyAuthMethod basic — always (git does not retry after the
//     proxy's 407 CONNECT challenge; see issue #370)
//  5. url."git@github.com:".insteadOf — only when MOAT_GIT_SSH_GITHUB is
//     exactly "1" (opt-out is "0"; any other value also skips)
//
// Every command is best-effort at execution time (stderr discarded, failure
// ignored — GIT-07); the caller only runs them when a git binary is on PATH
// (GIT-01).
// gitConfigPhase mirrors the git-configuration block: gated on a git binary
// being on PATH (GIT-01 — no git, no config, no error), then each command
// runs best-effort with stderr discarded (GIT-07: a read-only /etc/gitconfig
// or non-root run must not abort the start; git config set operations write
// nothing to stdout).
func gitConfigPhase(ctx *Context) error {
	sys := ctx.Sys
	if _, err := sys.LookPath("git"); err != nil {
		return nil
	}
	for _, argv := range gitConfigCommands(ctx.Cfg) {
		_, _ = sys.Run(Cmd{Argv: argv}) // best-effort: exit code and error both ignored
	}
	return nil
}

func gitConfigCommands(cfg *Config) [][]string {
	cmds := [][]string{
		{"git", "config", "--system", "--add", "safe.directory", "/workspace"},
	}
	if cfg.GitUserName != "" {
		cmds = append(cmds, []string{"git", "config", "--system", "user.name", cfg.GitUserName})
	}
	if cfg.GitUserEmail != "" {
		cmds = append(cmds, []string{"git", "config", "--system", "user.email", cfg.GitUserEmail})
	}
	cmds = append(cmds, []string{"git", "config", "--system", "http.proxyAuthMethod", "basic"})
	if cfg.GitSSHGitHub == "1" {
		cmds = append(cmds, []string{"git", "config", "--system", "url.git@github.com:.insteadOf", "https://github.com/"})
	}
	return cmds
}
