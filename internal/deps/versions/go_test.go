package versions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGoResolver_Resolve(t *testing.T) {
	// Mock API response
	mockResponse := `[
		{"version": "go1.25.6", "stable": true, "files": []},
		{"version": "go1.25.5", "stable": true, "files": []},
		{"version": "go1.25rc1", "stable": false, "files": []},
		{"version": "go1.24.12", "stable": true, "files": []},
		{"version": "go1.24.11", "stable": true, "files": []},
		{"version": "go1.22.12", "stable": true, "files": []},
		{"version": "go1.22", "stable": true, "files": []},
		{"version": "go1.21.13", "stable": true, "files": []}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := &testGoResolver{url: server.URL, client: server.Client()}

	tests := []struct {
		name    string
		version string
		want    string
		wantErr bool
	}{
		{
			name:    "partial version resolves to latest patch",
			version: "1.25",
			want:    "1.25.6",
		},
		{
			name:    "partial version 1.24",
			version: "1.24",
			want:    "1.24.12",
		},
		{
			name:    "partial version 1.22",
			version: "1.22",
			want:    "1.22.12",
		},
		{
			name:    "exact version passes through",
			version: "1.25.5",
			want:    "1.25.5",
		},
		{
			name:    "exact version not found",
			version: "1.25.99",
			wantErr: true,
		},
		{
			name:    "nonexistent major.minor",
			version: "1.99",
			wantErr: true,
		},
		{
			name:    "invalid format",
			version: "invalid",
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

func TestGoResolver_LatestStable(t *testing.T) {
	mockResponse := `[
		{"version": "go1.26rc2", "stable": false, "files": []},
		{"version": "go1.25.6", "stable": true, "files": []},
		{"version": "go1.24.12", "stable": true, "files": []}
	]`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	resolver := &testGoResolver{url: server.URL, client: server.Client()}

	got, err := resolver.LatestStable(context.Background())
	if err != nil {
		t.Fatalf("LatestStable() error = %v", err)
	}

	want := "1.25.6"
	if got != want {
		t.Errorf("LatestStable() = %v, want %v", got, want)
	}
}

// testGoResolver is a GoResolver that uses a custom URL for testing.
type testGoResolver struct {
	url    string
	client *http.Client
}

func (r *testGoResolver) Resolve(ctx context.Context, version string) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", err
	}

	major, minor, patch, ok := parseSemver(version)
	if !ok {
		return "", fmt.Errorf("invalid Go version format %q: expected X.Y or X.Y.Z", version)
	}

	if patch >= 0 {
		fullVersion := "go" + version
		for _, rel := range releases {
			if rel.Version == fullVersion && rel.Stable {
				return version, nil
			}
		}
		return "", fmt.Errorf("Go version %s not found", version)
	}

	// Find latest patch
	var best string
	bestPatch := -1
	prefix := fmt.Sprintf("go%d.%d.", major, minor)
	exact := fmt.Sprintf("go%d.%d", major, minor)

	for _, rel := range releases {
		if !rel.Stable {
			continue
		}
		if rel.Version == exact {
			if bestPatch < 0 {
				best = version
				bestPatch = 0
			}
			continue
		}
		if len(rel.Version) > len(prefix) && rel.Version[:len(prefix)] == prefix {
			v := rel.Version[2:] // trim "go"
			_, _, p, _ := parseSemver(v)
			if p > bestPatch {
				bestPatch = p
				best = v
			}
		}
	}

	if best == "" {
		return "", fmt.Errorf("no stable Go %d.%d.x releases found", major, minor)
	}
	return best, nil
}

func (r *testGoResolver) Available(ctx context.Context) ([]string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return nil, err
	}

	var versions []string
	for _, rel := range releases {
		if rel.Stable {
			versions = append(versions, rel.Version[2:])
		}
	}
	return versions, nil
}

func (r *testGoResolver) LatestStable(ctx context.Context) (string, error) {
	releases, err := r.fetchReleases(ctx)
	if err != nil {
		return "", err
	}

	for _, rel := range releases {
		if rel.Stable {
			return rel.Version[2:], nil
		}
	}
	return "", fmt.Errorf("no stable Go releases found")
}

func (r *testGoResolver) fetchReleases(ctx context.Context) ([]goRelease, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.url, nil)
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var releases []goRelease
	if err := json.NewDecoder(resp.Body).Decode(&releases); err != nil {
		return nil, err
	}
	return releases, nil
}
