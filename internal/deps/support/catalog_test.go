package support

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

func fixtureCatalog() Catalog {
	return Catalog{Dependencies: map[string]Product{
		"node": {Source: NodeScheduleURL, Lines: []Line{
			{Cycle: "18", Released: "2022-04-19", End: "2025-04-30"},
			{Cycle: "20", Released: "2023-04-18", End: "2026-04-30"},
			{Cycle: "22", Released: "2024-04-24", End: "2027-04-30"},
			{Cycle: "24", Released: "2025-05-06", End: "2028-04-30"},
			{Cycle: "26", Released: "2026-05-05", End: "2029-04-30"},
		}},
		"mysql":  {Source: "fixture", Lines: []Line{{Cycle: "8.0", Released: "2018-04-19", End: "2026-04-30"}, {Cycle: "8.4", Released: "2024-04-30", End: "2032-04-30"}}},
		"ollama": {Source: "fixture", PublishedOnly: true, Lines: []Line{{Cycle: "0.40.15", Released: "2026-11-03"}}},
	}}
}

func TestSupportLinesAllowPatchesAndRejectEOL(t *testing.T) {
	catalog := fixtureCatalog()
	for _, tc := range []struct {
		name, version string
		bad           bool
	}{
		{"node", "18.20.9", true},
		{"node", "20.19.5", true},
		{"node", "19", true},
		{"node", "22.99.123", false},
		{"node", "24", false},
		{"node", "26.13.0", false},
		{"node", "999.1.2", false},
		{"node", "latest", false},
		{"mysql", "8", false},
		{"mysql", "8.4.999", false},
		{"mysql", "8.0.44", true},
		{"ollama", "0.18.1", false},
		{"unknown-tool", "1.0.0", false},
	} {
		t.Run(tc.name+"@"+tc.version, func(t *testing.T) {
			err := catalog.Check(tc.name, tc.version, testNow)
			if (err != nil) != tc.bad {
				t.Fatalf("Check = %v, want rejection %v", err, tc.bad)
			}
			if err != nil && !strings.Contains(err.Error(), "upstream") {
				t.Errorf("missing actionable support reason: %v", err)
			}
		})
	}
}

func TestEOLBoundaryIsEvaluatedWithoutCatalogRefresh(t *testing.T) {
	catalog := fixtureCatalog()
	for _, tc := range []struct {
		at  string
		bad bool
	}{{"2027-04-30", false}, {"2027-05-01", true}} {
		now, _ := time.Parse(time.DateOnly, tc.at)
		err := catalog.Check("node", "22.99.0", now)
		if (err != nil) != tc.bad {
			t.Errorf("at %s: %v", tc.at, err)
		}
	}
}

func TestCatalogRejectsMalformedMetadata(t *testing.T) {
	for _, line := range []Line{
		{Cycle: "22;bad", Released: "2024-01-01"},
		{Cycle: "22", Released: "bad"},
		{Cycle: "22", Released: "2024-01-01", End: "2023-01-01"},
	} {
		catalog := Catalog{Dependencies: map[string]Product{"node": {Source: "fixture", Lines: []Line{line}}}}
		if err := catalog.Validate(); err == nil {
			t.Errorf("accepted bad metadata: %+v", line)
		}
	}
}

func TestRegistryRefreshIsMinimalAndIdempotent(t *testing.T) {
	catalog := fixtureCatalog()
	delete(catalog.Dependencies, "mysql")
	delete(catalog.Dependencies, "ollama")
	input := []byte("# registry comment\nnode:\n  type: runtime\n  default: \"18\"\n  versions: [\"18\"]\n  # preserve installer metadata\n  other: unchanged\n")
	output, err := UpdateRegistry(input, catalog, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `default: "22"`) {
		t.Errorf("unsupported default not advanced:\n%s", output)
	}
	if !strings.Contains(string(output), `versions: ["22","24","26"]`) {
		t.Errorf("wrong maintained list:\n%s", output)
	}
	if !strings.Contains(string(output), "# preserve installer metadata\n  other: unchanged") {
		t.Error("rewrote unrelated metadata")
	}
	again, err := UpdateRegistry(output, catalog, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(output) {
		t.Error("refresh is not idempotent")
	}
	healthy := strings.ReplaceAll(string(input), `default: "18"`, `default: "22"`)
	output, err = UpdateRegistry([]byte(healthy), catalog, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(output), `default: "22"`) {
		t.Error("maintained default changed")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestFetcherBuildsCatalogWithoutCredentials(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "" {
			t.Fatal("GitHub credential sent to another source")
		}
		body := ""
		switch r.URL.String() {
		case NodeScheduleURL:
			body = `{"v20":{"start":"2023-04-18","end":"2026-04-30"},"v22":{"start":"2024-04-24","end":"2027-04-30"},"v28":{"start":"2028-04-01","end":"2030-04-01"}}`
		case NodeReleasesURL:
			body = `[{"version":"v20.19.0"},{"version":"v22.14.0"},{"version":"v28.0.0-alpha.1"}]`
		default:
			body = `[{"cycle":"1.0","releaseDate":"2024-01-01","eol":false,"latest":"1.0.1"}]`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	catalog, err := (Fetcher{Client: client}).Fetch(context.Background(), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Dependencies) != 7 {
		t.Errorf("products = %d, want 7", len(catalog.Dependencies))
	}
	if got := strings.Join(catalog.Dependencies["node"].Versions(testNow), ","); got != "22" {
		t.Errorf("maintained Node = %q", got)
	}
	for _, line := range catalog.Dependencies["node"].Lines {
		if line.Cycle == "28" {
			t.Error("included unpublished prerelease line")
		}
	}
}

func TestFetcherFailsInsteadOfPublishingEmptyOrBadData(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{{503, `{}`}, {200, `not JSON`}, {200, `{}`}} {
		client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		if _, err := (Fetcher{Client: client}).Fetch(context.Background(), testNow); err == nil {
			t.Fatal("accepted failed source")
		}
	}
}

func ExampleCatalog_Check() {
	err := fixtureCatalog().Check("node", "20.19.0", testNow)
	fmt.Println(strings.Contains(err.Error(), "2026-04-30"))
	// Output: true
}

func TestDefaultsAdvanceConservativelyAfterEOL(t *testing.T) {
	catalog := fixtureCatalog()
	for _, tc := range []struct{ current, at, want string }{
		{"20", "2026-11-07", "22"},
		{"22", "2027-04-30", "22"},
		{"22", "2027-05-01", "24"},
	} {
		now, _ := time.Parse(time.DateOnly, tc.at)
		got, err := catalog.Default("node", tc.current, now)
		if err != nil || got != tc.want {
			t.Errorf("%+v: got %q, %v", tc, got, err)
		}
	}
}

func TestRegistryRefreshRejectsUnsupportedLayout(t *testing.T) {
	catalog := fixtureCatalog()
	delete(catalog.Dependencies, "mysql")
	delete(catalog.Dependencies, "ollama")
	for _, input := range []string{
		"node:\n  versions: [\"18\"]\n",
		"node:\n  default: \"18\"\n  versions:\n    - \"18\"\n",
	} {
		if _, err := UpdateRegistry([]byte(input), catalog, testNow); err == nil {
			t.Errorf("accepted unsafe layout: %s", input)
		}
	}
}
