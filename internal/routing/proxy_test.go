package routing

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReverseProxy(t *testing.T) {
	// Create a backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello from backend"))
	}))
	defer backend.Close()

	// Create route table with backend
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	// Create reverse proxy
	rp := NewReverseProxy(routes)

	// Test routing via Host header
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}

	body, _ := io.ReadAll(rec.Body)
	if string(body) != "hello from backend" {
		t.Errorf("Body = %q, want 'hello from backend'", body)
	}
}

func TestReverseProxyUnknownAgent(t *testing.T) {
	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	rp := NewReverseProxy(routes)

	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "web.unknown.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("Status = %d, want 404", rec.Code)
	}
}

func TestReverseProxyDefaultService(t *testing.T) {
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("default"))
	}))
	defer backend.Close()

	dir := t.TempDir()
	routes, _ := NewRouteTable(dir)
	routes.Add("myapp", map[string]string{
		"web": backend.Listener.Addr().String(),
	})

	rp := NewReverseProxy(routes)

	// Request without service prefix
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "myapp.localhost:8080"
	rec := httptest.NewRecorder()

	rp.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Status = %d, want 200", rec.Code)
	}
}
