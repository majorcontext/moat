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

// TestGCloudDirectMetadataRouting tests that direct (non-proxied) metadata
// requests are routed to the gcloud handler. This simulates what happens when
// GCE_METADATA_HOST points at the proxy and Python's bare http.client connects
// directly without HTTP_PROXY.
func TestGCloudDirectMetadataRouting(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Metadata-Flavor", "Google")
		w.WriteHeader(200)
	})

	p := NewProxy()
	p.SetGCloudDirectResolver(func() http.Handler { return mock })

	// Direct request: r.URL.Host is empty (not a proxied request).
	req := httptest.NewRequest("GET", "/computeMetadata/v1/project/project-id", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Errorf("expected gcloud direct handler to be called, got status %d", w.Code)
	}
}

// TestGCloudDirectPingRouting tests that the GCE detection ping (GET /)
// is handled for direct requests.
func TestGCloudDirectPingRouting(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.Header().Set("Metadata-Flavor", "Google")
		w.WriteHeader(200)
	})

	p := NewProxy()
	p.SetGCloudDirectResolver(func() http.Handler { return mock })

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Metadata-Flavor", "Google")
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if !called {
		t.Errorf("expected gcloud direct handler to be called for ping, got status %d", w.Code)
	}
}

// TestGCloudDirectNotRoutedWithoutFlavor tests that direct requests without
// Metadata-Flavor header are not routed to gcloud.
func TestGCloudDirectNotRoutedWithoutFlavor(t *testing.T) {
	var called bool
	mock := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	p := NewProxy()
	p.SetGCloudDirectResolver(func() http.Handler { return mock })

	req := httptest.NewRequest("GET", "/computeMetadata/v1/project/project-id", nil)
	// No Metadata-Flavor header
	w := httptest.NewRecorder()
	p.ServeHTTP(w, req)

	if called {
		t.Error("gcloud handler should not be called without Metadata-Flavor header")
	}
}
