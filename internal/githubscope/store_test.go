package githubscope

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStoreRequiresLockFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreFileName)
	require.NoError(t, os.Mkdir(path+".lock", 0o700))
	_, err := OpenStore(path)
	require.ErrorContains(t, err, "approval lock file")
	require.NoError(t, os.Remove(path+".lock"))
	store, err := OpenStore(path)
	require.NoError(t, err)
	require.NoError(t, store.Close())
	_, err = store.Get("/project")
	require.ErrorContains(t, err, "closed")
}

func TestStoreRejectsEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreFileName)
	// A fresh store has a lock file but no data file until its first write.
	store, err := OpenStore(path)
	require.NoError(t, err)
	project, err := store.Get("/project")
	require.NoError(t, err)
	require.Nil(t, project)
	require.NoError(t, store.Close())
	require.NoError(t, os.WriteFile(path, nil, 0o600))
	_, err = OpenStore(path)
	require.ErrorContains(t, err, "is empty")
}

func TestStoreIgnoresIncompleteTemporaryWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), StoreFileName)
	store, err := OpenStore(path)
	require.NoError(t, err)
	require.NoError(t, Approve(store, "/project", []Candidate{{Repo: "acme/app"}}, time.Now()))
	require.NoError(t, store.Close())
	// An interrupted write before rename leaves the committed store intact.
	require.NoError(t, os.WriteFile(path+".tmp", nil, 0o600))
	store, err = OpenStore(path)
	require.NoError(t, err)
	defer store.Close()
	project, err := store.Get("/project")
	require.NoError(t, err)
	require.Equal(t, []string{"acme/app"}, project.RepoNames())
}
