package credential

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGitHubDeviceAuth_Scopes(t *testing.T) {
	auth := &GitHubDeviceAuth{
		ClientID: "test-client-id",
		Scopes:   []string{"repo", "read:user"},
	}

	if auth.ClientID != "test-client-id" {
		t.Errorf("ClientID = %q, want %q", auth.ClientID, "test-client-id")
	}
	if len(auth.Scopes) != 2 {
		t.Errorf("Scopes length = %d, want 2", len(auth.Scopes))
	}
}

func TestGitHubDeviceAuth_HTTPClient(t *testing.T) {
	t.Run("uses default client when nil", func(t *testing.T) {
		auth := &GitHubDeviceAuth{ClientID: "test"}
		if auth.httpClient() != http.DefaultClient {
			t.Error("expected http.DefaultClient when HTTPClient is nil")
		}
	})

	t.Run("uses custom client when set", func(t *testing.T) {
		customClient := &http.Client{Timeout: 30 * time.Second}
		auth := &GitHubDeviceAuth{
			ClientID:   "test",
			HTTPClient: customClient,
		}
		if auth.httpClient() != customClient {
			t.Error("expected custom client when HTTPClient is set")
		}
	})
}

func TestGitHubDeviceAuth_RequestDeviceCode(t *testing.T) {
	t.Run("successful request", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Verify request method
			if r.Method != "POST" {
				t.Errorf("expected POST, got %s", r.Method)
			}

			// Verify headers
			if r.Header.Get("Accept") != "application/json" {
				t.Errorf("expected Accept: application/json, got %s", r.Header.Get("Accept"))
			}
			if r.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
				t.Errorf("expected Content-Type: application/x-www-form-urlencoded, got %s", r.Header.Get("Content-Type"))
			}

			// Verify body
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))

			if values.Get("client_id") != "test-client-id" {
				t.Errorf("expected client_id=test-client-id, got %s", values.Get("client_id"))
			}
			if values.Get("scope") != "repo read:user" {
				t.Errorf("expected scope=repo read:user, got %s", values.Get("scope"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(DeviceCodeResponse{
				DeviceCode:      "device-code-123",
				UserCode:        "USER-CODE",
				VerificationURI: "https://github.com/login/device",
				ExpiresIn:       900,
				Interval:        5,
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "test-client-id",
			Scopes:        []string{"repo", "read:user"},
			DeviceCodeURL: server.URL,
		}

		resp, err := auth.RequestDeviceCode(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.DeviceCode != "device-code-123" {
			t.Errorf("DeviceCode = %q, want %q", resp.DeviceCode, "device-code-123")
		}
		if resp.UserCode != "USER-CODE" {
			t.Errorf("UserCode = %q, want %q", resp.UserCode, "USER-CODE")
		}
		if resp.ExpiresIn != 900 {
			t.Errorf("ExpiresIn = %d, want 900", resp.ExpiresIn)
		}
		if resp.Interval != 5 {
			t.Errorf("Interval = %d, want 5", resp.Interval)
		}
	})

	t.Run("default scope when none provided", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))

			if values.Get("scope") != "repo" {
				t.Errorf("expected default scope=repo, got %s", values.Get("scope"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(DeviceCodeResponse{
				DeviceCode: "device-code",
				UserCode:   "CODE",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "test-client-id",
			DeviceCodeURL: server.URL,
		}

		_, err := auth.RequestDeviceCode(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("handles HTTP error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte("invalid client_id"))
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "invalid-client",
			DeviceCodeURL: server.URL,
		}

		_, err := auth.RequestDeviceCode(context.Background())
		if err == nil {
			t.Fatal("expected error for bad request")
		}
		if !strings.Contains(err.Error(), "status 400") {
			t.Errorf("expected error to contain 'status 400', got: %v", err)
		}
		if !strings.Contains(err.Error(), "invalid client_id") {
			t.Errorf("expected error to contain response body, got: %v", err)
		}
	})

	t.Run("handles 500 error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte("internal error"))
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "test-client",
			DeviceCodeURL: server.URL,
		}

		_, err := auth.RequestDeviceCode(context.Background())
		if err == nil {
			t.Fatal("expected error for server error")
		}
		if !strings.Contains(err.Error(), "status 500") {
			t.Errorf("expected error to contain 'status 500', got: %v", err)
		}
	})

	t.Run("properly encodes special characters in scope", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))

			// The scope should be properly decoded
			expectedScope := "repo read:user admin:org"
			if values.Get("scope") != expectedScope {
				t.Errorf("expected scope=%q, got %q", expectedScope, values.Get("scope"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(DeviceCodeResponse{DeviceCode: "code"})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "test-client",
			Scopes:        []string{"repo", "read:user", "admin:org"},
			DeviceCodeURL: server.URL,
		}

		_, err := auth.RequestDeviceCode(context.Background())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("context cancellation", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(100 * time.Millisecond)
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(DeviceCodeResponse{})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID:      "test-client",
			DeviceCodeURL: server.URL,
		}

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		_, err := auth.RequestDeviceCode(ctx)
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}

func TestGitHubDeviceAuth_CheckToken(t *testing.T) {
	t.Run("successful token response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			values, _ := url.ParseQuery(string(body))

			if values.Get("client_id") != "test-client" {
				t.Errorf("expected client_id=test-client, got %s", values.Get("client_id"))
			}
			if values.Get("device_code") != "device-code-123" {
				t.Errorf("expected device_code=device-code-123, got %s", values.Get("device_code"))
			}
			if values.Get("grant_type") != "urn:ietf:params:oauth:grant-type:device_code" {
				t.Errorf("unexpected grant_type: %s", values.Get("grant_type"))
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "gho_abc123",
				TokenType:   "bearer",
				Scope:       "repo",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		resp, err := auth.checkToken(context.Background(), "device-code-123")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.AccessToken != "gho_abc123" {
			t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "gho_abc123")
		}
		if resp.TokenType != "bearer" {
			t.Errorf("TokenType = %q, want %q", resp.TokenType, "bearer")
		}
	})

	t.Run("authorization pending", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				Error: "authorization_pending",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		resp, err := auth.checkToken(context.Background(), "device-code")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Error != "authorization_pending" {
			t.Errorf("Error = %q, want %q", resp.Error, "authorization_pending")
		}
	})

	t.Run("handles HTTP error status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte("unauthorized"))
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		_, err := auth.checkToken(context.Background(), "device-code")
		if err == nil {
			t.Fatal("expected error for unauthorized response")
		}
		if !strings.Contains(err.Error(), "status 401") {
			t.Errorf("expected error to contain 'status 401', got: %v", err)
		}
	})
}

