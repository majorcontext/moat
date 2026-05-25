package claude

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"
)

// TestReadOAuthTokenInput_pipedUsesSharedReader guards against re-introducing a
// second bufio.Reader on os.Stdin for piped input. The menu prompt reads the
// choice from a bufio.Reader, which can buffer the rest of a piped stdin (the
// token line arrives in the same read as the choice). The token read MUST use
// that same reader, or the buffered token is lost.
func TestReadOAuthTokenInput_pipedUsesSharedReader(t *testing.T) {
	// One reader over the whole piped stdin: menu choice + token, as a pipe
	// delivers it in a single read.
	reader := bufio.NewReader(strings.NewReader("2\nsk-ant-oat01-shared-token\n\n"))
	if _, err := reader.ReadString('\n'); err != nil { // menu consumes the choice
		t.Fatalf("consuming menu choice: %v", err)
	}

	// A pipe (not a TTY) selects the non-interactive branch; the token is read
	// from reader, not from this file.
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	pw.Close()
	defer pr.Close()

	got, err := readOAuthTokenInput(reader, pr)
	if err != nil {
		t.Fatalf("readOAuthTokenInput: %v", err)
	}
	if got != "sk-ant-oat01-shared-token" {
		t.Errorf("token = %q, want %q (token buffered by the menu reader was lost)", got, "sk-ant-oat01-shared-token")
	}
}

// TestScanBracketedPaste covers the interactive terminal path: a bracketed
// paste (ESC[200~ … ESC[201~) is captured whole even when the source terminal
// soft-wrapped the token across lines, with no terminator keystroke. Manually
// typed input still ends on Enter.
func TestScanBracketedPaste(t *testing.T) {
	const tok = "sk-ant-oat01-6EGPt5Yxvrb5tYAeTxjvGeFG7kmCOnbT"
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "paste single line", in: "\x1b[200~" + tok + "\x1b[201~", want: tok},
		{name: "paste wrapped with newlines", in: "\x1b[200~sk-ant-oat01-aaa\r\nbbb\nccc\x1b[201~", want: "sk-ant-oat01-aaabbbccc"},
		{name: "paste then stray enter ignored", in: "\x1b[200~" + tok + "\x1b[201~", want: tok},
		{name: "typed then enter", in: tok + "\r", want: tok},
		{name: "typed with backspace", in: "sk-ant-oat01-zX\x7f\r", want: "sk-ant-oat01-z"},
		{name: "ctrl-c cancels", in: "\x03", wantErr: true},
		{name: "other CSI ignored around paste", in: "\x1b[0m\x1b[200~" + tok + "\x1b[201~", want: tok},
		{name: "typed then EOF", in: tok, want: tok},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := scanBracketedPaste(strings.NewReader(c.in))
			if c.wantErr {
				if err == nil {
					t.Fatalf("expected error, got token %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("scanBracketedPaste(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// lineReader yields exactly one element per Read call, simulating a terminal
// in canonical mode where a multi-line paste is delivered to the process one
// line at a time (each only after its newline). This is the condition under
// which bufio.Buffered() is 0 between lines, which broke the first attempt.
type lineReader struct {
	lines []string
	i     int
}

func (lr *lineReader) Read(p []byte) (int, error) {
	if lr.i >= len(lr.lines) {
		return 0, io.EOF
	}
	n := copy(p, lr.lines[lr.i])
	lr.i++
	return n, nil
}

// TestReadPastedToken covers a pasted OAuth token that the source terminal
// soft-wrapped across lines, delivered line-by-line as a real terminal does.
// The token has no internal whitespace, so reassembly must drop it all and
// must not stop at the first newline.
func TestReadPastedToken(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  string
	}{
		{"single line then blank", []string{"sk-ant-oat01-abc\n", "\n"}, "sk-ant-oat01-abc"},
		{"wrapped, blank-line terminated", []string{"sk-ant-oat01-aaa\n", " bbb\n", "\n"}, "sk-ant-oat01-aaabbb"},
		{"wrapped then EOF (ctrl-d)", []string{"sk-ant-oat01-1\n", "2"}, "sk-ant-oat01-12"},
		{"no trailing newline, EOF", []string{"sk-ant-oat01-xyz"}, "sk-ant-oat01-xyz"},
		{"crlf wraps", []string{"sk-ant-oat01-1\r\n", "2\r\n", "\r\n"}, "sk-ant-oat01-12"},
		{"leading blank ignored", []string{"\n", "sk-ant-oat01-z\n", "\n"}, "sk-ant-oat01-z"},
		{"empty", []string{}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := readPastedToken(bufio.NewReader(&lineReader{lines: c.lines}))
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != c.want {
				t.Errorf("readPastedToken(%q) = %q, want %q", c.lines, got, c.want)
			}
		})
	}
}
