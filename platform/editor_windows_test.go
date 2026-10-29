//go:build windows

package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/testlock"
	"gonativeui/internal/win32"
)

// The Windows editor (W4): the N4 matrix ported. Keys are posted to the
// editor's window as the pump would deliver them (WM_KEYDOWN, translated to
// WM_CHAR), and SendInput types in one test; marked text is driven through
// IMM with Microsoft's Japanese IME (P5).

// newTestEditor opens a window with an editor placed and focused.
func newTestEditor(t *testing.T, o EditorOptions) (*Window, *Editor) {
	t.Helper()
	lockThread(t)
	w, err := Open(Options{Title: "editor", Width: 420, Height: 260})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	e, err := w.NewEditor(o)
	if err != nil {
		t.Fatalf("NewEditor: %v", err)
	}
	t.Cleanup(e.Release)
	place(w, HostRegion{ID: e.Host(), Rect: [4]float32{20, 20, 380, 220}, Clip: [4]float32{20, 20, 380, 220}})
	e.Focus()
	settle(w)
	e.TakeSignals()
	w.Events()
	return w, e
}

// typeKeys posts what the keyboard would: WM_KEYDOWN for keys, WM_CHAR for
// characters (the pump translates nothing posted as WM_CHAR).
func typeText(e *Editor, s string) {
	for _, u := range utf16Of(s) {
		win32.Call(pPostMessageW, e.hwnd, wmChar, uintptr(u), 1)
	}
}

func utf16Of(s string) []uint16 {
	var u []uint16
	for _, r := range s {
		if r >= 0x10000 {
			r -= 0x10000
			u = append(u, uint16(0xD800+(r>>10)), uint16(0xDC00+(r&0x3FF)))
		} else {
			u = append(u, uint16(r))
		}
	}
	return u
}

func pressKey(e *Editor, vk uintptr) { postKey(e.hwnd, vk) }

// pumpSignals pumps until the editor reports something matching want (or d
// passes) and returns everything it reported.
func pumpSignals(w *Window, e *Editor, d time.Duration, want EditorSignals) EditorSignals {
	var got EditorSignals
	for end := time.Now().Add(d); time.Now().Before(end); {
		Pump(int64(5 * time.Millisecond))
		w.Events()
		got |= e.TakeSignals()
		if got&want == want && want != 0 {
			break
		}
	}
	return got
}

func TestEditorTypesReadsAndMapsBytes(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{Placeholder: "Message"})
	if !e.Focused() {
		t.Fatal("the editor did not take the focus")
	}
	typeText(e, "hi é😀")
	got := pumpSignals(w, e, 2*time.Second, EditorChanged)
	if got&EditorChanged == 0 || string(e.Text()) != "hi é😀" {
		t.Fatalf("typed: signals %b, text %q", got, e.Text())
	}
	if s := e.Selection(); s != (TextSelection{len("hi é😀"), len("hi é😀")}) {
		t.Errorf("caret %+v after typing, want the end", s)
	}
	// Bytes and UTF-16 over an emoji, a combining mark and Arabic.
	text := "a😀éبج\nz"
	if err := e.SetText([]byte(text)); err != nil {
		t.Fatal(err)
	}
	if string(e.Text()) != text {
		t.Fatalf("SetText: text %q, want %q", e.Text(), text)
	}
	if got := e.TakeSignals(); got&EditorChanged != 0 {
		t.Errorf("SetText fired EditorChanged (%b)", got)
	}
	if u, _ := e.ToUTF16(len("a😀")); u != 3 {
		t.Errorf("ToUTF16 after the emoji = %d, want 3", u)
	}
	if b := e.FromUTF16(2); b != 1 {
		t.Errorf("FromUTF16 inside the surrogate pair = %d, want 1 (its start)", b)
	}
	if err := e.SetSelection(TextSelection{len("a😀é"), 1}); err != nil {
		t.Fatal(err)
	}
	if s := e.Selection(); s != (TextSelection{len("a😀é"), 1}) {
		t.Errorf("backward selection read back as %+v", s)
	}
	if err := e.SetSelection(TextSelection{2, 2}); err != ErrNotBoundary {
		t.Errorf("selection inside the emoji's bytes: %v, want ErrNotBoundary", err)
	}
	if err := e.Replace(TextRange{1, len("a😀")}, []byte("x"), TextRange{2, 2}); err != nil {
		t.Fatal(err)
	}
	if want := "axéبج\nz"; string(e.Text()) != want {
		t.Errorf("Replace: %q, want %q", e.Text(), want)
	}
}

