package moatinit

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// User is a resolved account (the subset of user.User the entrypoint needs).
type User struct {
	UID int
	GID int
}

// Cmd describes a subprocess invocation.
type Cmd struct {
	Argv   []string
	Dir    string    // working directory ("" = inherit)
	Env    []string  // nil = inherit the current process environment
	Stdout io.Writer // nil = discard
	Stderr io.Writer // nil = discard

	// LogFile, when set, redirects both stdout and stderr to this file
	// (container-absolute; re-rooted like other fs paths), truncating it —
	// the Go form of `>/var/log/dockerd.log 2>&1`. Overrides Stdout/Stderr.
	LogFile string

	// Timeout, when positive, bounds the run so a hanging probe inside a
	// bounded retry loop cannot consume the loop's whole budget (plan
	// Appendix B: per-attempt timeout below the loop budget). A timed-out
	// command reports a non-zero exit code.
	Timeout time.Duration
}

// Sys abstracts every identity, filesystem, subprocess, and DNS operation the
// entrypoint performs, so phases can be exercised under `go test` against an
// injected temp root with no container and no root privileges. OSSys is the
// production implementation; tests embed *OSSys (pointed at a t.TempDir()
// root) and shadow the identity/subprocess/DNS methods they need to fake.
//
// Filesystem methods take absolute container paths (/etc/hosts,
// /home/moatuser/...); OSSys re-roots them under Root when set.
type Sys interface {
	// Identity. Lookups use pure-Go os/user (files NSS: /etc/passwd,
	// /etc/group) — parity with the script's `id`/`getent` for the standard
	// moat images; documented divergence for LDAP/SSSD-backed custom images
	// (detection only — the privilege drop itself is delegated to gosu).
	Geteuid() int
	LookupUser(name string) (User, bool)
	LookupGroupByName(name string) (gid string, ok bool)
	LookupGroupByGID(gid string) (name string, ok bool)

	// Process environment. Mirrors the shell's export/unset so that children
	// spawned later (pre_run hook, gosu, the exec'd command) inherit exactly
	// what they would have under the script.
	Getenv(key string) string
	Setenv(key, value string)
	Unsetenv(key string)
	Environ() []string

	// Filesystem.
	Stat(path string) (fs.FileInfo, error)
	Lstat(path string) (fs.FileInfo, error)
	MkdirAll(path string, perm fs.FileMode) error
	Chmod(path string, perm fs.FileMode) error
	Chown(path string, uid, gid int) error
	Lchown(path string, uid, gid int) error
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm fs.FileMode) error
	AppendFile(path string, data []byte) error
	Remove(path string) error
	CopyFilePreserving(src, dst string) error // cp -p
	CopyTreePreserving(src, dst string) error // cp -rp
	WalkDir(root string, fn fs.WalkDirFunc) error
	Getpid() int

	// RealPath maps a container-absolute path to the path a SUBPROCESS must
	// use to reach it. In production this is the identity; under an
	// injected test root it prefixes the root, so phases that hand paths to
	// tar/git/socat (argv or working directory) stay testable against the
	// same tree the filesystem methods manipulate.
	RealPath(path string) string

	// Subprocesses.
	LookPath(file string) (string, error)
	Run(c Cmd) (exitCode int, err error)
	StartDetached(c Cmd) (pid int, err error)
	ProcessAlive(pid int) bool
	Pipe(src, dst Cmd) (srcExit, dstExit int, err error)

	// DNS. ResolveIPv4First is the port of `getent ahostsv4 | awk '{print
	// $1; exit}'`; ResolveAnyFirst of the `getent hosts` fallback. Both
	// consult /etc/hosts then DNS (Go resolver) and return "" on failure.
	ResolveIPv4First(host string) string
	ResolveAnyFirst(host string) string

	Sleep(d time.Duration)

	// Exec replaces the process image (syscall.Exec). It only returns on
	// error. argv[0] is resolved via PATH when not absolute.
	Exec(argv []string, env []string) error
}

// OSSys is the production Sys backed by the real operating system. Root, when
// non-empty, re-roots all filesystem paths beneath it (used by integration
// tests to run phases against a temp directory).
type OSSys struct {
	Root string

	// resolveTimeout bounds each DNS lookup attempt so a retry loop's total
	// budget is honored even when the resolver hangs (plan Appendix B:
	// per-attempt timeout must stay below the loop budget).
	ResolveTimeout time.Duration
}

// NewSys returns the production Sys.
func NewSys() *OSSys {
	return &OSSys{ResolveTimeout: 2 * time.Second}
}

// path re-roots an absolute container path under Root for tests.
func (s *OSSys) path(p string) string {
	if s.Root == "" {
		return p
	}
	return filepath.Join(s.Root, strings.TrimPrefix(p, "/"))
}

