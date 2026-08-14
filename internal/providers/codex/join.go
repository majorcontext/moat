package codex

import (
	"errors"

	"github.com/majorcontext/moat/internal/provider"
)

// JoinCommand builds the in-container command for a joined codex session,
// mirroring the BuildCommand closure in runCodex (cli.go).
//
// Approval policy and sandbox mode are deliberately absent: they live in the
// generated ~/.codex/config.toml so every launch path gets the same behavior,
// and `codex` and `codex exec` accept different flags.
func (p *Provider) JoinCommand(opts provider.JoinOpts) ([]string, error) {
	// moat has never wired resume/continue for codex. Erroring is honest;
	// silently dropping the flag would look like it worked.
	if opts.Continue {
		return nil, errors.New("codex join does not support --continue")
	}
	if opts.Resume != "" {
		return nil, errors.New("codex join does not support --resume")
	}
	if opts.Prompt != "" {
		return []string{"codex", "exec", opts.Prompt}, nil
	}
	return []string{"codex"}, nil
}

// IdentifiesAs reports whether a run with the given recorded Agent field was
// created by the codex provider. This serves only the pre-upgrade fallback path
// in `moat join` — runs created before joinable_agents existed. Runs created
// since are matched against their persisted capability set instead.
func (p *Provider) IdentifiesAs(agent string) bool {
	return agent == "codex"
}
