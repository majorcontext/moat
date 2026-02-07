package codex

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"
)

// DoctorSection implements doctor.Section for Codex configuration.
type DoctorSection struct{}

// Name returns the section name.
func (d *DoctorSection) Name() string {
	return "Codex Configuration"
}

// Print outputs Codex diagnostic information.
func (d *DoctorSection) Print(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	codexConfigDir := filepath.Join(home, ".codex")
	configPath := filepath.Join(codexConfigDir, "config.toml")

	// Check if .codex directory exists
	if _, statErr := os.Stat(codexConfigDir); os.IsNotExist(statErr) {
		fmt.Fprintln(w, "⚠️  No Codex configuration found")
		fmt.Fprintf(w, "   Expected at: %s\n", codexConfigDir)
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)

	fmt.Fprintf(tw, "Config directory:\t%s\n", codexConfigDir)

	// Check for config.toml
	if data, readErr := os.ReadFile(configPath); readErr == nil {
		fmt.Fprintf(tw, "Config file:\t%s (%d bytes)\n", configPath, len(data))
	} else if os.IsNotExist(readErr) {
		fmt.Fprintln(tw, "Config file:\tnot found")
	}

	// Check for auth.json (should not exist on host - only in containers)
	authPath := filepath.Join(codexConfigDir, "auth.json")
	if data, readErr := os.ReadFile(authPath); readErr == nil {
		var auth map[string]string
		if json.Unmarshal(data, &auth) == nil {
			fmt.Fprintln(tw, "Auth file:\tpresent (should only exist in containers)")
			if key, ok := auth["OPENAI_API_KEY"]; ok {
				if len(key) > 8 {
					fmt.Fprintf(tw, "  OPENAI_API_KEY:\t%s...\n", key[:8])
				}
			}
		}
	}

	// Check for sessions
	sessionMgr, err := NewSessionManager()
	if err == nil {
		sessions, err := sessionMgr.List()
		if err == nil {
			activeCount := 0
			for _, s := range sessions {
				if s.State == SessionStateRunning {
					activeCount++
				}
			}
			fmt.Fprintf(tw, "Sessions:\t%d total, %d active\n", len(sessions), activeCount)
		}
	}

	return tw.Flush()
}
