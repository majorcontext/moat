package term

import (
	"bytes"
	"io"
	"testing"
)

func TestEscapeProxy_PassThrough(t *testing.T) {
	// Normal data should pass through unchanged
	input := []byte("hello world")
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !bytes.Equal(out, input) {
		t.Errorf("got %q, want %q", out, input)
	}
}

func TestEscapeProxy_Detach(t *testing.T) {
	// Ctrl-/ d should trigger detach
	input := []byte{EscapePrefix, 'd', 'x', 'y', 'z'}
	r := NewEscapeProxy(bytes.NewReader(input))

	buf := make([]byte, 10)
	n, err := r.Read(buf)

	if !IsEscapeError(err) {
		t.Fatalf("expected EscapeError, got: %v", err)
	}
	if GetEscapeAction(err) != EscapeDetach {
		t.Errorf("expected EscapeDetach, got: %v", GetEscapeAction(err))
	}
	if n != 0 {
		t.Errorf("expected 0 bytes, got %d", n)
	}

	// Remaining bytes should be available
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("unexpected error reading remaining: %v", err)
	}
	if string(buf[:n]) != "xyz" {
		t.Errorf("remaining bytes: got %q, want %q", buf[:n], "xyz")
	}
}

func TestEscapeProxy_Stop(t *testing.T) {
	// Ctrl-/ k should trigger stop
	input := []byte{EscapePrefix, 'k'}
	r := NewEscapeProxy(bytes.NewReader(input))

	buf := make([]byte, 10)
	_, err := r.Read(buf)

	if !IsEscapeError(err) {
		t.Fatalf("expected EscapeError, got: %v", err)
	}
	if GetEscapeAction(err) != EscapeStop {
		t.Errorf("expected EscapeStop, got: %v", GetEscapeAction(err))
	}
}

func TestEscapeProxy_LiteralPrefix(t *testing.T) {
	// Ctrl-/ Ctrl-/ should send a single Ctrl-/
	input := []byte{EscapePrefix, EscapePrefix, 'x'}
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []byte{EscapePrefix, 'x'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %v, want %v", out, expected)
	}
}

func TestEscapeProxy_UnrecognizedEscape(t *testing.T) {
	// Ctrl-/ followed by unrecognized key should pass both through
	input := []byte{EscapePrefix, 'x', 'y'}
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []byte{EscapePrefix, 'x', 'y'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %v, want %v", out, expected)
	}
}

func TestEscapeProxy_MixedContent(t *testing.T) {
	// Normal content with escape in the middle
	input := []byte{'a', 'b', EscapePrefix, 'd', 'c'}
	r := NewEscapeProxy(bytes.NewReader(input))

	buf := make([]byte, 10)
	n, err := r.Read(buf)

	// First read should return "ab" then the escape
	// Behavior depends on read size - may get "ab" then escape on next read
	// or escape immediately

	if IsEscapeError(err) {
		// Escape detected - "ab" should be buffered or returned
		if n > 0 {
			if string(buf[:n]) != "ab" {
				t.Errorf("before escape: got %q, want %q", buf[:n], "ab")
			}
		}
	} else if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEscapeProxy_EscapeAtEnd(t *testing.T) {
	// Escape prefix at end of input - treated as literal
	input := []byte{'a', 'b', EscapePrefix}
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get "ab" plus the trailing prefix (treated as literal at EOF)
	expected := []byte{'a', 'b', EscapePrefix}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %v, want %v", out, expected)
	}
}

func TestEscapeProxy_SmallReads(t *testing.T) {
	// Read one byte at a time
	input := []byte{'a', EscapePrefix, 'd', 'b'}
	r := NewEscapeProxy(bytes.NewReader(input))

	buf := make([]byte, 1)

	// Read 'a'
	n, err := r.Read(buf)
	if err != nil {
		t.Fatalf("read 1: unexpected error: %v", err)
	}
	if n != 1 || buf[0] != 'a' {
		t.Errorf("read 1: got %d bytes %q, want 'a'", n, buf[:n])
	}

	// Read should hit escape
	_, err = r.Read(buf)
	if !IsEscapeError(err) {
		t.Fatalf("read 2: expected EscapeError, got: %v", err)
	}
	if GetEscapeAction(err) != EscapeDetach {
		t.Errorf("read 2: expected EscapeDetach, got: %v", GetEscapeAction(err))
	}

	// Read 'b'
	n, err = r.Read(buf)
	if err != nil {
		t.Fatalf("read 3: unexpected error: %v", err)
	}
	if n != 1 || buf[0] != 'b' {
		t.Errorf("read 3: got %d bytes %q, want 'b'", n, buf[:n])
	}
}

