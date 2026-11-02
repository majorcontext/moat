package githubscope

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultAPIURL is GitHub's REST API base URL.
const DefaultAPIURL = "https://api.github.com"

// RepoMeta is GitHub's identity for a repository.
type RepoMeta struct {
	// FullName is the canonical owner/repo, lowercased. It differs from the
	// name in the remote URL when the repository was renamed or transferred.
	FullName string
	ID       int64
	// NodeIDs holds the repository's GraphQL node IDs in both the legacy
	// format (MDEw...) and the next-generation format (R_...). GitHub accepts
	// either as input, and clients send whichever form they last received.
	NodeIDs []string
}

// LookupRepos fetches RepoMeta for each repository with token, in parallel.
// Failures are returned per repository; callers fall back to cached metadata.
func LookupRepos(ctx context.Context, client *http.Client, apiURL, token string, repos []string) (map[string]RepoMeta, map[string]error) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if apiURL == "" {
		apiURL = DefaultAPIURL
	}
	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		meta = map[string]RepoMeta{}
		errs = map[string]error{}
		sem  = make(chan struct{}, 4)
	)
	for _, repo := range repos {
		wg.Add(1)
		go func(repo string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			m, err := lookupRepo(ctx, client, apiURL, token, repo)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs[repo] = err
				return
			}
			meta[repo] = m
		}(repo)
	}
	wg.Wait()
	return meta, errs
}

func lookupRepo(ctx context.Context, client *http.Client, apiURL, token, repo string) (RepoMeta, error) {
	var m RepoMeta
	for _, nextGen := range []string{"1", "0"} {
		r, err := getRepo(ctx, client, apiURL, token, repo, nextGen)
		if err != nil {
			return RepoMeta{}, err
		}
		m.FullName = strings.ToLower(r.FullName)
		m.ID = r.ID
		if r.NodeID != "" && !slices.Contains(m.NodeIDs, r.NodeID) {
			m.NodeIDs = append(m.NodeIDs, r.NodeID)
		}
	}
	return m, nil
}

type repoResponse struct {
	FullName string `json:"full_name"`
	ID       int64  `json:"id"`
	NodeID   string `json:"node_id"`
}

func getRepo(ctx context.Context, client *http.Client, apiURL, token, repo, nextGen string) (repoResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(apiURL, "/")+"/repos/"+repo, nil)
	if err != nil {
		return repoResponse{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("X-Github-Next-Global-ID", nextGen)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return repoResponse{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return repoResponse{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return repoResponse{}, fmt.Errorf("GET /repos/%s: %s", repo, resp.Status)
	}
	var r repoResponse
	if err := json.Unmarshal(body, &r); err != nil {
		return repoResponse{}, fmt.Errorf("GET /repos/%s: decoding response: %w", repo, err)
	}
	if r.ID == 0 || r.FullName == "" {
		return repoResponse{}, fmt.Errorf("GET /repos/%s: response has no id", repo)
	}
	return r, nil
}
