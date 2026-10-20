// Command demo is app/notify in a bundled app: it posts a note with a reply
// action and reports what comes back. Run it with gnui (from the module
// root):
//
//	go run ./cmd/gnui run -c app/notify/internal/demo/gnui.json                    # the round trip: asks for permission if needed, waits for a reply
//	go run ./cmd/gnui run -c app/notify/internal/demo/gnui.json -- -mode check     # never asks; posts (and removes) only if already allowed
//	go run ./cmd/gnui run -c app/notify/internal/demo/gnui.json -- -mode post      # posts the note and quits: reply to it later, which launches the app
//	go run ./cmd/gnui run -c app/notify/internal/demo/gnui.json -- -mode idle      # the delegate installed, nothing else, until -timeout
//
// It prints "R key=value" lines (the live test reads them) and appends them
// to $TMPDIR/notify-demo.log, where an instance the system launched for a
// reply writes too. It quits when done or after -timeout.
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
	"gonativeui/app/notify"
)

const actReply, actLater = 1, 2

var out io.Writer = os.Stdout

func say(format string, args ...any) { fmt.Fprintf(out, "R "+format+"\n", args...) }

func main() {
	mode := flag.String("mode", "hand", "hand, check, post or idle")
	timeout := flag.Duration("timeout", 3*time.Minute, "quit after this long")
	bare := flag.Bool("bare", false, "idle mode without notify.Setup (the baseline of the idle cost)")
	flag.Parse()
	if f, err := os.OpenFile(filepath.Join(os.TempDir(), "notify-demo.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		out = io.MultiWriter(os.Stdout, f)
	}
	say("start=%s mode=%s pid=%d args=%d", time.Now().Format("15:04:05"), *mode, os.Getpid(), len(os.Args)-1)
	if *bare {
		time.AfterFunc(*timeout, func() { app.Post(app.Event{ID: 1}, nil) })
		app.Main(app.AppConfig{KeepRunning: true, Policy: app.PolicyAccessory}, bareApp{})
		return
	}
	t0 := time.Now()
	err := notify.Setup(notify.Options{Foreground: true})
	say("available=%v setup_us=%d setup_err=%s", notify.Available(), time.Since(t0).Microseconds(), word(err))
	if err != nil {
		os.Exit(1)
	}
	d := &demo{mode: *mode}
	policy := app.PolicyRegular
	if d.mode == "check" || d.mode == "idle" {
		policy = app.PolicyAccessory
	}
	time.AfterFunc(*timeout, func() {
		if d.mode != "idle" {
			say("timeout=true")
		}
		app.Post(app.Event{ID: 1}, nil)
	})
	if err := app.Main(app.AppConfig{KeepRunning: true, Policy: policy}, d); err != nil {
		say("main_err=%s", word(err))
		os.Exit(1)
	}
}

// bareApp idles without notifications until the timeout.
type bareApp struct{ app.DefaultHandler }

func (bareApp) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost {
		a.Quit()
	}
}

type demo struct {
	app.DefaultHandler
	mode   string
	posted notify.PostResult
}

func (d *demo) Start(a *app.App) error {
	err := notify.RegisterCategories(notify.Category{ID: "demo", Actions: []notify.Action{
		{ID: actReply, Title: "Reply", Reply: true, ReplyPlaceholder: "Type a reply"},
		{ID: actLater, Title: "Later"},
	}})
	say("categories_err=%s", word(err))
	if _, err := notify.Status(); err != nil {
		say("status_err=%s", word(err))
	}
	return nil
}

func (d *demo) quit(a *app.App) {
	live, stale := blockCounts()
	say("blocks_live=%d stale_calls=%d slots_held=%d", live, stale, notify.ReadStats().Reserved)
	a.Quit()
}

func (d *demo) Event(a *app.App, e *app.Event) {
	if e.Kind == app.EvPost && e.ID == 1 { // the timeout, or done
		d.quit(a)
		return
	}
	if r, ok := notify.ResultOf(e); ok {
		d.result(a, r)
		return
	}
	if r, ok := notify.ResponseOf(e); ok {
		say("response_action=%d response_reply=%q response_userdata=%q response_ticket_matches=%v",
			r.Action, r.Reply, r.UserData, r.Ticket == d.posted.Response)
		notify.Post(notify.Note{Title: "Notify Demo", Body: "Got: " + string(r.Reply)})
		time.AfterFunc(3*time.Second, func() { app.Post(app.Event{ID: 1}, nil) })
		return
	}
	a.DefaultEvent(e)
}

func (d *demo) result(a *app.App, r notify.Result) {
	switch r.Kind {
	case notify.ResultRestored:
		say("restored=%d overflow=%d", r.Restored, r.Overflow)
	case notify.ResultStatus:
		say("status=%d", r.Status)
		allowed := r.Status == notify.StatusAuthorized || r.Status == notify.StatusProvisional
		switch {
		case d.mode == "idle":
		case allowed || d.mode == "check":
			d.post(a) // check, not allowed: the system refuses the post (an error result); nothing is shown
		case r.Status == notify.StatusNotDetermined:
			say("asking=true")
			if _, err := notify.RequestPermission(notify.Alert | notify.Sound); err != nil {
				say("permission_err=%s", word(err))
				d.quit(a)
			}
		default:
			say("denied=true hint=allow_notifications_for_Notify_Demo_in_System_Settings")
			d.quit(a)
		}
	case notify.ResultPermission:
		say("granted=%v permission_err=%s", r.Granted, text(r.Err))
		if r.Granted {
			d.post(a)
		} else {
			d.quit(a)
		}
	case notify.ResultPost:
		say("post_code=%d post_err=%s", r.ErrCode, text(r.Err))
		switch {
		case d.mode == "check":
			if r.ErrCode == 0 {
				notify.Remove(d.posted.ID)
			}
			d.quit(a)
		case r.ErrCode != 0 || d.mode == "post":
			d.quit(a)
		default:
			say("waiting_for_reply=true")
		}
	}
}

func (d *demo) post(a *app.App) {
	n := notify.Note{ID: "demo-1", Thread: "demo", Title: "Notify Demo", Body: "Reply to this note.",
		Category: "demo", UserData: []byte("demo-user-data"), Sound: notify.SoundDefault}
	if d.mode == "check" {
		n.Body, n.Sound = "A check of app/notify; removed at once.", ""
	}
	r, err := notify.Post(n)
	d.posted = r
	say("post_reservation=%d post_err=%s", r.Reservation, word(err))
	if err != nil {
		d.quit(a)
	}
}

// word is an error as one word for the report ("none" for nil).
func word(err error) string {
	if err == nil {
		return "none"
	}
	return text(err.Error())
}

// text is s as one word for the report ("none" for "").
func text(s string) string {
	if s == "" {
		return "none"
	}
	return strings.ReplaceAll(s, " ", "_")
}
