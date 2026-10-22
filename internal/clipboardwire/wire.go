// Package clipboardwire carries text clipboard changes over a container exec
// stream. It has no native clipboard dependencies and is also used by moat-init.
package clipboardwire

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"unicode/utf8"
)

// MaxTextBytes bounds both clipboard reads and stream frame allocations.
const MaxTextBytes = 1024 * 1024

// Write sends one text update. Base64 preserves newlines and trailing spaces.
func Write(w io.Writer, data []byte) error {
	if len(data) > MaxTextBytes || !utf8.Valid(data) {
		return fmt.Errorf("clipboard text is oversized or not UTF-8")
	}
	_, err := fmt.Fprintln(w, base64.StdEncoding.EncodeToString(data))
	return err
}

// Read consumes updates until EOF. Malformed or oversized frames stop the stream.
func Read(r io.Reader, update func([]byte)) error {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 4096), base64.StdEncoding.EncodedLen(MaxTextBytes)+2)
	for s.Scan() {
		data, err := base64.StdEncoding.DecodeString(s.Text())
		if err != nil || len(data) > MaxTextBytes || !utf8.Valid(data) {
			return fmt.Errorf("invalid clipboard update")
		}
		update(data)
	}
	return s.Err()
}
