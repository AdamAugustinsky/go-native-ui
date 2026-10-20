//go:build darwin

package menulive

import (
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
	t       *testing.T
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
	f.t.Cleanup(platform.DebugAssumeKey(a.Win)) // the keys posted to it are a key window's, activated or not
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
			f := &fieldApp{t: t, inModal: inModal}
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
	}
	d := k.BeginDialog("blocking", "Blocking", &s.dialog)
	k.EndDialog(&d)
	k.End()
	if s.mode == "repaint" {
		// A keyboard choice: Down opens the focused select's menu once its
		// focus has settled, and then nothing asks for a frame but the
		// choice (no pointer, no animation).
		if s.frame == settleFrames {
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
		s.drew = m.Frame() && m.Frame()
		m.Choose(1) // "B" as the menu showed it
	case "modal":
		s.dialog = true
		s.drew = m.Frame() && m.Frame()
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
		t.Fatalf("the select was clicked at frame %d, its menu opened at %d", s.clicked, s.opened)
	}
	return s
}

// needNested ends the test where no frame could be drawn while the menu was
// open: the display link does not tick where the display sleeps (a locked
// screen), so nothing changes during the tracking. CI's runners have a
// display that ticks.
func needNested(t *testing.T, s *selectApp) {
	t.Helper()
	switch {
	case s.drew && s.nested > 0:
	case os.Getenv("CI") == "" && !s.drew:
		t.Skip("the display link does not tick here (display asleep): no frame while the menu is open")
	default:
		t.Fatalf("frames while the menu was open: handler ran %v, built %d", s.drew, s.nested)
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
