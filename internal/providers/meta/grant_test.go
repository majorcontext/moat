package meta

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestValidateMetaToken(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		body     any
		wantName string
		wantErr  bool
	}{
		{
			name:     "valid token",
			status:   http.StatusOK,
			body:     map[string]string{"id": "123", "name": "Test User"},
			wantName: "Test User",
		},
		{
			name:     "valid token name fallback to id",
			status:   http.StatusOK,
			body:     map[string]string{"id": "456", "name": ""},
			wantName: "456",
		},
		{
			name:    "invalid token",
			status:  http.StatusUnauthorized,
			body:    map[string]any{"error": map[string]string{"message": "bad token"}},
			wantErr: true,
		},
		{
			name:    "expired token",
			status:  http.StatusBadRequest,
			body:    map[string]any{"error": map[string]string{"message": "Session has expired"}},
			wantErr: true,
		},
		{
			name:    "error without message",
			status:  http.StatusInternalServerError,
			body:    "not json",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v21.0/me" {
					t.Errorf("unexpected path: %s", r.URL.Path)
				}
				if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
					t.Errorf("Authorization = %q, want Bearer test-token", got)
				}
				w.WriteHeader(tt.status)
				if s, ok := tt.body.(string); ok {
					w.Write([]byte(s))
				} else {
					json.NewEncoder(w).Encode(tt.body)
				}
			}))
			defer srv.Close()

			name, err := validateMetaToken(context.Background(), "test-token", srv.URL)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
		})
	}
}
