package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestNodeResolver_Resolve(t *testing.T) {
	// Mock API response
	mockResponse := `[
		{"version": "v22.13.0", "lts": false, "date": "2025-01-21"},
		{"version": "v22.12.0", "lts": false, "date": "2024-12-17"},
		{"version": "v20.18.2", "lts": "Iron", "date": "2025-01-14"},
		{"version": "v20.18.1", "lts": "Iron", "date": "2024-11-21"},
		{"version": "v20.17.0", "lts": "Iron", "date": "2024-08-21"},
		{"version": "v18.20.5", "lts": "Hydrogen", "date": "2024-11-12"},
		{"version": "v18.20.4", "lts": "Hydrogen", "date": "2024-07-08"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := &testNodeResolver{url: server.URL, client: server.Client()}

	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "major only resolves to latest",
			version: "20",
			want:    "20.18.2",
		},
		{
			name:    "major only 22",
			version: "22",
			want:    "22.13.0",
		},
		{
			name:    "major only 18",
			version: "18",
			want:    "18.20.5",
		},
		{
			name:    "major.minor resolves to latest patch",
			version: "20.18",
			want:    "20.18.2",
		},
		{
			name:    "exact version passes through",
			version: "20.18.1",
			want:    "20.18.1",
		},
		{
			name:    "exact version not found",
			version: "20.18.99",
			wantErr: true,
		},
		{
			name:    "nonexistent major",
			version: "99",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.Resolve(context.Background(), tt.version)
			if (err != nil) != tt.wantErr {
				t.Errorf("Resolve() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("Resolve() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNodeResolver_LatestStable(t *testing.T) {
	mockResponse := `[
		{"version": "v23.5.0", "lts": false, "date": "2025-01-14"},
		{"version": "v22.13.0", "lts": "Jod", "date": "2025-01-21"},
		{"version": "v20.18.2", "lts": "Iron", "date": "2025-01-14"}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := &testNodeResolver{url: server.URL, client: server.Client()}

	got, err := resolver.LatestStable(context.Background())
	if err != nil {
		t.Fatalf("LatestStable() error = %v", err)
	}

	// Should return first LTS version
	want := "22.13.0"
	if got != want {
		t.Errorf("LatestStable() = %v, want %v", got, want)
	}
}

// testNodeResolver is a NodeResolver that uses a custom URL for testing.
type testNodeResolver struct {
	url    string
	client *http.Client
}

func (r *testNodeResolver) Resolve(ctx context.Context, version string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.url, nil)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var releases []nodeRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", err
	}

	return resolveNodeVersion(version, releases)
}

func (r *testNodeResolver) Available(ctx context.Context) ([]string, error) {
	return nil, nil
}

func (r *testNodeResolver) LatestStable(ctx context.Context) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.url, nil)
	resp, err := r.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var releases []nodeRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return "", err
	}

	// Find first LTS
	for _, rel := range releases {
		if rel.LTS != false && rel.LTS != nil {
			return rel.Version[1:], nil // trim 'v'
		}
	}

	if len(releases) > 0 {
		return releases[0].Version[1:], nil
	}
	return "", nil
}

func resolveNodeVersion(version string, releases []nodeRelease) (string, error) {
	parts := strings.Split(version, ".")
	if len(parts) < 1 || len(parts) > 3 {
		return "", fmt.Errorf("invalid Node.js version format %q", version)
	}

	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", fmt.Errorf("invalid Node.js major version %q", parts[0])
	}

	var minor, patch int = -1, -1
	if len(parts) >= 2 {
		minor, _ = strconv.Atoi(parts[1])
	}
	if len(parts) == 3 {
		patch, _ = strconv.Atoi(parts[2])
	}

	if patch >= 0 {
		fullVersion := "v" + version
		for _, rel := range releases {
			if rel.Version == fullVersion {
				return version, nil
			}
		}
		return "", fmt.Errorf("Node.js version %s not found", version)
	}

	var candidates []string
	for _, rel := range releases {
		v := strings.TrimPrefix(rel.Version, "v")
		relParts := strings.Split(v, ".")
		if len(relParts) < 3 {
			continue
		}

		relMajor, _ := strconv.Atoi(relParts[0])
		relMinor, _ := strconv.Atoi(relParts[1])

		if relMajor != major {
			continue
		}
		if minor >= 0 && relMinor != minor {
			continue
		}

		candidates = append(candidates, v)
	}

	if len(candidates) == 0 {
		return "", fmt.Errorf("no Node.js %d.x releases found", major)
	}

	// First one is latest (API returns newest first)
	return candidates[0], nil
}
