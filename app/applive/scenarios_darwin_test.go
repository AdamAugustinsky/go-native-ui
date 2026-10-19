//go:build darwin

package applive

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"gonativeui/app"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
)

// handler is a scenario's app: event sees every event first and returns true
// when it handled it; the rest goes to DefaultEvent.
type handler struct {
	start    func(a *app.App) error
	event    func(a *app.App, e *app.Event) bool
	requests int
}

func (h *handler) Start(a *app.App) error {
	swizzleReply()
	if h.start != nil {
		return h.start(a)
	}
	return nil
}

func (h *handler) Event(a *app.App, e *app.Event) {
	switch e.Kind {
	case app.EvQuitRequested:
		h.requests++
		quitAsked++
	case app.EvWillTerminate:
		say("terminated=1 replies=%d early=%d ours=%d last=%s requests=%d", replies.Load(), earlyReplies.Load(), platform.RepliesSent(), lastTerminate(), h.requests)
	}
	if h.event != nil && h.event(a, e) {
		return
	}
	a.DefaultEvent(e)
}

func envInt(k string) int { n, _ := strconv.Atoi(os.Getenv(k)); return n }

func windowNumber(w objc.ID) uintptr { return uintptr(objc.Send(w, objc.Sel("windowNumber"))) }

func init() {
	// route: the Dock's Quit (ROUTE=dock) or ⌘Q (ROUTE=cmdq) with WINDOWS
	// windows open; the default handler allows it.
	scenarios["route"] = func() {
		route, n := os.Getenv("ROUTE"), envInt("WINDOWS")
		h := &handler{}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, n, &view{}); !ok {
				os.Exit(0)
			}
			after(400*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch e.Kind {
			case app.EvPost:
				if route == "dock" {
					quitLater()
				} else {
					var num uintptr
					if a.Win != nil {
						num = windowNumber(ourWindow(a))
					}
					postCmdQ(num)
				}
				return true
			case app.EvQuitRequested:
				say("reason=%d windows=%d asking=%v", e.A, visibleWindows(), platform.QuitAsking())
			}
			return false
		}
		err := app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned err=%v", err)
	}

	// latch: answers inside applicationShouldTerminate: (MODE allow,
	// refuse or nested), none of which may reach replyToApplicationShouldTerminate:.
	scenarios["latch"] = func() {
		mode := os.Getenv("MODE")
		var theApp *app.App
		h := &handler{}
		h.start = func(a *app.App) error {
			theApp = a
			d := objc.Define("GNUITestNested", objc.Cls("NSObject"), nil)
			d.Method(objc.Sel("gnuiNested"), "v@:", func(*ffi.CB) { theApp.ReplyQuit(true) })
			nested := objc.Send(objc.Send(d.Register(), objc.Sel("alloc")), objc.Sel("init"))
			h.event = func(a *app.App, e *app.Event) bool {
				switch {
				case e.Kind == app.EvPost && e.ID == 1:
					quitLater()
				case e.Kind == app.EvQuitRequested && mode == "allow":
					a.ReplyQuit(true)
				case e.Kind == app.EvQuitRequested && mode == "refuse":
					a.ReplyQuit(false)
					after(300*time.Millisecond, 2)
				case e.Kind == app.EvQuitRequested && mode == "nested":
					objc.Send(nested, objc.Sel("gnuiNested")) // a native callback that answers, nested in the event
				case e.Kind == app.EvPost && e.ID == 2:
					say("alive=1 early=%d ours=%d last=%s pending=%v", earlyReplies.Load(), platform.RepliesSent(), lastTerminate(), a.QuitPending())
					a.Quit()
				default:
					return false
				}
				return true
			}
			after(300*time.Millisecond, 1)
			return nil
		}
		err := app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned err=%v", err)
	}

	// refuseallow: a refused quit, then a second one that is allowed.
	scenarios["refuseallow"] = func() {
		h := &handler{}
		h.start = func(a *app.App) error { after(300*time.Millisecond, 1); return nil }
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				quitLater()
			case e.Kind == app.EvQuitRequested && h.requests == 1:
				say("first=refused")
				a.ReplyQuit(false)
				after(300*time.Millisecond, 2)
			case e.Kind == app.EvQuitRequested:
				say("second=allowed")
				a.ReplyQuit(true)
			case e.Kind == app.EvPost && e.ID == 2:
				postCmdQ(0)
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}

	// quitmodel: ⌘Q is the native menu's Quit item, which the model decides:
	// while the model's Quit command is disabled ⌘Q asks nothing; enabled, it
	// asks (WINDOWS 0 or 1).
	scenarios["quitmodel"] = func() {
		n := envInt("WINDOWS")
		m, err := menu.NewModel([]menu.Command{{ID: 9, Role: menu.RoleQuit, Scope: menu.ScopeApp, Shortcut: menu.Shortcut{Key: 'Q', Primary: true}}},
			[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 9}}}}, []int{0})
		if err != nil {
			fail("model: %v", err)
		}
		m.SetEnabled(9, false)
		h := &handler{}
		cmdQ := func(a *app.App) {
			var num uintptr
			if a.Win != nil {
				num = windowNumber(ourWindow(a))
			}
			postCmdQ(num)
		}
		h.start = func(a *app.App) error {
			if _, ok := openWindows(a, n, &view{}); !ok {
				os.Exit(0)
			}
			a.SetMenu(m)
			after(500*time.Millisecond, 1)
			return nil
		}
		h.event = func(a *app.App, e *app.Event) bool {
			switch {
			case e.Kind == app.EvPost && e.ID == 1:
				cmdQ(a)
				after(800*time.Millisecond, 2)
			case e.Kind == app.EvPost && e.ID == 2:
				say("requests_disabled=%d", h.requests)
				m.SetEnabled(9, true)
				cmdQ(a)
			case e.Kind == app.EvQuitRequested:
				say("reason=%d", e.A)
				return false // the default allows it
			default:
				return false
			}
			return true
		}
		app.Main(app.AppConfig{KeepRunning: true}, h)
		say("main=returned")
	}
	scenarios["delayed"] = delayedScenario
	scenarios["noreply"] = noReplyScenario
	scenarios["saturation"] = saturationScenario
	scenarios["opens"] = opensScenario
	scenarios["services"] = servicesScenario
	scenarios["hidden"] = hiddenScenario
}

