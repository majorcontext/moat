package term

import (
	"bytes"
	"io"
	"testing"
	"time"
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

func TestEscapeProxy_UnrecognizedKeyPassesThrough(t *testing.T) {
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

// --- Kitty keyboard protocol ---
//
// Agents that enable the kitty keyboard protocol (Codex pushes CSI > 7 u at
// startup) change how the terminal encodes Ctrl+/: instead of the legacy 0x1f
// byte it sends CSI 47 ; 5 u. Without recognizing that form, moat's escape
// prefix silently stops working for those agents while continuing to work for
// agents that leave the protocol off, such as Claude Code.

// drainActions reads r to completion, collecting escape actions and output.
func drainActions(t *testing.T, r *EscapeProxy) ([]EscapeAction, []byte) {
	t.Helper()
	var actions []EscapeAction
	r.OnAction(func(a EscapeAction) { actions = append(actions, a) })
	var out []byte
	buf := make([]byte, 64)
	for {
		n, err := r.Read(buf)
		out = append(out, buf[:n]...)
		if err != nil {
			break
		}
	}
	return actions, out
}

func TestEscapeProxy_KittyPrefixSnapshot(t *testing.T) {
	// CSI 47;5u is Ctrl+/ under the kitty protocol; 's' still arrives as a
	// plain byte because flag 7 does not report text keys as escape codes.
	input := append([]byte("\x1b[47;5u"), 's')
	actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))

	if len(actions) != 1 || actions[0] != EscapeSnapshot {
		t.Fatalf("expected one snapshot action, got %v", actions)
	}
	if len(out) != 0 {
		t.Errorf("escape sequence should be consumed, leaked %q", out)
	}
}

func TestEscapeProxy_KittyPrefixWithPressEvent(t *testing.T) {
	// Flag 7 includes "report event types", so the press carries :1.
	input := append([]byte("\x1b[47;5:1u"), 's')
	actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))

	if len(actions) != 1 || actions[0] != EscapeSnapshot {
		t.Fatalf("expected one snapshot action, got %v", actions)
	}
	if len(out) != 0 {
		t.Errorf("escape sequence should be consumed, leaked %q", out)
	}
}

func TestEscapeProxy_KittyReleaseDoesNotDoubleFire(t *testing.T) {
	// A real key press emits press (:1) then release (:3). The release must not
	// be treated as the command key, and must not fire a second action.
	input := append([]byte("\x1b[47;5:1u\x1b[47;5:3u"), 's')
	actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))

	if len(actions) != 1 || actions[0] != EscapeSnapshot {
		t.Fatalf("expected exactly one snapshot action, got %v", actions)
	}
	if len(out) != 0 {
		t.Errorf("escape sequences should be consumed, leaked %q", out)
	}
}

func TestEscapeProxy_KittyPrefixSplitAcrossReads(t *testing.T) {
	// The sequence can arrive in pieces; a partial prefix must not be lost.
	pr, pw := io.Pipe()
	r := NewEscapeProxy(pr)
	var actions []EscapeAction
	done := make(chan struct{})
	r.OnAction(func(a EscapeAction) { actions = append(actions, a) })
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			if _, err := r.Read(buf); err != nil {
				return
			}
		}
	}()
	for _, chunk := range []string{"\x1b[4", "7;5", "u", "s"} {
		if _, err := pw.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	pw.Close()
	<-done

	if len(actions) != 1 || actions[0] != EscapeSnapshot {
		t.Fatalf("expected one snapshot action across split reads, got %v", actions)
	}
}

func TestEscapeProxy_NonPrefixCSIUPassesThrough(t *testing.T) {
	// Other kitty-encoded keys must reach the child untouched. CSI 97;5u is
	// Ctrl+A; CSI 47;6u is Ctrl+Shift+/ (a different chord).
	for _, seq := range []string{"\x1b[97;5u", "\x1b[47;6u", "\x1b[47;5~", "\x1b[A"} {
		actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader([]byte(seq))))
		if len(actions) != 0 {
			t.Errorf("%q should not trigger an action, got %v", seq, actions)
		}
		if string(out) != seq {
			t.Errorf("%q should pass through unchanged, got %q", seq, out)
		}
	}
}

func TestEscapeProxy_LegacyPrefixStillWorks(t *testing.T) {
	// Agents that leave the protocol off keep sending the raw byte.
	input := []byte{EscapePrefix, 's'}
	actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))

	if len(actions) != 1 || actions[0] != EscapeSnapshot {
		t.Fatalf("expected one snapshot action, got %v", actions)
	}
	if len(out) != 0 {
		t.Errorf("escape sequence should be consumed, leaked %q", out)
	}
}

