//go:build !darwin && !windows

package githubscope

import "path/filepath"

func canonicalProjectPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