func TestGitHubDeviceAuth_PollForToken(t *testing.T) {
	t.Run("immediate success", func(t *testing.T) {
		callCount := 0
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				AccessToken: "gho_success",
				TokenType:   "bearer",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Use a very short interval for testing
		resp, err := auth.PollForToken(ctx, "device-code", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if resp.AccessToken != "gho_success" {
			t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "gho_success")
		}
	})

	t.Run("context timeout", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				Error: "authorization_pending",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		_, err := auth.PollForToken(ctx, "device-code", 1)
		if err == nil {
			t.Fatal("expected timeout error")
		}
		if err != context.DeadlineExceeded {
			t.Errorf("expected context.DeadlineExceeded, got: %v", err)
		}
	})

	t.Run("expired token error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(TokenResponse{
				Error: "expired_token",
			})
		}))
		defer server.Close()

		auth := &GitHubDeviceAuth{
			ClientID: "test-client",
			TokenURL: server.URL,
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := auth.PollForToken(ctx, "device-code", 1)
		if err == nil {
			t.Fatal("expected error for expired token")
		}
		if !strings.Contains(err.Error(), "expired_token") {
			t.Errorf("expected error to contain 'expired_token', got: %v", err)
		}
	})
}

func TestURLHelpers(t *testing.T) {
	t.Run("deviceCodeURL returns default when not set", func(t *testing.T) {
		auth := &GitHubDeviceAuth{ClientID: "test"}
		if auth.deviceCodeURL() != githubDeviceCodeURL {
			t.Errorf("expected default URL %s, got %s", githubDeviceCodeURL, auth.deviceCodeURL())
		}
	})

	t.Run("deviceCodeURL returns custom when set", func(t *testing.T) {
		auth := &GitHubDeviceAuth{
			ClientID:      "test",
			DeviceCodeURL: "https://custom.example.com/device",
		}
		if auth.deviceCodeURL() != "https://custom.example.com/device" {
			t.Errorf("expected custom URL, got %s", auth.deviceCodeURL())
		}
	})

	t.Run("tokenURL returns default when not set", func(t *testing.T) {
		auth := &GitHubDeviceAuth{ClientID: "test"}
		if auth.tokenURL() != githubTokenURL {
			t.Errorf("expected default URL %s, got %s", githubTokenURL, auth.tokenURL())
		}
	})

	t.Run("tokenURL returns custom when set", func(t *testing.T) {
		auth := &GitHubDeviceAuth{
			ClientID: "test",
			TokenURL: "https://custom.example.com/token",
		}
		if auth.tokenURL() != "https://custom.example.com/token" {
			t.Errorf("expected custom URL, got %s", auth.tokenURL())
		}
	})
}
