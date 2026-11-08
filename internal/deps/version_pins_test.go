package deps

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAllRegistryDependenciesAcceptExplicitNewVersions(t *testing.T) {
	for name, spec := range AllSpecs() {
		// Bundles and Docker access modes do not identify upstream release artifacts.
		if spec.Type == TypeMeta || spec.Type == TypeDocker {
			continue
		}
		t.Run(name, func(t *testing.T) {
			inputs := []string{name + "@987.654.321"}
			seen := map[string]bool{name: true}
			var addRequirements func(DepSpec)
			addRequirements = func(spec DepSpec) {
				for _, requirement := range spec.Requires {
					if seen[requirement] {
						continue
					}
					seen[requirement] = true
					inputs = append(inputs, requirement)
					required, ok := GetSpec(requirement)
					if !ok {
						t.Fatalf("unknown requirement %s", requirement)
					}
					addRequirements(required)
				}
			}
			addRequirements(spec)
			parsed, err := ParseAll(inputs)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(parsed); err != nil {
				t.Fatalf("version blocked by registry: %v", err)
			}
			if parsed[0].Version != "987.654.321" {
				t.Fatalf("explicit version changed: %+v", parsed[0])
			}
			// Services are rendered by the sidecar path, covered in run tests.
			if spec.Type != TypeService {
				image, err := GenerateDockerfile(parsed, nil)
				if err != nil {
					t.Fatal(err)
				}
				script, err := GenerateInstallScript(parsed)
				if err != nil {
					t.Fatal(err)
				}
				for _, output := range []string{image.Dockerfile, script} {
					if !strings.Contains(output, "987.654.321") {
						t.Errorf("installer dropped explicit pin:\n%s", output)
					}
				}
			}
		})
	}
}

func TestCustomInstallersPreserveVersionPins(t *testing.T) {
	for _, tc := range []struct{ name, version, want string }{
		{"playwright", "1.58.0", "npm install -g playwright@1.58.0"},
		{"aws", "2.32.21", "awscli-exe-linux-${ARCH}-2.32.21.zip"},
		{"gcloud", "551.0.0", "google-cloud-cli-551.0.0-linux-${ARCH}.tar.gz"},
		{"rust", "1.93.0", "--default-toolchain 1.93.0"},
		{"kubectl", "1.35.1", "/release/v1.35.1/bin/linux/"},
		{"terraform", "1.14.1", "terraform_1.14.1_linux_"},
		{"helm", "4.0.1", "helm-v4.0.1-linux-"},
		{"protoc", "33.1", "protoc-33.1-linux-"},
		{"npm", "11.7.0", "npm@11.7.0"},
		{"yarn", "4.10.3", "corepack prepare yarn@4.10.3 --activate"},
		{"pnpm", "10.21.0", "corepack prepare pnpm@10.21.0 --activate"},
		{"claude-code", "2.0.61", "bash /tmp/moat-claude-install.sh 2.0.61"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmds := strings.Join(getCustomCommands(tc.name, tc.version).Commands, "\n")
			if !strings.Contains(cmds, tc.want) {
				t.Fatalf("pin missing %q:\n%s", tc.want, cmds)
			}
		})
	}
}

func TestPackagePinsReachBothWriters(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []string
		want   string
	}{
		{"apt security revision", []string{"jq@1.6-2.1+deb12u1"}, "jq=1.6-2.1+deb12u1"},
		{"apt epoch", []string{"git@1:2.47.0-1"}, "git=1:2.47.0-1"},
		{"npm", []string{"node", "typescript@5.9.3"}, "typescript@5.9.3"},
		{"uv tool", []string{"python", "uv", "ruff@0.14.1"}, "ruff==0.14.1"},
		{"GitHub binary", []string{"uv@0.11.6", "python"}, "/download/0.11.6/"},
		{"Go tool", []string{"go", "gopls@v0.20.0"}, "@v0.20.0"},
		{"Go tool unprefixed", []string{"go", "gopls@0.20.0"}, "@v0.20.0"},
		{"Go major query", []string{"go", "gopls@1"}, "@v1"},
		{"Go commit query", []string{"go", "gopls@1abc234def"}, "@1abc234def"},
		{"Go latest query", []string{"go", "gopls@latest"}, "@latest"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseAll(tc.inputs)
			if err != nil {
				t.Fatal(err)
			}
			if err := Validate(parsed); err != nil {
				t.Fatal(err)
			}
			image, err := GenerateDockerfile(parsed, nil)
			if err != nil {
				t.Fatal(err)
			}
			script, err := GenerateInstallScript(parsed)
			if err != nil {
				t.Fatal(err)
			}
			for _, output := range []string{image.Dockerfile, script} {
				if !strings.Contains(output, tc.want) {
					t.Errorf("pin %q missing:\n%s", tc.want, output)
				}
			}
		})
	}
}

