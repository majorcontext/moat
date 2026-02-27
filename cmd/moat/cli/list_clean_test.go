package cli

import (
	"context"
	"io"
	"testing"

	"github.com/majorcontext/moat/internal/container"
)

// listCleanStubRuntime is a minimal mock of container.Runtime for testing
// isImageInUse. Only ListContainers is implemented; all other methods return
// zero values.
type listCleanStubRuntime struct {
	containers []container.Info
	listErr    error
}

func (s *listCleanStubRuntime) ListContainers(ctx context.Context) ([]container.Info, error) {
	return s.containers, s.listErr
}

// Stubs for the rest of container.Runtime — not exercised by isImageInUse.
// These panic to catch unexpected calls if isImageInUse is ever extended.
func (s *listCleanStubRuntime) Type() container.RuntimeType { panic("unexpected call to Type") }
func (s *listCleanStubRuntime) Ping(ctx context.Context) error {
	panic("unexpected call to Ping")
}
func (s *listCleanStubRuntime) CreateContainer(ctx context.Context, cfg container.Config) (string, error) {
	panic("unexpected call to CreateContainer")
}
func (s *listCleanStubRuntime) StartContainer(ctx context.Context, id string) error {
	panic("unexpected call to StartContainer")
}
func (s *listCleanStubRuntime) StopContainer(ctx context.Context, id string) error {
	panic("unexpected call to StopContainer")
}
func (s *listCleanStubRuntime) WaitContainer(ctx context.Context, id string) (int64, error) {
	panic("unexpected call to WaitContainer")
}
func (s *listCleanStubRuntime) RemoveContainer(ctx context.Context, id string) error {
	panic("unexpected call to RemoveContainer")
}
func (s *listCleanStubRuntime) ContainerLogs(ctx context.Context, id string) (io.ReadCloser, error) {
	panic("unexpected call to ContainerLogs")
}
func (s *listCleanStubRuntime) ContainerLogsAll(ctx context.Context, id string) ([]byte, error) {
	panic("unexpected call to ContainerLogsAll")
}
func (s *listCleanStubRuntime) GetPortBindings(ctx context.Context, id string) (map[int]int, error) {
	panic("unexpected call to GetPortBindings")
}
func (s *listCleanStubRuntime) GetHostAddress() string {
	panic("unexpected call to GetHostAddress")
}
func (s *listCleanStubRuntime) SupportsHostNetwork() bool {
	panic("unexpected call to SupportsHostNetwork")
}
func (s *listCleanStubRuntime) NetworkManager() container.NetworkManager {
	panic("unexpected call to NetworkManager")
}
func (s *listCleanStubRuntime) SidecarManager() container.SidecarManager {
	panic("unexpected call to SidecarManager")
}
func (s *listCleanStubRuntime) BuildManager() container.BuildManager {
	panic("unexpected call to BuildManager")
}
func (s *listCleanStubRuntime) ServiceManager() container.ServiceManager {
	panic("unexpected call to ServiceManager")
}
func (s *listCleanStubRuntime) Close() error { panic("unexpected call to Close") }
func (s *listCleanStubRuntime) SetupFirewall(ctx context.Context, id string, proxyHost string, proxyPort int) error {
	panic("unexpected call to SetupFirewall")
}
func (s *listCleanStubRuntime) ListImages(ctx context.Context) ([]container.ImageInfo, error) {
	panic("unexpected call to ListImages")
}
func (s *listCleanStubRuntime) ContainerState(ctx context.Context, id string) (string, error) {
	panic("unexpected call to ContainerState")
}
func (s *listCleanStubRuntime) RemoveImage(ctx context.Context, id string) error {
	panic("unexpected call to RemoveImage")
}
func (s *listCleanStubRuntime) Exec(ctx context.Context, id string, opts container.ExecOptions) (int, error) {
	panic("unexpected call to Exec")
}
func (s *listCleanStubRuntime) ResizeExec(ctx context.Context, id string, height, width uint) error {
	panic("unexpected call to ResizeExec")
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
