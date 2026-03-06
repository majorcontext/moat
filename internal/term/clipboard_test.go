package term

import (
	"bytes"
	"io"
	"testing"
)

func TestClipboardProxy_PassThrough(t *testing.T) {
	input := []byte("hello world")
	called := false
	r := NewClipboardProxy(bytes.NewReader(input), func() {
		called = true
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %q, want %q", out, input)
	}
	if called {
		t.Error("onCtrlV should not be called for normal input")
	}
}

func TestClipboardProxy_DetectsCtrlV(t *testing.T) {
	input := []byte{0x16}
	called := false
	r := NewClipboardProxy(bytes.NewReader(input), func() {
		called = true
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, []byte{0x16}) {
		t.Errorf("got %v, want [0x16]", out)
	}
	if !called {
		t.Error("onCtrlV should be called when 0x16 is detected")
	}
}

func TestClipboardProxy_CtrlVInMiddle(t *testing.T) {
	input := []byte{'a', 'b', 0x16, 'c', 'd'}
	callCount := 0
	r := NewClipboardProxy(bytes.NewReader(input), func() {
		callCount++
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %v, want %v", out, input)
	}
	if callCount != 1 {
		t.Errorf("onCtrlV called %d times, want 1", callCount)
	}
}

func TestClipboardProxy_MultipleCtrlV(t *testing.T) {
	input := []byte{0x16, 'a', 0x16}
	callCount := 0
	r := NewClipboardProxy(bytes.NewReader(input), func() {
		callCount++
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %v, want %v", out, input)
	}
	if callCount != 2 {
		t.Errorf("onCtrlV called %d times, want 2", callCount)
	}
}

func TestClipboardProxy_NilCallback(t *testing.T) {
	input := []byte{0x16, 'a'}
	r := NewClipboardProxy(bytes.NewReader(input), nil)

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %v, want %v", out, input)
	}
}
