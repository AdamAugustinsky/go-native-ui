package term_test

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"gonativeui/term"
)

var update = flag.Bool("update", false, "rewrite testdata/screens from the replays")

func newTerm(t testing.TB, o term.Options) *term.Terminal {
	t.Helper()
	term.NeedLib(t)
	tm, err := term.New(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(tm.Close)
	return tm
}

func TestCellIsEightBytes(t *testing.T) {
	if s := unsafe.Sizeof(term.Cell{}); s != 8 {
		t.Fatalf("Cell is %d bytes, D74 says 8", s)
	}
}

func TestColors(t *testing.T) {
	if c := term.Indexed(196); c.Kind() != term.ColorIndexed || c.Index() != 196 {
		t.Errorf("Indexed: %#x", c)
	}
	if r, g, b := term.RGB(1, 2, 3).RGB(); r != 1 || g != 2 || b != 3 || term.RGB(1, 2, 3).Kind() != term.ColorRGB {
		t.Error("RGB")
	}
	if term.DefaultColor.Kind() != term.ColorDefault {
		t.Error("default")
	}
	if term.Xterm256(196) != 0xff0000 || term.Xterm256(232) != 0x080808 || term.Xterm256(16) != 0 || term.Xterm256(9) != 0xff0000 {
		t.Errorf("Xterm256: %06x %06x", term.Xterm256(196), term.Xterm256(232))
	}
}

// TestMissingLibrary runs the test binary again with the library pointed
// at a path that does not exist: New must fail with ErrUnavailable, naming
// where it looked. (A process loads the library once, so it takes a child.)
func TestMissingLibrary(t *testing.T) {
	if os.Getenv("TERM_MISSING_CHILD") != "" {
		_, err := term.New(term.Options{})
		if !errors.Is(err, term.ErrUnavailable) {
			t.Fatalf("New = %v, want ErrUnavailable", err)
		}
		if !strings.Contains(err.Error(), "nowhere") || !strings.Contains(err.Error(), term.LibraryName) {
			t.Fatalf("the error does not say where it looked: %v", err)
		}
		if term.LibraryPath() != "" || !errors.Is(term.Available(), term.ErrUnavailable) {
			t.Fatal("Available or LibraryPath disagree with New")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestMissingLibrary$", "-test.v")
	cmd.Env = append(os.Environ(), "TERM_MISSING_CHILD=1", term.EnvLibrary+"="+filepath.Join(t.TempDir(), "nowhere", term.LibraryName))
	out, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(out, []byte("--- PASS: TestMissingLibrary")) {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

func TestLibraryLoads(t *testing.T) {
	term.NeedLib(t)
	if p := term.LibraryPath(); !strings.HasSuffix(p, term.LibraryName) {
		t.Fatalf("LibraryPath = %q", p)
	}
}

// screen is the terminal's screen as plain text.
func screen(tm *term.Terminal) string { return string(tm.AppendScreen(nil, term.Plain)) }

// TestRenderRows reads styles, wide characters, graphemes, hyperlinks and
// the cursor through the render state.
func TestRenderRows(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 20, Rows: 4})
	tm.Write([]byte("\x1b[1;31mred\x1b[0m \x1b[38;2;1;2;3m日\x1b[0me\u0301\r\n"))
	tm.Write([]byte("\x1b]8;;https://x.test/a\x1b\\link\x1b]8;;\x1b\\ \x1b[44m  \x1b[0m"))
	if d := tm.Update(); d == term.Clean {
		t.Fatal("Update after writes is Clean")
	}
	var r term.Row
	if !tm.NextRow(&r) {
		t.Fatal("no first row")
	}
	tm.DecodeRow(&r)
	if len(r.Cells) != 20 {
		t.Fatalf("%d cells, want 20", len(r.Cells))
	}
	st := tm.Style(r.Cells[0].Style)
	if r.Cells[0].Rune != 'r' || st.Attr&term.Bold == 0 || st.FG != term.Indexed(1) {
		t.Errorf("cell 0 = %+v style %+v", r.Cells[0], st)
	}
	if c := r.Cells[4]; c.Rune != '日' || c.Flags&term.Wide == 0 || tm.Style(c.Style).FG != term.RGB(1, 2, 3) {
		t.Errorf("wide cell = %+v style %+v", c, tm.Style(c.Style))
	}
	if r.Cells[5].Flags&term.WideTail == 0 {
		t.Errorf("tail = %+v", r.Cells[5])
	}
	if c := r.Cells[6]; c.Flags&term.Grapheme == 0 || string(r.Grapheme(6)) != "e\u0301" {
		t.Errorf("grapheme cell = %+v %q", c, string(r.Grapheme(6)))
	}
	if !tm.NextRow(&r) {
		t.Fatal("no second row")
	}
	tm.DecodeRow(&r)
	if r.Cells[0].Flags&term.Hyperlink == 0 || r.Cells[4].Flags&term.Hyperlink != 0 {
		t.Errorf("hyperlink flags %+v %+v", r.Cells[0], r.Cells[4])
	}
	if bg := tm.Style(r.Cells[5].Style).BG; bg != term.Indexed(4) {
		t.Errorf("background cell style %+v", tm.Style(r.Cells[5].Style))
	}
	if u, ok := tm.HyperlinkAt(nil, term.Point{X: 1, Y: 1}); !ok || string(u) != "https://x.test/a" {
		t.Errorf("HyperlinkAt = %q %v", u, ok)
	}
	if _, ok := tm.HyperlinkAt(nil, term.Point{X: 6, Y: 1}); ok {
		t.Error("HyperlinkAt off the link")
	}
	n := 2
	for tm.NextRow(&r) {
		n++
	}
	if n != 4 {
		t.Errorf("%d rows, want 4", n)
	}
	if c := tm.Cursor(); !c.InView || c.Y != 1 || c.X != 7 || !c.Visible {
		t.Errorf("cursor %+v", c)
	}
	tm.CleanFrame()
	if d := tm.Update(); d != term.Clean {
		t.Errorf("Update after CleanFrame with no writes = %v", d)
	}
}

// TestWrapAndResize checks soft wraps and the reflow on resize.
func TestWrapAndResize(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 10, Rows: 3})
	tm.Write([]byte("0123456789abcde"))
	tm.Update()
	var r term.Row
	tm.NextRow(&r)
	tm.DecodeRow(&r)
	if !r.Wrapped {
		t.Error("the first row is not marked wrapped")
	}
	if err := tm.Resize(20, 3, 8, 16); err != nil {
		t.Fatal(err)
	}
	if s := screen(tm); !strings.HasPrefix(s, "0123456789abcde") {
		t.Errorf("after reflow the screen is %q", s)
	}
	if c, rows := tm.Size(); c != 20 || rows != 3 {
		t.Errorf("Size = %d×%d", c, rows)
	}
}

// TestCallbacks covers the program's queries, bells, the title and OSC 52.
func TestCallbacks(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 40, Rows: 10})
	tm.Write([]byte("ab\x1b[6n"))
	if got := string(tm.TakeReplies(nil)); got != "\x1b[1;3R" {
		t.Errorf("DSR reply %q", got)
	}
	tm.Write([]byte("\x1b[c"))
	if got := string(tm.TakeReplies(nil)); !strings.HasPrefix(got, "\x1b[?") {
		t.Errorf("DA reply %q", got)
	}
	tm.SetColors(term.Colors{FG: 0x112233, BG: 0xfafafa, Cursor: 0x000000})
	tm.Write([]byte("\x1b]11;?\x07"))
	if got := string(tm.TakeReplies(nil)); !strings.Contains(got, "fafa/fafa/fafa") {
		t.Errorf("OSC 11 reply %q", got)
	}
	tm.Write([]byte("\x07\x07"))
	if n := tm.TakeBells(); n != 2 {
		t.Errorf("bells %d", n)
	}
	tm.Write([]byte("\x1b]0;hello title\x07"))
	if s := tm.Title(); s != "hello title" {
		t.Errorf("Title %q", s)
	}
	tm.Write([]byte("\x1b]52;c;aGVsbG8gY2xpcA==\x07"))
	if b, ok := tm.TakeClipboard(nil); !ok || string(b) != "hello clip" {
		t.Errorf("TakeClipboard %q %v", b, ok)
	}
	if _, ok := tm.TakeClipboard(nil); ok {
		t.Error("a second TakeClipboard")
	}
}

