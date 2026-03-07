// Package clipboard reads the host system clipboard.
package clipboard

import (
	"encoding/base64"
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

// osascriptImageRead is an AppleScript that reads image data from the clipboard
// using the Objective-C bridge (available since macOS 10.10). It returns the
// image as a base64-encoded string, avoiding binary encoding issues.
const osascriptImageRead = `use framework "AppKit"
use scripting additions
set pb to current application's NSPasteboard's generalPasteboard()
set pngType to current application's NSPasteboardTypePNG
set imgData to pb's dataForType:pngType
if imgData is missing value then
	error "no image"
end if
set b64 to (imgData's base64EncodedStringWithOptions:0) as text
return b64`

// readDarwin reads clipboard on macOS.
func readDarwin() (*Content, error) {
	// Try image first via osascript (always available, no extra installs)
	out, err := exec.Command("osascript", "-e", osascriptImageRead).Output()
	if err == nil && len(out) > 0 {
		// Output is base64-encoded PNG, decode it
		b64 := strings.TrimSpace(string(out))
		imgData, decErr := base64.StdEncoding.DecodeString(b64)
		if decErr == nil && len(imgData) > 0 {
			return &Content{Data: imgData, MIMEType: "image/png"}, nil
		}
	}

	// Fall back to text via pbpaste
	out, err = exec.Command("pbpaste").Output()
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
