//go:build darwin

package menulive

import (
	"fmt"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/internal/objc"
	"gonativeui/menu"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget"
	"gonativeui/widget/widgettest"
)

// run opens the real window with b, with the native menus, and runs it until
// b quits. The test is skipped where no window can be opened, but not in CI.
func run(t *testing.T, b app.Builder) {
	t.Helper()
	var err error
	widgettest.OnMain(func() {
		_, err = app.RunWith(app.Config{Title: "menu test", Width: 480, Height: 320}, b, app.RunOptions{})
	})
	if err != nil {
		if os.Getenv("CI") == "" {
			t.Skipf("no window to open: %v", err)
		}
		t.Fatalf("opening the window on CI: %v", err)
	}
}

// The AppKit side of the tests: real events posted to the application's
// queue, which the app's loop takes as it takes the user's.

func post(ev objc.ID) {
	app := objc.Send(objc.Cls("NSApplication"), objc.Sel("sharedApplication"))
	objc.Send2(app, objc.Sel("postEvent:atStart:"), uintptr(ev), 0)
}

func uptime() float64 {
	return objc.SendF64(objc.Send(objc.Cls("NSProcessInfo"), objc.Sel("processInfo")), objc.Sel("systemUptime"))
}

// postKey posts a key-down of the key with virtual code code, typing chars,
// with modifier flags (NSEventModifierFlags).
func postKey(win objc.ID, chars string, flags uintptr, code uint16) {
	s := objc.Str(chars)
	num := objc.Send(win, objc.Sel("windowNumber"))
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	ts := uptime()
	f := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(10).ArgPoint(10, 10).Arg(flags).ArgF64(ts).Arg(uintptr(num)).Arg(0).Arg(uintptr(s)).Arg(uintptr(s))
	if runtime.GOARCH == "arm64" {
		f.Arg32(uint32(code) << 16) // isARepeat (NO) at byte 0, the keyCode at byte 2 of the packed stack
	} else {
		f.Arg(0).Arg(uintptr(code))
	}
	post(objc.ID(f.Call(objc.MsgSend).R0()))
}

// postMouse posts a mouse event of type typ (NSEventType) at x, y window
// pixels from the content's top left.
func postMouse(a *app.App, typ uintptr, x, y float32) {
	n := a.Win.Native()
	win, view := objc.ID(n.NSWindow), objc.ID(n.NSView)
	_, _, scale := a.Size()
	h := objc.SendRect(view, objc.Sel("bounds")).H
	num := objc.Send(win, objc.Sel("windowNumber"))
	cls := objc.Cls("NSEvent")
	sel := objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
	ts := uptime()
	clicks, pressure := uintptr(1), float32(1)
	if typ == 5 {
		clicks, pressure = 0, 0
	}
	ev := objc.F.Reset().Arg(uintptr(cls)).Arg(uintptr(sel)).Arg(typ).ArgPoint(float64(x/scale), h-float64(y/scale)).Arg(0).
		ArgF64(ts).Arg(uintptr(num)).Arg(0).Arg(0).Arg(clicks).ArgF32(pressure).Call(objc.MsgSend).R0()
	post(objc.ID(ev))
}

// typeText gives the view text as the input method does (insertText:).
func typeText(a *app.App, s string) {
	view := objc.ID(a.Win.Native().NSView)
	str := objc.Str(s)
	sel := objc.Sel("insertText:replacementRange:") // before the call is assembled: both use the frame
	objc.F.Reset().Arg(uintptr(view)).Arg(uintptr(sel)).Arg(uintptr(str)).Arg(1<<63 - 1).Arg(0).Call(objc.MsgSend)
}

// centreOf is the centre of the first semantic node of role named name, in
// window pixels.
func centreOf(c *ui.Context, role ui.Role, name string) (x, y float32, ok bool) {
	var t ui.SemTree
	c.Semantics(&t)
	for _, n := range t.Nodes {
		if n.Role == role && string(t.Text(n.Name)) == name {
			return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2, true
		}
	}
	return 0, 0, false
}

const cmdClear menu.ID = 1

// fieldApp is a text field and a command Clear bound to ⌘Z, a chord the
// focused field takes for its own undo; inModal puts the field in a dialog
// and marks Clear InModal.
type fieldApp struct {
	a       *app.App
	k       *widget.Kit
	m       *menu.Model
	inModal bool

	buf      ui.TextBuffer
	key      ui.Key
	frame    int
	cleared  int
	typed    string // the field's text before ⌘Z
	result   string // and after
	focused  bool
	sentUndo bool
}

