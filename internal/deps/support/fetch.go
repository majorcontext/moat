package support

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	NodeScheduleURL = "https://raw.githubusercontent.com/nodejs/Release/main/schedule.json"
	NodeReleasesURL = "https://nodejs.org/dist/index.json"
)

// Fetcher allows bounded upstream requests and fixture HTTP clients in tests.
type Fetcher struct {
	Client *http.Client
}

func (f Fetcher) json(ctx context.Context, address string, out any) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: HTTP %d", address, response.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(out); err != nil {
		return fmt.Errorf("%s: %w", address, err)
	}
	return nil
}

func (f Fetcher) Fetch(ctx context.Context, now time.Time) (Catalog, error) {
	result := Catalog{Dependencies: map[string]Product{}}
	var schedule map[string]struct {
		Start string `json:"start"`
		End   string `json:"end"`
	}
	if err := f.json(ctx, NodeScheduleURL, &schedule); err != nil {
		return result, err
	}
	var releases []struct {
		Version string `json:"version"`
	}
	if err := f.json(ctx, NodeReleasesURL, &releases); err != nil {
		return result, err
	}
	published := map[string]bool{}
	for _, release := range releases {
		v := strings.TrimPrefix(release.Version, "v")
		if strings.ContainsAny(v, "-+") {
			continue
		}
		ps := strings.Split(v, ".")
		if len(ps) >= 3 {
			cycle := ps[0]
			if ps[0] == "0" {
				cycle += "." + ps[1]
			}
			published[cycle] = true
		}
	}
	node := Product{Source: NodeScheduleURL}
	for cycle, dates := range schedule {
		cycle = strings.TrimPrefix(cycle, "v")
		if published[cycle] {
			node.Lines = append(node.Lines, Line{Cycle: cycle, Released: dates.Start, End: dates.End})
		}
	}
	result.Dependencies["node"] = node
	for _, source := range []struct{ name, product string }{{"python", "python"}, {"go", "go"}, {"postgres", "postgresql"}, {"mysql", "mysql"}, {"redis", "redis"}} {
		address := "https://endoflife.date/api/" + source.product + ".json"
		var cycles []struct {
			Cycle       string          `json:"cycle"`
			ReleaseDate string          `json:"releaseDate"`
			EOL         json.RawMessage `json:"eol"`
		}
		if err := f.json(ctx, address, &cycles); err != nil {
			return result, err
		}
		product := Product{Source: address}
		for _, cycle := range cycles {
			line := Line{Cycle: cycle.Cycle, Released: cycle.ReleaseDate}
			switch string(cycle.EOL) {
			case "true":
				line.Ended = true
			case "false":
			default:
				if err := json.Unmarshal(cycle.EOL, &line.End); err != nil || line.End == "" {
					return result, fmt.Errorf("%s: invalid EOL for %s", address, cycle.Cycle)
				}
			}
			product.Lines = append(product.Lines, line)
		}
		result.Dependencies[source.name] = product
	}
	// Ollama publishes no lifecycle policy. Keep that fact, rather than a
	// rolling patch list that churns daily without affecting validation.
	result.Dependencies["ollama"] = Product{Source: "https://github.com/ollama/ollama/releases", PublishedOnly: true}
	if err := result.Validate(); err != nil {
		return result, err
	}
	for name, product := range result.Dependencies {
		if !product.PublishedOnly && len(product.Versions(now)) == 0 {
			return result, fmt.Errorf("%s: upstream returned no maintained release lines", name)
		}
		sort.Slice(product.Lines, func(i, j int) bool { return Compare(product.Lines[i].Cycle, product.Lines[j].Cycle) < 0 })
		result.Dependencies[name] = product
	}
	return result, nil
}
