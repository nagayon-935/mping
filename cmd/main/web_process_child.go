package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/nagayon-935/mping/internal/privilege"
	"github.com/nagayon-935/mping/internal/web"
	"github.com/spf13/pflag"
)

// webProcessCommand is the hidden subcommand mping runs its web UI
// frontend as.
const webProcessCommand = "__web"

// runWebProcess is `mping __web`: drop privileges for good, serve the
// frontend in front of the backend socket, report "ready <url>" (or
// "error <message>") on out, and exit once in reaches EOF — when mping
// closes it or exits. On the way out it removes the socket and its
// directory, which a killed mping cannot do itself.
func runWebProcess(args []string, in io.Reader, out io.Writer) int {
	fs := pflag.NewFlagSet(webProcessCommand, pflag.ContinueOnError)
	fs.SetOutput(io.Discard)
	socket := fs.String("socket", "", "")
	port := fs.Int("port", 0, "")
	uid := fs.Int("uid", -1, "")
	gid := fs.Int("gid", -1, "")
	fail := func(err error) int {
		fmt.Fprintf(out, "error %v\n", err)
		return 1
	}
	if err := fs.Parse(args); err != nil {
		return fail(err)
	}
	if *uid >= 0 {
		if err := privilege.DropTo(*uid, *gid); err != nil {
			return fail(fmt.Errorf("web UI process: %w", err))
		}
	}
	srv, err := web.StartFrontend(web.FrontendOptions{Port: *port, Backend: *socket})
	if err != nil {
		return fail(err)
	}
	fmt.Fprintf(out, "ready %s\n", srv.URL())
	_, _ = io.Copy(io.Discard, in)
	_ = srv.Close()
	// Only the socket and the then-empty directory: never anything else.
	_ = os.Remove(*socket)
	_ = os.Remove(filepath.Dir(*socket))
	return 0
}