func TestPythonVersionPreservedAcrossImageChoices(t *testing.T) {
	for _, tc := range []struct {
		name   string
		inputs []string
		opts   *ImageSpec
		want   string
	}{
		{"single", []string{"python@3.15.2"}, nil, "FROM python:3.15.2-slim"},
		{"mixed", []string{"node@26.13.0", "python@3.15.2"}, nil, "uv python install --default --force 3.15.2"},
		{"custom base", []string{"python@3.15.2"}, &ImageSpec{BaseImage: "debian:bookworm-slim"}, "uv python install --default --force 3.15.2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parsed, err := ParseAll(tc.inputs)
			if err != nil {
				t.Fatal(err)
			}
			image, err := GenerateDockerfile(parsed, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(image.Dockerfile, tc.want) {
				t.Fatalf("requested Python lost:\n%s", image.Dockerfile)
			}
			if strings.Contains(image.Dockerfile, "apt-get install -y python3") {
				t.Error("requested Python replaced with distro version")
			}
		})
	}
}

func TestPinsOverrideDefaultsAndBundlesInEitherOrder(t *testing.T) {
	for _, tc := range []struct {
		inputs        []string
		name, version string
	}{
		{[]string{"node", "node@26.13.0"}, "node", "26.13.0"},
		{[]string{"node@26.13.0", "node"}, "node", "26.13.0"},
		{[]string{"python", "python-dev", "ruff@0.14.1"}, "ruff", "0.14.1"},
		{[]string{"python", "ruff@0.14.1", "python-dev"}, "ruff", "0.14.1"},
		{[]string{"node", "npm:eslint", "npm:eslint@9.39.0"}, "eslint", "9.39.0"},
		{[]string{"node", "npm:eslint@9.39.0", "npm:eslint"}, "eslint", "9.39.0"},
	} {
		t.Run(strings.Join(tc.inputs, ","), func(t *testing.T) {
			parsed, err := ParseAll(tc.inputs)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, dep := range parsed {
				if dep.Name == tc.name {
					count++
					if dep.Version != tc.version {
						t.Errorf("pin = %q, want %q", dep.Version, tc.version)
					}
				}
			}
			if count != 1 {
				t.Errorf("%s appears %d times", tc.name, count)
			}
		})
	}
}

func TestExactPinsCannotReuseFloatingRuntimeImages(t *testing.T) {
	for _, tc := range []struct{ name, partial, full string }{
		{"node", "26", "26.13.0"}, {"python", "3.15", "3.15.2"}, {"go", "1.27", "1.27.3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			floating := ImageTag([]Dependency{{Name: tc.name, Version: tc.full, OriginalVersion: tc.partial}}, nil)
			exact := ImageTag([]Dependency{{Name: tc.name, Version: tc.full, OriginalVersion: tc.full}}, nil)
			unresolvedExact := ImageTag([]Dependency{{Name: tc.name, Version: tc.full}}, nil)
			if floating == exact {
				t.Error("exact pin can reuse floating image")
			}
			if exact != unresolvedExact {
				t.Error("identical exact pins get different cache tags")
			}
		})
	}
}