// ---- Alert sheets (the native dialogs are N2's; these are the test's own) ----

type sheets struct {
	t    *platform.Tickets
	kind *objc.BlockKind
}

func newSheets() *sheets {
	s := &sheets{t: platform.NewTickets(platform.SourceApp+1, 8, 16)}
	s.kind = objc.NewBlockKind("v16@?0q8", true, func(ctx uintptr, c *ffi.CB) {
		s.t.Complete(platform.Ticket(ctx), int64(c.Int(1)), 0, nil)
	}, nil)
	return s
}

// begin shows an alert sheet with message on win; its answer arrives as an
// EvService with the returned ticket. It returns the alert.
func (s *sheets) begin(win objc.ID, message string) (objc.ID, platform.Ticket) {
	tk, err := s.t.Reserve(0)
	if err != nil {
		fail("reserve: %v", err)
	}
	req, err := s.kind.New(uintptr(tk))
	if err != nil {
		fail("block: %v", err)
	}
	alert := objc.Send(objc.Send(objc.Cls("NSAlert"), objc.Sel("alloc")), objc.Sel("init"))
	objc.Send1(alert, objc.Sel("setMessageText:"), uintptr(objc.Str(message)))
	objc.Send1(alert, objc.Sel("addButtonWithTitle:"), uintptr(objc.Str("Save")))
	objc.Send1(alert, objc.Sel("addButtonWithTitle:"), uintptr(objc.Str("Cancel")))
	objc.Send2(alert, objc.Sel("beginSheetModalForWindow:completionHandler:"), uintptr(win), uintptr(req.Block()))
	req.Sent()
	return alert, tk
}