func TestEscapeProxy_KittyPartialAtEOFIsReleased(t *testing.T) {
	// A truncated sequence must reach the child rather than vanish.
	input := []byte("\x1b[47;5")
	_, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))
	if !bytes.Equal(out, input) {
		t.Errorf("truncated sequence should pass through, got %q want %q", out, input)
	}
}

func TestEscapeProxy_KittyPrefixThenStop(t *testing.T) {
	// The stop action unwinds Read with an error, unlike snapshot.
	r := NewEscapeProxy(bytes.NewReader(append([]byte("\x1b[47;5u"), 'k')))
	buf := make([]byte, 32)
	var got EscapeAction
	for {
		_, err := r.Read(buf)
		if err == nil {
			continue
		}
		if IsEscapeError(err) {
			got = GetEscapeAction(err)
		}
		break
	}
	if got != EscapeStop {
		t.Errorf("expected EscapeStop, got %v", got)
	}
}

// Releasing the chord does not always report the '/' key with Ctrl still held.
// Lifting Ctrl first reports different modifiers, and the modifier key itself
// has its own release event. None of these are the command key, so none may
// cancel the armed prefix.
func TestEscapeProxy_KittyReleaseVariantsDoNotCancelPrefix(t *testing.T) {
	for name, release := range map[string]string{
		"ctrl lifted first":  "\x1b[47;1:3u",
		"ctrl still held":    "\x1b[47;5:3u",
		"left ctrl key up":   "\x1b[57442;1:3u",
		"alternate key form": "\x1b[47:47;5:3u",
	} {
		t.Run(name, func(t *testing.T) {
			input := []byte("\x1b[47;5:1u" + release + "s")
			actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))
			if len(actions) != 1 || actions[0] != EscapeSnapshot {
				t.Fatalf("prefix did not survive the release: got actions %v", actions)
			}
			if len(out) != 0 {
				t.Errorf("nothing should leak to the child, got %q", out)
			}
		})
	}
}

func TestEscapeProxy_KittyPressWhileArmedIsNotSwallowed(t *testing.T) {
	// A different key *press* after the prefix is not a command; the existing
	// contract passes the prefix and the unrecognized bytes through.
	input := []byte("\x1b[47;5u\x1b[A")
	_, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))
	if !bytes.Contains(out, []byte{EscapePrefix}) {
		t.Errorf("unrecognized command should emit the literal prefix, got %q", out)
	}
	if !bytes.Contains(out, []byte("\x1b[A")) {
		t.Errorf("the arrow key should reach the child, got %q", out)
	}
}

// A realistic chord: the terminal reports a press, then autorepeat while the
// keys are held, then releases. Only the press arms the prefix; nothing else
// in that stream may cancel it.
func TestEscapeProxy_KittyHeldChordKeepsPrefixArmed(t *testing.T) {
	for name, stream := range map[string]string{
		"tap":                 "\x1b[47;5:1u\x1b[47;5:3u",
		"held with repeats":   "\x1b[47;5:1u\x1b[47;5:2u\x1b[47;5:2u\x1b[47;5:3u",
		"repeat then ctrl up": "\x1b[47;5:1u\x1b[47;5:2u\x1b[47;1:3u\x1b[57442;1:3u",
	} {
		t.Run(name, func(t *testing.T) {
			actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader([]byte(stream+"s"))))
			if len(actions) != 1 || actions[0] != EscapeSnapshot {
				t.Fatalf("prefix did not survive the chord: got actions %v", actions)
			}
			if len(out) != 0 {
				t.Errorf("nothing should leak to the child, got %q", out)
			}
		})
	}
}

