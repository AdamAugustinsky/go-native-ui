package rexui_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gonativeui/internal/ghosttyvt"
	"gonativeui/rexui"
	"gonativeui/term"
	"gonativeui/theme"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

// The terminal tests use the library tools/libghostty built into the
// repository's cache unless GNUI_LIBGHOSTTY_VT names another.
func init() {
	if os.Getenv(term.EnvLibrary) != "" {
		return
	}
	if d, err := os.Getwd(); err == nil {
		for ; d != filepath.Dir(d); d = filepath.Dir(d) {
			if _, err := os.Stat(filepath.Join(d, "go.mod")); err == nil {
				os.Setenv(term.EnvLibrary, filepath.Join(d, ".cache", "libghostty-vt", ghosttyvt.Short, runtime.GOOS+"-"+runtime.GOARCH))
				return
			}
		}
	}
}

// newTermState makes a terminal view state, skipping the test without the
// library unless GNUI_REQUIRE_LIBGHOSTTY is set.
func newTermState(t testing.TB, o term.Options) *rexui.TerminalState {
	t.Helper()
	s, err := rexui.NewTerminal(o)
	if err != nil {
		if os.Getenv("GNUI_REQUIRE_LIBGHOSTTY") != "" {
			t.Fatal(err)
		}
		t.Skip(err)
	}
	t.Cleanup(s.Close)
	return s
}

// termRig is a rig whose window is one terminal view.
func termRig(t *testing.T, m theme.Mode, s *rexui.TerminalState, sig *rexui.TerminalSignal) *rig {
	t.Helper()
	focused := false
	return newRig(t, m, nil, func(g *rig) {
		c := g.c
		theme.At(c, 0, 0, float32(g.w), float32(g.h))
		c.Push(c.Box(theme.Floating, "root"))
		*sig = rexui.Terminal(g.k, "shell", s, rexui.TerminalOpts{})
		c.Pop()
		if !focused {
			g.k.Focus(sig.Key)
			focused = true
		}
	})
}

func TestTerminalViewInput(t *testing.T) {
	s := newTermState(t, term.Options{Scrollback: 100})
	var sig rexui.TerminalSignal
	g := termRig(t, theme.Light, s, &sig)
	if sig.Cols < 20 || sig.Rows < 5 {
		t.Fatalf("grid %d×%d for a 500×200 pt window", sig.Cols, sig.Rows)
	}
	if c, r := s.T.Size(); c != sig.Cols || r != sig.Rows {
		t.Fatalf("terminal %d×%d, view reports %d×%d", c, r, sig.Cols, sig.Rows)
	}
	take := func() string { return string(s.TakeInput(nil)) }
	take() // the focus report, if any

	g.typeText("ls")
	if got := take(); got != "ls" {
		t.Errorf("typed text sent %q", got)
	}
	g.key(ui.KeyEnter, 0)
	if got := take(); got != "\r" {
		t.Errorf("Enter sent %q", got)
	}
	g.key(ui.KeyUp, 0)
	if got := take(); got != "\x1b[A" {
		t.Errorf("Up sent %q", got)
	}
	g.key('C', ui.ModCtrl)
	if got := take(); got != "\x03" {
		t.Errorf("Ctrl+C sent %q", got)
	}
	// Application cursor keys (vim's DECCKM) come from the terminal's modes.
	s.Feed([]byte("\x1b[?1h"))
	g.frame()
	g.key(ui.KeyUp, 0)
	if got := take(); got != "\x1bOA" {
		t.Errorf("Up in application mode sent %q", got)
	}
	// Bracketed paste.
	s.Feed([]byte("\x1b[?2004h"))
	g.frame()
	g.in.SetPaste(sig.Key, []byte("echo hi"), true)
	g.frame()
	if got := take(); got != "\x1b[200~echo hi\x1b[201~" {
		t.Errorf("paste sent %q", got)
	}
	// Answers to the program's queries go back as input: cursor position.
	s.Feed([]byte("\x1b[2J\x1b[3;5H\x1b[6n"))
	g.frame()
	if got := take(); got != "\x1b[3;5R" {
		t.Errorf("DSR answered %q", got)
	}
	// Focus reports when the program asks for them.
	s.Feed([]byte("\x1b[?1004h"))
	g.frame()
	g.in.Focused = false
	g.frame()
	g.in.Focused = true
	g.frame()
	if got := take(); got != "\x1b[O\x1b[I" {
		t.Errorf("focus reports %q", got)
	}
}

func TestTerminalViewSelectionAndScroll(t *testing.T) {
	s := newTermState(t, term.Options{Scrollback: 1000})
	var sig rexui.TerminalSignal
	g := termRig(t, theme.Dark, s, &sig)
	var clip []byte
	g.c.ClipboardSet = func(b []byte) { clip = append(clip[:0], b...) }
	s.Feed([]byte("\x1b[2J\x1b[Hhello world"))
	g.frame()
	cw, ch := s.CellSize()
	pad := float32(12) // 6 pt at scale 2
	// Double-click the first word, then copy it.
	x, y := pad+2.5*cw, pad+ch/2
	g.in.MouseX, g.in.MouseY = x, y
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, true, 2)
	g.frame()
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame()
	g.key('C', ui.PrimaryMod()|map[bool]ui.Mods{true: ui.ModShift}[ui.PrimaryMod() == ui.ModCtrl])
	if string(clip) != "hello" {
		t.Errorf("double-click and copy gave %q", clip)
	}
	if got := s.TakeInput(nil); bytes.Contains(got, []byte("\x03")) {
		t.Errorf("copy reached the program: %q", got)
	}
	// Scrollback: fill it, scroll up with the wheel, typing returns.
	for i := 0; i < 300; i++ {
		s.Feed([]byte("line\r\n"))
	}
	g.frame()
	if !s.T.AtBottom() {
		t.Fatal("not at the bottom after output")
	}
	g.in.AddScroll(0, 5, false)
	g.frame()
	if s.T.AtBottom() {
		t.Error("the wheel did not scroll back")
	}
	g.typeText("x")
	if !s.T.AtBottom() {
		t.Error("typing did not return to the bottom")
	}
	// With mouse reporting on, the wheel goes to the program.
	s.TakeInput(nil)
	s.Feed([]byte("\x1b[?1000h\x1b[?1006h"))
	g.frame()
	g.in.AddScroll(0, -1, false)
	g.frame()
	if got := string(s.TakeInput(nil)); len(got) < 6 || got[:4] != "\x1b[<6" {
		t.Errorf("wheel in mouse mode sent %q", got)
	}
}

