package credential

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAnthropicAuth_HTTPClient(t *testing.T) {
	t.Run("uses default client when nil", func(t *testing.T) {
		auth := &AnthropicAuth{}
		if auth.httpClient() != http.DefaultClient {
			t.Error("expected http.DefaultClient when HTTPClient is nil")
		}
	})

	t.Run("uses custom client when set", func(t *testing.T) {
		customClient := &http.Client{Timeout: 30 * time.Second}
		auth := &AnthropicAuth{HTTPClient: customClient}
		if auth.httpClient() != customClient {
			t.Error("expected custom client when HTTPClient is set")
		}
	})
}

func TestAnthropicAuth_APIURL(t *testing.T) {
	t.Run("uses default URL when not set", func(t *testing.T) {
		auth := &AnthropicAuth{}
		if auth.apiURL() != anthropicAPIURL {
			t.Errorf("expected default URL %s, got %s", anthropicAPIURL, auth.apiURL())
		}
	})

	t.Run("uses custom URL when set", func(t *testing.T) {
		auth := &AnthropicAuth{APIURL: "https://custom.example.com/v1/messages"}
		if auth.apiURL() != "https://custom.example.com/v1/messages" {
			t.Errorf("expected custom URL, got %s", auth.apiURL())
		}
	})
}

func TestAnthropicAuth_ValidateKey(t *testing.T) {
	t.Run("valid key", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if r.Header.Get("x-api-key") != "sk-ant-test123" {
				t.Errorf("expected x-api-key header, got %s", r.Header.Get("x-api-key"))
			}
			if r.Header.Get("anthropic-version") != "2023-06-01" {
				t.Errorf("expected anthropic-version header, got %s", r.Header.Get("anthropic-version"))
			}
			if r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("expected Content-Type: application/json, got %s", r.Header.Get("Content-Type"))
			}

			// Return success response
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"content": []map[string]string{{"type": "text", "text": "hi"}},
				"model":   "claude-sonnet-4-20250514",
			})
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}
		err := auth.ValidateKey(context.Background(), "sk-ant-test123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("invalid key - unauthorized", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"type":    "authentication_error",
					"message": "Invalid API key",
				},
			})
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}
		err := auth.ValidateKey(context.Background(), "invalid-key")
		if err == nil {
			t.Fatal("expected error for invalid key")
		}
		if !strings.Contains(err.Error(), "invalid API key") {
			t.Errorf("expected 'invalid API key' in error, got: %v", err)
		}
	})

	t.Run("forbidden - insufficient permissions", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"type":    "permission_error",
					"message": "API key lacks required permissions",
				},
			})
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}
		err := auth.ValidateKey(context.Background(), "restricted-key")
		if err == nil {
			t.Fatal("expected error for forbidden")
		}
		if !strings.Contains(err.Error(), "lacks required permissions") {
			t.Errorf("expected 'lacks required permissions' in error, got: %v", err)
		}
	})

	t.Run("insufficient credits", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"type":    "invalid_request_error",
					"message": "Your credit balance is too low",
				},
			})
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}
		err := auth.ValidateKey(context.Background(), "no-credits-key")
		if err == nil {
			t.Fatal("expected error for insufficient credits")
		}
		if !strings.Contains(err.Error(), "insufficient credits") {
			t.Errorf("expected 'insufficient credits' in error, got: %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		err := auth.ValidateKey(ctx, "test-key")
		if err == nil {
			t.Fatal("expected error for canceled context")
		}
	})

	t.Run("server error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"error": map[string]string{
					"type":    "api_error",
					"message": "Internal server error",
				},
			})
		}))
		defer server.Close()

		auth := &AnthropicAuth{APIURL: server.URL}
		err := auth.ValidateKey(context.Background(), "test-key")
		if err == nil {
			t.Fatal("expected error for server error")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("expected status code in error, got: %v", err)
		}
	})
}

func TestAnthropicAuth_CreateCredential(t *testing.T) {
	auth := &AnthropicAuth{}
	cred := auth.CreateCredential("sk-ant-test123")

	if cred.Provider != ProviderAnthropic {
		t.Errorf("Provider = %q, want %q", cred.Provider, ProviderAnthropic)
	}
	if cred.Token != "sk-ant-test123" {
		t.Errorf("Token = %q, want %q", cred.Token, "sk-ant-test123")
	}
	if cred.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
}
