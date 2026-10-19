//go:build darwin

package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

var fakePanelClass, fakeURLs objc.ID

// The native alert and panel sheets, driven programmatically: a sheet is ended
// by pressing one of its buttons (performClick:) or with endSheet:returnCode:,
// and the answers are read from the window's own event queue, as the app
// would. Every scenario runs on the main thread inside one onMain call.

// dlgRig is a real, visible window and the dialog events it has delivered.
type dlgRig struct {
	w       *Window
	results []DialogResult
	keys    int // EvKey events the app saw
	errs    []string
}

func (r *dlgRig) failf(f string, a ...any) { r.errs = append(r.errs, fmt.Sprintf(f, a...)) }

// openDlgRig opens a window and shows it; nil when no window can be opened.
func openDlgRig() (*dlgRig, error) { return openDlgRigOpts(false) }

func openDlgRigOpts(hidden bool) (*dlgRig, error) {
	if os.Getenv("GNUI_NO_WINDOWS") != "" {
		return nil, fmt.Errorf("GNUI_NO_WINDOWS")
	}
	w, err := Open(Options{Title: "dialog test", Width: 480, Height: 320, Hidden: hidden})
	if err != nil {
		return nil, err
	}
	return &dlgRig{w: w}, nil
}

// pump runs the event loop until done reports true or the time is up, and
// reports whether done became true. Dialog results and keys are recorded.
func (r *dlgRig) pump(d time.Duration, done func() bool) bool {
	end := time.Now().Add(d)
	for {
		if done != nil && done() {
			return true
		}
		if !time.Now().Before(end) {
			return false
		}
		for _, e := range r.w.Wait(5e6) {
			r.record(e)
		}
	}
}

func (r *dlgRig) record(e Event) {
	switch e.Kind {
	case EvDialog:
		r.results = append(r.results, r.w.DialogResult(e))
	case EvKey:
		r.keys++
	}
}

// drain records what the queue holds without waiting.
func (r *dlgRig) drain() {
	for _, e := range r.w.Wait(0) {
		r.record(e)
	}
}

// attached waits until a sheet is on the window.
func (r *dlgRig) attached() bool {
	return r.pump(5*time.Second, func() bool { return objc.Send(r.w.win, selAttachedSheet) != 0 })
}

func (r *dlgRig) button(a objc.ID, i int) objc.ID {
	return objc.Send1(objc.Send(a, objc.Sel("buttons")), selURLAt, uintptr(i))
}

func withRig(f func(*dlgRig)) (errs []string, skip string, leaked bool) {
	return withRigOpts(false, f)
}

func withRigOpts(hidden bool, f func(*dlgRig)) (errs []string, skip string, leaked bool) {
	base := objc.Leaks()
	onMain(func() {
		r, err := openDlgRigOpts(hidden)
		if err != nil {
			skip = "cannot open a window here: " + err.Error()
			return
		}
		f(r)
		if r.w != nil && !r.w.aux.closed {
			r.w.Close()
		}
		errs = r.errs
		after := objc.Leaks()
		leaked = after.Blocks != base.Blocks || after.KeptBlocks != base.KeptBlocks
		for i := range dialogs {
			if dialogs[i].ticket != 0 {
				errs = append(errs, "a dialog record is still taken")
				dialogs[i] = dialogRec{}
			}
		}
	})
	return
}

func runScenario(t *testing.T, f func(*dlgRig)) {
	t.Helper()
	errs, skip, leaked := withRig(f)
	if skip != "" {
		t.Skip(skip)
	}
	for _, e := range errs {
		t.Error(e)
	}
	if leaked {
		t.Errorf("leak counters did not return to the baseline: %+v", objc.Leaks())
	}
}