func (f *fieldApp) Init(a *app.App) error {
	f.a = a
	f.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	var err error
	f.m, err = menu.NewModel([]menu.Command{{ID: cmdClear, Title: "Clear", Shortcut: menu.Shortcut{Key: 'Z', Primary: true}, InModal: f.inModal}},
		[]menu.Menu{{Title: "File", Items: []menu.Item{{Kind: menu.ItemCommand, Command: cmdClear}}}}, []int{0})
	if err != nil {
		return err
	}
	f.k.SetMenu(f.m)
	a.SetMenu(f.m)
	return nil
}

func (f *fieldApp) Build(c *ui.Context) {
	f.frame++
	k := f.k
	k.Begin(c)
	if k.Command(cmdClear) {
		f.cleared++
	}
	if f.inModal {
		open := true
		d := k.BeginDialog("dialog", "Dialog", &open)
		if d.Visible {
			f.key = k.TextField("Name", &f.buf, "").Key
		}
		k.EndDialog(&d)
	} else {
		f.key = k.TextField("Name", &f.buf, "").Key
	}
	if f.frame == 3 {
		k.Focus(f.key)
	}
	k.End()
	switch {
	case f.frame == 8:
		f.focused = c.TextFieldFocused()
		typeText(f.a, "hello")
	case f.frame == 14:
		f.typed = f.buf.String()
		postKey(objc.ID(f.a.Win.Native().NSWindow), "z", 1<<20, 0x06) // ⌘Z
		f.sentUndo = true
	case f.frame == 24:
		f.result = f.buf.String()
		f.a.Quit()
	}
	c.RequestFrame()
}

// The focused text field comes before the native menu's key equivalents
// (D28): with a non-edit command bound to ⌘Z, ⌘Z sent through AppKit undoes
// the typing in the field and the command does not fire, in the main window
// and in a dialog where the command is InModal.
func TestFocusedFieldTakesItsChordBeforeTheMenu(t *testing.T) {
	for _, inModal := range []bool{false, true} {
		name := "main"
		if inModal {
			name = "dialog"
		}
		t.Run(name, func(t *testing.T) {
			f := &fieldApp{inModal: inModal}
			dog := time.AfterFunc(20*time.Second, func() { f.a.Quit(); platform.Wake() }) // no frames (occluded): end anyway
			run(t, f)
			dog.Stop()
			if !f.focused || !f.sentUndo {
				t.Fatalf("the field was focused %v, ⌘Z sent %v (after %d frames)", f.focused, f.sentUndo, f.frame)
			}
			if f.typed != "hello" {
				t.Fatalf("typed %q, want hello", f.typed)
			}
			if f.cleared != 0 || f.result != "" {
				t.Errorf("⌘Z: Clear fired %d times and the field holds %q; want the field's undo (empty) and no Clear", f.cleared, f.result)
			}
		})
	}
}

// selectApp is a select that is clicked open; while AppKit tracks its native
// menu, frames go on (DebugMenu.Frame) and change things, then an option is
// chosen.
type selectApp struct {
	a    *app.App
	k    *widget.Kit
	mode string // reorder, modal, repaint

	opts     []string
	sel      int
	dialog   bool
	frame    int
	clicked  int  // the frame the select was clicked in
	opened   int  // the frame the menu opened after
	tracking bool // the menu is open
	nested   int  // frames built while it was
	drew     bool // the display link ran the frame handler while it was
	shown    []string
	quit     atomic.Bool

	// Evidence for an environment that stopped the test (see needNested,
	// runSelect): the frame source around DebugMenu.Frame's wait, and
	// whether the window was key when it took the focus and when the key
	// was posted.
	link0, link1         platform.DebugState
	keyAtFocus, keyAtKey bool
}

