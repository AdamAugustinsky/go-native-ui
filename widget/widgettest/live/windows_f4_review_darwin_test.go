//go:build darwin

package live

// The flows of several windows the review of F4 found wrong, each a Scenario
// (a child process with the application object, app.Main): a key equivalent
// right after a focus change or right after a window opened, the standard
// bar shared by windows without a menu model, the window's identity inside
// its Init, a sheet left open on a closing window, five windows at five
// scales, a Start that fails with windows open, posts for one window, a
// document-edited mark after the Handler's window is opened again.

import (
	"errors"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/gpu"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

const cmdCloseDoc menu.ID = 7

// docWin is a window with a menu whose Close Document command (⌘W, an app
// command, not AppKit's performClose:) closes it.
type docWin struct {
	a      *app.App
	k      *widget.Kit
	closes atomic.Int64
	init   func(a *app.App) // at the end of Init
}

func (d *docWin) Init(a *app.App) error {
	d.a = a
	d.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	m, err := menu.NewModel([]menu.Command{{ID: cmdCloseDoc, Title: "Close Document", Shortcut: menu.Shortcut{Key: 'W', Primary: true}}},
		[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdCloseDoc}}}}, []int{0})
	if err != nil {
		return err
	}
	d.k.SetMenu(m)
	a.SetMenu(m)
	if d.init != nil {
		d.init(a)
	}
	return nil
}

func (d *docWin) Build(c *ui.Context) {
	k := d.k
	k.Begin(c)
	if k.Command(cmdCloseDoc) {
		d.closes.Add(1)
		d.a.Window().Close()
	}
	k.Label("document")
	k.End()
}

// makeKey makes w the key window; where the app cannot activate (a session
// locked or in use), AppKit's windowDidBecomeKey: is sent as making it key
// would send it.
func makeKey(w *app.Win) {
	nw := objc.ID(w.App().Win.Native().NSWindow)
	objc.Send1(nw, objc.Sel("makeKeyAndOrderFront:"), 0)
	if !objc.SendBool(nw, objc.Sel("isKeyWindow")) {
		objc.Send1(objc.Send(nw, objc.Sel("delegate")), objc.Sel("windowDidBecomeKey:"), 0)
	}
}

// idPane records the handle App.Window gives inside Init and the first Build,
// and whether old (a closed window's handle) looks open meanwhile.
type idPane struct {
	mwPane
	a               *app.App
	old             *app.Win
	inInit, inBuild *app.Win
	initOpen        bool // the handle of Init was open there
	oldOpenInInit   bool
	oldOpenInBuild  bool
	builds          int
}

func (p *idPane) Init(a *app.App) error {
	p.a, p.inInit = a, a.Window()
	p.initOpen = p.inInit != nil && p.inInit.Open()
	if p.old != nil {
		p.oldOpenInInit = p.old.Open() || p.old.App() != nil
	}
	return nil
}

func (p *idPane) Build(c *ui.Context) {
	if p.builds++; p.builds == 1 {
		p.inBuild = p.a.Window()
		if p.old != nil {
			p.oldOpenInBuild = p.old.Open() || p.old.App() != nil
		}
	}
	p.mwPane.Build(c)
}