func TestTerminalViewClipboardPolicy(t *testing.T) {
	s := newTermState(t, term.Options{})
	var sig rexui.TerminalSignal
	g := termRig(t, theme.Light, s, &sig)
	set := false
	g.c.ClipboardSet = func([]byte) { set = true }
	s.Feed([]byte("\x1b]52;c;aGk=\x07"))
	var req []byte
	g.build = func(g *rig) {
		c := g.c
		theme.At(c, 0, 0, float32(g.w), float32(g.h))
		c.Push(c.Box(theme.Floating, "root"))
		if sig = rexui.Terminal(g.k, "shell", s, rexui.TerminalOpts{}); sig.ClipboardRequest != nil {
			req = append(req[:0], sig.ClipboardRequest...)
		}
		c.Pop()
	}
	g.frame()
	if set || string(req) != "hi" {
		t.Errorf("OSC 52 without AllowClipboard: set %v, request %q", set, req)
	}
}

// A frame with no new output, and one with a line of it, allocate nothing.
func TestTerminalViewAllocations(t *testing.T) {
	s := newTermState(t, term.Options{Scrollback: 1000})
	var sig rexui.TerminalSignal
	g := termRig(t, theme.Dark, s, &sig)
	s.Feed([]byte("\x1b[1;31mred\x1b[0m \x1b[4munder\x1b[0m wide 漢字 box ─┼─ \x1b[7minverse\x1b[0m\r\n$ "))
	g.settle()
	if n := testing.AllocsPerRun(50, g.frame); n != 0 {
		t.Errorf("%v allocations per idle frame", n)
	}
	line := []byte("\x1b[32mok\x1b[0m 0123456789\r\n")
	// Warm up: rows keep their buffers as they move up the screen, and every
	// buffer of the view's two frames of rows has held a line once.
	for range 3 * sig.Rows {
		s.Feed(line)
		g.frame()
	}
	if n := testing.AllocsPerRun(50, func() { s.Feed(line); g.frame() }); n != 0 {
		t.Errorf("%v allocations per frame of output", n)
	}
}

// The terminal screen passes the layout audit in light and dark: a recorded
// vim session, and the scrollback scrolled so the scroll-to-bottom button
// shows.
func TestTerminalViewLayoutAudit(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "term", "testdata", "sessions", "vim.vt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []theme.Mode{theme.Light, theme.Dark} {
		s := newTermState(t, term.Options{Scrollback: 1000})
		var sig rexui.TerminalSignal
		g := termRig(t, m, s, &sig)
		s.Feed(data[:len(data)/2])
		g.settle()
		widgettest.AuditLayout(t, fmt.Sprintf("terminal, vim, mode %d", m), g.c)
		s.Feed([]byte("\x1b[?1049l\x1b[2J\x1b[H"))
		for i := range 200 {
			s.Feed(fmt.Appendf(nil, "line %d\r\n", i))
		}
		g.frame()
		g.in.MouseX, g.in.MouseY = 100, 100
		g.in.AddScroll(0, 5, false)
		g.settle()
		if s.T.AtBottom() {
			t.Fatal("the wheel did not scroll back")
		}
		widgettest.AuditLayout(t, fmt.Sprintf("terminal, scrolled back, mode %d", m), g.c)
		g.c.Semantics(&g.sem)
		b := g.node(ui.RoleButton, "Scroll to bottom")
		g.click(centre(b))
		if !s.T.AtBottom() {
			t.Error("the scroll-to-bottom button did not go back")
		}
	}
}

// In Brazilian Portuguese the scroll-to-bottom button is Rolar até o fim
// (rexui.Strings.ScrollToBottom), and it still goes back.
func TestTerminalScrollToBottomPtBR(t *testing.T) {
	usePtBR(t)
	s := newTermState(t, term.Options{Scrollback: 1000})
	var sig rexui.TerminalSignal
	g := termRig(t, theme.Light, s, &sig)
	for i := range 200 {
		s.Feed(fmt.Appendf(nil, "line %d\r\n", i))
	}
	g.frame()
	g.in.MouseX, g.in.MouseY = 100, 100
	g.in.AddScroll(0, 5, false)
	g.settle()
	if s.T.AtBottom() {
		t.Fatal("the wheel did not scroll back")
	}
	g.c.Semantics(&g.sem)
	if g.has(ui.RoleButton, "Scroll to bottom") {
		t.Error("the button is still named Scroll to bottom")
	}
	g.click(centre(g.node(ui.RoleButton, "Rolar até o fim")))
	if !s.T.AtBottom() {
		t.Error("the button did not go back")
	}
}
