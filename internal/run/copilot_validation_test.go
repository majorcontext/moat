package run

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/config"
	"github.com/majorcontext/moat/internal/credential"
)

func TestValidateCopilotGitHubGrant(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     "github-token",
		CreatedAt: time.Now(),
	})

	origValidate := validateCopilotGitHubToken
	t.Cleanup(func() { validateCopilotGitHubToken = origValidate })

	var gotToken string
	validateCopilotGitHubToken = func(ctx context.Context, token string) error {
		gotToken = token
		return nil
	}

	err := validateCopilotGitHubGrant(context.Background(), &config.Config{Agent: "copilot"}, []string{"github"}, store)
	if err != nil {
		t.Fatalf("validateCopilotGitHubGrant() error = %v", err)
	}
	if gotToken != "github-token" {
		t.Fatalf("validated token = %q, want github-token", gotToken)
	}
}

func TestValidateCopilotGitHubGrantFailure(t *testing.T) {
	store := newMockStore()
	store.Save(credential.Credential{
		Provider:  credential.ProviderGitHub,
		Token:     "github-token",
		CreatedAt: time.Now(),
	})

	origValidate := validateCopilotGitHubToken
	t.Cleanup(func() { validateCopilotGitHubToken = origValidate })
	validateCopilotGitHubToken = func(ctx context.Context, token string) error {
		return errors.New("missing Copilot Requests")
	}

	err := validateCopilotGitHubGrant(context.Background(), &config.Config{Agent: "copilot"}, []string{"github"}, store)
	if err == nil {
		t.Fatal("validateCopilotGitHubGrant() = nil, want error")
	}
	if !strings.Contains(err.Error(), "moat grant github") || !strings.Contains(err.Error(), "missing Copilot Requests") {
		t.Fatalf("validateCopilotGitHubGrant() error = %v, want grant guidance and cause", err)
	}
}

func TestValidateCopilotGitHubGrantSkippedForNonCopilotRun(t *testing.T) {
	origValidate := validateCopilotGitHubToken
	t.Cleanup(func() { validateCopilotGitHubToken = origValidate })
	validateCopilotGitHubToken = func(ctx context.Context, token string) error {
		t.Fatal("validateCopilotGitHubToken should not be called")
		return nil
	}

	err := validateCopilotGitHubGrant(context.Background(), &config.Config{Agent: "bash"}, []string{"github"}, newMockStore())
	if err != nil {
		t.Fatalf("validateCopilotGitHubGrant() error = %v", err)
	}
}

func TestValidateCopilotGitHubGrantRequiresGitHubGrant(t *testing.T) {
	origValidate := validateCopilotGitHubToken
	t.Cleanup(func() { validateCopilotGitHubToken = origValidate })
	validateCopilotGitHubToken = func(ctx context.Context, token string) error {
		t.Fatal("validateCopilotGitHubToken should not be called without github grant")
		return nil
	}

	err := validateCopilotGitHubGrant(context.Background(), &config.Config{Agent: "copilot"}, nil, newMockStore())
	if err == nil {
		t.Fatal("validateCopilotGitHubGrant() = nil, want missing github grant error")
	}
	if !strings.Contains(err.Error(), "requires the github grant") || !strings.Contains(err.Error(), "moat grant github") {
		t.Fatalf("validateCopilotGitHubGrant() error = %v, want github grant guidance", err)
	}
}

func TestNormalizeCopilotGrantNames(t *testing.T) {
	got := normalizeCopilotGrantNames([]string{"copilot", "ssh:github.com", "github", "aws", "copilot"})
	want := []string{"github", "ssh:github.com", "aws"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("normalizeCopilotGrantNames() = %v, want %v", got, want)
	}
}
