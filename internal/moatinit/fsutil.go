package moatinit

import "io/fs"

// isFile mirrors `[ -f path ]`: the path exists and is a regular file
// (following symlinks, like test -f).
func isFile(sys Sys, path string) bool {
	info, err := sys.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// isDir mirrors `[ -d path ]`.
func isDir(sys Sys, path string) bool {
	info, err := sys.Stat(path)
	return err == nil && info.IsDir()
}

// isSocket mirrors `[ -S path ]`.
func isSocket(sys Sys, path string) bool {
	info, err := sys.Stat(path)
	return err == nil && info.Mode()&fs.ModeSocket != 0
}

// moatuserExists mirrors `id moatuser >/dev/null 2>&1` (EXEC-14: every
// branch uses the same existence check).
func moatuserExists(sys Sys) bool {
	_, ok := sys.LookupUser("moatuser")
	return ok
}

// recursiveChownBestEffortPruned mirrors `chown -R user:group root 2>/dev/null
// || true` with the named subtree(s) skipped entirely — the Go port of
// `find ROOT -path ROOT/NAME -prune -o -exec chown ... +`, which neither
// re-owns the pruned directory itself nor descends into it. Every node in the
// tree is re-owned via lchown (GNU chown -R does not dereference symlinks
// encountered during traversal, so out-of-tree symlink targets are never
// re-owned), and every error — including walk errors — is swallowed. prune
// names are relative to root; an empty prune is a plain recursive chown.
func recursiveChownBestEffortPruned(sys Sys, root string, uid, gid int, prune []string) {
	skip := make(map[string]bool, len(prune))
	for _, name := range prune {
		skip[root+"/"+name] = true
	}
	_ = sys.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort: skip unreadable entries
		}
		if skip[path] {
			return fs.SkipDir
		}
		_ = sys.Lchown(path, uid, gid)
		return nil
	})
}