func TestKeys(t *testing.T) {
	tm := newTerm(t, term.Options{OptionAsAlt: true})
	enc := func(e term.KeyEvent) string { return string(tm.EncodeKey(nil, e)) }
	for _, c := range []struct {
		e    term.KeyEvent
		want string
	}{
		{term.KeyEvent{Key: term.KeyUp}, "\x1b[A"},
		{term.KeyEvent{Key: term.KeyEnter}, "\r"},
		{term.KeyEvent{Key: term.KeyBackspace}, "\x7f"},
		{term.KeyEvent{Key: term.KeyTab}, "\t"},
		{term.KeyEvent{Key: term.KeyEscape}, "\x1b"},
		{term.KeyEvent{Key: term.KeyF1}, "\x1bOP"},
		{term.KeyEvent{Key: term.KeyDelete}, "\x1b[3~"},
		{term.KeyEvent{Key: term.KeyRight, Mods: term.ModCtrl}, "\x1b[1;5C"},
		{term.KeyEvent{Text: "a", Unshifted: 'a'}, "a"},
		{term.KeyEvent{Text: "A", Unshifted: 'a', Mods: term.ModShift}, "A"},
		{term.KeyEvent{Unshifted: 'c', Mods: term.ModCtrl}, "\x03"},
		{term.KeyEvent{Text: "x", Unshifted: 'x', Mods: term.ModAlt}, "\x1bx"},
		{term.KeyEvent{Text: "é"}, "é"},
		{term.KeyEvent{Key: term.KeyUp, Action: term.KeyRelease}, ""},
	} {
		if got := enc(c.e); got != c.want {
			t.Errorf("EncodeKey(%+v) = %q, want %q", c.e, got, c.want)
		}
	}
	tm.Write([]byte("\x1b[?1h")) // application cursor keys
	if got := enc(term.KeyEvent{Key: term.KeyUp}); got != "\x1bOA" {
		t.Errorf("KeyUp in DECCKM = %q", got)
	}
	if !tm.Modes().AppCursor {
		t.Error("Modes().AppCursor")
	}
}

