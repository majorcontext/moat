package term

import "io"

const (
	// ctrlV is the byte value for Ctrl+V (ASCII SYN, 0x16).
	ctrlV byte = 0x16
)

// ClipboardProxy wraps a reader and calls a callback when Ctrl+V (0x16)
// is detected in the byte stream. The byte is still forwarded to the
// consumer so the agent can proceed with its own clipboard read logic.
type ClipboardProxy struct {
	r       io.Reader
	onCtrlV func() // called synchronously when 0x16 is detected
}

// NewClipboardProxy creates a ClipboardProxy that wraps the given reader.
// The onCtrlV callback is called synchronously during Read() calls before
// the 0x16 byte is returned. It should write clipboard data into the
// container so the agent's subsequent clipboard read succeeds.
//
// The callback must use a timeout to avoid blocking stdin indefinitely.
func NewClipboardProxy(r io.Reader, onCtrlV func()) *ClipboardProxy {
	return &ClipboardProxy{r: r, onCtrlV: onCtrlV}
}

// Read implements io.Reader. It scans for 0x16 bytes and calls onCtrlV
// for each one found. All bytes (including 0x16) are passed through.
func (c *ClipboardProxy) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 && c.onCtrlV != nil {
		for i := 0; i < n; i++ {
			if p[i] == ctrlV {
				c.onCtrlV()
			}
		}
	}
	return n, err
}
