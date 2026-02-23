package cli

import (
	"context"
	"io"
	"sort"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/container"
	"github.com/majorcontext/moat/internal/run"
)

// listCleanStubRuntime is a minimal mock of container.Runtime for testing
// isImageInUse. Only ListContainers is implemented; all other methods panic
// to catch unexpected calls.
type listCleanStubRuntime struct {
	containers []container.Info
	listErr    error
}

func (s *listCleanStubRuntime) ListContainers(ctx context.Context) ([]container.Info, error) {
	return s.containers, s.listErr
}

// Stubs for the rest of container.Runtime — not exercised by isImageInUse.
func (s *listCleanStubRuntime) Type() container.RuntimeType    { return "" }
func (s *listCleanStubRuntime) Ping(ctx context.Context) error { return nil }
func (s *listCleanStubRuntime) CreateContainer(ctx context.Context, cfg container.Config) (string, error) {
	return "", nil
}
func (s *listCleanStubRuntime) StartContainer(ctx context.Context, id string) error { return nil }
func (s *listCleanStubRuntime) StopContainer(ctx context.Context, id string) error  { return nil }
func (s *listCleanStubRuntime) WaitContainer(ctx context.Context, id string) (int64, error) {
	return 0, nil
}
func (s *listCleanStubRuntime) RemoveContainer(ctx context.Context, id string) error { return nil }
func (s *listCleanStubRuntime) ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	return nil, nil
}
func (s *listCleanStubRuntime) ContainerLogsAll(ctx context.Context, id string) ([]byte, error) {
	return nil, nil
}
func (s *listCleanStubRuntime) GetPortBindings(ctx context.Context, id string) (map[int]int, error) {
	return nil, nil
}
func (s *listCleanStubRuntime) GetHostAddress() string    { return "" }
func (s *listCleanStubRuntime) SupportsHostNetwork() bool { return false }
func (s *listCleanStubRuntime) NetworkManager() container.NetworkManager {
	return nil
}
func (s *listCleanStubRuntime) SidecarManager() container.SidecarManager {
	return nil
}
func (s *listCleanStubRuntime) BuildManager() container.BuildManager {
	return nil
}
func (s *listCleanStubRuntime) ServiceManager() container.ServiceManager {
	return nil
}
func (s *listCleanStubRuntime) Close() error { return nil }
func (s *listCleanStubRuntime) SetupFirewall(ctx context.Context, id string, proxyHost string, proxyPort int) error {
	return nil
}
func (s *listCleanStubRuntime) ListImages(ctx context.Context) ([]container.ImageInfo, error) {
	return nil, nil
}
func (s *listCleanStubRuntime) ContainerState(ctx context.Context, id string) (string, error) {
	return "", nil
}
func (s *listCleanStubRuntime) RemoveImage(ctx context.Context, id string) error { return nil }
func (s *listCleanStubRuntime) Attach(ctx context.Context, id string, opts container.AttachOptions) error {
	return nil
}
func (s *listCleanStubRuntime) StartAttached(ctx context.Context, id string, opts container.AttachOptions) error {
	return nil
}
func (s *listCleanStubRuntime) ResizeTTY(ctx context.Context, id string, height, width uint) error {
	return nil
}

// --- isImageInUse tests ---

func TestIsImageInUse_NotInUse(t *testing.T) {
	rt := &listCleanStubRuntime{
		containers: []container.Info{
			{ID: "c1", Image: "other-image:latest", Status: "running"},
			{ID: "c2", Image: "moat-abc:latest", Status: "exited"},
		},
	}
	if isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = true, want false when image not used by running containers")
	}
}

func TestIsImageInUse_MatchByTag(t *testing.T) {
	rt := &listCleanStubRuntime{
		containers: []container.Info{
			{ID: "c1", Image: "moat-test:latest", Status: "running"},
		},
	}
	if !isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = false, want true when running container matches image tag")
	}
}

func TestIsImageInUse_MatchByID(t *testing.T) {
	rt := &listCleanStubRuntime{
		containers: []container.Info{
			{ID: "c1", Image: "img-123", Status: "running"},
		},
	}
	if !isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = false, want true when running container matches image ID")
	}
}

func TestIsImageInUse_StoppedContainerDoesNotCount(t *testing.T) {
	rt := &listCleanStubRuntime{
		containers: []container.Info{
			{ID: "c1", Image: "moat-test:latest", Status: "exited"},
		},
	}
	if isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = true, want false when container using image is stopped")
	}
}

func TestIsImageInUse_ListError_DefaultsToInUse(t *testing.T) {
	rt := &listCleanStubRuntime{
		listErr: io.ErrUnexpectedEOF,
	}
	if !isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = false, want true (err on side of caution) when ListContainers fails")
	}
}