func TestMouseFocusPaste(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 80, Rows: 24, CellWidth: 8, CellHeight: 16})
	at := func(x, y int) (float32, float32) { return float32(x*8 + 4), float32(y*16 + 8) }
	mouse := func(a term.MouseAction, b term.MouseButton, x, y int) string {
		px, py := at(x, y)
		return string(tm.EncodeMouse(nil, term.MouseEvent{Action: a, Button: b, X: px, Y: py}))
	}
	if got := mouse(term.MousePressed, term.MouseLeft, 2, 3); got != "" || tm.MouseTracking() {
		t.Errorf("a press with no mouse mode = %q", got)
	}
	tm.Write([]byte("\x1b[?1000h\x1b[?1006h"))
	if !tm.MouseTracking() || tm.Modes().Mouse != term.MousePress || !tm.Modes().SGRMouse {
		t.Errorf("modes %+v", tm.Modes())
	}
	if got := mouse(term.MousePressed, term.MouseLeft, 2, 3); got != "\x1b[<0;3;4M" {
		t.Errorf("SGR press = %q", got)
	}
	if got := mouse(term.MouseReleased, term.MouseLeft, 2, 3); got != "\x1b[<0;3;4m" {
		t.Errorf("SGR release = %q", got)
	}
	if got := mouse(term.MousePressed, term.WheelUp, 0, 0); got != "\x1b[<64;1;1M" {
		t.Errorf("SGR wheel = %q", got)
	}
	tm.Write([]byte("\x1b[?1006l"))
	if got := mouse(term.MousePressed, term.MouseRight, 1, 1); got != "\x1b[M\x22\x22\x22" {
		t.Errorf("X10 press = %q", got)
	}

	if got := tm.EncodeFocus(nil, true); len(got) != 0 {
		t.Errorf("focus with mode 1004 off = %q", got)
	}
	tm.Write([]byte("\x1b[?1004h"))
	if got := string(tm.EncodeFocus(nil, true)) + string(tm.EncodeFocus(nil, false)); got != "\x1b[I\x1b[O" {
		t.Errorf("focus = %q", got)
	}

	if got := string(tm.EncodePaste(nil, []byte("a\nb\x1bc"))); got != "a\rb c" {
		t.Errorf("paste = %q", got)
	}
	if tm.PasteSafe([]byte("ls\n")) || !tm.PasteSafe([]byte("plain")) {
		t.Error("PasteSafe")
	}
	tm.Write([]byte("\x1b[?2004h"))
	if got := string(tm.EncodePaste(nil, []byte("x\ny"))); !strings.HasPrefix(got, "\x1b[200~x") || !strings.HasSuffix(got, "y\x1b[201~") {
		t.Errorf("bracketed paste = %q", got)
	}
}