func TestAlertButtons(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		w := r.w
		for want := 0; want < 3; want++ {
			r.results = nil
			tk, err := w.Alert(AlertSpec{Style: AlertWarning, Message: "Delete the file?", Info: "This cannot be undone.",
				Buttons: []string{"Delete", "Keep", "Cancel"}, Destructive: []int{0}})
			if err != nil || tk == 0 {
				r.failf("Alert: ticket %d, err %v", tk, err)
				return
			}
			if !r.attached() {
				r.failf("the sheet did not attach")
				return
			}
			a := dialogs[0].obj
			if want == 0 {
				// What AppKit holds for the sheet: the texts, the style and the buttons in order.
				str := func(o objc.ID, sel string) string { return string(objc.AppendUTF8(nil, objc.Send(o, objc.Sel(sel)))) }
				if got := str(a, "messageText"); got != "Delete the file?" {
					r.failf("message text %q", got)
				}
				if got := str(a, "informativeText"); got != "This cannot be undone." {
					r.failf("informative text %q", got)
				}
				if got := objc.Send(a, objc.Sel("alertStyle")); got != 0 { // NSAlertStyleWarning
					r.failf("alert style %d, want warning (0)", got)
				}
				if n := objc.Send(objc.Send(a, objc.Sel("buttons")), selURLCount); n != 3 {
					r.failf("%d buttons, want 3", n)
				}
				for i, title := range []string{"Delete", "Keep", "Cancel"} {
					if got := str(r.button(a, i), "title"); got != title {
						r.failf("button %d titled %q, want %q", i, got, title)
					}
				}
				if got := objc.Send(objc.Send(a, selWindow), objc.Sel("initialFirstResponder")); got != r.button(a, 0) {
					r.failf("the initial first responder is not the default button")
				}
				for i, wantD := range []bool{true, false, false} {
					if got := objc.SendBool(r.button(a, i), objc.Sel("hasDestructiveAction")); got != wantD {
						r.failf("button %d hasDestructiveAction %v, want %v", i, got, wantD)
					}
				}
				if !w.dialogOpen() {
					r.failf("dialogOpen is false while the sheet is up")
				}
				if _, err := w.Alert(AlertSpec{Message: "second", Buttons: []string{"OK"}}); err != ErrDialogBusy {
					r.failf("a second dialog on the window: err %v, want ErrDialogBusy", err)
				}
			}
			objc.Send1(r.button(a, want), objc.Sel("performClick:"), 0)
			if !r.pump(5*time.Second, func() bool { return len(r.results) > 0 }) {
				r.failf("button %d: no result", want)
				return
			}
			r.drain()
			if len(r.results) != 1 {
				r.failf("button %d: %d results, want 1", want, len(r.results))
				return
			}
			got := r.results[0]
			if got.Ticket != tk || got.Kind != DialogAlert || got.Outcome != DialogAnswered || got.Button != want || got.Suppressed || got.Paths != nil {
				r.failf("button %d: result %+v (ticket %d)", want, got, tk)
			}
			if w.dialogOpen() {
				r.failf("dialogOpen is true after the answer")
			}
		}
	})
}

func TestAlertSuppression(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		for _, on := range []bool{false, true} {
			r.results = nil
			if _, err := r.w.Alert(AlertSpec{Message: "Quit?", Buttons: []string{"Quit", "Cancel"}, Suppression: "Do not ask again"}); err != nil {
				r.failf("Alert: %v", err)
				return
			}
			if !r.attached() {
				r.failf("the sheet did not attach")
				return
			}
			a := dialogs[0].obj
			sb := objc.Send(a, selSuppressionBtn)
			if got := string(objc.AppendUTF8(nil, objc.Send(sb, objc.Sel("title")))); got != "Do not ask again" {
				r.failf("suppression title %q", got)
			}
			if on {
				objc.Send1(sb, objc.Sel("performClick:"), 0)
			}
			objc.Send1(r.button(a, 1), objc.Sel("performClick:"), 0)
			if !r.pump(5*time.Second, func() bool { return len(r.results) > 0 }) {
				r.failf("no result")
				return
			}
			if got := r.results[0]; got.Suppressed != on || got.Button != 1 {
				r.failf("suppression %v: result %+v", on, got)
			}
		}
	})
}

