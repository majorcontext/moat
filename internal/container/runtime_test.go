package container

import (
	"reflect"
	"testing"
)

func TestDefaultDNS(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{
			name:  "empty returns defaults",
			input: []string{},
			want:  []string{"8.8.8.8", "8.8.4.4"},
		},
		{
			name:  "nil returns defaults",
			input: nil,
			want:  []string{"8.8.8.8", "8.8.4.4"},
		},
		{
			name:  "custom DNS preserved",
			input: []string{"1.1.1.1"},
			want:  []string{"1.1.1.1"},
		},
		{
			name:  "multiple custom DNS preserved",
			input: []string{"1.1.1.1", "8.8.8.8", "192.168.1.1"},
			want:  []string{"1.1.1.1", "8.8.8.8", "192.168.1.1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultDNS(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("DefaultDNS() = %v, want %v", got, tt.want)
			}
		})
	}
}
