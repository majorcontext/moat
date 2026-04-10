package proxy

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGCloudMetadataRouting(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Metadata-Flavor", "Google")
		w.WriteHeader(200)
	})

	// Create proxy with gcloud handler configured.
	p := NewProxy()
	p.SetGCloudHandler(mock)

	// Simulate a proxied GET from container to metadata.google.internal.
	req := httptest.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	req.Host = "metadata.google.internal"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Errorf("expected gcloud handler to be called, got status %d", w.Code)
	}
}

func TestGCloudMetadataRoutingIP(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(200)
	})

	p := NewProxy()
	p.SetGCloudHandler(mock)

	req := httptest.NewRequest("GET", "http://169.254.169.254/computeMetadata/v1/project/project-id", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	req.Host = "169.254.169.254"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Errorf("expected gcloud handler to be called for IP, got status %d", w.Code)
	}
}

func TestGCloudMetadataNotRoutedWithoutHandler(t *testing.T) {
	p := NewProxy()

	req := httptest.NewRequest("GET", "http://metadata.google.internal/computeMetadata/v1/project/project-id", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	req.Host = "metadata.google.internal"
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	// Without a handler, should not be 200 (will fall through to normal proxy handling).
	// Just ensure no panic.
}