func init() {
	// f4-menukey: two windows with their own menus; the first becomes key
	// (its bar), then, in one turn of the loop, the second becomes key and
	// ⌘W (Close Document) is pressed: the second closes, the first stays.
	widgettest.RegisterScenario("f4-menukey", func() {
		first, second := &docWin{}, &docWin{}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			w1, err := a.OpenWindow(mwConfig("first"), first)
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			w2, err := a.OpenWindow(mwConfig("second"), second)
			if err != nil {
				widgettest.Fail("second window: %v", err)
			}
			widgettest.After(400*time.Millisecond, func() {
				makeKey(w1)
				widgettest.After(400*time.Millisecond, func() {
					widgettest.Say("first_owns_bar=%v", w1.App().Win.MenuBarOwner())
					makeKey(w2)
					nw := widgettest.NativeWindow(w2.App())
					// The keys, as the keyboard's: the pump takes them in this
					// same turn, before the loop syncs anything.
					widgettest.Post(nw.KeyEvent(true, "w", "w", 0x0D, ui.ModSuper, false))
					widgettest.Post(nw.KeyEvent(false, "w", "w", 0x0D, ui.ModSuper, false))
					widgettest.After(600*time.Millisecond, func() {
						widgettest.Say("first_open=%v second_open=%v first_closes=%d second_closes=%d", w1.Open(), w2.Open(), first.closes.Load(), second.closes.Load())
						a.Quit()
					})
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-openkey: the first window, key, owns the bar; an app event opens
	// the second at the dispatcher's safe point, inside the pump; it becomes
	// key as it opens, and ⌘W (Close Document) is in the queue that same
	// pump drains next, before the loop turns again: the second closes, the
	// first stays.
	widgettest.RegisterScenario("f4-openkey", func() {
		first := &docWin{}
		second := &docWin{init: func(a *app.App) {
			nw := objc.ID(a.Win.Native().NSWindow)
			// AppKit makes a window key as it opens where the app is
			// active; where it cannot activate, windowDidBecomeKey: is sent
			// as it would be.
			objc.Send1(objc.Send(nw, objc.Sel("delegate")), objc.Sel("windowDidBecomeKey:"), 0)
			kw := widgettest.NativeWindow(a)
			widgettest.Post(kw.KeyEvent(true, "w", "w", 0x0D, ui.ModSuper, false))
			widgettest.Post(kw.KeyEvent(false, "w", "w", 0x0D, ui.ModSuper, false))
		}}
		var w2 *app.Win
		h := &flowHandler{}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind != app.EvPost {
				return false
			}
			var err error
			if w2, err = a.OpenWindow(mwConfig("second"), second); err != nil {
				widgettest.Fail("second window: %v", err)
			}
			return true
		}
		h.start = func(a *app.App) error {
			w1, err := a.OpenWindow(mwConfig("first"), first)
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			widgettest.After(400*time.Millisecond, func() {
				makeKey(w1)
				widgettest.After(400*time.Millisecond, func() {
					widgettest.Say("first_owns_bar=%v", w1.App().Win.MenuBarOwner())
					for !app.Post(app.Event{ID: 1}, nil) {
						time.Sleep(time.Millisecond)
					}
					widgettest.After(800*time.Millisecond, func() {
						widgettest.Say("opened=%v first_open=%v second_open=%v first_closes=%d second_closes=%d",
							w2 != nil && w2.ID() != 0, w1.Open(), w2 != nil && w2.Open(), first.closes.Load(), second.closes.Load())
						a.Quit()
					})
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-stdbar: two windows without a menu model (the standard bar):
	// focus moving between them switches the bar's window, building no new
	// native menu.
	widgettest.RegisterScenario("f4-stdbar", func() {
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			w1, err := a.OpenWindow(mwConfig("first"), &mwPane{})
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			w2, err := a.OpenWindow(mwConfig("second"), &mwPane{})
			if err != nil {
				widgettest.Fail("second window: %v", err)
			}
			mainMenu := func() objc.ID { return objc.Send(widgettest.NSApp(), objc.Sel("mainMenu")) }
			widgettest.After(400*time.Millisecond, func() {
				makeKey(w1)
				bar, owners, builds := mainMenu(), 0, 0
				order := []*app.Win{w2, w1, w2, w1}
				var step func(i int)
				step = func(i int) {
					if w := order[(i+len(order)-1)%len(order)]; i > 0 && w.App().Win.MenuBarOwner() {
						owners++
					}
					if m := mainMenu(); m != bar {
						builds++
						bar = m
					}
					if i == len(order) {
						widgettest.Say("bar=%v switches=%d owners=%d builds=%d", bar != 0, len(order), owners, builds)
						a.Quit()
						return
					}
					makeKey(order[i])
					widgettest.After(150*time.Millisecond, func() { step(i + 1) })
				}
				widgettest.After(300*time.Millisecond, func() { bar = mainMenu(); step(0) })
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-identity: App.Window inside Init and the first Build gives the
	// handle OpenWindow returns, open; the Handler's window opened again
	// (KeepRunning) does not make the closed one's handle look open meanwhile.
	widgettest.RegisterScenario("f4-identity", func() {
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			p1, p2 := &idPane{}, &idPane{}
			w1, err := a.OpenWindow(mwConfig("first"), p1)
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			w2, err := a.OpenWindow(mwConfig("second"), p2)
			if err != nil {
				widgettest.Fail("second window: %v", err)
			}
			same := func(p *idPane, w *app.Win) bool {
				return p.inInit == w && p.inBuild == w && p.initOpen && w.Open()
			}
			widgettest.After(300*time.Millisecond, func() {
				widgettest.Say("first_same=%v second_same=%v", same(p1, w1), same(p2, w2))
				w1.Close()
				widgettest.After(300*time.Millisecond, func() {
					p3 := &idPane{old: w1}
					w3, err := a.OpenWindow(mwConfig("again"), p3)
					if err != nil {
						widgettest.Fail("again: %v", err)
					}
					widgettest.Say("again_same=%v again_root=%v old_open_in_init=%v old_open_in_build=%v", same(p3, w3), w3.App() == a, p3.oldOpenInInit, p3.oldOpenInBuild)
					a.Quit()
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{KeepRunning: true}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-sheetclose: a second window closes with its alert sheet on screen:
	// the sheet's cancellation reaches the Handler as an EvService with the
	// alert's ticket and the closed window's id, and the window's App's
	// TakeDialog.
	widgettest.RegisterScenario("f4-sheetclose", func() {
		var (
			w2     *app.Win
			w2App  *app.App
			ticket platform.DialogTicket
			got    atomic.Int32
		)
		h := &flowHandler{}
		h.event = func(a *app.App, e *app.Event) bool {
			if e.Kind == app.EvService && e.Source == platform.SourceDialog {
				got.Add(1)
				r, ok := w2App.TakeDialog()
				widgettest.Say("dialog_ticket_ok=%v dialog_window_ok=%v dialog_cancelled=%v take_ok=%v", platform.DialogTicket(e.ID) == ticket, e.Window == w2.ID(),
					platform.DialogOutcome(e.A>>8&0xff) == platform.DialogCancelled, ok && r.Ticket == ticket && r.Outcome == platform.DialogCancelled)
				return true
			}
			return false
		}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("first"), &mwPane{}); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			var err error
			if w2, err = a.OpenWindow(mwConfig("second"), &mwPane{}); err != nil {
				widgettest.Fail("second window: %v", err)
			}
			widgettest.After(300*time.Millisecond, func() {
				w2App = w2.App()
				if ticket, err = w2.App().Alert(platform.AlertSpec{Message: "left open", Buttons: []string{"OK", "Cancel"}}); err != nil {
					widgettest.Fail("alert: %v", err)
				}
				nw := objc.ID(w2.App().Win.Native().NSWindow)
				var wait func(n int)
				wait = func(n int) {
					if objc.Send(nw, objc.Sel("attachedSheet")) == 0 {
						if n == 0 {
							widgettest.Fail("the alert's sheet did not show")
						}
						widgettest.After(50*time.Millisecond, func() { wait(n - 1) })
						return
					}
					w2.Close() // with the sheet on screen
					widgettest.After(1500*time.Millisecond, func() {
						widgettest.Say("dialog_events=%d", got.Load())
						a.Quit()
					})
				}
				wait(60)
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-scales: five windows, each at its own zoom (five effective
	// scales): the fonts of every scale a window is at stay alive. Then
	// all but the one that drew last close, and it draws again: no more
	// than fontScales (4) font sets stay.
	widgettest.RegisterScenario("f4-scales", func() {
		zooms := []float32{0.8, 0.9, 1.1, 1.25, 1.5}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			var wins []*app.Win
			for i := range zooms {
				w, err := a.OpenWindow(mwConfig("scale"), &mwText{label: "Text at its own scale"})
				if err != nil {
					if i == 0 {
						widgettest.Say("skip=no-window")
						os.Exit(0)
					}
					widgettest.Fail("window %d: %v", i, err)
				}
				wins = append(wins, w)
			}
			widgettest.After(300*time.Millisecond, func() {
				for i, w := range wins {
					w.App().SetZoom(zooms[i])
					w.Wake()
				}
				widgettest.After(1200*time.Millisecond, func() {
					alive := a.Text.Scales()
					missing := 0
					for _, w := range wins {
						if !slices.Contains(alive, w.App().EffectiveScale()) {
							missing++
						}
					}
					widgettest.Say("alive=%d missing=%d", len(alive), missing)
					last := wins[len(wins)-1]
					last.Wake() // the one that draws last: its scale is the latest
					widgettest.After(400*time.Millisecond, func() {
						for _, w := range wins[:len(wins)-1] {
							w.Close()
						}
						widgettest.After(400*time.Millisecond, func() {
							last.Wake()
							widgettest.After(400*time.Millisecond, func() {
								la := last.App() // (the Handler's App held the first window, closed now)
								widgettest.Say("after_close_alive=%d last_alive=%v", len(la.Text.Scales()), slices.Contains(la.Text.Scales(), la.EffectiveScale()))
								a.Quit()
							})
						})
					})
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-startfail: Start opens two windows, then fails: Main returns its
	// error with both windows closed and their surfaces released.
	widgettest.RegisterScenario("f4-startfail", func() {
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("first"), &mwPane{}); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			if _, err := a.OpenWindow(mwConfig("second"), &mwPane{}); err != nil {
				widgettest.Fail("second window: %v", err)
			}
			return errors.New("start failed")
		}
		err := app.Main(app.AppConfig{}, h)
		widgettest.Say("main_err_ok=%v surfaces=%d visible=%d", err != nil && err.Error() == "start failed", gpu.LiveSurfaces(), widgettest.VisibleWindows())
	})

	// f4-posts: posts for the second window (Event.Window) while the first
	// is idle: the second draws, the first draws nothing.
	widgettest.RegisterScenario("f4-posts", func() {
		first, second := &mwPane{}, &mwPane{}
		h := &flowHandler{}
		h.event = func(a *app.App, e *app.Event) bool { return e.Kind == app.EvPost }
		h.start = func(a *app.App) error {
			if _, err := a.OpenWindow(mwConfig("first"), first); err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			w2, err := a.OpenWindow(mwConfig("second"), second)
			if err != nil {
				widgettest.Fail("second window: %v", err)
			}
			tries, firstDrew, secondDrew := 0, 0, 0
			var try func()
			try = func() {
				tries++
				f0, s0 := first.builds.Load(), second.builds.Load()
				go func() {
					for i := 0; i < 10; i++ {
						for !app.Post(app.Event{Window: w2.ID(), ID: uint64(i)}, nil) {
							time.Sleep(time.Millisecond)
						}
						time.Sleep(30 * time.Millisecond)
					}
				}()
				widgettest.After(800*time.Millisecond, func() {
					if first.builds.Load() > f0 {
						firstDrew++
					}
					if second.builds.Load() > s0 {
						secondDrew++
					}
					if firstDrew < tries || tries == 3 { // once clean is enough: another process may move the focus
						widgettest.Say("tries=%d first_drew=%d second_drew=%d", tries, firstDrew, secondDrew)
						a.Quit()
						return
					}
					try()
				})
			}
			widgettest.After(800*time.Millisecond, try)
			return nil
		}
		if err := app.Main(app.AppConfig{}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})

	// f4-edited: the Handler's window marked edited, closed and opened again
	// (KeepRunning): SetDocumentEdited(true) on the new window marks it.
	widgettest.RegisterScenario("f4-edited", func() {
		edited := func(w *app.Win) bool {
			return objc.SendBool(objc.ID(w.App().Win.Native().NSWindow), objc.Sel("isDocumentEdited"))
		}
		h := &flowHandler{}
		h.start = func(a *app.App) error {
			w1, err := a.OpenWindow(mwConfig("first"), &mwPane{})
			if err != nil {
				widgettest.Say("skip=no-window")
				os.Exit(0)
			}
			widgettest.After(300*time.Millisecond, func() {
				w1.SetDocumentEdited(true)
				widgettest.After(200*time.Millisecond, func() {
					widgettest.Say("first_edited=%v", edited(w1))
					w1.Close()
					widgettest.After(300*time.Millisecond, func() {
						w2, err := a.OpenWindow(mwConfig("again"), &mwPane{})
						if err != nil {
							widgettest.Fail("again: %v", err)
						}
						w2.SetDocumentEdited(true)
						widgettest.After(200*time.Millisecond, func() {
							widgettest.Say("again_root=%v again_edited=%v", w2.App() == a, edited(w2))
							a.Quit()
						})
					})
				})
			})
			return nil
		}
		if err := app.Main(app.AppConfig{KeepRunning: true}, h); err != nil {
			widgettest.Say("main_err=%v", err)
		}
	})
}

func skipped(t *testing.T, r widgettest.ScenarioResult) {
	t.Helper()
	if r.Get("skip") != "" {
		t.Skipf("cannot open a window here (%s)", r.Get("skip"))
	}
}

// A key equivalent right after a focus change acts on the new key window:
// the bar becomes its as it becomes key, not at the loop's next turn.
func TestAKeyEquivalentAfterAFocusChangeActsOnTheNewKeyWindow(t *testing.T) {
	r := widgettest.Scenario(t, "f4-menukey", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("first_owns_bar") != "true" {
		t.Fatalf("exit %d; the first window, key, owns the bar: %q\n%s", r.Code, r.Get("first_owns_bar"), r.Out)
	}
	if r.Get("second_open") != "false" || r.Get("first_open") != "true" || r.Get("first_closes") != "0" {
		t.Errorf("⌘W right after the second window became key: first open %s (%s closes), second open %s (%s closes); want the second closed only\n%s",
			r.Get("first_open"), r.Get("first_closes"), r.Get("second_open"), r.Get("second_closes"), r.Out)
	}
}

// A window opened at a safe point of the pump (an app event's) that becomes
// key as it opens has its bar at once: ⌘W pressed before the loop turns again
// closes it, not the window that owned the bar.
func TestAKeyEquivalentRightAfterAWindowOpenedActsOnIt(t *testing.T) {
	r := widgettest.Scenario(t, "f4-openkey", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("first_owns_bar") != "true" || r.Get("opened") != "true" {
		t.Fatalf("exit %d; the first window, key, owns the bar: %q; the second opened: %q\n%s", r.Code, r.Get("first_owns_bar"), r.Get("opened"), r.Out)
	}
	if r.Get("second_open") != "false" || r.Get("first_open") != "true" || r.Get("first_closes") != "0" {
		t.Errorf("⌘W right after the second window opened and became key: first open %s (%s closes), second open %s (%s closes); want the second closed only\n%s",
			r.Get("first_open"), r.Get("first_closes"), r.Get("second_open"), r.Get("second_closes"), r.Out)
	}
}

// Windows without a menu model share the standard bar: focus moving between
// them makes the bar the key window's without building a new native menu.
func TestWindowsWithTheStandardBarShareIt(t *testing.T) {
	r := widgettest.Scenario(t, "f4-stdbar", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("bar") != "true" {
		t.Fatalf("exit %d; a bar installed: %s\n%s", r.Code, r.Get("bar"), r.Out)
	}
	if r.Get("owners") != r.Get("switches") {
		t.Errorf("the bar was the key window's after %s of %s focus switches\n%s", r.Get("owners"), r.Get("switches"), r.Out)
	}
	if r.Get("builds") != "0" {
		t.Errorf("%s focus switches between windows with the standard bar built %s new native menus, want none\n%s", r.Get("switches"), r.Get("builds"), r.Out)
	}
}

// App.Window inside Init and the first Build is the handle OpenWindow
// returns, and it is open; a closed window's handle does not look open while
// the Handler's App opens its next window.
func TestTheWindowsIdentityIsThereForInitAndTheFirstBuild(t *testing.T) {
	r := widgettest.Scenario(t, "f4-identity", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 {
		t.Fatalf("exit %d\n%s", r.Code, r.Out)
	}
	for _, k := range []string{"first_same", "second_same", "again_same", "again_root"} {
		if r.Get(k) != "true" {
			t.Errorf("%s=%s, want true\n%s", k, r.Get(k), r.Out)
		}
	}
	if r.Get("old_open_in_init") != "false" || r.Get("old_open_in_build") != "false" {
		t.Errorf("the closed window's handle looks open while the next one opens: in Init %s, in the first Build %s", r.Get("old_open_in_init"), r.Get("old_open_in_build"))
	}
}

// A window closed with its sheet on screen: the sheet's cancellation reaches
// the Handler once, with the dialog's ticket and the closed window's id, and
// the window's App has it for TakeDialog.
func TestASheetOfAClosedWindowIsAnswered(t *testing.T) {
	r := widgettest.Scenario(t, "f4-sheetclose", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("dialog_events") != "1" {
		t.Fatalf("exit %d; %q dialog events, want 1\n%s", r.Code, r.Get("dialog_events"), r.Out)
	}
	for _, k := range []string{"dialog_ticket_ok", "dialog_window_ok", "dialog_cancelled", "take_ok"} {
		if r.Get(k) != "true" {
			t.Errorf("%s=%s, want true\n%s", k, r.Get(k), r.Out)
		}
	}
}

// Five windows at five scales: the fonts of each window's scale stay alive
// (a released scale would leave its kit with ids of fonts that are gone).
func TestEveryWindowsScaleKeepsItsFonts(t *testing.T) {
	r := widgettest.Scenario(t, "f4-scales", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("missing") != "0" || r.Int("alive") < 5 {
		t.Fatalf("exit %d; %s font sets alive, %s of the windows' scales missing; want all five\n%s", r.Code, r.Get("alive"), r.Get("missing"), r.Out)
	}
	if n := r.Int("after_close_alive"); n > 4 || n == 0 || r.Get("last_alive") != "true" {
		t.Errorf("after all but the latest window closed and it drew again: %s font sets alive (its own alive %s); want at most 4, its own among them\n%s", r.Get("after_close_alive"), r.Get("last_alive"), r.Out)
	}
}

// A Start that fails after opening two windows: Main returns its error with
// both closed and their surfaces released.
func TestAFailedStartClosesEveryWindow(t *testing.T) {
	r := widgettest.Scenario(t, "f4-startfail", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("main_err_ok") != "true" {
		t.Fatalf("exit %d; Main returned Start's error: %s\n%s", r.Code, r.Get("main_err_ok"), r.Out)
	}
	if r.Get("surfaces") != "0" || r.Get("visible") != "0" {
		t.Errorf("after the failed Start: %s surfaces, %s windows visible; want none\n%s", r.Get("surfaces"), r.Get("visible"), r.Out)
	}
}

// Posts for one window (Event.Window) draw that window, not an idle other
// one: once clean in up to three tries (another test process may move the
// focus, which draws).
func TestPostsForOneWindowLeaveTheOthersIdle(t *testing.T) {
	r := widgettest.Scenario(t, "f4-posts", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Int("tries") == 0 {
		t.Fatalf("exit %d\n%s", r.Code, r.Out)
	}
	if r.Int("first_drew") >= r.Int("tries") {
		t.Errorf("posts for the second window drew the idle first one in %s tries of %s\n%s", r.Get("first_drew"), r.Get("tries"), r.Out)
	}
	if r.Int("second_drew") != r.Int("tries") {
		t.Errorf("the second window drew for its posts in %s tries of %s\n%s", r.Get("second_drew"), r.Get("tries"), r.Out)
	}
}

// The Handler's window marked edited, closed and opened again: the new
// window's SetDocumentEdited(true) marks it (no state of the closed one's
// is kept).
func TestTheReopenedWindowTakesItsEditedMark(t *testing.T) {
	r := widgettest.Scenario(t, "f4-edited", 60*time.Second)
	skipped(t, r)
	if r.Code != 0 || r.Get("first_edited") != "true" || r.Get("again_root") != "true" {
		t.Fatalf("exit %d; the first window edited %s, the new one the Handler's %s\n%s", r.Code, r.Get("first_edited"), r.Get("again_root"), r.Out)
	}
	if r.Get("again_edited") != "true" {
		t.Errorf("SetDocumentEdited(true) on the window opened again: isDocumentEdited %s, want true\n%s", r.Get("again_edited"), r.Out)
	}
}
