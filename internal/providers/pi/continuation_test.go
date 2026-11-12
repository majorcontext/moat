package pi

import (
	"os/exec"
	"slices"
	"strings"
	"testing"

	"github.com/majorcontext/moat/internal/config"
	"github.com/spf13/cobra"
)

func TestPiContinuationPersistence(t *testing.T) {
	origContinue, origSync := piContinue, piSyncLogsFlag
	t.Cleanup(func() { piContinue, piSyncLogsFlag = origContinue, origSync })
	for _, tc := range []struct {
		flag     string
		syncFlag string
		yaml     *bool
		wantErr  bool
	}{
		{"--continue", "", nil, false},
		{"-c", "", nil, false},
		{"--continue", "", piBool(true), false},
		{"--continue", "", piBool(false), true},
		{"--continue", "--sync-logs=false", nil, true},
		{"--continue", "--sync-logs", piBool(false), false},
		{"--continue=false", "", piBool(false), false},
	} {
		root := &cobra.Command{Use: "moat"}
		(&Provider{}).RegisterCLI(root)
		cmd, _, _ := root.Find([]string{"pi"})
		args := []string{tc.flag}
		if tc.syncFlag != "" {
			args = append(args, tc.syncFlag)
		}
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		cfg := &config.Config{Pi: config.PiConfig{SyncLogs: tc.yaml}}
		err := validatePiContinuation(cmd, cfg)
		if (err != nil) != tc.wantErr {
			t.Fatalf("%v yaml=%v: %v", args, tc.yaml, err)
		}
		if err == nil {
			configurePiLogSync(cmd, cfg)
			if cfg.ShouldSyncPiLogs() != piContinue {
				t.Fatalf("%v: persistence not configured", args)
			}
		}
	}
}

func piBool(v bool) *bool { return &v }

func TestPiContinuationLaunch(t *testing.T) {
	origContinue, origProvider, origModel := piContinue, piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piContinue, piResolvedProvider, piResolvedModel = origContinue, origProvider, origModel })
	for _, backend := range supportedBackends {
		for _, model := range []string{"", "chosen-model"} {
			for _, mode := range []string{"interactive", "initial", "print"} {
				t.Run(backend+"/"+model+"/"+mode, func(t *testing.T) {
					fakePiListing(t, backend+" chosen-model 128K\n", 0)
					piContinue, piResolvedProvider, piResolvedModel = true, backend, model
					prompt, initial := "", ""
					message := "quotes ' and $(exit 99)"
					if mode == "print" {
						prompt = message
					}
					if mode == "initial" {
						initial = message
					}
					args := buildPiCommand(prompt, initial)
					out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
					if err != nil {
						t.Fatalf("%v: %s", err, out)
					}
					got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
					want := []string{"--provider", backend, "--model", "chosen-model", "--append-system-prompt", PiInitMountPath + "/" + ContextFileName, "--continue"}
					if mode == "print" {
						want = append(want, "-p")
					}
					if mode != "interactive" {
						want = append(want, message)
					}
					if !slices.Equal(got, want) {
						t.Fatalf("got %q, want %q", got, want)
					}
				})
			}
		}
	}
}