func TestEditorUndoGroupsReplace(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	typeText(e, "abc")
	pumpSignals(w, e, time.Second, EditorChanged)
	if err := e.Replace(TextRange{0, 3}, []byte("xyz"), TextRange{3, 3}); err != nil {
		t.Fatal(err)
	}
	if string(e.Text()) != "xyz" {
		t.Fatalf("after Replace: %q", e.Text())
	}
	e.send(emUndo, 0, 0)
	if string(e.Text()) != "abc" {
		t.Errorf("one undo after Replace: %q, want abc (Replace is one step)", e.Text())
	}
	e.send(emUndo, 0, 0)
	if string(e.Text()) != "" {
		t.Errorf("undo of the typing: %q, want empty", e.Text())
	}
	e.send(emRedo, 0, 0)
	e.send(emRedo, 0, 0)
	if string(e.Text()) != "xyz" {
		t.Errorf("two redos: %q, want xyz", e.Text())
	}
	// SetText clears the stack.
	e.SetText([]byte("new"))
	e.send(emCanUndo, 0, 0)
	if e.lres != 0 {
		t.Error("SetText left an undo stack")
	}
}

func TestEditorEnterPolicies(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{Enter: EnterSends})
	typeText(e, "hi")
	pumpSignals(w, e, time.Second, EditorChanged)
	pressKey(e, vkReturn)
	got := pumpSignals(w, e, time.Second, EditorSubmitted)
	if got&EditorSubmitted == 0 || string(e.Text()) != "hi" {
		t.Errorf("EnterSends: Enter gave %b, text %q: want Submitted, text unchanged", got, e.Text())
	}
	e.SetOptions(EditorOptions{Enter: EnterNewline})
	pressKey(e, vkReturn)
	typeText(e, "x")
	got = pumpSignals(w, e, time.Second, EditorChanged)
	if got&EditorSubmitted != 0 || string(e.Text()) != "hi\nx" {
		t.Errorf("EnterNewline: Enter gave %b, text %q: want a line, no Submitted", got, e.Text())
	}
}

// The completion lasts while the editor has the focus: a window of another
// test process that takes the foreground ends it (WM_KILLFOCUS), and the keys
// that follow are plain keys ("completion commands []", 40805350430 and
// 40816023392 on Windows amd64). The test holds the activation as the other
// packages' tests that take the foreground do, and tries again, up to three
// times, only when the editor did lose the focus meanwhile.
func TestEditorCompletionAndHostCommands(t *testing.T) {
	testlock.Hold(t, "activation")
	w, e := newTestEditor(t, EditorOptions{})
	typeText(e, "@al")
	pumpSignals(w, e, time.Second, EditorChanged)
	var cmds []EditorCommand
	for try := 1; ; try++ {
		w.Activate()
		e.Focus()
		settle(w)
		e.TakeSignals()
		e.SetCompletionActive(true)
		for _, vk := range []uintptr{vkDown, vkUp, vkTab, vkReturn, vkEscape} {
			pressKey(e, vk)
		}
		cmds = cmds[:0]
		var got EditorSignals
		for end := time.Now().Add(5 * time.Second); len(cmds) < 5 && time.Now().Before(end); {
			Pump(int64(5 * time.Millisecond))
			w.Events()
			got |= e.TakeSignals()
			for c := e.TakeCommand(); c != CmdNone; c = e.TakeCommand() {
				cmds = append(cmds, c)
			}
		}
		lost := got&EditorCancelled != 0 // what WM_KILLFOCUS reports for a completion it ends
		if !lost || try == 3 {
			break
		}
		t.Logf("try %d: the editor lost the focus to another window (commands %v); again", try, cmds)
		e.SetCompletionActive(false)
	}
	if want := []EditorCommand{CmdDown, CmdUp, CmdTab, CmdAccept, CmdCancel}; len(cmds) != len(want) || cmds[0] != want[0] || cmds[1] != want[1] || cmds[2] != want[2] || cmds[3] != want[3] || cmds[4] != want[4] {
		t.Errorf("completion commands %v, want %v", cmds, want)
	}
	if string(e.Text()) != "@al" {
		t.Errorf("the completion keys changed the text: %q", e.Text())
	}
	// Without a completion, Tab and Escape go back to the app.
	e.SetCompletionActive(false)
	w.Events()
	pressKey(e, vkTab)
	pressKey(e, vkEscape)
	var evs []Event
	for end := time.Now().Add(time.Second); time.Now().Before(end) && countKind(evs, EvHostCommand) < 2; {
		Pump(int64(5 * time.Millisecond))
		evs = append(evs, w.Events()...)
	}
	var keys []Key
	for _, ev := range evs {
		if ev.Kind == EvHostCommand && HostID(ev.Tag) == e.Host() {
			keys = append(keys, ev.Key)
		}
	}
	if len(keys) != 2 || keys[0] != KeyTab || keys[1] != KeyEscape {
		t.Errorf("host commands %v, want Tab, Escape", keys)
	}
	if got := e.TakeSignals(); got&EditorCancelled == 0 {
		t.Errorf("Escape without a completion: signals %b, want Cancelled", got)
	}
}

