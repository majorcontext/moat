package ui

import (
	"bytes"
	"os"
	"testing"
)

func TestWarn(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Warn("something happened")

	if got := buf.String(); got != "Warning: something happened\n" {
		t.Errorf("Warn output = %q, want %q", got, "Warning: something happened\n")
	}
}

func TestWarnf(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Warnf("skipping %q: reason %s", "plugin", "unknown")

	want := "Warning: skipping \"plugin\": reason unknown\n"
	if got := buf.String(); got != want {
		t.Errorf("Warnf output = %q, want %q", got, want)
	}
}

func TestError(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Error("something failed")

	if got := buf.String(); got != "Error: something failed\n" {
		t.Errorf("Error output = %q, want %q", got, "Error: something failed\n")
	}
}

func TestErrorf(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Errorf("failed to connect: %s", "timeout")

	want := "Error: failed to connect: timeout\n"
	if got := buf.String(); got != want {
		t.Errorf("Errorf output = %q, want %q", got, want)
	}
}

func TestInfo(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Info("something informational")

	if got := buf.String(); got != "something informational\n" {
		t.Errorf("Info output = %q, want %q", got, "something informational\n")
	}
}

func TestInfof(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	defer SetWriter(nil)

	Infof("hint: use %s instead", "-i")

	want := "hint: use -i instead\n"
	if got := buf.String(); got != want {
		t.Errorf("Infof output = %q, want %q", got, want)
	}
}

func TestColorFunctionsEnabled(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	tests := []struct {
		name string
		fn   func(string) string
		code string
	}{
		{"Bold", Bold, "1"},
		{"Dim", Dim, "2"},
		{"Green", Green, "32"},
		{"Red", Red, "31"},
		{"Yellow", Yellow, "33"},
		{"Cyan", Cyan, "36"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn("hello")
			want := "\033[" + tt.code + "mhello\033[0m"
			if got != want {
				t.Errorf("%s(\"hello\") = %q, want %q", tt.name, got, want)
			}
		})
	}
}

func TestColorFunctionsDisabled(t *testing.T) {
	SetColorEnabled(false)

	tests := []struct {
		name string
		fn   func(string) string
	}{
		{"Bold", Bold},
		{"Dim", Dim},
		{"Green", Green},
		{"Red", Red},
		{"Yellow", Yellow},
		{"Cyan", Cyan},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn("hello")
			if got != "hello" {
				t.Errorf("%s(\"hello\") with color disabled = %q, want %q", tt.name, got, "hello")
			}
		})
	}
}

func TestTags(t *testing.T) {
	SetColorEnabled(true)
	defer SetColorEnabled(false)

	if got := OKTag(); got != "\033[32m✓\033[0m" {
		t.Errorf("OKTag() = %q, want green ✓", got)
	}
	if got := FailTag(); got != "\033[31m✗\033[0m" {
		t.Errorf("FailTag() = %q, want red ✗", got)
	}
	if got := WarnTag(); got != "\033[33m⚠\033[0m" {
		t.Errorf("WarnTag() = %q, want yellow ⚠", got)
	}
	if got := InfoTag(); got != "\033[36mℹ\033[0m" {
		t.Errorf("InfoTag() = %q, want cyan ℹ", got)
	}
}

func TestTagsNoColor(t *testing.T) {
	SetColorEnabled(false)

	if got := OKTag(); got != "✓" {
		t.Errorf("OKTag() = %q, want plain ✓", got)
	}
	if got := FailTag(); got != "✗" {
		t.Errorf("FailTag() = %q, want plain ✗", got)
	}
	if got := WarnTag(); got != "⚠" {
		t.Errorf("WarnTag() = %q, want plain ⚠", got)
	}
	if got := InfoTag(); got != "ℹ" {
		t.Errorf("InfoTag() = %q, want plain ℹ", got)
	}
}

func TestNO_COLOR(t *testing.T) {
	t.Setenv("NO_COLOR", "1")

	f, err := os.CreateTemp("", "ui-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(f.Name())
	defer f.Close()

	got := detectColor(f)
	if got {
		t.Error("detectColor should return false when NO_COLOR is set")
	}
}

func TestColorEnabled(t *testing.T) {
	SetColorEnabled(true)
	if !ColorEnabled() {
		t.Error("ColorEnabled() should be true after SetColorEnabled(true)")
	}
	SetColorEnabled(false)
	if ColorEnabled() {
		t.Error("ColorEnabled() should be false after SetColorEnabled(false)")
	}
}

func TestWarnColoredPrefix(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	SetColorEnabled(true)
	defer func() {
		SetWriter(nil)
		SetColorEnabled(false)
	}()

	Warn("test message")
	got := buf.String()
	want := "\033[33mWarning:\033[0m test message\n"
	if got != want {
		t.Errorf("Warn with color = %q, want %q", got, want)
	}
}

func TestErrorColoredPrefix(t *testing.T) {
	var buf bytes.Buffer
	SetWriter(&buf)
	SetColorEnabled(true)
	defer func() {
		SetWriter(nil)
		SetColorEnabled(false)
	}()

	Error("test message")
	got := buf.String()
	want := "\033[31mError:\033[0m test message\n"
	if got != want {
		t.Errorf("Error with color = %q, want %q", got, want)
	}
}