func TestAlertSpecErrors(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		for _, s := range []AlertSpec{
			{Message: "no buttons"},
			{Message: "too many", Buttons: make([]string, MaxAlertButtons+1)},
			{Message: "bad destructive", Buttons: []string{"OK"}, Destructive: []int{1}},
		} {
			if _, err := r.w.Alert(s); err == nil {
				r.failf("%q: no error", s.Message)
			}
		}
		if _, err := r.w.OpenPanel(OpenSpec{Types: []string{"not.a.known.type.identifier.zzz"}}); err == nil {
			// An unknown identifier still resolves to a dynamic type on some systems; that is fine.
			r.drain()
			for i := range dialogs {
				if dialogs[i].ticket != 0 {
					r.w.CancelDialog(dialogs[i].ticket)
				}
			}
		}
		if _, err := r.w.OpenPanel(OpenSpec{Types: []string{"."}}); err == nil {
			r.failf("an empty extension was accepted")
		}
	})
}

func TestSavePanelResult(t *testing.T) {
	dir := t.TempDir()
	runScenario(t, func(r *dlgRig) {
		tk, err := r.w.SavePanel(SaveSpec{Name: "notes.txt", Types: []string{"public.plain-text"}, Start: dir, Title: "Save", Prompt: "Save"})
		if err != nil {
			r.failf("SavePanel: %v", err)
			return
		}
		if !r.attached() {
			r.failf("the sheet did not attach")
			return
		}
		p := dialogs[0].obj
		if got := string(objc.AppendUTF8(nil, objc.Send(p, objc.Sel("nameFieldStringValue")))); got != "notes.txt" {
			r.failf("name field %q", got)
		}
		objc.Send2(r.w.win, selEndSheet, uintptr(p), 1) // NSModalResponseOK
		if !r.pump(5*time.Second, func() bool { return len(r.results) > 0 }) {
			r.failf("no result")
			return
		}
		got := r.results[0]
		want := filepath.Join(dir, "notes.txt")
		if got.Ticket != tk || got.Kind != DialogSave || got.Outcome != DialogAnswered || got.Button != -1 || len(got.Paths) != 1 || got.Paths[0] != want {
			r.failf("result %+v, want the path %q", got, want)
		}
	})
}

func TestPanelCancelled(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		for _, open := range []bool{true, false} {
			r.results = nil
			var tk DialogTicket
			var err error
			if open {
				tk, err = r.w.OpenPanel(OpenSpec{Files: true, Dirs: true, Multiple: true, Types: []string{"public.png", ".jpg"}})
			} else {
				tk, err = r.w.SavePanel(SaveSpec{Name: "x"})
			}
			if err != nil {
				r.failf("panel: %v", err)
				return
			}
			if !r.attached() {
				r.failf("the sheet did not attach")
				return
			}
			p := dialogs[0].obj
			if open {
				if !objc.SendBool(p, objc.Sel("canChooseDirectories")) || !objc.SendBool(p, objc.Sel("canChooseFiles")) || !objc.SendBool(p, objc.Sel("allowsMultipleSelection")) {
					r.failf("open panel flags")
				}
				if n := objc.Send(objc.Send(p, objc.Sel("allowedContentTypes")), selURLCount); n != 2 {
					r.failf("%d allowed content types, want 2", n)
				}
			}
			objc.Send2(r.w.win, selEndSheet, uintptr(p), 0) // NSModalResponseCancel, what the Cancel button ends it with
			if !r.pump(5*time.Second, func() bool { return len(r.results) > 0 }) {
				r.failf("no result")
				return
			}
			if got := r.results[0]; got.Ticket != tk || got.Outcome != DialogCancelled || got.Paths != nil || got.Button != -1 {
				r.failf("open %v: result %+v", open, got)
			}
		}
	})
}

