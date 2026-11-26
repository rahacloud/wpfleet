package admin

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// OpenCommand is the command that opens url in the default browser on goos.
func OpenCommand(goos, url string) []string {
	switch goos {
	case "darwin":
		return []string{"open", url}
	case "windows":
		return []string{"rundll32", "url.dll,FileProtocolHandler", url}
	default:
		return []string{"xdg-open", url}
	}
}

// Open opens url in the default browser. It does not wait for the opener,
// which on some desktops runs as long as the browser it started.
func Open(ctx context.Context, url string) error {
	args := OpenCommand(runtime.GOOS, url)

	//nolint:gosec // the opener is fixed per OS, and the URL is one argument, not shell
	opener := exec.CommandContext(context.WithoutCancel(ctx), args[0], args[1:]...)

	err := opener.Start()
	if err != nil {
		return fmt.Errorf("opening the link: %w", err)
	}

	err = opener.Process.Release()
	if err != nil {
		return fmt.Errorf("opening the link: %w", err)
	}

	return nil
}
