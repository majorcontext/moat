// Package clipboard reads the host system clipboard.
package clipboard

import (
	"errors"
	"fmt"
	"strings"

	nativeclipboard "github.com/aymanbagabas/go-nativeclipboard"
)

// Content holds clipboard data and its MIME type.
type Content struct {
	Data     []byte
	MIMEType string // e.g., "text/plain", "image/png"
}

// IsImage returns true if the content is an image type.
func (c *Content) IsImage() bool {
	return strings.HasPrefix(c.MIMEType, "image/")
}

// MIMEToXclipTarget converts a MIME type to an xclip -target value.
func MIMEToXclipTarget(mime string) string {
	switch {
	case strings.HasPrefix(mime, "image/"):
		return mime
	default:
		return "UTF8_STRING"
	}
}

// Read reads the current clipboard content from the host.
// Returns nil Content if the clipboard is empty.
// Returns an error if the clipboard is unavailable on this platform.
func Read() (content *Content, err error) {
	// purego can panic when native libraries (e.g. libX11) are missing.
	// Recover gracefully instead of crashing the process.
	defer func() {
		if r := recover(); r != nil {
			content = nil
			err = fmt.Errorf("clipboard unavailable: %v", r)
		}
	}()

	// Try image first (returns PNG bytes)
	img, imgErr := nativeclipboard.Image.Read()
	if imgErr == nil && len(img) > 0 {
		return &Content{Data: img, MIMEType: "image/png"}, nil
	}

	// Fall back to text
	text, textErr := nativeclipboard.Text.Read()
	if textErr != nil {
		// If both failed with ErrUnavailable, report that
		if errors.Is(textErr, nativeclipboard.ErrUnavailable) {
			return nil, fmt.Errorf("clipboard not available on this platform")
		}
		return nil, textErr
	}
	if len(text) == 0 {
		return nil, nil
	}
	return &Content{Data: text, MIMEType: "text/plain"}, nil
}