// finishFake runs finishDialog for an open panel record whose URLs are urls, as
// the real completion would, and returns the results the window queued.
func finishFake(r *dlgRig, urls objc.ID) []DialogResult {
	dialogInit()
	fake := fakePanel(urls)
	for i := range dialogs { // a record as beginDialog makes it, with no sheet
		if dialogs[i].ticket == 0 {
			req, err := dialogBlock.New(uintptr(i))
			if err != nil {
				r.failf("block: %v", err)
				return nil
			}
			req.Sent()
			lastTicket++
			dialogs[i] = dialogRec{ticket: lastTicket, kind: DialogOpen, w: r.w, obj: fake, req: req}
			finishDialog(i, 1)
			req.Cancel() // the real completion would have freed the slot
			break
		}
	}
	r.results = nil
	r.drain()
	return r.results
}

// A panel's paths are copied out as owned strings, whatever they hold.
func TestPanelPathsMarshal(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		dialogInit()
		paths := []string{"/tmp/a.txt", "/tmp/\u65e5\u672c\u8a9e \U0001F600.png", "/tmp/with\nnewline", "/"}
		urls := objc.Send(clsNSMutableArray, selArray)
		for _, p := range paths {
			str := nsStr(p)
			f := objc.F.Reset().Arg(uintptr(clsNSURL)).Arg(uintptr(selFileURLWithPath)).Arg(uintptr(str)).ArgBool(false)
			objc.Send1(urls, selAddObject, f.Call(objc.MsgSend).R0())
		}
		res := finishFake(r, urls)
		if len(res) != 1 {
			r.failf("%d results", len(res))
			return
		}
		if got := res[0]; got.Outcome != DialogAnswered || !slices.Equal(got.Paths, paths) {
			r.failf("paths %q, want %q (%+v)", got.Paths, paths, got)
		}
	})
}

// A path that is not valid UTF-8 comes back byte for byte: the paths are the
// file system's representation, not an NSString's UTF-8 view of them. APFS on
// current macOS refuses to create such a file (EILSEQ), so the test builds the
// URL from the raw bytes of a path that does not exist and checks the bytes
// that come back; it skips where AppKit refuses even that.
func TestPanelPathNotUTF8(t *testing.T) {
	name := "/tmp/gnui-bad\xff\xfename.txt"
	runScenario(t, func(r *dlgRig) {
		dialogInit()
		raw := append([]byte(name), 0) // heap memory, NUL-terminated
		sel := objc.Sel("fileURLWithFileSystemRepresentation:isDirectory:relativeToURL:")
		url := objc.ID(objc.F.Reset().Arg(uintptr(clsNSURL)).Arg(uintptr(sel)).ArgPtr(unsafe.Pointer(&raw[0])).ArgBool(false).Arg(0).Call(objc.MsgSend).R0())
		if url == 0 {
			r.failf("AppKit made no NSURL for raw bytes that are not UTF-8")
			return
		}
		urls := objc.Send(clsNSMutableArray, selArray)
		objc.Send1(urls, selAddObject, uintptr(url))
		res := finishFake(r, urls)
		if len(res) != 1 || len(res[0].Paths) != 1 {
			r.failf("results %+v", res)
			return
		}
		if got := res[0].Paths[0]; got != name {
			r.failf("path %q (% x), want %q (% x)", got, got, name, name)
		}
	})
}

// fakePanel returns an object that answers URLs like a finished NSOpenPanel.
func fakePanel(urls objc.ID) objc.ID {
	if fakePanelClass == 0 {
		def := objc.Define("N2FakePanel", objc.Cls("NSObject"), nil)
		def.Method(selURLs, "@@:", func(c *ffi.CB) { c.Ret(uintptr(fakeURLs)) })
		fakePanelClass = def.Register()
	}
	fakeURLs = urls
	return objc.Send(objc.Send(fakePanelClass, selAlloc), selInit)
}