func TestEscapeError_Error(t *testing.T) {
	tests := []struct {
		action EscapeAction
		want   string
	}{
		{EscapeDetach, "escape: detach"},
		{EscapeStop, "escape: stop"},
		{EscapeNone, "escape: unknown"},
	}

	for _, tt := range tests {
		err := EscapeError{Action: tt.action}
		if got := err.Error(); got != tt.want {
			t.Errorf("EscapeError{%v}.Error() = %q, want %q", tt.action, got, tt.want)
		}
	}
}

func TestGetEscapeAction_NonEscapeError(t *testing.T) {
	err := io.EOF
	if got := GetEscapeAction(err); got != EscapeNone {
		t.Errorf("GetEscapeAction(io.EOF) = %v, want EscapeNone", got)
	}
}

func TestEscapeProxy_OnPrefixChange(t *testing.T) {
	tests := []struct {
		name           string
		input          []byte
		wantCallbacks  []bool // sequence of callback invocations expected
		wantFinalState bool
	}{
		{
			name:           "prefix detected then completed with detach",
			input:          []byte{EscapePrefix, 'd'},
			wantCallbacks:  []bool{true, false},
			wantFinalState: false,
		},
		{
			name:           "prefix detected then completed with stop",
			input:          []byte{EscapePrefix, 'k'},
			wantCallbacks:  []bool{true, false},
			wantFinalState: false,
		},
		{
			name:           "prefix canceled with literal",
			input:          []byte{EscapePrefix, 'x'},
			wantCallbacks:  []bool{true, false},
			wantFinalState: false,
		},
		{
			name:           "prefix canceled with double ctrl-/",
			input:          []byte{EscapePrefix, EscapePrefix},
			wantCallbacks:  []bool{true, false},
			wantFinalState: false,
		},
		{
			name:           "normal data no callbacks",
			input:          []byte("hello"),
			wantCallbacks:  []bool{},
			wantFinalState: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var callbacks []bool
			r := NewEscapeProxy(bytes.NewReader(tt.input))
			r.OnPrefixChange(func(active bool) {
				callbacks = append(callbacks, active)
			})

			// Read all data
			buf := make([]byte, 100)
			for {
				_, err := r.Read(buf)
				if err != nil {
					break
				}
			}

			// Check callback sequence
			if len(callbacks) != len(tt.wantCallbacks) {
				t.Errorf("got %d callbacks, want %d: %v", len(callbacks), len(tt.wantCallbacks), callbacks)
			} else {
				for i, want := range tt.wantCallbacks {
					if callbacks[i] != want {
						t.Errorf("callback %d: got %v, want %v", i, callbacks[i], want)
					}
				}
			}
		})
	}
}

func TestEscapeProxy_OnPrefixChange_SplitReads(t *testing.T) {
	// Test that prefix state is correctly tracked when EOF occurs after prefix.
	// When prefix is followed by EOF, it's treated as a literal and state is cleared.
	input := []byte{EscapePrefix}
	r := NewEscapeProxy(bytes.NewReader(input))

	var callbacks []bool
	r.OnPrefixChange(func(active bool) {
		callbacks = append(callbacks, active)
	})

	// First read gets the prefix, but EOF cancels it
	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if err != io.EOF {
		t.Fatalf("expected EOF after reading prefix, got: %v", err)
	}

	// Should have callbacks for: true (prefix seen), false (canceled by EOF)
	if len(callbacks) != 2 {
		t.Errorf("after prefix read: got %d callbacks %v, want 2 callbacks [true, false]", len(callbacks), callbacks)
	} else if callbacks[0] != true || callbacks[1] != false {
		t.Errorf("after prefix read: got callbacks %v, want [true, false]", callbacks)
	}
}