// end ends an alert's sheet with code, as a click on its button would.
func endSheet(win, alert objc.ID, code int) {
	objc.Send2(win, objc.Sel("endSheet:returnCode:"), uintptr(objc.Send(alert, objc.Sel("window"))), uintptr(code))
}

// delayed: the quit answer is deferred 2 s; meanwhile a worker's posts and an
// alert sheet's completion are delivered during AppKit's wait, and a "Save
// changes?" sheet asked for then opens; its answer allows the quit. The
// sheets begin after the request: with a sheet up, AppKit's terminate:
// returns without asking the delegate.
func delayedScenario() {
	v := &view{}
	var (
		sh                      *sheets
		report, save            objc.ID
		reportTk, saveTk        platform.Ticket
		quitT0                  time.Time
		workerDuring, workerAll int
		reportDuring, saveOpen  bool
		buildsAtQuit            int64
	)
	h := &handler{}
	h.start = func(a *app.App) error {
		if _, ok := openWindows(a, 1, v); !ok {
			os.Exit(0)
		}
		sh = newSheets()
		after(500*time.Millisecond, 1)
		return nil
	}
	h.event = func(a *app.App, e *app.Event) bool {
		switch {
		case e.Kind == app.EvPost && e.ID == 1:
			quitLater()
		case e.Kind == app.EvQuitRequested:
			quitT0, buildsAtQuit = time.Now(), v.builds.Load()
			go func() {
				for i := 0; i < 5; i++ {
					time.Sleep(100 * time.Millisecond)
					postEvent(10, "worker")
				}
			}()
			// An alert sheet begins during the wait (AppKit asks nothing while a
			// sheet is up: terminate: returns at once), and ends 700 ms later.
			a.AtSafePoint(func() { report, reportTk = sh.begin(ourWindow(a), "Report") })
			after(700*time.Millisecond, 20)
			// no answer: AppKit waits
		case e.Kind == app.EvPost && e.ID == 10:
			workerAll++
			if a.QuitPending() {
				workerDuring++
			}
		case e.Kind == app.EvPost && e.ID == 20:
			a.AtSafePoint(func() { endSheet(ourWindow(a), report, 1000) })
		case e.Kind == app.EvService && e.ID == uint64(reportTk):
			reportDuring = a.QuitPending()
			a.AtSafePoint(func() { // runs from the dispatcher: our loop is suspended in AppKit's wait
				save, saveTk = sh.begin(ourWindow(a), "Save changes?")
				saveOpen = objc.Send(ourWindow(a), objc.Sel("attachedSheet")) != 0 && a.QuitPending()
				go func() {
					time.Sleep(time.Until(quitT0.Add(2 * time.Second)))
					postEvent(30, "")
				}()
			})
		case e.Kind == app.EvPost && e.ID == 30:
			a.AtSafePoint(func() { endSheet(ourWindow(a), save, 1000) })
		case e.Kind == app.EvService && e.ID == uint64(saveTk):
			say("worker=%d/%d report_during=%v save_open=%v save_answer=%d pending=%v waited_ms=%d builds_during=%d",
				workerDuring, workerAll, reportDuring, saveOpen, e.A, a.QuitPending(), time.Since(quitT0).Milliseconds(), v.builds.Load()-buildsAtQuit)
			a.ReplyQuit(e.A == 1000)
		default:
			return false
		}
		return true
	}
	app.Main(app.AppConfig{}, h)
	say("main=returned")
}

