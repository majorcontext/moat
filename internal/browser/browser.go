// Package browser opens URLs in the user's default web browser.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the user's default browser pointed at url. It is best-effort:
// it returns the error from spawning the opener, and callers should always
// print the URL too so headless/SSH users can open it manually.
func Open(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
