package clipboard

import (
	"testing"
)

func TestContentIsImage(t *testing.T) {
	tests := []struct {
		name     string
		mime     string
		expected bool
	}{
		{"text", "text/plain", false},
		{"png", "image/png", true},
		{"jpeg", "image/jpeg", true},
		{"empty", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Content{Data: []byte("test"), MIMEType: tt.mime}
			if got := c.IsImage(); got != tt.expected {
				t.Errorf("IsImage() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestMIMEToXclipTarget(t *testing.T) {
	tests := []struct {
		mime   string
		target string
	}{
		{"text/plain", "UTF8_STRING"},
		{"image/png", "image/png"},
		{"image/jpeg", "image/jpeg"},
		{"", "UTF8_STRING"},
		{"application/json", "UTF8_STRING"},
	}
	for _, tt := range tests {
		t.Run(tt.mime, func(t *testing.T) {
			got := MIMEToXclipTarget(tt.mime)
			if got != tt.target {
				t.Errorf("MIMEToXclipTarget(%q) = %q, want %q", tt.mime, got, tt.target)
			}
		})
	}
}
