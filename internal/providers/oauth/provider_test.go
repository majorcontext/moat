package oauth

import (
	"context"
	"testing"
	"time"

	"github.com/majorcontext/moat/internal/provider"
)

func TestProvider_Name(t *testing.T) {
	p := &Provider{}
	if got := p.Name(); got != "oauth" {
		t.Errorf("Name() = %q, want %q", got, "oauth")
	}
}

func TestProvider_Grant(t *testing.T) {
	p := &Provider{}
	_, err := p.Grant(context.Background())
	if err == nil {
		t.Fatal("expected error from Grant(), got nil")
	}
}

func TestProvider_ConfigureProxy(t *testing.T) {
	p := &Provider{}
	// Should not panic.
	p.ConfigureProxy(nil, nil)
}

func TestProvider_ContainerEnv(t *testing.T) {
	p := &Provider{}
	if env := p.ContainerEnv(nil); env != nil {
		t.Errorf("ContainerEnv() = %v, want nil", env)
	}
}

func TestProvider_ContainerMounts(t *testing.T) {
	p := &Provider{}
	mounts, cleanup, err := p.ContainerMounts(nil, "/home/user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mounts != nil {
		t.Errorf("ContainerMounts() mounts = %v, want nil", mounts)
	}
	if cleanup != "" {
		t.Errorf("ContainerMounts() cleanup = %q, want empty", cleanup)
	}
}

func TestProvider_ImpliedDependencies(t *testing.T) {
	p := &Provider{}
	if deps := p.ImpliedDependencies(); deps != nil {
		t.Errorf("ImpliedDependencies() = %v, want nil", deps)
	}
}

func TestProvider_CanRefresh(t *testing.T) {
	p := &Provider{}

	tests := []struct {
		name string
		cred *provider.Credential
		want bool
	}{
		{
			name: "nil metadata",
			cred: &provider.Credential{},
			want: false,
		},
		{
			name: "wrong token source",
			cred: &provider.Credential{
				Metadata: map[string]string{
					provider.MetaKeyTokenSource: "cli",
					"refresh_token":             "rt_abc",
				},
				ExpiresAt: time.Now().Add(time.Hour),
			},
			want: false,
		},
		{
			name: "oauth source but no expiry",
			cred: &provider.Credential{
				Metadata: map[string]string{
					provider.MetaKeyTokenSource: "oauth",
					"refresh_token":             "rt_abc",
				},
			},
			want: false,
		},
		{
			name: "oauth source but no refresh token",
			cred: &provider.Credential{
				Metadata: map[string]string{
					provider.MetaKeyTokenSource: "oauth",
				},
				ExpiresAt: time.Now().Add(time.Hour),
			},
			want: false,
		},
		{
			name: "all conditions met",
			cred: &provider.Credential{
				Metadata: map[string]string{
					provider.MetaKeyTokenSource: "oauth",
					"refresh_token":             "rt_abc",
				},
				ExpiresAt: time.Now().Add(time.Hour),
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.CanRefresh(tt.cred); got != tt.want {
				t.Errorf("CanRefresh() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestProvider_RefreshInterval(t *testing.T) {
	p := &Provider{}
	if got := p.RefreshInterval(); got != 5*time.Minute {
		t.Errorf("RefreshInterval() = %v, want %v", got, 5*time.Minute)
	}
}

func TestProvider_Refresh(t *testing.T) {
	p := &Provider{}
	_, err := p.Refresh(context.Background(), nil, nil)
	if err != provider.ErrRefreshNotSupported {
		t.Errorf("Refresh() error = %v, want ErrRefreshNotSupported", err)
	}
}