func TestEditorEdgeArrows(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	e.SetEdgeArrows(true)
	pressKey(e, vkUp)
	if got := pumpSignals(w, e, time.Second, EditorEdgeUp); got&EditorEdgeUp == 0 {
		t.Errorf("Up in an empty editor: %b, want EdgeUp", got)
	}
	e.SetText([]byte("one\ntwo"))
	e.SetSelection(TextSelection{len("one\ntw"), len("one\ntw")})
	pressKey(e, vkUp)
	if got := pumpSignals(w, e, 300*time.Millisecond, EditorEdgeUp); got&EditorEdgeUp != 0 {
		t.Errorf("Up on the last of two lines reported EdgeUp")
	}
	e.SetSelection(TextSelection{len("one\ntw"), len("one\ntw")})
	pressKey(e, vkDown)
	if got := pumpSignals(w, e, time.Second, EditorEdgeDown); got&EditorEdgeDown == 0 {
		t.Errorf("Down on the last line: %b, want EdgeDown", got)
	}
}

func TestEditorMaxLenAndHeight(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{MaxLen: 5, MaxLines: 3})
	typeText(e, "abcdefg")
	pumpSignals(w, e, time.Second, 0)
	if string(e.Text()) != "abcde" {
		t.Errorf("MaxLen 5: text %q", e.Text())
	}
	if err := e.Replace(TextRange{5, 5}, []byte("x"), TextRange{6, 6}); err != ErrTooLong {
		t.Errorf("Replace over MaxLen: %v, want ErrTooLong", err)
	}
	e.SetOptions(EditorOptions{MaxLines: 3})
	e.SetText(nil)
	e.TakeSignals()
	one := e.Height()
	e.SetText([]byte("1\n2"))
	if got := e.TakeSignals(); got&EditorHeightChanged == 0 {
		t.Errorf("two lines: no HeightChanged (%b)", got)
	}
	two := e.Height()
	e.SetText([]byte("1\n2\n3\n4\n5\n6"))
	e.TakeSignals()
	most := e.Height()
	t.Logf("heights: one line %.1f pt, two %.1f, six (MaxLines 3) %.1f; line %d px", one, two, most, e.lineH)
	if !(one > 0 && two > one && most > two) {
		t.Errorf("heights do not grow: %v, %v, %v", one, two, most)
	}
	if lim := 3*float32(e.lineH)/e.scale + 0.5; most > lim {
		t.Errorf("six lines with MaxLines 3: %v pt, over %v", most, lim)
	}
}

func TestEditorPasteRouting(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	orig := Clipboard()
	t.Cleanup(func() { SetClipboardText([]byte(orig)); settleCopy(w) })

	SetClipboardText([]byte("pasted\r\ntext"))
	settleCopy(w)
	e.send(0x0302 /* WM_PASTE */, 0, 0)
	if string(e.Text()) != "pasted\ntext" {
		t.Errorf("text paste: %q", e.Text())
	}
	e.SetText(nil)
	e.TakeSignals()

	f := filepath.Join(t.TempDir(), "doc.txt")
	os.WriteFile(f, []byte("x"), 0o600)
	otherData(t, "hdrop", f)
	e.send(0x0302, 0, 0)
	if got := e.TakeSignals(); got&EditorPastedFiles == 0 || len(e.Text()) != 0 {
		t.Errorf("file paste: signals %b, text %q: want PastedFiles, nothing inserted", got, e.Text())
	}
	otherData(t, "dib24")
	e.send(0x0302, 0, 0)
	if got := e.TakeSignals(); got&EditorPastedImage == 0 || len(e.Text()) != 0 {
		t.Errorf("image paste: signals %b, text %q: want PastedImage, nothing inserted", got, e.Text())
	}
}

