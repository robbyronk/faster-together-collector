// Package cli holds the small bits of user-facing output the collector
// prints. No colors yet; ANSI-friendly TTYs are out of scope for v1.
package cli

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
)

// OpenBrowser tries to launch the user's default browser at `url`, but
// never fails the caller — the URL is also printed in case the launch
// doesn't take.
func OpenBrowser(stdout io.Writer, url string) {
	fmt.Fprintf(stdout, "Open this URL in your browser:\n  %s\n", url)

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	default:
		return
	}

	// Fire-and-forget. Errors here don't matter — the user can always
	// click/copy-paste the URL.
	_ = cmd.Start()
}