func (s *OSSys) Geteuid() int { return os.Geteuid() }

func (s *OSSys) LookupUser(name string) (User, bool) {
	u, err := user.Lookup(name)
	if err != nil {
		return User{}, false
	}
	uid, err1 := strconv.Atoi(u.Uid)
	gid, err2 := strconv.Atoi(u.Gid)
	if err1 != nil || err2 != nil {
		return User{}, false
	}
	return User{UID: uid, GID: gid}, true
}

func (s *OSSys) LookupGroupByName(name string) (string, bool) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return "", false
	}
	return g.Gid, true
}

func (s *OSSys) LookupGroupByGID(gid string) (string, bool) {
	g, err := user.LookupGroupId(gid)
	if err != nil {
		return "", false
	}
	return g.Name, true
}

func (s *OSSys) Getenv(key string) string { return os.Getenv(key) }
func (s *OSSys) Setenv(key, value string) { os.Setenv(key, value) } //nolint:errcheck // parity: shell export cannot fail
func (s *OSSys) Unsetenv(key string)      { os.Unsetenv(key) }      //nolint:errcheck // parity: shell unset cannot fail
func (s *OSSys) Environ() []string        { return os.Environ() }
func (s *OSSys) Getpid() int              { return os.Getpid() }
func (s *OSSys) Sleep(d time.Duration)    { time.Sleep(d) }

func (s *OSSys) Stat(path string) (fs.FileInfo, error)  { return os.Stat(s.path(path)) }
func (s *OSSys) Lstat(path string) (fs.FileInfo, error) { return os.Lstat(s.path(path)) }

func (s *OSSys) MkdirAll(path string, perm fs.FileMode) error {
	return os.MkdirAll(s.path(path), perm)
}

func (s *OSSys) Chmod(path string, perm fs.FileMode) error {
	return os.Chmod(s.path(path), perm)
}

func (s *OSSys) Chown(path string, uid, gid int) error {
	return os.Chown(s.path(path), uid, gid)
}

func (s *OSSys) Lchown(path string, uid, gid int) error {
	return os.Lchown(s.path(path), uid, gid)
}

func (s *OSSys) ReadFile(path string) ([]byte, error) { return os.ReadFile(s.path(path)) }

func (s *OSSys) WriteFile(path string, data []byte, perm fs.FileMode) error {
	return os.WriteFile(s.path(path), data, perm)
}

func (s *OSSys) AppendFile(path string, data []byte) error {
	f, err := os.OpenFile(s.path(path), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if werr != nil {
		return werr
	}
	return cerr
}

func (s *OSSys) Remove(path string) error { return os.Remove(s.path(path)) }

func (s *OSSys) RealPath(path string) string { return s.path(path) }

// CopyFilePreserving mirrors `cp -p src dst`: bytes, mode, and timestamps are
// preserved (failure is an error); ownership preservation is attempted but,
// like cp -p without appropriate privileges, its failure is not an error.
func (s *OSSys) CopyFilePreserving(src, dst string) error {
	rsrc, rdst := s.path(src), s.path(dst)
	info, err := os.Stat(rsrc)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(rsrc)
	if err != nil {
		return err
	}
	if err := os.WriteFile(rdst, data, info.Mode().Perm()); err != nil {
		return err
	}
	// WriteFile only applies the mode at creation; force it for pre-existing
	// destinations (cp truncates and keeps applying -p semantics).
	if err := os.Chmod(rdst, info.Mode().Perm()); err != nil {
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(rdst, int(st.Uid), int(st.Gid)) // best-effort, like cp -p as non-root
	}
	return os.Chtimes(rdst, time.Now(), info.ModTime())
}

// CopyTreePreserving mirrors `cp -rp src dst` where dst is the destination
// path of the copied tree (POSIX -R semantics: symlinks are duplicated as
// symlinks, never followed).
func (s *OSSys) CopyTreePreserving(src, dst string) error {
	rsrc := s.path(src)
	return filepath.WalkDir(rsrc, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(rsrc, p)
		if err != nil {
			return err
		}
		target := dst
		if rel != "." {
			target = filepath.Join(dst, rel)
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			dest, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(dest, s.path(target))
		case d.IsDir():
			info, err := d.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(s.path(target), info.Mode().Perm())
		default:
			// p is already re-rooted; strip Root before re-entering the seam.
			relSrc := p
			if s.Root != "" {
				relSrc = "/" + strings.TrimPrefix(strings.TrimPrefix(p, s.Root), "/")
			}
			return s.CopyFilePreserving(relSrc, target)
		}
	})
}