func TestSelectionAndSearch(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 30, Rows: 5})
	tm.Write([]byte("hello world\r\nsecond line here\r\n"))
	tm.SelectWord(term.Point{X: 7, Y: 0})
	if got := string(tm.AppendSelection(nil)); got != "world" {
		t.Errorf("SelectWord: %q", got)
	}
	tm.BeginSelection(term.Point{X: 0, Y: 0})
	tm.ExtendSelection(term.Point{X: 5, Y: 1}, false)
	if got := string(tm.AppendSelection(nil)); got != "hello world\nsecond" {
		t.Errorf("drag selection: %q", got)
	}
	tm.Update()
	var r term.Row
	tm.NextRow(&r)
	if !r.Selected || r.SelFrom != 0 {
		t.Errorf("row 0 selection %+v", r)
	}
	tm.NextRow(&r)
	if !r.Selected || r.SelFrom != 0 || r.SelTo != 5 {
		t.Errorf("row 1 selection %d..%d %v", r.SelFrom, r.SelTo, r.Selected)
	}
	tm.SelectLine(term.Point{X: 3, Y: 1})
	if got := string(tm.AppendSelection(nil)); got != "second line here" {
		t.Errorf("SelectLine: %q", got)
	}
	tm.ClearSelection()
	if tm.HasSelection() || len(tm.AppendSelection(nil)) != 0 {
		t.Error("ClearSelection")
	}

	if n := tm.Find("e"); n != 5 {
		t.Errorf("Find(e) = %d matches, want 5", n)
	}
	if i := tm.FindNext(false); i < 0 {
		t.Errorf("FindNext = %d", i)
	}
	tm.Update()
	spans := tm.AppendViewportMatches(nil)
	if len(spans) != 5 || spans[0] != (term.Span{Y: 0, From: 1, To: 1}) {
		t.Errorf("viewport matches %v", spans)
	}
	tm.Find("")
}

func TestScrollback(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 20, Rows: 5})
	for i := 0; i < 50; i++ {
		tm.Write([]byte("line\r\n"))
	}
	sb := tm.Scrollbar()
	if sb.Total < 50 || sb.Len != 5 || !tm.AtBottom() || tm.ScrollbackRows() < 45 {
		t.Fatalf("scrollbar %+v, rows %d", sb, tm.ScrollbackRows())
	}
	tm.ScrollBy(-10)
	if tm.AtBottom() || tm.Scrollbar().Offset != sb.Offset-10 {
		t.Errorf("after ScrollBy(-10): %+v", tm.Scrollbar())
	}
	tm.ScrollToTop()
	if tm.Scrollbar().Offset != 0 {
		t.Errorf("ScrollToTop: %+v", tm.Scrollbar())
	}
	tm.ScrollToBottom()
	if !tm.AtBottom() {
		t.Error("ScrollToBottom")
	}
}

// fixture is one recorded session in testdata/sessions.
type fixture struct {
	name       string
	data       []byte
	cols, rows int
}

func fixtures(t *testing.T) []fixture {
	paths, _ := filepath.Glob(filepath.Join("testdata", "sessions", "*.vt"))
	if len(paths) == 0 {
		t.Fatal("no fixtures")
	}
	var fs []fixture
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		meta, err := os.ReadFile(strings.TrimSuffix(p, ".vt") + ".json")
		if err != nil {
			t.Fatal(err)
		}
		f := fixture{name: strings.TrimSuffix(filepath.Base(p), ".vt"), data: data}
		f.cols, f.rows = jsonInt(meta, "cols"), jsonInt(meta, "rows")
		fs = append(fs, f)
	}
	return fs
}

