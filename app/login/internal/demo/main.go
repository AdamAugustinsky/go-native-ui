// Command demo is app/login in a bundled app. Run it with gnui (from the
// module root):
//
//	go run ./cmd/gnui run -c app/login/internal/demo/gnui.json -- -mode status    # the states of the app's login item and of the "hidden" agent
//	go run ./cmd/gnui run -c app/login/internal/demo/gnui.json -- -mode enable    # registers the agent (the system may ask for approval in Login Items)
//	go run ./cmd/gnui run -c app/login/internal/demo/gnui.json -- -mode disable   # unregisters it
//	go run ./cmd/gnui run -c app/login/internal/demo/gnui.json                    # the app itself: opens a window unless --start-hidden
//
// The agent starts it with --start-hidden. In the default mode it opens a
// window, reports what AppKit says about its windows, the Dock policy and
// activation, and quits after -timeout. It prints "R key=value" lines (the live
// test reads them) and appends them to the user's Caches/gnui-login-demo.log,
// where an instance the system launched writes too.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gonativeui/app"
	"gonativeui/app/login"
	"gonativeui/ui"
)

var out io.Writer = os.Stdout

func say(format string, args ...any) { fmt.Fprintf(out, "R "+format+"\n", args...) }

func word(err error) string {
	if err == nil {
		return "none"
	}
	return strings.ReplaceAll(err.Error(), " ", "_")
}

func main() {
	mode := flag.String("mode", "run", "run, status, enable or disable")
	timeout := flag.Duration("timeout", 20*time.Second, "quit after this long (run)")
	flag.Bool("start-hidden", false, "the agent's argument; app.Main and login.StartHidden read it")
	flag.Parse()
	if dir, err := os.UserCacheDir(); err == nil {
		if f, err := os.OpenFile(filepath.Join(dir, "gnui-login-demo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
			out = io.MultiWriter(os.Stdout, f)
		}
	}
	say("start=%s mode=%s pid=%d args=%q", time.Now().Format("15:04:05"), *mode, os.Getpid(), strings.Join(os.Args[1:], ","))
	switch *mode {
	case "status", "enable", "disable":
		item := login.Agent("hidden")
		var st, mst login.State
		var err error
		switch *mode {
		case "status":
			st, err = item.Status()
		case "enable":
			st, err = item.Enable()
		case "disable":
			st, err = item.Disable()
		}
		mst, _ = login.Status()
		say("%s_agent=%d agent_state=%s agent_err=%s app_state=%s", *mode, st, strings.ReplaceAll(st.String(), " ", "_"), word(err), strings.ReplaceAll(mst.String(), " ", "_"))
		return
	}
	d := &demo{}
	time.AfterFunc(*timeout, func() { app.Post(app.Event{ID: 2}, nil) })
	if err := app.Main(app.AppConfig{KeepRunning: true}, d); err != nil {
		say("main_err=%s", word(err))
		os.Exit(1)
	}
}

type demo struct{ app.DefaultHandler }

type empty struct{}

func (empty) Build(c *ui.Context) {}

func (d *demo) Start(a *app.App) error {
	if _, err := a.OpenWindow(app.WindowConfig{Title: "Login Demo", Width: 400, Height: 300}, empty{}); err != nil {
		return err
	}
	time.AfterFunc(1500*time.Millisecond, func() { app.Post(app.Event{ID: 1}, nil) })
	return nil
}

func (d *demo) Event(a *app.App, e *app.Event) {
	if e.Kind != app.EvPost {
		a.DefaultEvent(e)
		return
	}
	if e.ID == 2 {
		a.Quit()
		return
	}
	windows, visible, active, policy := probe()
	say("report=1 start_hidden=%v app_start_hidden=%v windows=%d visible=%d active=%v policy=%d pid=%d",
		login.StartHidden(), a.StartHidden(), windows, visible, active, policy, os.Getpid())
}