func (s *OSSys) WalkDir(root string, fn fs.WalkDirFunc) error {
	rroot := s.path(root)
	return filepath.WalkDir(rroot, func(p string, d fs.DirEntry, err error) error {
		// Report container-absolute paths to the callback so phase logic
		// stays independent of the injected root.
		rel := p
		if s.Root != "" {
			rel = "/" + strings.TrimPrefix(strings.TrimPrefix(p, s.Root), "/")
		}
		return fn(rel, d, err)
	})
}

func (s *OSSys) LookPath(file string) (string, error) { return exec.LookPath(file) }

func (s *OSSys) Run(c Cmd) (int, error) {
	var cmd *exec.Cmd
	if c.Timeout > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), c.Timeout)
		defer cancel()
		cmd = exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	} else {
		cmd = exec.Command(c.Argv[0], c.Argv[1:]...)
	}
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	err := cmd.Run()
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitCodeOf(err), nil
	}
	return -1, err
}

// StartDetached launches a long-lived background child, exactly like the
// shell's `cmd ... &`: the child shares the entrypoint's process group (job
// control is off in non-interactive sh, so backgrounded children are NOT
// re-grouped — parity requires the same signal delivery the shell had), and
// it survives the entrypoint's exec because exec replaces the process image
// without touching children. A reaper goroutine collects the child if it
// dies before the handoff, so ProcessAlive mirrors the shell's `kill -0` on
// a reaped background job instead of seeing a signalable zombie.
func (s *OSSys) StartDetached(c Cmd) (int, error) {
	cmd := exec.Command(c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	if c.LogFile != "" {
		f, err := os.OpenFile(s.path(c.LogFile), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return 0, err
		}
		// The child inherits the fd; our copy closes after Start.
		defer f.Close()
		cmd.Stdout = f
		cmd.Stderr = f
	}
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait() //nolint:errcheck // reap-only; the child outlives us by design
	return cmd.Process.Pid, nil
}

func (s *OSSys) ProcessAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// lockedWriter serializes writes from the two pipe legs when they share a
// destination that is not an *os.File (files are handed to the children as
// fds with no in-process copy goroutine, so os.Stderr needs no locking —
// but a shared in-memory writer, as tests use, would race).
type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}

// Pipe runs `src | dst` and returns both exit codes, mirroring the script's
// capture of the source tar's status alongside the destination's ($? after a
// POSIX pipeline only reports the rightmost command).
func (s *OSSys) Pipe(src, dst Cmd) (int, int, error) {
	if src.Stderr != nil && src.Stderr == dst.Stderr {
		if _, isFile := src.Stderr.(*os.File); !isFile {
			shared := &lockedWriter{w: src.Stderr}
			src.Stderr = shared
			dst.Stderr = shared
		}
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return -1, -1, err
	}
	srcCmd := exec.Command(src.Argv[0], src.Argv[1:]...)
	srcCmd.Dir = src.Dir
	srcCmd.Env = src.Env
	srcCmd.Stdout = pw
	srcCmd.Stderr = src.Stderr
	dstCmd := exec.Command(dst.Argv[0], dst.Argv[1:]...)
	dstCmd.Dir = dst.Dir
	dstCmd.Env = dst.Env
	dstCmd.Stdin = pr
	dstCmd.Stdout = dst.Stdout
	dstCmd.Stderr = dst.Stderr

	if err := srcCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		return -1, -1, err
	}
	if err := dstCmd.Start(); err != nil {
		pw.Close()
		pr.Close()
		_ = srcCmd.Wait()
		return -1, -1, err
	}
	// Close the parent's copies so the pipe sees EOF when src exits.
	pw.Close()
	pr.Close()

	srcRC := exitCodeOf(srcCmd.Wait())
	dstRC := exitCodeOf(dstCmd.Wait())
	return srcRC, dstRC, nil
}

// exitCodeOf translates a Wait error into a shell-style exit code,
// including the 128+signal convention for signal-terminated children (the
// shell's $? would report 128+n; exec.ExitError.ExitCode() reports -1).
func exitCodeOf(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if ws, ok := exitErr.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return exitErr.ExitCode()
	}
	return -1
}

func (s *OSSys) resolveCtx() (context.Context, context.CancelFunc) {
	timeout := s.ResolveTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return context.WithTimeout(context.Background(), timeout)
}

func (s *OSSys) ResolveIPv4First(host string) string {
	ctx, cancel := s.resolveCtx()
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0].String()
}

func (s *OSSys) ResolveAnyFirst(host string) string {
	ctx, cancel := s.resolveCtx()
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return ""
	}
	return ips[0].String()
}

func (s *OSSys) Exec(argv []string, env []string) error {
	path := argv[0]
	if !strings.Contains(path, "/") {
		resolved, err := exec.LookPath(path)
		if err != nil {
			return err
		}
		path = resolved
	}
	return syscall.Exec(path, argv, env)
}
