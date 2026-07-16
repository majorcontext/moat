package moatinit

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
)

// workspaceVolumeEnabled mirrors WS-01: populate_workspace_volume runs only
// when MOAT_WORKSPACE_VOLUME is exactly the string "1" — not "true", "01",
// " 1", or any other value.
func workspaceVolumeEnabled(v string) bool {
	return v == "1"
}

// stagingDir mirrors WS-03: `staging="${MOAT_WORKSPACE_STAGING:-/mnt/host-workspace}"`
// — the `:-` expansion defaults on unset AND on empty.
func stagingDir(staging string) string {
	if staging == "" {
		return "/mnt/host-workspace"
	}
	return staging
}

// excludeFileContent mirrors WS-04: the exclude file is created empty and,
// when MOAT_WORKSPACE_EXCLUDES is non-empty, receives the value verbatim
// (printf '%s' — no trailing newline appended). Patterns are
// newline-delimited "./"-prefixed paths produced by run.workspaceExcludes;
// an empty exclude file excludes nothing (WS-06).
func excludeFileContent(excludes string) string {
	return excludes
}

// volumeChownPaths mirrors the named-volume chown loop's word-splitting:
// `set -f` disables glob expansion (a target containing [ ] * ? is treated
// literally, never expanded against the filesystem), while word-splitting on
// default IFS stays on — the paths are space-separated.
func volumeChownPaths(v string) []string {
	return strings.Fields(v)
}

// populateWorkspaceVolumePhase mirrors populate_workspace_volume (WS
// region): copy the read-only staging tree into the named /workspace volume
// before the privilege drop.
//
// Go owns the business logic — the "1" gate, the root guard, staging
// resolution, exclude-file assembly, the tar argument vectors, BOTH pipe
// exit codes, and the recursive chown — while the byte copy itself stays a
// targeted `tar -cf - . | tar -xf -` subprocess pair. Symlinks are copied
// as symlinks (tar's default; the explicit no-dereference long option only
// exists in GNU tar 1.35+, and debian bookworm ships 1.34 which rejects it),
// and excludes go through a temp file so the user-controlled value never
// expands on a command line. Excludes are newline-delimited, NOT --null:
// GNU tar 1.34's `--null --exclude-from` applies only the first record.
func populateWorkspaceVolumePhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if !workspaceVolumeEnabled(cfg.WorkspaceVolume) {
		return nil // WS-01: exact-"1" gate, checked before everything else
	}
	// WS-02: defensive root guard — the call site is before the privilege
	// drop; this makes a refactor that moves it after the drop fail loudly
	// instead of hitting a silent chown EPERM.
	if sys.Geteuid() != 0 {
		fmt.Fprintln(ctx.Stderr, "moat: populate_workspace_volume must run as root")
		return exitError{code: 1}
	}

	staging := stagingDir(cfg.WorkspaceStaging)
	excludeFile := "/tmp/moat-excludes." + strconv.Itoa(sys.Getpid())
	if err := sys.WriteFile(excludeFile, []byte(excludeFileContent(cfg.WorkspaceExcludes)), 0o644); err != nil {
		return fatalPhaseError(ctx, "writing exclude file", err)
	}

	// The exclude-file path lands in tar's argv and the directories in its
	// working dirs — subprocess-visible, so they go through RealPath.
	srcRC, dstRC, err := sys.Pipe(
		Cmd{
			Argv:   []string{"tar", "--exclude-from=" + sys.RealPath(excludeFile), "-cf", "-", "."},
			Dir:    sys.RealPath(staging),
			Stderr: ctx.Stderr,
		},
		Cmd{
			Argv:   []string{"tar", "-xf", "-"},
			Dir:    sys.RealPath("/workspace"),
			Stderr: ctx.Stderr,
		},
	)
	// Temp-file cleanup happens BEFORE the status check so it runs on the
	// failure path too (WS-11).
	_ = sys.Remove(excludeFile)
	if err != nil {
		// The source tar could not even start (typically a missing staging
		// dir). Fail closed with the script's rc-check message — never a
		// silently empty /workspace.
		srcRC, dstRC = 1, 0
	}
	if srcRC != 0 || dstRC != 0 {
		fmt.Fprintf(ctx.Stderr, "moat: failed to populate workspace volume (src=%d dst=%d)\n", srcRC, dstRC)
		return exitError{code: 1}
	}

	// Hand the fresh (root-owned) volume to the agent user. Unlike the
	// staging chowns this is UNGUARDED in the script — a failure (including
	// a missing moatuser account) is fatal (WS-10). lchown per node: the
	// re-own must never follow a symlink out of the tree.
	u, ok := sys.LookupUser("moatuser")
	if !ok {
		return fatalPhaseError(ctx, "chowning /workspace", fmt.Errorf("user moatuser does not exist"))
	}
	var chownErr error
	walkErr := sys.WalkDir("/workspace", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if cerr := sys.Lchown(path, u.UID, u.GID); cerr != nil && chownErr == nil {
			chownErr = cerr
		}
		return nil
	})
	if walkErr != nil {
		return fatalPhaseError(ctx, "chowning /workspace", walkErr)
	}
	if chownErr != nil {
		return fatalPhaseError(ctx, "chowning /workspace", chownErr)
	}
	return nil
}

// namedVolumeChownPhase mirrors the named-volume ownership block: Docker
// named volumes are created root-owned, so each mount root is chowned to
// moatuser — NON-recursively on purpose (a fresh volume's root is the only
// root-owned node; chown -R over a multi-GB cache on every start would
// reintroduce the slowness this feature avoids) and best-effort.
func namedVolumeChownPhase(ctx *Context) error {
	cfg, sys := ctx.Cfg, ctx.Sys
	if cfg.VolumeChown == "" || sys.Geteuid() != 0 || !moatuserExists(sys) {
		return nil
	}
	u, ok := sys.LookupUser("moatuser")
	if !ok {
		return nil
	}
	for _, p := range volumeChownPaths(cfg.VolumeChown) {
		_ = sys.Chown(p, u.UID, u.GID) // best-effort, silent
	}
	return nil
}