func jsonInt(b []byte, key string) int {
	i := bytes.Index(b, []byte(`"`+key+`":`))
	if i < 0 {
		return 0
	}
	n := 0
	for _, c := range b[i+len(key)+3:] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// TestFixtureReplays replays every recorded session (vim, nvim, less, git
// log with and without a pager, btop, Claude Code, OpenCode, mixed UTF-8)
// whole, byte by byte and in random chunks. At eight checkpoints through
// each session (most programs end by restoring an empty main screen) the
// screens in the VT format (text, styles, cursor, modes) must be identical
// across the chunkings, and the plain text must match testdata/screens.
func TestFixtureReplays(t *testing.T) {
	term.NeedLib(t)
	for _, f := range fixtures(t) {
		t.Run(f.name, func(t *testing.T) {
			const nCheck = 8
			var checks [nCheck]int
			for k := range checks {
				checks[k] = len(f.data) * (k + 1) / nCheck
			}
			replay := func(chunk func(rest int) int) (vt [nCheck][]byte, plain []byte) {
				tm := newTerm(t, term.Options{Cols: f.cols, Rows: f.rows})
				off, k := 0, 0
				for off < len(f.data) {
					n := min(chunk(len(f.data)-off), checks[k]-off)
					tm.Write(f.data[off : off+n])
					tm.TakeReplies(nil)
					off += n
					if off == checks[k] {
						vt[k] = tm.AppendScreen(nil, term.VT)
						plain = append(plain, fmt.Sprintf("=== checkpoint %d at byte %d of %d ===\n", k+1, off, len(f.data))...)
						plain = append(plain, tm.AppendScreen(nil, term.Plain)...)
						plain = append(plain, '\n')
						k++
					}
				}
				return vt, plain
			}
			whole, plain := replay(func(rest int) int { return rest })
			same := func(how string, got [nCheck][]byte) {
				t.Helper()
				for k := range got {
					if !bytes.Equal(got[k], whole[k]) {
						t.Errorf("%s: checkpoint %d differs from the whole replay:\n%s", how, k+1, diffAt(whole[k], got[k]))
						return
					}
				}
			}
			bb, _ := replay(func(int) int { return 1 })
			same("byte by byte", bb)
			rng := rand.New(rand.NewPCG(uint64(len(f.data)), 7))
			for i := range 5 {
				rc, _ := replay(func(int) int { return 1 + rng.IntN(64<<i) })
				same(fmt.Sprintf("random chunks up to %d", 64<<i), rc)
			}
			golden := filepath.Join("testdata", "screens", f.name+".txt")
			if *update {
				os.MkdirAll(filepath.Dir(golden), 0o755)
				if err := os.WriteFile(golden, plain, 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}
			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update to record it)", err)
			}
			if !bytes.Equal(plain, want) {
				t.Errorf("screens differ from %s:\n%s", golden, diffAt(want, plain))
			}
		})
	}
}

func diffAt(a, b []byte) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	lo := max(0, i-40)
	return strings.NewReplacer("\x1b", `\e`).Replace("at byte " + itoa(i) + ": want …" + string(a[lo:min(len(a), i+40)]) + "… got …" + string(b[lo:min(len(b), i+40)]) + "…")
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for ; i > 0; i /= 10 {
		b = append([]byte{byte('0' + i%10)}, b...)
	}
	return string(b)
}

// TestSteadyStateAllocations is the zero-allocation rule on our side of
// the boundary: output in, an Update, every row read and decoded, the
// encoders and the Take functions, once warm.
func TestSteadyStateAllocations(t *testing.T) {
	tm := newTerm(t, term.Options{Cols: 120, Rows: 40})
	line := []byte("\x1b[32mok\x1b[0m \x1b[1mbuild\x1b[0m 日本 e\u0301 \x1b]8;;https://x\x1b\\l\x1b]8;;\x1b\\ step done\r\n")
	var row term.Row
	var out []byte
	frame := func() {
		tm.Write(line)
		tm.Update()
		for tm.NextRow(&row) {
			tm.DecodeRow(&row)
		}
		tm.CleanFrame()
		out = tm.EncodeKey(out[:0], term.KeyEvent{Key: term.KeyUp})
		out = tm.EncodeKey(out, term.KeyEvent{Text: "a", Unshifted: 'a'})
		out = tm.EncodeMouse(out, term.MouseEvent{Action: term.MouseMoved, X: 10, Y: 10})
		out = tm.EncodePaste(out, line[:10])
		out = tm.TakeReplies(out)
		tm.TakeBells()
		_ = tm.Cursor()
		_ = tm.Title()
	}
	for range 100 {
		frame()
	}
	if n := testing.AllocsPerRun(200, frame); n != 0 {
		t.Fatalf("%v allocations per steady-state frame, want 0", n)
	}
}
