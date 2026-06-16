package claude

import "github.com/majorcontext/moat/internal/provider"

// JoinCommand builds the in-container command for a joined claude session.
// Mirrors the BuildCommand closure in runClaudeCode (cli.go). Joined sessions
// always run with --dangerously-skip-permissions, matching the create-path
// default (the container provides isolation).
func (p *OAuthProvider) JoinCommand(opts provider.JoinOpts) ([]string, error) {
	cmd := []string{"claude", "--dangerously-skip-permissions"}
	if opts.Continue {
		cmd = append(cmd, "--continue")
	}
	if opts.Resume != "" {
		cmd = append(cmd, "--resume", opts.Resume)
	}
	if opts.Prompt != "" {
		cmd = append(cmd, "-p", opts.Prompt)
	}
	return cmd, nil
}

// IdentifiesAs reports whether a run with the given recorded Agent field was
// created by the claude provider. cfg.Agent defaults to the provider name
// ("claude") but is "claude-code" when set explicitly in moat.yaml.
func (p *OAuthProvider) IdentifiesAs(agent string) bool {
	return agent == "claude" || agent == "claude-code"
}