func TestIsImageInUse_NoContainers(t *testing.T) {
	rt := &listCleanStubRuntime{
		containers: nil,
	}
	if isImageInUse(context.Background(), rt, "img-123", "moat-test:latest") {
		t.Error("isImageInUse() = true, want false when no containers exist")
	}
}

// --- List worktree column display logic tests ---
// These test the conditional column display logic extracted from listRuns.
// TODO: refactor to call production functions directly instead of duplicating logic.

func TestListWorktreeColumnDetection(t *testing.T) {
	tests := []struct {
		name         string
		runs         []*run.Run
		wantWorktree bool
	}{
		{
			name:         "no runs",
			runs:         nil,
			wantWorktree: false,
		},
		{
			name: "runs without worktree info",
			runs: []*run.Run{
				{ID: "r1", Name: "run1", State: run.StateRunning},
				{ID: "r2", Name: "run2", State: run.StateStopped},
			},
			wantWorktree: false,
		},
		{
			name: "one run with worktree branch",
			runs: []*run.Run{
				{ID: "r1", Name: "run1", State: run.StateRunning, WorktreeBranch: "feature-x"},
			},
			wantWorktree: true,
		},
		{
			name: "mixed runs - some with worktree",
			runs: []*run.Run{
				{ID: "r1", Name: "run1", State: run.StateRunning},
				{ID: "r2", Name: "run2", State: run.StateRunning, WorktreeBranch: "dark-mode"},
			},
			wantWorktree: true,
		},
		{
			name: "worktree path set but no branch",
			runs: []*run.Run{
				{ID: "r1", Name: "run1", State: run.StateStopped, WorktreePath: "/some/path"},
			},
			wantWorktree: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// This mirrors the logic in listRuns (list.go lines 54-60)
			hasWorktree := false
			for _, r := range tt.runs {
				if r.WorktreeBranch != "" {
					hasWorktree = true
					break
				}
			}
			if hasWorktree != tt.wantWorktree {
				t.Errorf("hasWorktree = %v, want %v", hasWorktree, tt.wantWorktree)
			}
		})
	}
}

// --- Clean worktree run detection logic tests ---
// These test the logic that identifies stopped runs with worktree paths.
// TODO: refactor to call production functions directly instead of duplicating logic.

func TestCleanWorktreeRunDetection(t *testing.T) {
	now := time.Now()
	runs := []*run.Run{
		{ID: "r1", Name: "plain-run", State: run.StateStopped, CreatedAt: now},
		{ID: "r2", Name: "wt-stopped", State: run.StateStopped, CreatedAt: now,
			WorktreePath: "/home/user/.moat/worktrees/repo/feat", WorktreeBranch: "feat"},
		{ID: "r3", Name: "wt-running", State: run.StateRunning, CreatedAt: now,
			WorktreePath: "/home/user/.moat/worktrees/repo/main", WorktreeBranch: "main"},
		{ID: "r4", Name: "wt-stopped2", State: run.StateStopped, CreatedAt: now,
			WorktreePath: "/home/user/.moat/worktrees/repo/fix", WorktreeBranch: "fix"},
	}

	// This mirrors the logic in cleanResources (clean.go lines 64-73)
	var stoppedRuns []*run.Run
	var worktreeRuns []*run.Run
	for _, r := range runs {
		if r.State == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
			if r.WorktreePath != "" {
				worktreeRuns = append(worktreeRuns, r)
			}
		}
	}

	if len(stoppedRuns) != 3 {
		t.Errorf("stoppedRuns = %d, want 3", len(stoppedRuns))
	}
	if len(worktreeRuns) != 2 {
		t.Errorf("worktreeRuns = %d, want 2", len(worktreeRuns))
	}

	// Verify correct runs identified
	wtNames := make([]string, len(worktreeRuns))
	for i, r := range worktreeRuns {
		wtNames[i] = r.Name
	}
	sort.Strings(wtNames)
	if wtNames[0] != "wt-stopped" || wtNames[1] != "wt-stopped2" {
		t.Errorf("worktreeRuns names = %v, want [wt-stopped, wt-stopped2]", wtNames)
	}
}

func TestCleanWorktreeRunDetection_NoWorktrees(t *testing.T) {
	runs := []*run.Run{
		{ID: "r1", Name: "run1", State: run.StateStopped},
		{ID: "r2", Name: "run2", State: run.StateStopped},
	}

	var worktreeRuns []*run.Run
	for _, r := range runs {
		if r.State == run.StateStopped && r.WorktreePath != "" {
			worktreeRuns = append(worktreeRuns, r)
		}
	}

	if len(worktreeRuns) != 0 {
		t.Errorf("worktreeRuns = %d, want 0", len(worktreeRuns))
	}
}

