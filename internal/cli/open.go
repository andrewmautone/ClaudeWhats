package cli

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
)

// openBrowserFn is a package var so tests can stub it instead of actually
// spawning a browser.
var openBrowserFn = openBrowser

// openBrowser asks the OS to open path (typically the QR html page) in the
// default browser. Failures are the caller's to report as a warning; opening
// a browser is never worth failing `pair` over.
func openBrowser(path string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	return cmd.Start()
}

// warnf reports a non-fatal problem to stderr, leaving stdout free for the
// command's normal (possibly JSON) output.
func warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "aviso: "+format+"\n", a...)
}