// noreply: the handler never answers. WAIT s later the app still runs and
// answers events; with DEADLINE ms the deadline's ACTION (cancel, allow)
// applies.
func noReplyScenario() {
	wait, deadline := envInt("WAIT"), envInt("DEADLINE")
	act := app.QuitCancel
	if os.Getenv("ACTION") == "allow" {
		act = app.QuitAllow
	}
	if wait > 0 {
		watchdog(time.Duration(wait+60) * time.Second)
	}
	var t0 time.Time
	h := &handler{}
	h.start = func(a *app.App) error { after(300*time.Millisecond, 1); return nil }
	h.event = func(a *app.App, e *app.Event) bool {
		switch {
		case e.Kind == app.EvPost && e.ID == 1:
			quitLater()
		case e.Kind == app.EvQuitRequested:
			t0 = time.Now()
			if wait > 0 {
				after(time.Duration(wait)*time.Second, 2)
			}
		case e.Kind == app.EvPost && e.ID == 2:
			say("alive=1 pending=%v waited_s=%.1f", a.QuitPending(), time.Since(t0).Seconds())
			a.ReplyQuit(false)
			after(100*time.Millisecond, 3)
		case e.Kind == app.EvQuitDeadline:
			say("deadline=1 action=%d after_ms=%d pending=%v", e.A, time.Since(t0).Milliseconds(), a.QuitPending())
			after(200*time.Millisecond, 3)
		case e.Kind == app.EvPost && e.ID == 3:
			say("continued=1 pending=%v", a.QuitPending())
			a.Quit()
		default:
			return false
		}
		return true
	}
	app.Main(app.AppConfig{KeepRunning: true, QuitDeadline: time.Duration(deadline) * time.Millisecond, QuitDeadlineAction: act}, h)
	say("main=returned")
}

