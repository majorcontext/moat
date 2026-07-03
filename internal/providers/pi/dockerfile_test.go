package pi

import (
	"strings"
	"testing"
)

func TestGenerateDockerfileSnippet_bakesSettingsWithNoPackages(t *testing.T) {
	r := GenerateDockerfileSnippet(nil, "moatuser")
	if r.ScriptName == "" || len(r.ScriptContent) == 0 {
		t.Fatal("expected a generated script even with no packages")
	}
	script := string(r.ScriptContent)
	if strings.Contains(script, "pi install ") {
		t.Errorf("no packages: script should not run pi install, got:\n%s", script)
	}
	for _, want := range []string{`defaultProjectTrust:"never"`, "enableInstallTelemetry:false", "enableAnalytics:false", "quietStartup:true", ".pi/agent"} {
		if !strings.Contains(script, want) {
			t.Errorf("script missing %q", want)
		}
	}
	// Dockerfile snippet runs as the container user and copies+runs the script.
	for _, want := range []string{"USER moatuser", "WORKDIR /home/moatuser", "COPY --chown=moatuser " + r.ScriptName, "RUN bash /tmp/" + r.ScriptName} {
		if !strings.Contains(r.DockerfileSnippet, want) {
			t.Errorf("dockerfile snippet missing %q, got:\n%s", want, r.DockerfileSnippet)
		}
	}
}

func TestGenerateDockerfileSnippet_installsPackagesSortedAndQuoted(t *testing.T) {
	r := GenerateDockerfileSnippet([]string{"npm:b@2", "npm:a@1"}, "moatuser")
	script := string(r.ScriptContent)
	ai := strings.Index(script, "pi install 'npm:a@1'")
	bi := strings.Index(script, "pi install 'npm:b@2'")
	if ai < 0 || bi < 0 {
		t.Fatalf("expected both packages single-quoted, got:\n%s", script)
	}
	if ai > bi {
		t.Errorf("packages should install in sorted order (a before b), got:\n%s", script)
	}
	// settings merge still present after installs
	if !strings.Contains(script, `defaultProjectTrust:"never"`) {
		t.Errorf("settings merge missing after installs")
	}
}
