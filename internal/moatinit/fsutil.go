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

// moatuserExists mirrors `id moatuser >/dev/null 2>&1` (EXEC-14: every
// branch uses the same existence check).
func moatuserExists(sys Sys) bool {
	_, ok := sys.LookupUser("moatuser")
	return ok
}

// recursiveChownBestEffort mirrors `chown -R user:group root 2>/dev/null ||
// true`: every node in the tree is re-owned via lchown (GNU chown -R does
// not dereference symlinks encountered during traversal, so out-of-tree
// symlink targets are never re-owned), and every error — including walk
// errors — is swallowed.
func recursiveChownBestEffort(sys Sys, root string, uid, gid int) {
	_ = sys.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr // best-effort: skip unreadable entries
		}
		_ = sys.Lchown(path, uid, gid)
		return nil
	})
}