// TestWindowClosedUnderSheet: closing a window with a sheet attached ends the
// sheet with an abort first, so the app gets exactly one cancelled result and
// no request keeps its slot (the leak check around every scenario).
func TestWindowClosedUnderSheet(t *testing.T) {
	for _, kind := range []string{"alert", "open", "save"} {
		t.Run(kind, func(t *testing.T) {
			runScenario(t, func(r *dlgRig) {
				var tk DialogTicket
				var err error
				switch kind {
				case "alert":
					tk, err = r.w.Alert(AlertSpec{Message: "Open when closed", Buttons: []string{"OK"}})
				case "open":
					tk, err = r.w.OpenPanel(OpenSpec{})
				default:
					tk, err = r.w.SavePanel(SaveSpec{Name: "x"})
				}
				if err != nil {
					r.failf("%v", err)
					return
				}
				if !r.attached() {
					r.failf("the sheet did not attach")
					return
				}
				w := r.w
				w.q.reset()
				w.Close()
				if !w.aux.closed {
					r.failf("the window is not marked closed")
				}
				// What reached the queue now, and whatever AppKit still calls
				// later, must be exactly one cancelled result.
				var got []DialogResult
				collect := func() {
					for _, e := range w.q.ev {
						if e.Kind == EvDialog {
							got = append(got, w.DialogResult(e))
						}
					}
				}
				collect()
				if len(got) != 1 { // AppKit runs the completion inside endSheet, so Close itself delivers it
					t.Logf("results delivered inside Close: %d", len(got))
				}
				w.q.ev = w.q.ev[:0]
				spin(0.4)
				collect()
				if len(got) != 1 {
					r.failf("%d results after closing the window, want exactly 1: %+v", len(got), got)
					return
				}
				if g := got[0]; g.Ticket != tk || g.Outcome != DialogCancelled || g.Button != -1 || g.Paths != nil {
					r.failf("result %+v, want a cancelled result for ticket %d", g, tk)
				}
				if _, err := w.Alert(AlertSpec{Message: "late", Buttons: []string{"OK"}}); err == nil {
					r.failf("a dialog was started on a closed window")
				}
			})
		})
	}
}

// A dialog whose sheet is not attached to the window when it closes (queued, or
// never shown) cannot be ended with endSheet:; the window answers it itself,
// cancels its request so that the late completion is stale, and frees its slot.
func TestAbortUnattachedDialog(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		dialogInit()
		req, err := dialogBlock.New(0)
		if err != nil {
			r.failf("block: %v", err)
			return
		}
		req.Sent()
		lastTicket++
		tk := lastTicket
		dialogs[0] = dialogRec{ticket: tk, kind: DialogSave, w: r.w, obj: fakePanel(0), req: req}
		r.w.q.reset()
		r.w.abortDialogs()
		if req.Live() {
			r.failf("the request is still live")
		}
		if dialogs[0].ticket != 0 {
			r.failf("the record is still taken")
		}
		var got []DialogResult
		for _, e := range r.w.q.ev {
			if e.Kind == EvDialog {
				got = append(got, r.w.DialogResult(e))
			}
		}
		if len(got) != 1 || got[0].Ticket != tk || got[0].Outcome != DialogCancelled || got[0].Kind != DialogSave {
			r.failf("results %+v, want one cancelled save result", got)
		}
	})
}