// saturation: app.Post and an observation flooded from 8 goroutines while 64
// completions (a full table: the 65th request is refused), responses to 32
// of 256 actionable notifications (the 257th has no slot), 40 second-instance
// requests (32 accepted, 8 busy and accepted on retry) and a quit request
// arrive. The quit is answered once everything accepted was delivered.
func saturationScenario() {
	const (
		nDialogs   = platform.MaxTickets
		nResponses = 32
		nInstance  = 40
	)
	var (
		dialogs   *platform.Tickets
		responses *platform.Tickets
		ring      *platform.Ring
		obs       *platform.Observation

		postsOK, postsRefused atomic.Int64
		obsSet                atomic.Int64
		floodsDone            atomic.Bool
		acks                  atomic.Int64
		firstAccepted, busy   atomic.Int64
		retriesDone           atomic.Int64
		stop                  atomic.Bool
		floods                sync.WaitGroup

		gotPosts, gotObs, gotDialogs, gotResponses, gotInstance int64
		badReplies                                              int
		answered                                                bool
		respTk                                                  []platform.Ticket
		dialogTk                                                []platform.Ticket
	)
	h := &handler{}
	h.start = func(a *app.App) error {
		if _, ok := openWindows(a, 1, &view{}); !ok {
			os.Exit(0)
		}
		dialogs = platform.NewTickets(platform.SourceDialog, platform.MaxTickets, 32)
		responses = platform.NewTickets(platform.SourceNotifyResponse, platform.MaxNotifyResponses, platform.MaxNotifyReply)
		ring = platform.NewRing(platform.SourceInstance, platform.RingRecords, platform.RingBytes)
		obs = platform.NewObservation(platform.SourceMedia)
		for i := 0; i < nDialogs; i++ {
			tk, err := dialogs.Reserve(1)
			if err != nil {
				fail("dialog %d: %v", i, err)
			}
			dialogTk = append(dialogTk, tk)
		}
		_, err := dialogs.Reserve(1)
		say("dialog65_busy=%v", errors.Is(err, platform.ErrBusy))
		for i := 0; i < platform.MaxNotifyResponses; i++ {
			tk, err := responses.Reserve(0)
			if err != nil {
				fail("response slot %d: %v", i, err)
			}
			respTk = append(respTk, tk)
		}
		_, err = responses.Reserve(0)
		say("notify257_without_actions=%v", errors.Is(err, platform.ErrBusy)) // the notification is posted without actions
		after(300*time.Millisecond, 1)
		return nil
	}
	check := func(a *app.App) {
		if answered || !floodsDone.Load() || gotPosts != postsOK.Load() || gotDialogs != nDialogs ||
			gotResponses != nResponses || gotInstance != nInstance || retriesDone.Load() != busy.Load() || acks.Load() != nResponses ||
			gotObs != int64(obs.Sets()-obs.Coalesced()) { // the last value set may still be on its way
			return
		}
		answered = true
		a.ReplyQuit(true)
	}
	h.event = func(a *app.App, e *app.Event) bool {
		switch {
		case e.Kind == app.EvPost && e.ID == 1:
			for g := 0; g < 8; g++ {
				floods.Add(1)
				go func() {
					defer floods.Done()
					for i := 0; !stop.Load(); i++ {
						if app.Post(app.Event{ID: 100, A: int64(g)}, []byte("flood")) {
							postsOK.Add(1)
						} else {
							postsRefused.Add(1)
							time.Sleep(50 * time.Microsecond)
						}
						obs.Set(int64(i), int64(g))
						obsSet.Add(1)
					}
				}()
			}
			for i, tk := range dialogTk {
				go func() {
					time.Sleep(time.Duration(i) * 5 * time.Millisecond)
					if !dialogs.Complete(tk, int64(i), 0, []byte("paths")) {
						fail("dialog completion %d not taken", i)
					}
				}()
			}
			for i := 0; i < nResponses; i++ {
				go func() {
					time.Sleep(time.Duration(i) * 7 * time.Millisecond)
					// The response is stored in its slot before the system's
					// completion handler is called (the acknowledgement).
					if responses.Complete(respTk[i], int64(i), 0, []byte(fmt.Sprintf("reply-%d", i))) {
						acks.Add(1)
					}
				}()
			}
			quitLater()
		case e.Kind == app.EvQuitRequested:
			// 40 second-instance requests while the main thread is held here:
			// nothing drains, so exactly the ring's 32 are accepted.
			var first sync.WaitGroup
			for i := 0; i < nInstance; i++ {
				first.Add(1)
				go func() {
					data := []byte(fmt.Sprintf("--open=/tmp/doc-%d", i))
					if ring.Offer(int64(i), 0, data) {
						firstAccepted.Add(1)
						first.Done()
						return
					}
					busy.Add(1)
					first.Done()
					for d := time.Millisecond; ; d = min(2*d, 100*time.Millisecond) { // the secondary's backoff
						time.Sleep(d)
						if ring.Offer(int64(i), 0, data) {
							retriesDone.Add(1)
							return
						}
					}
				}()
			}
			first.Wait()
			say("instance_first_accepted=%d instance_busy=%d", firstAccepted.Load(), busy.Load())
			time.AfterFunc(1500*time.Millisecond, func() { // the floods end; everything they had accepted is still delivered
				stop.Store(true)
				floods.Wait()
				floodsDone.Store(true)
				for { // a last event, so the check runs after the floods are counted
					if app.Post(app.Event{ID: 2}, nil) {
						return
					}
					time.Sleep(time.Millisecond)
				}
			})
			// no answer yet: AppKit waits while the rest arrives
		case e.Kind == app.EvPost && e.ID == 100:
			gotPosts++
		case e.Kind == app.EvPost && e.ID == 2:
		case e.Kind == app.EvService && e.Source == platform.SourceMedia:
			gotObs++
		case e.Kind == app.EvService && e.Source == platform.SourceDialog:
			gotDialogs++
		case e.Kind == app.EvService && e.Source == platform.SourceNotifyResponse:
			gotResponses++
			if string(e.Data()) != fmt.Sprintf("reply-%d", e.A) {
				badReplies++
			}
		case e.Kind == app.EvService && e.Source == platform.SourceInstance:
			gotInstance++
		case e.Kind == app.EvWillTerminate:
			say("posts_ok=%d posts_refused=%d posts_delivered=%d obs_set=%d obs_coalesced=%d obs_delivered=%d dialogs=%d responses=%d bad_replies=%d acks=%d instance=%d retried=%d",
				postsOK.Load(), postsRefused.Load(), gotPosts, obs.Sets(), obs.Coalesced(), gotObs, gotDialogs, gotResponses, badReplies, acks.Load(), gotInstance, retriesDone.Load())
			return false
		default:
			return false
		}
		check(a)
		return true
	}
	app.Main(app.AppConfig{}, h)
	say("main=returned")
}