// A real composition through IMM with Microsoft's Japanese IME (P5): marked
// text while composing, then committed.
func TestEditorIMEComposition(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	user := win32.LoadSystem("user32.dll")
	imm := win32.LoadSystem("imm32.dll")
	before := win32.Call(win32.Proc(user, "GetKeyboardLayout"), 0)
	ja := win32.Call(win32.Proc(user, "LoadKeyboardLayoutW"), uintptr(unsafe.Pointer(mustUTF16("00000411"))), 1)
	defer win32.Call(win32.Proc(user, "ActivateKeyboardLayout"), before, 0)
	if ja == 0 || !win32.Bool(win32.Call(win32.Proc(imm, "ImmIsIME"), ja)) {
		t.Skip("no Japanese IME on this machine")
	}
	h := win32.Call(pImmGetContext, e.hwnd)
	if h == 0 {
		t.Skip("no input context")
	}
	defer win32.Call(pImmReleaseContext, e.hwnd, h)
	win32.Call(win32.Proc(imm, "ImmSetOpenStatus"), h, 1)
	defer win32.Call(win32.Proc(imm, "ImmSetOpenStatus"), h, 0)
	typeText(e, "x")
	pumpSignals(w, e, time.Second, EditorChanged)
	comp := []uint16{0x3042, 0}
	set := win32.Call(win32.Proc(imm, "ImmSetCompositionStringW"), h, 9 /* SCS_SETSTR */, uintptr(unsafe.Pointer(&comp[0])), 2, 0, 0)
	got := pumpSignals(w, e, 2*time.Second, EditorComposing)
	mr, marked := e.Marked()
	if !marked || got&EditorComposing == 0 || string(e.Text()[mr.Start:mr.End]) != "あ" {
		n := int32(win32.Call(pImmGetCompStringW, h, gcsCompStr, 0, 0))
		t.Fatalf("composing: signals %b, marked %v %+v, text %q, ImmSetCompositionStringW %d, comp bytes %d, IME messages %d %x, focus %#x (editor %#x): want あ marked",
			got, marked, mr, e.Text(), set, n, e.imeMsgs, e.imeLog[:min(e.imeMsgs, len(e.imeLog))], win32.Call(pGetFocus), e.hwnd)
	}
	e.SetCompletionActive(true)
	if e.CompletionActive() {
		t.Error("a completion started while composing")
	}
	if err := e.Replace(TextRange{0, 1}, []byte("y"), TextRange{1, 1}); err != ErrComposing {
		t.Errorf("Replace while composing: %v, want ErrComposing", err)
	}
	win32.Call(pImmNotifyIME, h, niCompStr, cpsComplete, 0)
	got = pumpSignals(w, e, 2*time.Second, EditorChanged)
	if _, marked := e.Marked(); marked || string(e.Text()) != "xあ" || got&EditorComposing != 0 {
		t.Errorf("committed: signals %b, marked %v, text %q: want xあ, not marked", got, marked, e.Text())
	}
}