// A window that was never shown has no sheet attached when it closes: the
// ticket is still answered once, cancelled, and its slot freed.
func TestWindowClosedBeforeSheetAttached(t *testing.T) {
	errs, skip, leaked := withRigOpts(true, func(r *dlgRig) {
		tk, err := r.w.Alert(AlertSpec{Message: "Not shown", Buttons: []string{"OK"}})
		if err != nil {
			r.failf("%v", err)
			return
		}
		w := r.w
		w.q.reset()
		w.Close()
		var got []DialogResult
		for i := 0; i < 2; i++ {
			for _, e := range w.q.ev {
				if e.Kind == EvDialog {
					got = append(got, w.DialogResult(e))
				}
			}
			w.q.ev = w.q.ev[:0]
			spin(0.3)
		}
		if len(got) != 1 || got[0].Ticket != tk || got[0].Outcome != DialogCancelled {
			r.failf("results %+v, want one cancelled result for ticket %d", got, tk)
		}
	})
	if skip != "" {
		t.Skip(skip)
	}
	for _, e := range errs {
		t.Error(e)
	}
	if leaked {
		t.Errorf("leak counters did not return to the baseline: %+v", objc.Leaks())
	}
}

// A dialog the app withdraws closes its sheet and delivers nothing.
func TestCancelDialog(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		w := r.w
		tk, err := w.Alert(AlertSpec{Message: "Never mind", Buttons: []string{"OK"}})
		if err != nil {
			r.failf("%v", err)
			return
		}
		if !r.attached() {
			r.failf("the sheet did not attach")
			return
		}
		stale0 := objc.Leaks().StaleCalls
		if !w.CancelDialog(tk) || w.CancelDialog(tk) {
			r.failf("CancelDialog should report true once, then false")
		}
		r.pump(5*time.Second, func() bool { return objc.Send(w.win, selAttachedSheet) == 0 })
		r.pump(300*time.Millisecond, nil)
		if len(r.results) != 0 {
			r.failf("a withdrawn dialog delivered %+v", r.results)
		}
		if w.dialogOpen() {
			r.failf("the dialog is still open")
		}
		if objc.Leaks().StaleCalls != stale0+1 {
			r.failf("the dropped completion did not arrive as a stale call (%d, want %d)", objc.Leaks().StaleCalls, stale0+1)
		}
		// The window takes a new dialog at once.
		if _, err := w.Alert(AlertSpec{Message: "Again", Buttons: []string{"OK"}}); err != nil {
			r.failf("a dialog after a withdrawn one: %v", err)
		}
	})
}

// While a sheet is up, the window's keys and pointer events belong to it.
func TestSheetOwnsInput(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		w := r.w
		tk, err := w.Alert(AlertSpec{Message: "Modal", Buttons: []string{"OK"}})
		if err != nil {
			r.failf("%v", err)
			return
		}
		if !r.attached() {
			r.failf("the sheet did not attach")
			return
		}
		w.q.reset()
		for _, down := range []bool{true, false} {
			w.handle(testKeyEvent(down, 0x07, "x")) // the X key
		}
		if len(w.q.ev) != 0 {
			r.failf("the app saw %d events under the sheet: %+v", len(w.q.ev), w.q.ev)
		}
		w.CancelDialog(tk)
		r.pump(2*time.Second, func() bool { return objc.Send(w.win, selAttachedSheet) == 0 })
		w.q.reset()
		w.handle(testKeyEvent(true, 0x07, "x"))
		if n := len(w.q.ev); n == 0 || w.q.ev[0].Kind != EvKey {
			r.failf("after the sheet, the key did not reach the app: %+v", w.q.ev)
		}
	})
}

func testKeyEvent(down bool, code uint16, chars string) objc.ID {
	typ := uintptr(11)
	if down {
		typ = 10
	}
	s1 := nsStr(chars)
	s2 := nsStr(chars)
	sel := objc.Sel("keyEventWithType:location:modifierFlags:timestamp:windowNumber:context:characters:charactersIgnoringModifiers:isARepeat:keyCode:")
	return objc.ID(objc.F.Reset().Arg(uintptr(objc.Cls("NSEvent"))).Arg(uintptr(sel)).Arg(typ).ArgPoint(0, 0).Arg(0).ArgF64(0).
		Arg(0).Arg(0).Arg(uintptr(s1)).Arg(uintptr(s2)).ArgBool(false).Arg(uintptr(code)).Call(objc.MsgSend).R0())
}