// opens: a URL handed over during Start (the startup list), then a get-URL
// Apple event and an open-documents Apple event.
func opensScenario() {
	var order []string
	dir, _ := os.MkdirTemp("", "applive")
	doc := filepath.Join(dir, "note.txt")
	os.WriteFile(doc, []byte("x"), 0o644)
	defer os.RemoveAll(dir)
	h := &handler{}
	h.start = func(a *app.App) error {
		url := objc.Send1(objc.Cls("NSURL"), objc.Sel("URLWithString:"), uintptr(objc.Str("gnui-test://startup")))
		arr := objc.Send1(objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), uintptr(url))
		objc.Send2(objc.ID(platform.AppDelegate()), objc.Sel("application:openURLs:"), uintptr(nsApp()), uintptr(arr))
		order = append(order, "start-returns")
		after(300*time.Millisecond, 1)
		return nil
	}
	h.event = func(a *app.App, e *app.Event) bool {
		switch e.Kind {
		case app.EvOpenURLs:
			order = append(order, "urls:"+strings.Join(e.Strings(), ","))
		case app.EvOpenFiles:
			order = append(order, "files:"+strings.Join(e.Strings(), ","))
		case app.EvPost:
			if e.ID == 2 {
				say("order=%s", strings.Join(order, "|"))
				a.Quit()
				return true
			}
			say("geturl_via=%s", sendAppleEvent(0x4755524C, 0x4755524C, func(ev objc.ID) {
				s := objc.Send1(objc.Cls("NSAppleEventDescriptor"), objc.Sel("descriptorWithString:"), uintptr(objc.Str("gnui-test://hello")))
				objc.Send2(ev, objc.Sel("setParamDescriptor:forKeyword:"), uintptr(s), 0x2D2D2D2D)
			}, nil))
			say("odoc_via=%s", sendAppleEvent(0x61657674, 0x6F646F63, func(ev objc.ID) { // 'aevt' 'odoc'
				u := objc.Send1(objc.Cls("NSURL"), objc.Sel("fileURLWithPath:"), uintptr(objc.Str(doc)))
				d := objc.Send1(objc.Cls("NSAppleEventDescriptor"), objc.Sel("descriptorWithFileURL:"), uintptr(u))
				list := objc.Send(objc.Cls("NSAppleEventDescriptor"), objc.Sel("listDescriptor"))
				objc.Send2(list, objc.Sel("insertDescriptor:atIndex:"), uintptr(d), 0)
				objc.Send2(ev, objc.Sel("setParamDescriptor:forKeyword:"), uintptr(list), 0x2D2D2D2D)
			}, nil))
			after(1500*time.Millisecond, 2)
		default:
			return false
		}
		return true
	}
	app.Main(app.AppConfig{KeepRunning: true}, h)
	say("doc=%s main=returned", doc)
}

