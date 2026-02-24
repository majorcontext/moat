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