// spin runs the main run loop for sec seconds.
func spin(sec float64) {
	end := time.Now().Add(time.Duration(sec * float64(time.Second)))
	rl := objc.Send(clsRunLoop, selMainRunLoop)
	for time.Now().Before(end) {
		pool := objc.F.Call0(objc.PoolPush)
		date := objc.ID(objc.F.Reset().Arg(uintptr(clsNSDate)).Arg(uintptr(selDateSinceNow)).ArgF64(0.01).Call(objc.MsgSend).R0())
		objc.Send2(rl, objc.Sel("runMode:beforeDate:"), uintptr(defaultMode), uintptr(date))
		objc.F.Call1(objc.PoolPop, pool)
	}
}

func TestDialogResultDecoding(t *testing.T) {
	w := &Window{q: newEventQueue()}
	off := len(w.q.text)
	w.q.text = append(w.q.text, "/a/b\x00/c d\x00"...)
	e := dialogEvent(7, DialogOpen, DialogAnswered, 2, false, off, len(w.q.text)-off, 1)
	got := w.DialogResult(e)
	if got.Ticket != 7 || got.Kind != DialogOpen || got.Outcome != DialogAnswered || strings.Join(got.Paths, "|") != "/a/b|/c d" || got.Button != -1 {
		t.Errorf("open result %+v", got)
	}
	got = w.DialogResult(dialogEvent(9, DialogAlert, DialogAnswered, 2, true, 0, 0, 1))
	if got.Button != 2 || !got.Suppressed || got.Kind != DialogAlert || got.Paths != nil {
		t.Errorf("alert result %+v", got)
	}
	got = w.DialogResult(dialogEvent(9, DialogAlert, DialogCancelled, 0, false, 0, 0, 1))
	if got.Button != -1 || got.Outcome != DialogCancelled {
		t.Errorf("cancelled alert result %+v", got)
	}
}