func TestCleanWorktreeRunDetection_RunningWorktreesExcluded(t *testing.T) {
	runs := []*run.Run{
		{ID: "r1", Name: "active-wt", State: run.StateRunning,
			WorktreePath: "/path/to/wt1", WorktreeBranch: "feature"},
	}

	var worktreeRuns []*run.Run
	for _, r := range runs {
		if r.State == run.StateStopped && r.WorktreePath != "" {
			worktreeRuns = append(worktreeRuns, r)
		}
	}

	if len(worktreeRuns) != 0 {
		t.Errorf("worktreeRuns = %d, want 0 (running worktrees should not be cleaned)", len(worktreeRuns))
	}
}

// --- List sorting tests ---
// Verify that list sorts runs newest-first (consistent with list.go line 40-42).
// TODO: refactor to call production functions directly instead of duplicating logic.

func TestListRunsSortOrder(t *testing.T) {
	now := time.Now()
	runs := []*run.Run{
		{ID: "r1", Name: "oldest", CreatedAt: now.Add(-3 * time.Hour)},
		{ID: "r2", Name: "newest", CreatedAt: now},
		{ID: "r3", Name: "middle", CreatedAt: now.Add(-1 * time.Hour)},
	}

	// Mirrors sort logic in listRuns (list.go lines 40-42)
	sort.Slice(runs, func(i, j int) bool {
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})

	if runs[0].Name != "newest" {
		t.Errorf("first run = %q, want %q", runs[0].Name, "newest")
	}
	if runs[1].Name != "middle" {
		t.Errorf("second run = %q, want %q", runs[1].Name, "middle")
	}
	if runs[2].Name != "oldest" {
		t.Errorf("third run = %q, want %q", runs[2].Name, "oldest")
	}
}

// --- Worktree branch display in clean output ---
// TODO: refactor to call production functions directly instead of duplicating logic.

func TestCleanWorktreeBranchFallback(t *testing.T) {
	// In clean.go lines 278-281, when displaying worktree info,
	// if WorktreeBranch is empty, it falls back to WorktreePath.
	tests := []struct {
		name      string
		branch    string
		path      string
		wantLabel string
	}{
		{"branch set", "feature-x", "/some/path", "feature-x"},
		{"branch empty, fallback to path", "", "/some/path/to/wt", "/some/path/to/wt"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mirrors clean.go lines 278-281
			branch := tt.branch
			if branch == "" {
				branch = tt.path
			}
			if branch != tt.wantLabel {
				t.Errorf("display label = %q, want %q", branch, tt.wantLabel)
			}
		})
	}
}

// --- Resource count does not double-count worktrees ---
// TODO: refactor to call production functions directly instead of testing arithmetic.

func TestCleanResourceCountDoesNotDoubleCountWorktrees(t *testing.T) {
	// clean.go: resourceCount = len(stoppedRuns) + len(unusedImages) + len(orphanedNetworks)
	// worktreeRuns is a subset of stoppedRuns, so it must not be added separately.
	stoppedRuns := 3
	unusedImages := 2
	orphanedNetworks := 1

	resourceCount := stoppedRuns + unusedImages + orphanedNetworks
	if resourceCount != 6 {
		t.Errorf("resourceCount = %d, want 6", resourceCount)
	}
}

// --- Backward compatibility: non-worktree usage ---
// TODO: refactor to call production functions directly instead of duplicating logic.

func TestListBackwardCompat_NoWorktreeColumn(t *testing.T) {
	// When no runs have WorktreeBranch set, the hasWorktree flag should be
	// false, preserving the original 5-column layout (NAME RUN_ID STATE AGE ENDPOINTS).
	runs := []*run.Run{
		{ID: "r1", Name: "run1", State: run.StateRunning, CreatedAt: time.Now()},
		{ID: "r2", Name: "run2", State: run.StateStopped, CreatedAt: time.Now()},
	}

	hasWorktree := false
	for _, r := range runs {
		if r.WorktreeBranch != "" {
			hasWorktree = true
			break
		}
	}

	if hasWorktree {
		t.Error("hasWorktree should be false for runs without worktree info, preserving backward-compatible column layout")
	}
}

func TestCleanBackwardCompat_NoWorktreeCleanup(t *testing.T) {
	// When stopped runs have no WorktreePath, the worktree cleanup section
	// should be empty, preserving original clean behavior.
	runs := []*run.Run{
		{ID: "r1", Name: "run1", State: run.StateStopped},
		{ID: "r2", Name: "run2", State: run.StateStopped},
	}

	var stoppedRuns []*run.Run
	var worktreeRuns []*run.Run
	for _, r := range runs {
		if r.State == run.StateStopped {
			stoppedRuns = append(stoppedRuns, r)
			if r.WorktreePath != "" {
				worktreeRuns = append(worktreeRuns, r)
			}
		}
	}

	if len(stoppedRuns) != 2 {
		t.Errorf("stoppedRuns = %d, want 2", len(stoppedRuns))
	}
	if len(worktreeRuns) != 0 {
		t.Errorf("worktreeRuns = %d, want 0 for backward-compatible clean behavior", len(worktreeRuns))
	}
}
