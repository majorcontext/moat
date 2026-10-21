package credential

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRefreshLockSerializesCLIWrites(t *testing.T) {
	if operation := os.Getenv("MOAT_TEST_CREDENTIAL_OPERATION"); operation != "" {
		store, err := NewProfileStore("base", make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ready")
		if operation == "save" {
			err = store.Save(Credential{Provider: "github", Token: "new-grant"})
		} else {
			err = store.Delete("github")
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	for _, operation := range []string{"save", "delete"} {
		t.Run(operation, func(t *testing.T) {
			_, base, child := inheritedStores(t)
			saveProfileToken(t, base, "github", "original")
			owner, unlock, err := LockRefreshStore(context.Background(), child, "github")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(unlock)
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executable, "-test.run=^TestRefreshLockSerializesCLIWrites$")
			cmd.Env = append(os.Environ(), "MOAT_TEST_CREDENTIAL_OPERATION="+operation)
			var stderr strings.Builder
			cmd.Stderr = &stderr
			stdout, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			ready, err := bufio.NewReader(stdout).ReadString('\n')
			if err != nil || ready != "ready\n" {
				t.Fatalf("writer did not start: %q, %v", ready, err)
			}
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			select {
			case err := <-done:
				t.Fatalf("writer bypassed refresh lock: %v, %s", err, &stderr)
			case <-time.After(100 * time.Millisecond):
			}
			// Check the file as well: a race-enabled subprocess may delay exit
			// after completing its write, which must not hide a missing lock.
			assertProfileToken(t, base, "github", "original")
			if err := owner.Save(Credential{Provider: "github", Token: "refreshed"}); err != nil {
				t.Fatal(err)
			}
			unlock()
			if err := <-done; err != nil {
				t.Fatalf("writer failed: %v, %s", err, &stderr)
			}
			if operation == "save" {
				assertProfileToken(t, base, "github", "new-grant")
			} else if _, err := base.Get("github"); !errors.Is(err, ErrNotFound) {
				t.Fatalf("revoked credential was resurrected: %v", err)
			}
		})
	}
}

func TestRefreshLockCancellation(t *testing.T) {
	_, base, child := inheritedStores(t)
	saveProfileToken(t, base, "github", "original")
	_, unlock, err := LockRefreshStore(context.Background(), child, "github")
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	_, release, err := LockRefreshStore(ctx, child, "github")
	if release != nil {
		release()
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("lock wait = %v", err)
	}
}

func TestDeleteFailureKeepsExclusion(t *testing.T) {
	root, _, child := inheritedStores(t)
	saveProfileToken(t, root, "github", "root")
	// A nonempty directory at the file path reliably fails removal, including
	// when the test runs as root (where permission-based failures do not).
	if err := os.Mkdir(child.path("github"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child.path("github"), "blocker"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := child.Delete("github")
	if err == nil || !strings.Contains(err.Error(), "excluded from inheritance") {
		t.Fatalf("unclear partial failure: %v", err)
	}
	if _, err := child.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed deletion exposed parent: %v", err)
	}
	assertProfileToken(t, root, "github", "root")
	if err := os.RemoveAll(child.path("github")); err != nil {
		t.Fatal(err)
	}
	if err := child.Delete("github"); err != nil {
		t.Fatal(err)
	}
	if _, err := child.Get("github"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retry removed exclusion: %v", err)
	}
}