func (s *selectApp) Init(a *app.App) error {
	s.a = a
	s.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

func (s *selectApp) Build(c *ui.Context) {
	if s.quit.Load() || s.frame > 600 {
		s.a.Quit()
		return
	}
	s.frame++
	if s.tracking {
		s.nested++
	}
	k := s.k
	k.Begin(c)
	cur := ""
	if s.sel >= 0 && s.sel < len(s.opts) {
		cur = s.opts[s.sel]
	}
	s.shown = append(s.shown, cur)
	sig := k.Select("Format", &s.sel, s.opts)
	if s.mode == "repaint" && s.frame == 3 {
		k.Focus(c.KeyOf(sig.ID))
		s.keyAtFocus = s.a.Win.DebugState().Key
	}
	d := k.BeginDialog("blocking", "Blocking", &s.dialog)
	k.EndDialog(&d)
	k.End()
	if s.mode == "repaint" {
		// A keyboard choice: Down opens the focused select's menu once its
		// focus has settled, and then nothing asks for a frame but the
		// choice (no pointer, no animation).
		if s.frame == settleFrames {
			s.keyAtKey = s.a.Win.DebugState().Key
			postKey(objc.ID(s.a.Win.Native().NSWindow), "\uF701", 0, 0x7D)
			s.clicked = s.frame
		}
		if s.frame < settleFrames {
			c.RequestFrame()
		}
		return
	}
	if s.clicked == 0 && s.frame == 6 {
		if x, y, ok := centreOf(c, ui.RoleComboBox, "Format"); ok {
			postMouse(s.a, 5, x, y)
			postMouse(s.a, 1, x, y)
			postMouse(s.a, 2, x, y)
			s.clicked = s.frame
		}
	}
	if s.opened != 0 && s.frame > s.opened+12 {
		s.quit.Store(true)
	}
	c.RequestFrame()
}

// settleFrames is the frame the keyboard test opens the menu in: the focus
// ring has long finished animating in by then.
const settleFrames = 40

func (s *selectApp) onMenuOpen(m platform.DebugMenu) {
	s.opened, s.tracking = s.frame, true
	defer func() { s.tracking = false }()
	switch s.mode {
	case "reorder":
		s.opts = []string{"B", "A"} // the next frame shows them so
		s.link0 = s.a.Win.DebugState()
		s.drew = m.Frame() && m.Frame()
		s.link1 = s.a.Win.DebugState()
		m.Choose(1) // "B" as the menu showed it
	case "modal":
		s.dialog = true
		s.link0 = s.a.Win.DebugState()
		s.drew = m.Frame() && m.Frame()
		s.link1 = s.a.Win.DebugState()
		m.Choose(1)
	case "repaint":
		m.Choose(1)
		time.AfterFunc(3*time.Second, func() { s.quit.Store(true); platform.Wake() })
	}
}

func runSelect(t *testing.T, mode string) *selectApp {
	t.Helper()
	s := &selectApp{mode: mode, opts: []string{"A", "B"}}
	widgettest.OnMain(func() { platform.DebugOnMenuOpen(s.onMenuOpen) })
	defer widgettest.OnMain(func() { platform.DebugOnMenuOpen(nil) })
	// A window that gets no frames (occluded) would wait forever: end the
	// loop without one.
	timedOut := atomic.Bool{}
	dog := time.AfterFunc(20*time.Second, func() { timedOut.Store(true); s.quit.Store(true); s.a.Quit(); platform.Wake() })
	run(t, s)
	dog.Stop()
	t.Logf("%s: %d frames; clicked at %d, menu open at %d; handler ran while open %v, frames built %d; sel %d; timed out %v",
		mode, s.frame, s.clicked, s.opened, s.drew, s.nested, s.sel, timedOut.Load())
	if s.clicked == 0 || s.opened == 0 {
		// A posted key goes to the key window. One that was key when it took
		// the focus and is not when the key is posted lost it to another
		// app: the environment's (CI: the menu never opened, once).
		if mode == "repaint" && s.clicked != 0 && s.keyAtFocus && !s.keyAtKey {
			t.Skipf("the window was key at frame 3 and no longer at frame %d, when the key was posted: another app took the keyboard", s.clicked)
		}
		t.Fatalf("the select was clicked at frame %d, its menu opened at %d (window key at the focus %v, at the key %v)", s.clicked, s.opened, s.keyAtFocus, s.keyAtKey)
	}
	return s
}

// needNested ends the test where no frame could be drawn while the menu was
// open because the display link did not tick: a frame was owed, the link
// was not paused by us, and it delivered no tick in the second
// DebugMenu.Frame waits (a display asleep, a locked screen; a hosted
// runner's display stalls, which CI saw also outside menus). A link that
// ticked without the handler running, or a handler that built no frame,
// fails.
func needNested(t *testing.T, s *selectApp) {
	t.Helper()
	switch skip, fail := nestedVerdict(s.drew, s.nested, s.link0, s.link1); {
	case skip != "":
		t.Skip(skip)
	case fail != "":
		t.Fatal(fail)
	}
}

// nestedVerdict is needNested's judgement: neither string when frames were
// built while the menu was open, a reason to skip when the display link was
// silent, a failure otherwise.
func nestedVerdict(drew bool, nested int, b, e platform.DebugState) (skip, fail string) {
	switch {
	case drew && nested > 0:
		return "", ""
	case !drew && b.Valid && e.Valid && e.Ticks == b.Ticks && (b.FrameArmed || e.FrameArmed) &&
		!b.LinkPaused && !e.LinkPaused && e.Now-b.Now >= 9e8:
		return fmt.Sprintf("the display link did not tick for %.1f s while a frame was owed and the menu open (ticks %d, visible %v, occlusion %d): no frame could be drawn",
			float64(e.Now-b.Now)/1e9, e.Ticks, e.Visible, e.Occlusion), ""
	}
	return "", fmt.Sprintf("frames while the menu was open: handler ran %v, built %d; link ticks %d -> %d, armed %v/%v, paused %v/%v, over %.2f s",
		drew, nested, b.Ticks, e.Ticks, b.FrameArmed, e.FrameArmed, b.LinkPaused, e.LinkPaused, float64(e.Now-b.Now)/1e9)
}

// A silent display link excuses the missing frames; a link that ticked, one
// we paused, or a handler that built nothing does not.
func TestNestedVerdict(t *testing.T) {
	at := func(now int64, ticks uint64, armed, paused bool) platform.DebugState {
		return platform.DebugState{Valid: true, Now: now, Ticks: ticks, FrameArmed: armed, LinkPaused: paused}
	}
	for _, c := range []struct {
		name       string
		drew       bool
		nested     int
		b, e       platform.DebugState
		skip, fail bool
	}{
		{"frames built", true, 2, at(0, 10, true, false), at(2e9, 130, false, false), false, false},
		{"link silent, a frame owed", false, 0, at(0, 10, true, false), at(2e9, 10, true, false), true, false},
		{"link ticked, no handler", false, 0, at(0, 10, true, false), at(2e9, 130, true, false), false, true},
		{"link paused by us", false, 0, at(0, 10, true, true), at(2e9, 10, true, true), false, true},
		{"no frame owed", false, 0, at(0, 10, false, false), at(2e9, 10, false, false), false, true},
		{"handler ran, nothing built", true, 0, at(0, 10, true, false), at(2e9, 12, true, false), false, true},
	} {
		skip, fail := nestedVerdict(c.drew, c.nested, c.b, c.e)
		if (skip != "") != c.skip || (fail != "") != c.fail {
			t.Errorf("%s: skip %q, fail %q", c.name, skip, fail)
		}
	}
}

// A choice is made in a native list menu while frames go on: one that no
// longer applies when it arrives is dropped (the options were reordered
// meanwhile; a modal dialog opened over the select), and one that applies
// is shown without any further event.
func TestSelectChoiceWhileTracking(t *testing.T) {
	t.Run("options reordered meanwhile", func(t *testing.T) {
		s := runSelect(t, "reorder")
		needNested(t, s)
		if s.sel != 0 {
			t.Errorf("sel %d (%q): the choice among the old options was applied to the new ones", s.sel, s.opts[s.sel])
		}
	})
	t.Run("a modal opened meanwhile", func(t *testing.T) {
		s := runSelect(t, "modal")
		needNested(t, s)
		if s.sel != 0 {
			t.Errorf("sel %d: the choice reached a select beneath the modal", s.sel)
		}
	})
	t.Run("shown without further events", func(t *testing.T) {
		s := runSelect(t, "repaint")
		if s.sel != 1 {
			t.Fatalf("sel %d, want the choice 1", s.sel)
		}
		if last := s.shown[len(s.shown)-1]; last != "B" {
			t.Errorf("the last frame drawn shows %q, want the choice B (frames shown %q)", last, s.shown[max(0, len(s.shown)-4):])
		}
	})
}
