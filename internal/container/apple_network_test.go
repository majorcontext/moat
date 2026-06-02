package container

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestAppleNetworkManagerImplementsInterface(t *testing.T) {
	var _ NetworkManager = (*appleNetworkManager)(nil)
}

func TestIsRetryableAppleNetworkDeleteError(t *testing.T) {
	// Real strings observed in ~/.moat/debug logs when teardown races Apple's
	// asynchronous container detach.
	retryable := []string{
		`cannot delete subnet moat-run_d2524c978e7d because the IP allocator cannot be disabled with active containers`,
		`network moat-run_d2524c978e7d has a pending operation`,
		`network moat-run_x is in use`,
	}
	for _, s := range retryable {
		if !isRetryableAppleNetworkDeleteError(s) {
			t.Errorf("expected retryable, got false for: %q", s)
		}
	}

	nonRetryable := []string{
		`permission denied`,
		`some unrelated fatal error`,
		``,
	}
	for _, s := range nonRetryable {
		if isRetryableAppleNetworkDeleteError(s) {
			t.Errorf("expected non-retryable, got true for: %q", s)
		}
	}
}

func TestRemoveNetworkRetriesUntilDetached(t *testing.T) {
	calls := 0
	m := &appleNetworkManager{
		retryBase: time.Millisecond,
		deleteFn: func(_ context.Context, _ string) (string, error) {
			calls++
			if calls < 3 {
				return `cannot delete subnet x because the IP allocator cannot be disabled with active containers`, errors.New("exit status 1")
			}
			return "", nil
		},
	}
	if err := m.RemoveNetwork(context.Background(), "moat-run_x"); err != nil {
		t.Fatalf("expected success after async detach, got %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 attempts (2 retries), got %d", calls)
	}
}

func TestRemoveNetworkNonRetryableFailsFast(t *testing.T) {
	calls := 0
	m := &appleNetworkManager{
		retryBase: time.Millisecond,
		deleteFn: func(_ context.Context, _ string) (string, error) {
			calls++
			return "permission denied", errors.New("exit status 1")
		},
	}
	if err := m.RemoveNetwork(context.Background(), "moat-run_x"); err == nil {
		t.Fatal("expected error for non-retryable failure")
	}
	if calls != 1 {
		t.Errorf("non-retryable error must not retry; expected 1 attempt, got %d", calls)
	}
}

func TestRemoveNetworkNotFoundIsSuccess(t *testing.T) {
	calls := 0
	m := &appleNetworkManager{
		deleteFn: func(_ context.Context, _ string) (string, error) {
			calls++
			return "Error: network not found", errors.New("exit status 1")
		},
	}
	if err := m.RemoveNetwork(context.Background(), "moat-run_x"); err != nil {
		t.Fatalf("not-found should be treated as success, got %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 attempt, got %d", calls)
	}
}

func TestRemoveNetworkGivesUpAfterMaxAttempts(t *testing.T) {
	calls := 0
	m := &appleNetworkManager{
		retryBase: time.Millisecond,
		deleteFn: func(_ context.Context, _ string) (string, error) {
			calls++
			return "network moat-run_x has a pending operation", errors.New("exit status 1")
		},
	}
	if err := m.RemoveNetwork(context.Background(), "moat-run_x"); err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if calls != networkDeleteMaxAttempts {
		t.Errorf("expected %d attempts, got %d", networkDeleteMaxAttempts, calls)
	}
}

func TestParseAppleNetworkList(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   []NetworkInfo
	}{
		{
			name: "header plus moat networks plus default",
			output: "NETWORK                STATE    SUBNET\n" +
				"moat-run_abc123def456  running  192.168.65.0/24\n" +
				"moat-run_fed987cba321  running  192.168.66.0/24\n" +
				"default                running  192.168.64.0/24\n",
			want: []NetworkInfo{
				{ID: "moat-run_abc123def456", Name: "moat-run_abc123def456"},
				{ID: "moat-run_fed987cba321", Name: "moat-run_fed987cba321"},
			},
		},
		{
			name:   "only header",
			output: "NETWORK  STATE    SUBNET\n",
			want:   nil,
		},
		{
			name:   "empty output",
			output: "",
			want:   nil,
		},
		{
			name: "blank lines are tolerated",
			output: "NETWORK                STATE    SUBNET\n" +
				"\n" +
				"moat-run_abc123def456  running  192.168.65.0/24\n",
			want: []NetworkInfo{
				{ID: "moat-run_abc123def456", Name: "moat-run_abc123def456"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAppleNetworkList(tt.output)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseAppleNetworkList() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