// Bytes captured from Ghostty with the kitty protocol enabled (printf '\033[>7u').
// Pinning the real encoding matters: the press carries NO event sub-parameter,
// and a release can report different modifiers than the press did.
//
//	^[[47;5u    ctrl+/ press
//	^[[47;5:3u  ctrl+/ release
//	^[[99;5u    ctrl+c press      (99 = 'c')
//	^[[99;1:3u  ctrl+c release, modifiers already lifted
func TestEscapeProxy_GhosttyCapturedBytes(t *testing.T) {
	t.Run("prefix then command survives the real chord", func(t *testing.T) {
		input := []byte("\x1b[47;5u\x1b[47;5:3u" + "s")
		actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader(input)))
		if len(actions) != 1 || actions[0] != EscapeSnapshot {
			t.Fatalf("expected snapshot from the captured chord, got %v", actions)
		}
		if len(out) != 0 {
			t.Errorf("nothing should leak to the child, got %q", out)
		}
	})

	t.Run("other ctrl chords reach the child untouched", func(t *testing.T) {
		// Ctrl+C and Ctrl+D are CSI-u encoded too and must not be disturbed;
		// the agent depends on receiving them.
		for _, seq := range []string{
			"\x1b[99;5u\x1b[99;5:3u",
			"\x1b[100;5u\x1b[100;5:3u",
			"\x1b[99;5u\x1b[99;1:3u",
		} {
			actions, out := drainActions(t, NewEscapeProxy(bytes.NewReader([]byte(seq))))
			if len(actions) != 0 {
				t.Errorf("%q should not trigger moat, got %v", seq, actions)
			}
			if string(out) != seq {
				t.Errorf("%q should pass through unchanged, got %q", seq, out)
			}
		}
	})
}

// Real terminals deliver the press and the release in separate reads. When the
// buffer ends while the prefix is armed, Read takes a one-byte shortcut to get
// the command key; that byte can be the ESC that begins the release sequence,
// which must not be mistaken for a command.
//
// Captured from Ghostty: moat emitted "\x1f\x1b[47;5:3ud" — a literal prefix,
// the leaked release, and the command key handed to the agent instead.
func TestEscapeProxy_KittyReleaseInSeparateRead(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewEscapeProxy(pr)
	var actions []EscapeAction
	r.OnAction(func(a EscapeAction) { actions = append(actions, a) })

	var out []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		buf := make([]byte, 64)
		for {
			n, err := r.Read(buf)
			out = append(out, buf[:n]...)
			if err != nil {
				return
			}
		}
	}()

	// Each write lands as its own Read, as a terminal would deliver them.
	for _, chunk := range []string{"\x1b[47;5u", "\x1b[47;5:3u", "d"} {
		if _, err := pw.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	pw.Close()
	<-done

	if len(actions) != 1 || actions[0] != EscapeDumpTUI {
		t.Fatalf("expected the dump action, got %v", actions)
	}
	if bytes.Contains(out, []byte{EscapePrefix}) {
		t.Errorf("a literal prefix leaked to the child: %q", out)
	}
	if bytes.Contains(out, []byte("d")) {
		t.Errorf("the command key leaked to the child: %q", out)
	}
}

// A lone Esc is a complete keypress and a common one — "esc to interrupt" in
// Codex, dismissing a prompt in Claude Code. It must not be held back waiting
// to see whether a kitty sequence follows, or Esc appears dead until the user
// presses another key.
func TestEscapeProxy_BareEscIsNotDelayed(t *testing.T) {
	pr, pw := io.Pipe()
	r := NewEscapeProxy(pr)

	go func() {
		pw.Write([]byte{0x1b})
		time.Sleep(60 * time.Millisecond)
		pw.Write([]byte("x"))
		pw.Close()
	}()

	buf := make([]byte, 16)
	start := time.Now()
	n, err := r.Read(buf)
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if n == 0 || buf[0] != 0x1b {
		t.Fatalf("expected the Esc byte, got %q", buf[:n])
	}
	if elapsed > 40*time.Millisecond {
		t.Errorf("Esc was withheld for %v awaiting a following key", elapsed.Round(time.Millisecond))
	}
}

// stepEOFReader delivers each chunk in its own Read, then EOF.
type stepEOFReader struct {
	steps [][]byte
	i     int
}

func (s *stepEOFReader) Read(p []byte) (int, error) {
	if s.i >= len(s.steps) {
		return 0, io.EOF
	}
	n := copy(p, s.steps[s.i])
	s.i++
	return n, nil
}

// An armed prefix and a held partial sequence can be outstanding at the same
// time. At EOF both must reach the child; handling only the prefix silently
// dropped the buffered bytes.
func TestEscapeProxy_FlushesPrefixAndPartialAtEOF(t *testing.T) {
	r := NewEscapeProxy(&stepEOFReader{steps: [][]byte{{EscapePrefix}, {0x1b}}})

	var got []byte
	buf := make([]byte, 16)
	for {
		n, err := r.Read(buf)
		got = append(got, buf[:n]...)
		if err != nil {
			break
		}
	}

	if !bytes.Contains(got, []byte{EscapePrefix}) {
		t.Errorf("the armed prefix should be flushed at EOF, got %q", got)
	}
	if !bytes.Contains(got, []byte{0x1b}) {
		t.Errorf("the held partial should be flushed at EOF, got %q", got)
	}
}
