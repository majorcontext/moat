package moatinit

import (
	"bytes"
	"testing"
	"time"
)

// chownCall records one (l)chown request so tests can assert ownership
// behavior without root privileges (the real syscall would fail under go
// test; recording matches the plan's "recording chown" seam).
type chownCall struct {
	path     string
	uid, gid int
	lchown   bool
}

// testSys embeds the production OSSys pointed at a t.TempDir() root — real
// filesystem semantics for mkdir/copy/chmod/write — and shadows identity,
// ownership, subprocess, and DNS with controllable fakes.
type testSys struct {
	*OSSys
	t *testing.T

	euid  int
	users map[string]User

	chowns   []chownCall
	chownErr error // returned (after recording) to prove best-effort paths

	resolve4     map[string]string
	resolveAny   map[string]string
	resolveCalls []string

	runs    []Cmd
	runHook func(c Cmd) (int, error)

	missingBinaries map[string]bool

	sleeps int

	env map[string]string // views/mutations of the live process env
}

func newTestSys(t *testing.T, euid int, withMoatuser bool) *testSys {
	t.Helper()
	ts := &testSys{
		OSSys:           &OSSys{Root: t.TempDir()},
		t:               t,
		euid:            euid,
		users:           map[string]User{},
		resolve4:        map[string]string{},
		resolveAny:      map[string]string{},
		missingBinaries: map[string]bool{},
		env:             map[string]string{},
	}
	if withMoatuser {
		ts.users["moatuser"] = User{UID: 5000, GID: 5000}
	}
	return ts
}

func (ts *testSys) Geteuid() int { return ts.euid }

func (ts *testSys) LookupUser(name string) (User, bool) {
	u, ok := ts.users[name]
	return u, ok
}

func (ts *testSys) Chown(path string, uid, gid int) error {
	ts.chowns = append(ts.chowns, chownCall{path: path, uid: uid, gid: gid})
	return ts.chownErr
}

func (ts *testSys) Lchown(path string, uid, gid int) error {
	ts.chowns = append(ts.chowns, chownCall{path: path, uid: uid, gid: gid, lchown: true})
	return ts.chownErr
}

func (ts *testSys) ResolveIPv4First(host string) string {
	ts.resolveCalls = append(ts.resolveCalls, "ip4:"+host)
	return ts.resolve4[host]
}

func (ts *testSys) ResolveAnyFirst(host string) string {
	ts.resolveCalls = append(ts.resolveCalls, "any:"+host)
	return ts.resolveAny[host]
}

func (ts *testSys) Sleep(_ time.Duration) { ts.sleeps++ } // no real waiting in tests

func (ts *testSys) LookPath(file string) (string, error) {
	if ts.missingBinaries[file] {
		return "", &missingBinaryError{name: file}
	}
	return "/usr/bin/" + file, nil
}

type missingBinaryError struct{ name string }

func (e *missingBinaryError) Error() string { return e.name + ": not found on PATH" }

func (ts *testSys) Run(c Cmd) (int, error) {
	ts.runs = append(ts.runs, c)
	if ts.runHook != nil {
		return ts.runHook(c)
	}
	return 0, nil
}

func (ts *testSys) Getenv(key string) string { return ts.env[key] }
func (ts *testSys) Setenv(key, value string) { ts.env[key] = value }
func (ts *testSys) Unsetenv(key string)      { delete(ts.env, key) }

// chowned reports whether a chown for path was recorded.
func (ts *testSys) chowned(path string) bool {
	for _, c := range ts.chowns {
		if c.path == path {
			return true
		}
	}
	return false
}

// newTestContext builds a Context around a testSys with a config literal.
func newTestContext(ts *testSys, cfg Config) (*Context, *bytes.Buffer) {
	stderr := &bytes.Buffer{}
	return &Context{Sys: ts, Cfg: &cfg, Argv: []string{"true"}, Stderr: stderr}, stderr
}
