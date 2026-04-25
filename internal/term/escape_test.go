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

func TestEscapeProxy_DPassesThrough(t *testing.T) {
	// Ctrl-/ q is not an escape sequence; both bytes should pass through
	input := []byte{EscapePrefix, 'q', 'x', 'y', 'z'}
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []byte{EscapePrefix, 'q', 'x', 'y', 'z'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %v, want %v", out, expected)
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
	// Normal content with unrecognized escape in the middle - both bytes pass through
	input := []byte{'a', 'b', EscapePrefix, 'q', 'c'}
	r := NewEscapeProxy(bytes.NewReader(input))

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := []byte{'a', 'b', EscapePrefix, 'q', 'c'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %v, want %v", out, expected)
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
	// Read one byte at a time with a stop escape
	input := []byte{'a', EscapePrefix, 'k', 'b'}
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
	if GetEscapeAction(err) != EscapeStop {
		t.Errorf("read 2: expected EscapeStop, got: %v", GetEscapeAction(err))
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
		{EscapeStop, "escape: stop"},
		{EscapeSnapshot, "escape: snapshot"},
		{EscapeDumpTUI, "escape: dump-tui"},
		{EscapeResetTUI, "escape: reset-tui"},
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

func TestEscapeProxy_Snapshot(t *testing.T) {
	// Ctrl-/ s should fire onAction callback with EscapeSnapshot
	input := []byte{EscapePrefix, 's'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var gotAction EscapeAction
	r.OnAction(func(action EscapeAction) {
		gotAction = action
	})

	buf := make([]byte, 10)
	_, err := r.Read(buf)
	// Should NOT return an EscapeError — snapshot is non-disruptive
	if IsEscapeError(err) {
		t.Fatalf("snapshot should not return EscapeError, got: %v", err)
	}

	if gotAction != EscapeSnapshot {
		t.Errorf("expected EscapeSnapshot callback, got: %v", gotAction)
	}
}

func TestEscapeProxy_SnapshotContinuesReading(t *testing.T) {
	// After Ctrl-/ s, data should continue flowing (no unwinding)
	input := []byte{'a', EscapePrefix, 's', 'b', 'c'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var actionCount int
	r.OnAction(func(action EscapeAction) {
		actionCount++
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should get "abc" — the escape sequence is consumed, surrounding data flows
	expected := []byte{'a', 'b', 'c'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %q, want %q", out, expected)
	}
	if actionCount != 1 {
		t.Errorf("expected 1 action callback, got %d", actionCount)
	}
}

func TestEscapeProxy_SnapshotPrefixChangeCallbacks(t *testing.T) {
	// Ctrl-/ s should fire onPrefixChange true then false
	input := []byte{EscapePrefix, 's', 'x'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var callbacks []bool
	r.OnPrefixChange(func(active bool) {
		callbacks = append(callbacks, active)
	})
	r.OnAction(func(action EscapeAction) {})

	buf := make([]byte, 100)
	for {
		_, err := r.Read(buf)
		if err != nil {
			break
		}
	}

	if len(callbacks) != 2 || callbacks[0] != true || callbacks[1] != false {
		t.Errorf("expected callbacks [true, false], got %v", callbacks)
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
			name:           "prefix detected then canceled with unrecognized q",
			input:          []byte{EscapePrefix, 'q'},
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

func TestEscapeProxy_DumpTUI(t *testing.T) {
	input := []byte{EscapePrefix, 'd'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var gotAction EscapeAction
	r.OnAction(func(action EscapeAction) {
		gotAction = action
	})

	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if IsEscapeError(err) {
		t.Fatalf("dump should not return EscapeError, got: %v", err)
	}
	if gotAction != EscapeDumpTUI {
		t.Errorf("expected EscapeDumpTUI callback, got: %v", gotAction)
	}
}

func TestEscapeProxy_ResetTUI(t *testing.T) {
	input := []byte{EscapePrefix, 'r'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var gotAction EscapeAction
	r.OnAction(func(action EscapeAction) {
		gotAction = action
	})

	buf := make([]byte, 10)
	_, err := r.Read(buf)
	if IsEscapeError(err) {
		t.Fatalf("reset should not return EscapeError, got: %v", err)
	}
	if gotAction != EscapeResetTUI {
		t.Errorf("expected EscapeResetTUI callback, got: %v", gotAction)
	}
}

func TestEscapeProxy_DumpAndResetContinueReading(t *testing.T) {
	input := []byte{'a', EscapePrefix, 'd', 'b', EscapePrefix, 'r', 'c'}
	r := NewEscapeProxy(bytes.NewReader(input))

	var actions []EscapeAction
	r.OnAction(func(action EscapeAction) {
		actions = append(actions, action)
	})

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expected := []byte{'a', 'b', 'c'}
	if !bytes.Equal(out, expected) {
		t.Errorf("got %q, want %q", out, expected)
	}
	if len(actions) != 2 || actions[0] != EscapeDumpTUI || actions[1] != EscapeResetTUI {
		t.Errorf("expected [EscapeDumpTUI, EscapeResetTUI], got %v", actions)
	}
}
