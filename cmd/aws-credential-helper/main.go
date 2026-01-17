// aws-credential-helper fetches AWS credentials from the AgentOps proxy.
// It implements the AWS credential_process interface for dynamic credential refresh.
// See: https://docs.aws.amazon.com/cli/latest/userguide/cli-configure-sourcing-external.html
package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "aws-credential-helper: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	url := os.Getenv("AGENTOPS_CREDENTIAL_URL")
	if url == "" {
		return fmt.Errorf("AGENTOPS_CREDENTIAL_URL not set")
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	if token := os.Getenv("AGENTOPS_CREDENTIAL_TOKEN"); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("fetching credentials: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("credential endpoint returned %d: %s", resp.StatusCode, string(body))
	}

	_, err = io.Copy(os.Stdout, resp.Body)
	return err
}
