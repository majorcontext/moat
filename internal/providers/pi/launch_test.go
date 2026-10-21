package pi

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

func TestPiDefaultModelLaunch(t *testing.T) {
	origProvider, origModel := piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piResolvedProvider, piResolvedModel = origProvider, origModel })
	for _, backend := range supportedBackends {
		for _, mode := range []string{"interactive", "initial prompt", "print"} {
			t.Run(backend+"/"+mode, func(t *testing.T) {
				// The search is fuzzy; only an exact provider-column match counts.
				// Model IDs with slashes and quotes must remain single arguments.
				model := "vendor/model-'quoted'"
				listing := "provider model context\n" + backend + "-other wrong-model 128K\n" +
					"other " + backend + "/wrong-model 128K\n" +
					backend + " " + model + " 128K\n" + backend + " second-model 128K\n"
				fakePiListing(t, listing, 0)
				piResolvedProvider, piResolvedModel = backend, ""
				prompt := "explain 'quotes', \"double quotes\", $HOME and $(exit 99)\nnext line"
				var printPrompt, initialPrompt string
				want := []string{
					"--provider", backend, "--model", model,
					"--append-system-prompt", PiInitMountPath + "/" + ContextFileName,
				}
				switch mode {
				case "initial prompt":
					initialPrompt = prompt
					want = append(want, prompt)
				case "print":
					printPrompt = prompt
					want = append(want, "-p", prompt)
				}
				args := buildPiCommand(printPrompt, initialPrompt)
				out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
				if err != nil {
					t.Fatalf("launch: %v\n%s", err, out)
				}
				got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
				if !slices.Equal(got, want) {
					t.Fatalf("Pi arguments = %q, want %q", got, want)
				}
			})
		}
	}
}

func TestPiDefaultModelLaunchFailsWithoutSelectedModels(t *testing.T) {
	origProvider, origModel := piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piResolvedProvider, piResolvedModel = origProvider, origModel })
	for _, tc := range []struct {
		name     string
		listing  string
		listExit int
		wantExit int
	}{
		{"empty listing", "", 0, 1},
		{"no matching provider", "provider model\nopenai some-model\nlunaroute-other wrong-model\n", 0, 1},
		{"listing fails with partial output", "lunaroute partial-model\n", 17, 17},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakePiListing(t, tc.listing, tc.listExit)
			piResolvedProvider, piResolvedModel = backendLunaRoute, ""
			args := buildPiCommand("", "")
			cmd := exec.Command(args[0], args[1:]...)
			out, err := cmd.CombinedOutput()
			if err == nil || cmd.ProcessState.ExitCode() != tc.wantExit {
				t.Fatalf("launch error = %v, want exit %d; output: %s", err, tc.wantExit, out)
			}
			if strings.ContainsRune(string(out), '\x00') {
				t.Fatalf("launched Pi despite missing models: %q", out)
			}
			if tc.listExit == 0 && !strings.Contains(string(out), "no available Pi models for provider 'lunaroute'") {
				t.Fatalf("missing diagnostic: %s", out)
			}
		})
	}
}

func TestPiExplicitModelSkipsDiscovery(t *testing.T) {
	origProvider, origModel := piResolvedProvider, piResolvedModel
	t.Cleanup(func() { piResolvedProvider, piResolvedModel = origProvider, origModel })
	for _, backend := range supportedBackends {
		t.Run(backend, func(t *testing.T) {
			fakePiListing(t, "", 17)
			piResolvedProvider, piResolvedModel = backend, "vendor/pinned-model:high"
			args := buildPiCommand("hello", "")
			out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
			if err != nil {
				t.Fatalf("explicit model must skip discovery: %v\n%s", err, out)
			}
			got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
			want := []string{
				"--provider", backend, "--model", piResolvedModel,
				"--append-system-prompt", PiInitMountPath + "/" + ContextFileName, "-p", "hello",
			}
			if !slices.Equal(got, want) {
				t.Fatalf("Pi arguments = %q, want %q", got, want)
			}
		})
	}
}

func fakePiListing(t *testing.T, listing string, exitCode int) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "models"), []byte(listing), 0o600); err != nil {
		t.Fatal(err)
	}
	// Listing errors must not fall through to the final invocation, whose
	// NUL-separated arguments are the observable result in these tests.
	script := `#!/bin/sh
if [ "$1" = "--list-models" ]; then
  cat "$MOAT_PI_TEST_MODELS"
  exit "$MOAT_PI_TEST_LIST_EXIT"
fi
printf '%s\000' "$@"
`
	if err := os.WriteFile(filepath.Join(dir, "pi"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	// Simulate a ready LunaRoute catalog so the composed launch script
	// exercises model selection after the existing warm-up check.
	if err := os.WriteFile(filepath.Join(dir, "node"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("MOAT_PI_TEST_MODELS", filepath.Join(dir, "models"))
	t.Setenv("MOAT_PI_TEST_LIST_EXIT", strconv.Itoa(exitCode))
}
