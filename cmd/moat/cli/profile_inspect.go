package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
	"github.com/spf13/cobra"
)

type profileInfo struct {
	Name        string   `json:"name"`
	Default     bool     `json:"default"`
	Active      bool     `json:"active"`
	Parent      string   `json:"parent,omitempty"`
	Chain       []string `json:"chain"`
	Environment []string `json:"environment"`
	Error       string   `json:"error,omitempty"`
}

func inspectProfile(cfg *config.GlobalConfig, name string) (profileInfo, error) {
	if name != "" {
		exists, err := cfg.ProfileExists(name)
		if err != nil {
			return profileInfo{}, err
		}
		if !exists {
			return profileInfo{}, fmt.Errorf("profile %q does not exist", name)
		}
	}
	chain, err := cfg.ProfileChain(name)
	if err != nil {
		return profileInfo{}, err
	}
	info := profileInfo{Name: profileSourceLabel(name), Default: name == "", Active: name == credential.ActiveProfile, Chain: make([]string, 0, len(chain)), Environment: []string{}}
	for _, item := range chain {
		info.Chain = append(info.Chain, profileSourceLabel(item))
	}
	if len(chain) > 1 {
		info.Parent = profileSourceLabel(chain[1])
	}
	for key := range cfg.ProfileEnv(name) {
		info.Environment = append(info.Environment, key)
	}
	sort.Strings(info.Environment)
	return info, nil
}

func newProfileListCommand() *cobra.Command {
	return &cobra.Command{
		Use: "list", Aliases: []string{"ls"}, Short: "List credential profiles and their inheritance", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.LoadProfileConfig()
			if err != nil {
				return err
			}
			names, err := credential.ListProfilesFromConfig(cfg)
			if err != nil {
				return err
			}
			rows := make([]profileInfo, 0, len(names)+1)
			for _, name := range append([]string{""}, names...) {
				info, err := inspectProfile(cfg, name)
				if err != nil {
					info = profileInfo{Name: profileSourceLabel(name), Default: name == "", Active: name == credential.ActiveProfile, Error: err.Error(), Chain: []string{}, Environment: []string{}}
				}
				rows = append(rows, info)
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(rows)
			}
			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tACTIVE\tINHERITS\tSTATUS")
			for _, row := range rows {
				active, parent := "", row.Parent
				if row.Active {
					active = "*"
				}
				if parent == "" {
					parent = "-"
				}
				status := "ok"
				if row.Error != "" {
					status = row.Error
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", row.Name, active, parent, status)
			}
			return w.Flush()
		},
	}
}

func newProfileShowCommand() *cobra.Command {
	return &cobra.Command{
		Use: "show [name]", Aliases: []string{"inspect"}, Short: "Inspect a profile's inheritance and environment variable names",
		Long: "Inspect a named profile, or the active profile when no name is supplied.\nEnvironment variable values and credential secrets are never printed.\nUse 'moat grant list --profile NAME' to inspect effective grants and their sources.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := credential.ActiveProfile
			if len(args) > 0 {
				name = args[0]
			}
			cfg, err := config.LoadProfileConfig()
			if err != nil {
				return err
			}
			info, err := inspectProfile(cfg, name)
			if err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(info)
			}
			parent := info.Parent
			if parent == "" {
				parent = "none (isolated)"
				if name == "" {
					parent = "n/a (unscoped store)"
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Name: %s\nActive: %t\nInherits: %s\nLookup order: %s\n", info.Name, info.Active, parent, strings.Join(info.Chain, " -> "))
			keys := "(none)"
			if len(info.Environment) > 0 {
				keys = strings.Join(info.Environment, ", ")
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Environment variable names: %s\n", keys)
			fmt.Fprint(cmd.OutOrStdout(), "Inspect grants: moat grant list")
			if name != "" {
				fmt.Fprintf(cmd.OutOrStdout(), " --profile %s", name)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			return nil
		},
	}
}

func newProfileClearCommand(action string) *cobra.Command {
	var force bool
	short, description := "Remove a named profile and its local credentials", "Remove profile settings, local credentials, SSH mappings and inheritance exclusions.\nProfiles inherited by another profile cannot be removed."
	if action == "reset" {
		short = "Clear local grants while preserving profile settings"
		description = "Clear local credentials, SSH mappings and inheritance exclusions.\nPreserves environment and inheritance settings; inherited grants remain available."
	}
	cmd := &cobra.Command{
		Use: action + " <name>", Short: short, Long: description + "\nOnly local storage is changed; existing runs and upstream tokens are not revoked.", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if err := config.ValidateProfileName(name); err != nil {
				return err
			}
			if dryRun {
				if err := config.CheckProfileClear(name, action == "remove"); err != nil {
					return err
				}
				if jsonOut {
					return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"name": name, "action": action, "dry_run": true})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Would %s profile %s\n", action, name)
				return nil
			}
			if !force {
				consequence := "delete local grants, SSH mappings and exclusions, keeping settings"
				if action == "remove" {
					consequence = "delete settings, local grants, SSH mappings and exclusions"
				}
				fmt.Fprintf(cmd.ErrOrStderr(), "%s profile %s: %s? [y/N] ", action, name, consequence)
				answer, err := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
				if err != nil && err != io.EOF {
					return err
				}
				if !strings.EqualFold(strings.TrimSpace(answer), "y") && !strings.EqualFold(strings.TrimSpace(answer), "yes") {
					return fmt.Errorf("canceled; use --force to skip confirmation")
				}
			}
			operation := config.RemoveProfile
			if action == "reset" {
				operation = config.ResetProfile
			}
			if err := operation(name); err != nil {
				return err
			}
			if jsonOut {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]string{"name": name, "action": action})
			}
			if action == "reset" {
				fmt.Fprintf(cmd.OutOrStdout(), "Reset profile %s; settings preserved and inherited grants remain available\n", name)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Removed profile %s\n", name)
			}
			return nil
		},
	}
	if action == "remove" {
		cmd.Aliases = []string{"rm", "delete"}
	}
	cmd.Flags().BoolVarP(&force, "force", "f", false, "skip confirmation prompt")
	return cmd
}
