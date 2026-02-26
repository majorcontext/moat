package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
)

// Client communicates with the daemon over a Unix socket.
type Client struct {
	sockPath   string
	httpClient *http.Client
}

// NewClient creates a daemon client connected to the given socket path.
func NewClient(sockPath string) *Client {
	return &Client{
		sockPath: sockPath,
		httpClient: &http.Client{
			Transport: &http.Transport{
				DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", sockPath)
				},
			},
		},
	}
}

// Health returns the daemon's health status.
func (c *Client) Health(ctx context.Context) (*HealthResponse, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/health", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()
	var health HealthResponse
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, err
	}
	return &health, nil
}

// RegisterRun registers a new run with the daemon.
func (c *Client) RegisterRun(ctx context.Context, regReq RegisterRequest) (*RegisterResponse, error) {
	body, err := json.Marshal(regReq)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon/v1/runs", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	var regResp RegisterResponse
	if err := json.NewDecoder(resp.Body).Decode(&regResp); err != nil {
		return nil, err
	}
	return &regResp, nil
}

// UpdateRun updates a run's container ID (phase 2 of registration).
func (c *Client) UpdateRun(ctx context.Context, token, containerID string) error {
	body, err := json.Marshal(UpdateRunRequest{ContainerID: containerID})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPatch, "http://daemon/v1/runs/"+token, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("run not found")
	}
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}

// UnregisterRun removes a run from the daemon.
func (c *Client) UnregisterRun(ctx context.Context, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, "http://daemon/v1/runs/"+token, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("daemon returned %d", resp.StatusCode)
	}
	return nil
}

// ListRuns returns all registered runs.
func (c *Client) ListRuns(ctx context.Context) ([]RunInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://daemon/v1/runs", nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connecting to daemon: %w", err)
	}
	defer resp.Body.Close()
	var runs []RunInfo
	if err := json.NewDecoder(resp.Body).Decode(&runs); err != nil {
		return nil, err
	}
	return runs, nil
}

// Shutdown requests the daemon to shut down gracefully.
func (c *Client) Shutdown(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://daemon/v1/shutdown", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil // Connection refused/reset is expected after shutdown
	}
	defer resp.Body.Close()
	return nil
}
