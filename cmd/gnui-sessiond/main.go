// Command gnui-sessiond is the term/session daemon on its own, for an app
// that would rather bundle a helper than launch itself in a daemon mode
// (docs/design/terminal-sessions.md). macOS and Linux only.
//
//	gnui-sessiond -dir DIR        serve DIR's socket until shut down
//	gnui-sessiond -app NAME       the same in session.DefaultDir(NAME)
//	gnui-sessiond -idle 1m        and exit after a minute with no sessions and no clients
package main

import (
	"flag"
	"fmt"
	"os"

	"gonativeui/term/session"
)

func main() {
	app := flag.String("app", "gnui", "the app whose daemon this is, for the default directory")
	dir := flag.String("dir", "", "the daemon's directory (default: session.DefaultDir of -app)")
	idle := flag.Duration("idle", 0, "exit after this long with no sessions and no clients (0: never)")
	ring := flag.Int("ring", 0, "bytes of output each session keeps for replay (0: 1 MiB)")
	flag.Parse()
	if *dir == "" {
		*dir = session.DefaultDir(*app)
	}
	o := session.ServeOptions{Config: session.Config{RingSize: *ring}, IdleExit: *idle}
	if err := session.RunDaemon(*dir, o); err != nil {
		fmt.Fprintln(os.Stderr, "gnui-sessiond:", err)
		os.Exit(1)
	}
}
