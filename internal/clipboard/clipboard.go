// Package clipboard reads the host system clipboard.
package clipboard

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
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
func Read() (*Content, error) {
	switch runtime.GOOS {
	case "darwin":
		return readDarwin()
	case "linux":
		return readLinux()
	default:
		return nil, fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
}

// readDarwin reads clipboard on macOS.
func readDarwin() (*Content, error) {
	// Check for image first (pngpaste outputs PNG if clipboard has an image)
	if pngpaste, err := exec.LookPath("pngpaste"); err == nil {
		out, err := exec.Command(pngpaste, "-").Output()
		if err == nil && len(out) > 0 {
			return &Content{Data: out, MIMEType: "image/png"}, nil
		}
	}

	// Fall back to text via pbpaste
	out, err := exec.Command("pbpaste").Output()
	if err != nil {
		return nil, fmt.Errorf("pbpaste: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return &Content{Data: out, MIMEType: "text/plain"}, nil
}

// readLinux reads clipboard on Linux using xclip.
func readLinux() (*Content, error) {
	// Check what targets are available
	out, err := exec.Command("xclip", "-selection", "clipboard", "-t", "TARGETS", "-o").Output()
	if err != nil {
		// Try wl-paste for Wayland
		return readLinuxWayland()
	}

	targets := string(out)

	// Check for image targets
	for _, imgType := range []string{"image/png", "image/jpeg", "image/bmp"} {
		if strings.Contains(targets, imgType) {
			imgData, imgErr := exec.Command("xclip", "-selection", "clipboard", "-t", imgType, "-o").Output()
			if imgErr == nil && len(imgData) > 0 {
				return &Content{Data: imgData, MIMEType: imgType}, nil
			}
		}
	}

	// Fall back to text
	textData, err := exec.Command("xclip", "-selection", "clipboard", "-o").Output()
	if err != nil {
		return nil, fmt.Errorf("xclip: %w", err)
	}
	if len(textData) == 0 {
		return nil, nil
	}
	return &Content{Data: textData, MIMEType: "text/plain"}, nil
}

// readLinuxWayland reads clipboard via wl-paste for Wayland sessions.
func readLinuxWayland() (*Content, error) {
	// Check for image
	mimeOut, err := exec.Command("wl-paste", "--list-types").Output()
	if err != nil {
		return nil, fmt.Errorf("no clipboard tool found (tried xclip, wl-paste)")
	}

	mimeTypes := string(mimeOut)
	for _, imgType := range []string{"image/png", "image/jpeg"} {
		if strings.Contains(mimeTypes, imgType) {
			imgData, imgErr := exec.Command("wl-paste", "--type", imgType).Output()
			if imgErr == nil && len(imgData) > 0 {
				return &Content{Data: imgData, MIMEType: imgType}, nil
			}
		}
	}

	// Fall back to text
	textData, err := exec.Command("wl-paste").Output()
	if err != nil {
		return nil, fmt.Errorf("wl-paste: %w", err)
	}
	if len(textData) == 0 {
		return nil, nil
	}
	return &Content{Data: textData, MIMEType: "text/plain"}, nil
}
