package session_test

import (
	"os"

	"gonativeui/term/session"
)

// A program in an in-process session, its output written to stdout as it
// comes, then the session closed. With session.DialOrLaunch instead of
// NewLocal the same calls run it in the daemon, where a Persist session
// outlives the view.
func Example() {
	svc := session.NewLocal(session.Config{})
	defer svc.Close()
	in, err := svc.Create(session.Options{Argv: []string{"/bin/sh", "-c", "echo hello"}, Size: session.Size{Cols: 80, Rows: 24}})
	if err != nil {
		return
	}
	a, err := svc.Attach(in.ID, os.Stdout, session.Size{}) // a terminal view's inbox, in an app
	if err != nil {
		return
	}
	<-a.Done() // the program exited and its output was written
	code, _ := a.Exit()
	_ = code
}
