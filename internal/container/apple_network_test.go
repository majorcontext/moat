package container

import (
	"reflect"
	"testing"
)

func TestAppleNetworkManagerImplementsInterface(t *testing.T) {
	var _ NetworkManager = (*appleNetworkManager)(nil)
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
