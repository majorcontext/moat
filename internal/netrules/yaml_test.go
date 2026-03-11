package netrules

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestNetworkRuleEntryUnmarshal(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		want    NetworkRuleEntry
		wantErr bool
	}{
		{
			name: "plain host string",
			yaml: `"api.github.com"`,
			want: NetworkRuleEntry{HostRules: HostRules{Host: "api.github.com"}},
		},
		{
			name: "host with rules",
			yaml: "\"api.github.com\":\n  - \"allow GET /repos/*\"\n  - \"deny DELETE /*\"",
			want: NetworkRuleEntry{HostRules: HostRules{
				Host: "api.github.com",
				Rules: []Rule{
					{Action: "allow", Method: "GET", PathPattern: "/repos/*"},
					{Action: "deny", Method: "DELETE", PathPattern: "/*"},
				},
			}},
		},
		{
			name:    "invalid rule string",
			yaml:    `"api.github.com": ["block GET /foo"]`,
			wantErr: true,
		},
		{
			name:    "empty host in mapping",
			yaml:    `"": ["allow GET /"]`,
			wantErr: true,
		},
		{
			name:    "empty host scalar",
			yaml:    `""`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got NetworkRuleEntry
			err := yaml.Unmarshal([]byte(tt.yaml), &got)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Host != tt.want.Host {
				t.Errorf("Host = %q, want %q", got.Host, tt.want.Host)
			}
			if len(got.Rules) != len(tt.want.Rules) {
				t.Fatalf("got %d rules, want %d", len(got.Rules), len(tt.want.Rules))
			}
			for i, r := range got.Rules {
				if r != tt.want.Rules[i] {
					t.Errorf("Rules[%d] = %+v, want %+v", i, r, tt.want.Rules[i])
				}
			}
		})
	}
}
