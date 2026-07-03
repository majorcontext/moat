package pi

import (
	"fmt"
	"sort"
	"strings"
)

// SnippetResult holds a Dockerfile snippet and the generated script it runs.
type SnippetResult struct {
	// DockerfileSnippet is Dockerfile text to append (USER/WORKDIR/COPY/RUN).
	DockerfileSnippet string
	// ScriptName is the build-context filename for ScriptContent.
	ScriptName string
	// ScriptContent is the generated shell script.
	ScriptContent []byte
}

// piConfigScriptName is the build-context filename for the generated bake script.
const piConfigScriptName = "pi-config.sh"

// piSettingsMergeJS assigns Moat's safe global Pi settings. httpProxy is omitted
// (redundant with the moat-owned HTTP_PROXY env). Bump the "pi-settings" hash
// marker in deps.ImageTag whenever this changes so cached images rebuild.
const piSettingsMergeJS = `Object.assign(s,{defaultProjectTrust:"never",enableInstallTelemetry:false,enableAnalytics:false,quietStartup:true})`

// GenerateDockerfileSnippet builds the Dockerfile snippet + script that installs
// the declared Pi packages and bakes Moat's safe global settings into
// ~/.pi/agent/settings.json, as containerUser at image build time.
//
// Commands are written to a separate script (a build-context file) rather than
// inline RUN steps — mirroring claude.GenerateDockerfileSnippet — to stay under
// the Apple containers builder's ~16KB Dockerfile gRPC limit.
//
// containerUser is inserted directly into the Dockerfile; callers must pass a
// safe, validated value (the hardcoded containerUser constant). Package sources
// are validated by config.validatePiPackages and are additionally single-quoted.
func GenerateDockerfileSnippet(packages []string, containerUser string) SnippetResult {
	sorted := make([]string, len(packages))
	copy(sorted, packages)
	sort.Strings(sorted)

	var s strings.Builder
	s.WriteString("#!/bin/bash\n")
	s.WriteString("# Auto-generated Pi config bake: declared packages + Moat safe global settings.\n")
	s.WriteString("set -e\n")
	fmt.Fprintf(&s, "export HOME=/home/%s\n", containerUser)
	s.WriteString("export GIT_TERMINAL_PROMPT=0\n")
	s.WriteString("export GIT_SSH_COMMAND='ssh -o BatchMode=yes -o ConnectTimeout=10'\n")
	s.WriteString("mkdir -p \"$HOME/.pi/agent\"\n")
	for _, p := range sorted {
		fmt.Fprintf(&s, "echo 'Installing Pi package %s'\n", p)
		fmt.Fprintf(&s, "pi install %s\n", shellSingleQuote(p))
	}
	// Merge Moat's safe global settings, preserving any packages array pi wrote.
	s.WriteString("node -e '")
	s.WriteString(`const fs=require("fs"),p=process.env.HOME+"/.pi/agent/settings.json";let s={};try{s=JSON.parse(fs.readFileSync(p,"utf8"))}catch(e){}`)
	s.WriteString(piSettingsMergeJS)
	s.WriteString(`;fs.writeFileSync(p,JSON.stringify(s,null,2))`)
	s.WriteString("'\n")

	var d strings.Builder
	d.WriteString("# Pi config (packages + Moat safe global settings)\n")
	fmt.Fprintf(&d, "USER %s\n", containerUser)
	fmt.Fprintf(&d, "WORKDIR /home/%s\n", containerUser)
	// --chown so the COPY'd script is owned by containerUser; the RUN below runs
	// as that user (USER above) and must be able to delete it afterward (a root-
	// owned file in sticky /tmp can't be removed by the non-root user).
	fmt.Fprintf(&d, "COPY --chown=%s %s /tmp/%s\n", containerUser, piConfigScriptName, piConfigScriptName)
	fmt.Fprintf(&d, "RUN bash /tmp/%s && rm -f /tmp/%s\n", piConfigScriptName, piConfigScriptName)

	return SnippetResult{
		DockerfileSnippet: d.String(),
		ScriptName:        piConfigScriptName,
		ScriptContent:     []byte(s.String()),
	}
}

// shellSingleQuote wraps s in single quotes, escaping embedded single quotes, so
// a validated package source cannot break out of the install command.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