// With a sheet open the app's menu key equivalents keep working, as in any
// AppKit app: ⌘Q asks to close, ⌘H hides the application, and an app command's
// shortcut delivers its command, while the keys no menu item takes are still
// withheld from the app behind the sheet.
func TestSheetKeepsMenuKeyEquivalents(t *testing.T) {
	var r struct {
		why                      string
		attached                 bool
		quitKeys, quitCloses     int
		hideActed                bool
		hideKeys                 int
		setCmds                  []uint64
		setKeys                  int
		plainKeys                int
		stillOpen                bool
		sawHideAsk, sawHideAsked bool
	}
	onMain(func() {
		dialogInit()
		rig, why := openMenuRig(testMenuSpec(), false)
		if why != "" {
			r.why = why
			return
		}
		w := rig.w
		defer func() {
			if !w.aux.closed {
				w.Close()
			}
		}()
		if _, err := w.Alert(AlertSpec{Message: "Modal", Buttons: []string{"OK"}}); err != nil {
			r.why = err.Error()
			return
		}
		d := &dlgRig{w: w}
		r.attached = d.attached()
		if !r.attached {
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		cmd := uintptr(1 << 20)
		w.q.reset()
		w.handle(keyDown(w, "q", cmd, 0x0C))
		r.quitKeys, _, r.quitCloses = countEvents(w.q.ev)
		w.q.reset()
		w.menuActed = false
		w.handle(keyDown(w, "h", cmd, 0x04))
		r.hideActed = w.menuActed
		r.hideKeys, _, _ = countEvents(w.q.ev)
		objc.Send1(w.app, objc.Sel("unhide:"), 0) // the test must not leave the application hidden
		w.q.reset()
		w.handle(keyDown(w, ",", cmd, 0x2B))
		r.setKeys, r.setCmds, _ = countEvents(w.q.ev)
		w.q.reset()
		w.handle(keyDown(w, "x", 0, 0x07)) // no menu item: the sheet's
		r.plainKeys, _, _ = countEvents(w.q.ev)
		r.stillOpen = w.dialogOpen()
		w.q.reset()
	})
	if r.why != "" {
		t.Skipf("cannot run here: %s", r.why)
	}
	if !r.attached {
		t.Skip("the sheet did not attach")
	}
	if r.quitKeys != 0 || r.quitCloses != 1 {
		t.Errorf("⌘Q under the sheet: %d key-downs and %d close requests, want none and one", r.quitKeys, r.quitCloses)
	}
	if !r.hideActed || r.hideKeys != 0 {
		t.Errorf("⌘H under the sheet: menu acted %v, %d key-downs, want it to act and no key-down", r.hideActed, r.hideKeys)
	}
	if r.setKeys != 0 || len(r.setCmds) != 1 || r.setCmds[0] != tcSettings {
		t.Errorf("⌘, under the sheet: %d key-downs and commands %v, want none and [%d]", r.setKeys, r.setCmds, tcSettings)
	}
	if r.plainKeys != 0 {
		t.Errorf("a plain key under the sheet reached the app (%d key-downs)", r.plainKeys)
	}
	if !r.stillOpen {
		t.Error("the sheet closed under the keys")
	}
}

// newTestAlert makes an NSAlert as Alert would, for a test that begins it
// behind the app's back.
func newTestAlert(msg string) objc.ID {
	a := objc.Send(objc.Send(clsNSAlert, selAlloc), selInit)
	objc.Send1(a, selSetMessageText, uintptr(nsStr(msg)))
	objc.Send1(a, selAddButton, uintptr(nsStr("OK")))
	return a
}

// A sheet that is not attached when its dialog is cancelled (AppKit queues a
// second sheet behind the first) must not appear later: the dialog stays
// tracked, withdrawn, and the sheet is dismissed the moment it attaches, with
// no result for the app.
func TestCancelQueuedSheet(t *testing.T) {
	runScenario(t, func(r *dlgRig) {
		dialogInit()
		w := r.w
		first, err := w.Alert(AlertSpec{Message: "First", Buttons: []string{"OK"}})
		if err != nil || !r.attached() {
			r.failf("first alert: %v", err)
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		// A second sheet behind the first, begun directly: Alert itself refuses.
		second, err := w.beginDialog(1, DialogAlert, newTestAlert("Queued"))
		if err != nil {
			r.failf("second alert: %v", err)
			return
		}
		sheet2 := dialogs[1].sheetWindow()
		if objc.Send(w.win, selAttachedSheet) == sheet2 {
			r.failf("the second sheet attached while the first is up: not a queued sheet")
			return
		}
		if !w.CancelDialog(second) {
			r.failf("CancelDialog(second) = false")
		}
		if dialogs[1].ticket == 0 || !dialogs[1].withdrawn {
			r.failf("the queued dialog is not tracked as withdrawn after the cancel")
		}
		if !w.dialogOpen() { // the first one is still open
			r.failf("the window lost its open dialog")
		}
		// The first sheet ends; the queued one would attach now.
		objc.Send2(w.win, selEndSheet, uintptr(dialogs[0].sheetWindow()), uintptr(alertFirstButtonRet))
		r.pump(3*time.Second, func() bool { return len(r.results) > 0 })
		r.pump(1500*time.Millisecond, nil) // the queued sheet gets every chance to show
		if len(r.results) != 1 || r.results[0].Ticket != first {
			r.failf("results %+v, want only the first dialog's", r.results)
		}
		if objc.Send(w.win, selAttachedSheet) != 0 {
			r.failf("a withdrawn sheet is on the window")
		}
		if dialogs[1].ticket != 0 {
			r.failf("the withdrawn record was never released")
		}
	})
}