func TestNodeSetupDownloadFailureStopsInstaller(t *testing.T) {
	dir := t.TempDir()
	for name, script := range map[string]string{
		"curl":    "#!/bin/sh\nexit 22\n",
		"apt-get": "#!/bin/sh\ntouch \"$MOAT_TEST_APT_MARKER\"\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(dir, "apt-ran")
	cmd := exec.Command("bash", "-c", "set -e\n"+getRuntimeCommands("node", "26").FormatForScript())
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "MOAT_TEST_APT_MARKER="+marker)
	err := cmd.Run()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 22 {
		t.Fatalf("exit = %v, want curl status 22", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("apt ran after setup download failed: %v", err)
	}
}

func TestConflictingExplicitVersionPinsFail(t *testing.T) {
	for _, input := range [][]string{
		{"node@22", "node@26.13.0"},
		{"node@26.13.0", "node@22"},
		{"node", "npm:eslint@9.38.0", "npm:eslint@9.39.0"},
	} {
		if _, err := ParseAll(input); err == nil || !strings.Contains(err.Error(), "conflicting versions") {
			t.Errorf("ParseAll(%v) = %v, want explicit conflict", input, err)
		}
	}
	if _, err := ParseAll([]string{"node@26.13.0", "node@26.13.0"}); err != nil {
		t.Errorf("identical repeated pins rejected: %v", err)
	}
}

func TestPinnedCorepackLaunchersKeepVersionAndArguments(t *testing.T) {
	for _, name := range []string{"yarn", "pnpm"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			cache := filepath.Join(dir, "seed")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			fake := `#!/bin/sh
case "$1" in
 enable) exit 0 ;;
 prepare) mkdir -p "$COREPACK_HOME"; exit 0 ;;
esac
printf '%s\n' "$COREPACK_HOME" "$@"
`
			if err := os.WriteFile(filepath.Join(bin, "corepack"), []byte(fake), 0o755); err != nil {
				t.Fatal(err)
			}
			script := getCustomCommands(name, "9.8.7").FormatForScript()
			script = strings.ReplaceAll(script, "/usr/local/bin/", bin+"/")
			script = strings.ReplaceAll(script, "/opt/moat-corepack", cache)
			cmd := exec.Command("bash", "-c", "set -e\n"+script)
			cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
			if output, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("launcher setup: %v\n%s", err, output)
			}
			aliases := []string{name, "yarnpkg"}
			if name == "pnpm" {
				aliases[1] = "pnpx"
			}
			for _, alias := range aliases {
				cmd := exec.Command(filepath.Join(bin, alias), "argument with spaces")
				cmd.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
				output, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("%s: %v\n%s", alias, err, output)
				}
				want := cache + "\n" + name + "@9.8.7\n"
				if alias == "pnpx" {
					want += "dlx\n"
				}
				want += "argument with spaces\n"
				if string(output) != want {
					t.Errorf("%s launcher = %q, want %q", alias, output, want)
				}
			}
		})
	}
}

func TestKnownUnsupportedReleaseLinesAreRejected(t *testing.T) {
	for _, input := range []string{"node@18", "node@20", "node@20.19.5", "python@3.10.19", "go@1.25.14", "mysql@8.0.44"} {
		parsed, err := ParseAll([]string{input})
		if err != nil {
			t.Fatal(err)
		}
		if err := Validate(parsed); err == nil || !strings.Contains(err.Error(), "upstream") {
			t.Errorf("%s: expected upstream support rejection, got %v", input, err)
		}
	}
}

func TestExpiredImplicitDefaultSelectsMaintainedSuccessor(t *testing.T) {
	now := time.Date(2027, 5, 1, 0, 0, 0, 0, time.UTC)
	spec := applySupport("node", DepSpec{Default: "22"}, now)
	if spec.Default != "24" {
		t.Fatalf("expired Node default: %+v", spec)
	}
	if err := releaseSupport.Check("node", spec.Default, now); err != nil {
		t.Fatal(err)
	}
	if err := releaseSupport.Check("node", "22", now); err == nil {
		t.Fatal("explicit EOL pin was accepted")
	}
}
