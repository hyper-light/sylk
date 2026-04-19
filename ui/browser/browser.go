// Package browser opens URLs in the user's default browser via the OS-provided
// command (open / xdg-open / rundll32). This is a user-invoked UI action, not
// an agent command, and is exempt from the strict-disk broker requirement.
package browser

import (
	"os/exec"
	"runtime"
)

// Open launches the user's default browser pointed at the given URL. Returns
// an error if the platform command fails to start.
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
