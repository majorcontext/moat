package run

import (
	"fmt"
	"sync"
	"testing"

	"github.com/majorcontext/moat/internal/storage"
)

// TestProviderMetaConcurrentAccess guards against the data race where
// SaveMetadata reads r.ProviderMeta while a provider stopped-hook writes it.
// Both paths must hold stateMu; run with -race to catch a regression.
func TestProviderMetaConcurrentAccess(t *testing.T) {
	store, err := storage.NewRunStore(t.TempDir(), "run_pmtest")
	if err != nil {
		t.Fatalf("NewRunStore: %v", err)
	}
	r := &Run{ID: "run_pmtest", Name: "pm", Store: store, ProviderMeta: map[string]string{}}

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		// Reader: mirrors SaveMetadata being called from monitorContainerExit/Stop.
		go func() {
			defer wg.Done()
			_ = r.SaveMetadata()
		}()
		// Writer: mirrors runProviderStoppedHooks merging provider metadata.
		go func(n int) {
			defer wg.Done()
			r.stateMu.Lock()
			r.ProviderMeta[fmt.Sprintf("k%d", n)] = "v"
			r.stateMu.Unlock()
		}(i)
	}
	wg.Wait()
}
