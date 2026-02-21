package ui

import (
	"os/exec"
	"runtime"
)

// openURLPlatform opens the given URL in the default browser.
// Returns an error if the platform command fails to start.
func openURLPlatform(url string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", url).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
