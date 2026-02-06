package ui

import (
	"bytes"
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
