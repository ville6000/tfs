// Package browser opens URLs in the user's default browser.
package browser

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"runtime"
)

// EnvBrowser overrides the command used to open URLs (e.g. "echo" to just print them).
const EnvBrowser = "TFS_BROWSER"

// Open opens an http(s) URL.
func Open(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Errorf("refusing to open non-http URL %q", rawURL)
	}
	var cmd *exec.Cmd
	switch b := os.Getenv(EnvBrowser); {
	case b != "":
		cmd = exec.Command(b, rawURL)
		cmd.Stdout = os.Stdout
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", rawURL)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("could not open browser: %w (URL: %s)", err, rawURL)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