// Keys typed with SendInput, as a user types them, reach the editor.
func TestEditorSendInput(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	w.Activate()
	settle(w)
	e.Focus()
	if win32.Call(win32.Proc(user32, "GetForegroundWindow")) != w.hwnd {
		t.Skip("the window is not in the foreground")
	}
	type keyInput struct {
		typ         uint32
		_           uint32
		vk, scan    uint16
		flags, time uint32
		extra       uintptr
		_           [8]byte
	}
	var in [4]keyInput
	for i, vk := range []uint16{'A', 'B'} {
		in[2*i] = keyInput{typ: 1, vk: vk}
		in[2*i+1] = keyInput{typ: 1, vk: vk, flags: 2 /* KEYEVENTF_KEYUP */}
	}
	n := win32.Call(win32.Proc(user32, "SendInput"), 4, uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if n != 4 {
		t.Skipf("SendInput sent %d of 4", n)
	}
	pumpSignals(w, e, 2*time.Second, EditorChanged)
	if got := strings.ToLower(string(e.Text())); got != "ab" {
		t.Errorf("SendInput typed %q, want ab", e.Text())
	}
}

// Colour emoji: RichEdit's Direct2D drawing shows 😀 in colour (yellow), in a
// window without a GDI surface (no ChildWindows): the swap chain child shows.
func TestEditorDrawsColourEmoji(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{FontSize: 36, Background: 0xFFFFFFFF, TextColor: 0x000000FF})
	w.Show()
	e.SetText([]byte("😀"))
	settle(w)
	// What DWM composes of the swap chain comes some time after the present:
	// on a loaded runner more than settle's 300 ms (yellow 0 after 2 frames
	// presented, 40806585827 on Windows arm64). The window is read again,
	// pumping, until the emoji shows or 10 s have passed.
	yellow := 0
	start := time.Now()
	for {
		cw, ch, _ := w.Size()
		px := clientPixels(t, w.hwnd, cw, ch)
		for y := int32(20); y < min(220, ch); y++ {
			for x := int32(20); x < min(380, cw); x++ {
				c := px[y*cw+x]
				r, g, b := c>>16&0xFF, c>>8&0xFF, c&0xFF
				if r > 200 && g > 150 && b < 100 {
					yellow++
				}
			}
		}
		if yellow > 0 || time.Since(start) > 10*time.Second {
			break
		}
		pumpEvents([]*Window{w}, 100*time.Millisecond, func([][]Event) bool { return false })
	}
	t.Logf("yellow pixels: %d after %v (frames presented %d)", yellow, time.Since(start).Round(time.Millisecond), e.surf.frames)
	if e.surf.frames == 0 {
		t.Error("the editor presented no frame")
	}
	if yellow == 0 {
		t.Error("no yellow under the emoji: it is not drawn in colour, or the editor's swap chain does not show")
	}
}

// An unfocused editor draws nothing while nothing happens.
func TestEditorIdlePresentsNothing(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{Placeholder: "idle"})
	w.FocusHost(0)
	settle(w)
	n := e.surf.frames
	pumpEvents([]*Window{w}, time.Second, func([][]Event) bool { return false })
	if d := e.surf.frames - n; d != 0 {
		t.Errorf("an unfocused idle editor presented %d frames in 1 s", d)
	}
}

// CaretRect is inside the editor's box.
func TestEditorCaretRect(t *testing.T) {
	w, e := newTestEditor(t, EditorOptions{})
	typeText(e, "abc")
	pumpSignals(w, e, time.Second, EditorChanged)
	r, ok := e.CaretRect()
	if !ok || r[0] < 20 || r[0] > 380 || r[1] < 20 || r[3] > 220 || r[3] <= r[1] {
		t.Errorf("CaretRect %v, %v: want inside [20 20 380 220]", r, ok)
	}
}

// Twenty editors made and released leak no COM object, host or window.
func TestEditorCyclesLeakNothing(t *testing.T) {
	lockThread(t)
	w := openW(t, "editor cycles")
	cycle := func() {
		e, err := w.NewEditor(EditorOptions{Placeholder: "p"})
		if err != nil {
			t.Fatal(err)
		}
		place(w, HostRegion{ID: e.Host(), Rect: [4]float32{0, 0, 200, 60}, Clip: [4]float32{0, 0, 200, 60}})
		e.SetText([]byte("text"))
		Pump(0)
		e.Release()
		Pump(0)
	}
	cycle()
	live, hosts := com.Leaks().Live, ShellObjects().Hosts
	gdi0, user0 := guiObjects()
	for range 20 {
		cycle()
	}
	gdi1, user1 := guiObjects()
	if got := com.Leaks().Live; got != live {
		t.Errorf("COM objects of ours alive: %d after 20 editors, want %d", got, live)
	}
	if got := ShellObjects().Hosts; got != hosts {
		t.Errorf("hosts alive: %d, want %d", got, hosts)
	}
	if user1 > user0+2 || gdi1 > gdi0+4 {
		t.Errorf("GUI objects grew over 20 editors: USER %d → %d, GDI %d → %d", user0, user1, gdi0, gdi1)
	}
}
