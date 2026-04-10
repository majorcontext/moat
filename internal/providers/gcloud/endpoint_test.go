package gcloud

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

func newTestHandler(tok *oauth2.Token, err error) *EndpointHandler {
	return NewEndpointHandlerFromTokenFunc(
		func(ctx context.Context) (*oauth2.Token, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return tok, err
		},
		"test-project",
		[]string{"https://www.googleapis.com/auth/cloud-platform"},
		"sa@test.iam.gserviceaccount.com",
	)
}

func metadataRequest(method, path string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	req.Header.Set("Metadata-Flavor", "Google")
	return req
}

func TestMetadataRequiresFlavorHeader(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	req := httptest.NewRequest("GET", "/computeMetadata/v1/project/project-id", nil)
	// No Metadata-Flavor header.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestMetadataToken(t *testing.T) {
	exp := time.Now().Add(1 * time.Hour)
	h := newTestHandler(&oauth2.Token{AccessToken: "ya29.test-token", Expiry: exp}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/token"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var resp map[string]any
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["access_token"] != "ya29.test-token" {
		t.Errorf("access_token = %v", resp["access_token"])
	}
	if resp["token_type"] != "Bearer" {
		t.Errorf("token_type = %v", resp["token_type"])
	}
	expiresIn, ok := resp["expires_in"].(float64)
	if !ok || expiresIn <= 0 {
		t.Errorf("expires_in = %v, want positive number", resp["expires_in"])
	}

	if w.Header().Get("Metadata-Flavor") != "Google" {
		t.Error("response missing Metadata-Flavor header")
	}
}

func TestMetadataProjectID(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/project/project-id"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "test-project" {
		t.Errorf("body = %q, want %q", body, "test-project")
	}
}

func TestMetadataNumericProjectID(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/project/numeric-project-id"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "0" {
		t.Errorf("body = %q, want %q", body, "0")
	}
}

func TestMetadataScopes(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/scopes"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "https://www.googleapis.com/auth/cloud-platform" {
		t.Errorf("body = %q", body)
	}
}

func TestMetadataEmail(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/email"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "sa@test.iam.gserviceaccount.com" {
		t.Errorf("body = %q", body)
	}
}

func TestMetadataServiceAccountsDirListing(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)

	t.Run("default account listing", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		for _, expected := range []string{"aliases", "email", "identity", "scopes", "token"} {
			if !strings.Contains(body, expected) {
				t.Errorf("body missing %q: %q", expected, body)
			}
		}
	})

	t.Run("accounts listing", func(t *testing.T) {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/"))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "default/") {
			t.Errorf("body missing default/: %q", body)
		}
		if !strings.Contains(body, "sa@test.iam.gserviceaccount.com/") {
			t.Errorf("body missing email/: %q", body)
		}
	})
}

func TestMetadataLiveness(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)

	for _, path := range []string{"/", "/computeMetadata/", "/computeMetadata/v1/"} {
		t.Run(path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, metadataRequest("GET", path))
			if w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200 for %s", w.Code, path)
			}
			if w.Header().Get("Metadata-Flavor") != "Google" {
				t.Error("response missing Metadata-Flavor header")
			}
		})
	}
}

func TestMetadataIdentityNotImplemented(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/identity?audience=https://example.com"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestMetadataTokenContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	h := newTestHandler(nil, fmt.Errorf("context canceled"))
	req := httptest.NewRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/token", nil)
	req = req.WithContext(ctx)
	req.Header.Set("Metadata-Flavor", "Google")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", w.Code)
	}
}

func TestMetadataAliases(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/aliases"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if body := w.Body.String(); body != "default" {
		t.Errorf("body = %q, want %q", body, "default")
	}
}

func TestMetadataUnknownPath(t *testing.T) {
	h := newTestHandler(&oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/unknown/path"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestMetadataDefaultEmail(t *testing.T) {
	h := NewEndpointHandlerFromTokenFunc(
		func(ctx context.Context) (*oauth2.Token, error) {
			return &oauth2.Token{AccessToken: "tok", Expiry: time.Now().Add(time.Hour)}, nil
		},
		"proj",
		[]string{DefaultScope},
		"", // empty email should default
	)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, metadataRequest("GET", "/computeMetadata/v1/instance/service-accounts/default/email"))
	if body := w.Body.String(); body != "default@moat.local" {
		t.Errorf("body = %q, want default@moat.local", body)
	}
}