// services: no window, the accessory policy; the Dock menu's command, a
// reopen, the system's sleep, wake, session and screen notifications; and
// the CPU an idle app with no window uses.
func servicesScenario() {
	var got []string
	var idleFrom, idleTries int
	measureIdle := func() {
		go func() { // idle with no window
			time.Sleep(500 * time.Millisecond)
			c0 := cpuNs()
			time.Sleep(3 * time.Second)
			postEvent(2, strconv.FormatInt(cpuNs()-c0, 10))
		}()
	}
	h := &handler{}
	h.start = func(a *app.App) error {
		say("policy=%d", platform.AppActivationPolicy())
		m, err := menu.NewModel([]menu.Command{{ID: 7, Title: "Dock Item", Scope: menu.ScopeApp}},
			[]menu.Menu{{Title: "Dock", Items: []menu.Item{{Kind: menu.ItemCommand, Command: 7}}}}, nil)
		if err != nil {
			fail("model: %v", err)
		}
		a.SetDockMenu(m, 0)
		after(300*time.Millisecond, 1)
		return nil
	}
	h.event = func(a *app.App, e *app.Event) bool {
		switch e.Kind {
		case app.EvPost:
			switch e.ID {
			case 1:
				got = append(got, "post")
				dm := objc.ID(platform.DockMenu())
				if dm == 0 {
					fail("no dock menu")
				}
				item := objc.Send1(dm, objc.Sel("itemAtIndex:"), 0)
				objc.Send3(nsApp(), objc.Sel("sendAction:to:from:"), uintptr(objc.Send(item, objc.Sel("action"))), uintptr(objc.Send(item, objc.Sel("target"))), uintptr(item))
				objc.Send2(objc.ID(platform.AppDelegate()), objc.Sel("applicationShouldHandleReopen:hasVisibleWindows:"), uintptr(nsApp()), 0)
				ws := objc.Send(objc.Send(objc.Cls("NSWorkspace"), objc.Sel("sharedWorkspace")), objc.Sel("notificationCenter"))
				for _, n := range []string{"NSWorkspaceWillSleepNotification", "NSWorkspaceDidWakeNotification",
					"NSWorkspaceSessionDidResignActiveNotification", "NSWorkspaceSessionDidBecomeActiveNotification"} {
					objc.Send2(ws, objc.Sel("postNotificationName:object:"), uintptr(objc.Str(n)), 0)
				}
				dc := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
				objc.Send2(dc, objc.Sel("postNotificationName:object:"), uintptr(objc.Str("NSApplicationDidChangeScreenParametersNotification")), uintptr(nsApp()))
				idleFrom = len(got) // the notifications above are delivered synchronously
				measureIdle()
			case 2:
				ns, _ := strconv.ParseInt(string(e.Data()), 10, 64)
				// A measurement during which the system sent events (a CI
				// runner's display reconfigures now and then) is not idle:
				// it is taken again, up to three times.
				if n := len(got); n != idleFrom && idleTries < 3 {
					idleTries++
					idleFrom = n
					measureIdle()
					return true
				}
				say("events=%s idle_cpu_pct=%.3f idle_tries=%d", strings.Join(got, ","), float64(ns)/3e9*100, idleTries+1)
				a.Quit()
			}
		case app.EvCommand:
			got = append(got, fmt.Sprintf("command%d", e.ID))
		case app.EvReopen:
			got = append(got, fmt.Sprintf("reopen%d", e.A))
		case app.EvWillSleep:
			got = append(got, "sleep")
		case app.EvDidWake:
			got = append(got, "wake")
		case app.EvSessionActive:
			got = append(got, fmt.Sprintf("session%d", e.A))
		case app.EvScreensChanged:
			got = append(got, "screens")
		default:
			return false
		}
		return true
	}
	app.Main(app.AppConfig{KeepRunning: true, Policy: app.PolicyAccessory}, h)
	say("main=returned")
}

// hidden: a window opened while starting hidden stays hidden until a reopen,
// which the default handler answers by showing it.
func hiddenScenario() {
	h := &handler{}
	h.start = func(a *app.App) error {
		if _, ok := openWindows(a, 1, &view{}); !ok {
			os.Exit(0)
		}
		after(500*time.Millisecond, 1)
		return nil
	}
	h.event = func(a *app.App, e *app.Event) bool {
		if e.Kind != app.EvPost {
			return false
		}
		if e.ID == 1 {
			say("hidden_visible=%d", visibleWindows())
			objc.Send2(objc.ID(platform.AppDelegate()), objc.Sel("applicationShouldHandleReopen:hasVisibleWindows:"), uintptr(nsApp()), 0)
			after(300*time.Millisecond, 2)
			return true
		}
		say("reopened_visible=%d", visibleWindows())
		a.Quit()
		return true
	}
	app.Main(app.AppConfig{StartHidden: true}, h)
	say("main=returned")
}

func cpuNs() int64 {
	var ru syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return ru.Utime.Nano() + ru.Stime.Nano()
}
