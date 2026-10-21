package moatinit

import (
	"os"
	"path/filepath"
	"testing"
)

func TestForeignOwned(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "f")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileInfo, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	me := os.Geteuid()
	other := me + 1
	if me == 0 {
		// Running as root: the file is owned by 0, so any non-root euid
		// sees it as foreign.
		other = 1000
	}

	tests := []struct {
		name string
		info os.FileInfo
		euid int
		want bool
	}{
		{"file owned by another uid", fileInfo, other, true},
		{"root never foreign", fileInfo, 0, false},
		{"directory not replaced", dirInfo, other, false},
	}
	if me != 0 {
		tests = append(tests, struct {
			name string
			info os.FileInfo
			euid int
			want bool
		}{"file owned by euid", fileInfo, me, false})
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := foreignOwned(tt.info, tt.euid); got != tt.want {
				t.Errorf("foreignOwned(%s, euid=%d) = %v, want %v", tt.info.Name(), tt.euid, got, tt.want)
			}
		})
	}
}

// Overwriting a destination the process already owns must keep cp -p
// semantics: contents replaced and the source mode applied.
func TestCopyFilePreservingOverwritesOwnedFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old contents"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := (&OSSys{}).CopyFilePreserving(src, dst); err != nil {
		t.Fatalf("CopyFilePreserving: %v", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("dst contents = %q, want %q", got, "new")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("dst mode = %o, want 600", info.Mode().Perm())
	}
}
