package cli

import "github.com/majorcontext/moat/internal/config"

// ApplyAgentDefaults runs the agents: → grants-merge → AppendDerivedGrants
// sequence shared by `moat run` and `moat wt`, plus the adjacent
// command-from-config defaulting. Both call sites relied on this exact
// sequence duplicated near-verbatim; keeping it in one place means a future
// fix to the merge/precedence logic can't be applied to only one of the two
// entry points.
//
// The ordering it preserves:
//
//  1. ExpandAgents mutates cfg.Dependencies and cfg.Network.Rules directly,
//     but returns derived credential grants separately rather than writing
//     them into cfg.Grants — see its doc comment for why (cfg.Grants is
//     treated as the "explicit" bucket during grant-precedence resolution,
//     and a derived grant must never win that precedence as if the user had
//     declared it).
//  2. *flagsGrants is defaulted from cfg.Grants + the derived grants ONLY
//     when the caller passed no --grant flags (*flagsGrants is empty). This
//     is override semantics, not merge semantics — do not change it.
//  3. AppendDerivedGrants writes the derived grants into cfg.Grants AFTER
//     step 2 has already read cfg.Grants — never before. cfg.Grants has its
//     own direct downstream readers (ShouldSyncCodexLogs,
//     ShouldSyncGeminiLogs, buildLocalMCPConfig's grant validation) that
//     only ever see cfg.Grants, not *flagsGrants, so running this earlier
//     would let a derived grant re-enter step 2 as if the user had declared
//     it — resurrecting the bug ExpandAgents' doc comment describes.
//
// command is defaulted from cfg.Command when the caller passed none —
// identical at both call sites and adjacent to the grants defaulting.
func ApplyAgentDefaults(cfg *config.Config, flagsGrants *[]string, command *[]string) error {
	derivedGrants, err := ExpandAgents(cfg)
	if err != nil {
		return err
	}

	if cfg == nil {
		return nil
	}

	if len(*flagsGrants) == 0 {
		grants := append([]string{}, cfg.Grants...)
		grants = append(grants, derivedGrants...)
		if len(grants) > 0 {
			*flagsGrants = grants
		}
	}
	AppendDerivedGrants(cfg, derivedGrants)

	if len(*command) == 0 && len(cfg.Command) > 0 {
		*command = cfg.Command
	}

	return nil
}
