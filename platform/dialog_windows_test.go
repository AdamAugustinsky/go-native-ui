//go:build windows

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// The file dialogs on Windows (W8), real IFileOpenDialog and IFileSaveDialog
// objects driven by the test from inside their own modal loop: the window's
// frame handler runs there (modalTick), finds the dialog's window once it is
// on screen (IOleWindow) and ends it, as a user would, with
// IFileDialog::Close, with IDOK, or by withdrawing it (CancelDialog). The
// results are read as the application object delivers them.

var (
	tickName       = mustUTF16("GoNativeUI.TestTick")
	pPostMessageWT = win32.Proc(user32, "PostMessageW")
)

const (
	wmCommandT = 0x0111
	idOK       = 1
)

// winDlgSink keeps the dialog results the application object delivers.
type winDlgSink struct{ got []DialogResult }

func (s *winDlgSink) AppEvent(e *AppEvent, data []byte) {
	if e.Kind == AppEvService && e.Source == SourceDialog {
		s.got = append(s.got, DialogResultOf(e, data))
	}
}

func (s *winDlgSink) AppSafePoint() {}

// useDlgSink makes a sink the application object's for the test.
func useDlgSink(t *testing.T) *winDlgSink {
	s := &winDlgSink{}
	prev := dispatch.sink
	SetAppSink(s)
	t.Cleanup(func() { SetAppSink(prev) })
	return s
}

// dlgDrive is what driveDialog saw.
type dlgDrive struct {
	ticks   int     // ticks the app window got
	inModal int     // of them, inside a native modal loop
	hwnd    uintptr // the dialog's window, once IOleWindow gave one
	since   int     // ticks since it had one
	visible int     // ticks with the dialog's window visible
	acted   bool
	rescued bool // the dialog was closed after 15 s without act
	frames  int  // frames the window's handler drew
}

// driveDialog runs the loop until the dialog of ticket tk has been on screen
// for a few ticks, then calls act once, and keeps pumping until done reports
// true or the time is up. The ticks are a message a goroutine posts to the
// app window every 20 ms, which the dialog's own loop dispatches: act runs on
// the main thread inside it. Each tick also asks w for a frame, which the
// modal loop draws through the frame handler. A dialog still up after 15 s
// is closed (rescued), so that a failure is reported, not a hung test.
func driveDialog(t *testing.T, w *Window, tk DialogTicket, act func(dlg uintptr), done func() bool) (d dlgDrive) {
	t.Helper()
	start := time.Now()
	w.SetFrameHandler(func() { d.frames++ })
	defer w.SetFrameHandler(nil)
	if err := HandleAppMessage(tickMsg(), func(uintptr, uint32, uintptr, uintptr) uintptr {
		d.ticks++
		if NativeLoop() {
			d.inModal++
		}
		w.RequestFrame()
		if d.acted || d.rescued {
			return 0
		}
		h := dialogWindow(tk)
		if h != 0 {
			d.hwnd = h
			d.since++
			if win32.Bool(win32.Call(pIsWindowVisible, h)) {
				d.visible++
			}
		}
		// On screen for a few ticks; on windows-2025 the dialog's window can
		// read as not visible all along, so a second after it exists will do.
		if d.visible >= 5 || d.since >= 50 {
			d.acted = true
			act(h)
			return 0
		}
		if time.Since(start) > 15*time.Second {
			d.rescued = true
			closeDialog(tk)
		}
		return 0
	}); err != nil {
		t.Fatal(err)
	}
	defer HandleAppMessage(tickMsg(), nil)
	stop := make(chan struct{})
	defer close(stop)
	app := AppWindow()
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(20 * time.Millisecond):
				win32.Call(pPostMessageWT, app, uintptr(tickMsg()), 0, 0)
			}
		}
	}()
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); {
		Pump(int64(5 * time.Millisecond))
		w.Events()
		DispatchApp()
		if done() {
			break
		}
	}
	return d
}

var tickID uint32

// tickMsg is driveDialog's tick message.
func tickMsg() uint32 {
	if tickID == 0 {
		tickID = uint32(win32.Call(pRegisterWindowMsg, uintptr(unsafe.Pointer(tickName))))
	}
	return tickID
}

// dialogsIdle reports whether no dialog record is held.
func dialogsIdle() bool {
	for i := range dialogs {
		if dialogs[i].ticket != 0 {
			return false
		}
	}
	return true
}

// closeDialog ends a dialog as a user's Cancel does (IFileDialog::Close, and
// its Cancel button).
func closeDialog(tk DialogTicket) {
	for i := range dialogs {
		if dialogs[i].ticket == tk && dialogs[i].obj != 0 {
			dialogs[i].end()
		}
	}
}

// Twenty dialogs, open and save in turn, each cancelled from inside its own
// loop, give one cancelled result each under their tickets, and leave no
// dialog object, COM object or record behind.
func TestFileDialogsCancelledOnceEach(t *testing.T) {
	lockThread(t)
	w := openW(t, "dialogs")
	sink := useDlgSink(t)
	leaks := com.Leaks()
	for i := 0; i < 20; i++ {
		var tk DialogTicket
		var err error
		kind := DialogOpen
		if i%2 == 0 {
			tk, err = w.OpenPanel(OpenSpec{Types: []string{".txt", "public.png"}, Multiple: i%4 == 0, Start: t.TempDir(), Title: "W8 open"})
		} else {
			kind = DialogSave
			tk, err = w.SavePanel(SaveSpec{Name: "untitled.txt", Types: []string{".txt"}, Start: t.TempDir(), Prompt: "Keep"})
		}
		if err != nil {
			t.Fatalf("dialog %d: %v", i, err)
		}
		if _, err := w.OpenPanel(OpenSpec{}); !errors.Is(err, ErrDialogBusy) {
			t.Fatalf("a second dialog on the window: %v, want ErrDialogBusy", err)
		}
		sink.got = nil
		d := driveDialog(t, w, tk, func(uintptr) { closeDialog(tk) }, func() bool { return len(sink.got) > 0 })
		if !d.acted || d.frames == 0 {
			t.Fatalf("dialog %d: %+v (want it seen on screen, closed, and frames drawn meanwhile)", i, d)
		}
		Pump(int64(20 * time.Millisecond))
		DispatchApp()
		if len(sink.got) != 1 || sink.got[0].Ticket != tk || sink.got[0].Outcome != DialogCancelled || sink.got[0].Kind != kind {
			t.Fatalf("dialog %d (%d): results %+v, want one cancelled", i, tk, sink.got)
		}
	}
	if liveDialogs != 0 {
		t.Fatalf("%d dialog objects alive", liveDialogs)
	}
	if !dialogsIdle() {
		t.Fatalf("records still held: %+v", dialogs)
	}
	if got := com.Leaks(); got.Live != leaks.Live {
		t.Fatalf("COM objects alive: %d before, %d after", leaks.Live, got.Live)
	}
}

// A save dialog confirmed with its OK button answers with the path of the
// name it suggested, in the folder it opened in.
func TestSaveDialogAnswersWithThePath(t *testing.T) {
	lockThread(t)
	w := openW(t, "save")
	sink := useDlgSink(t)
	dir := t.TempDir()
	tk, err := w.SavePanel(SaveSpec{Name: "w8 saved ü.txt", Types: []string{".txt"}, Start: dir})
	if err != nil {
		t.Fatal(err)
	}
	var d dlgDrive
	d = driveDialog(t, w, tk, func(h uintptr) { win32.Call(pPostMessageWT, h, wmCommandT, idOK, 0) }, func() bool { return len(sink.got) > 0 })
	if len(sink.got) == 0 {
		closeDialog(tk)
		t.Fatalf("IDOK did not answer the save dialog: %+v", d)
	}
	r := sink.got[0]
	if r.Ticket != tk || r.Outcome != DialogAnswered || len(r.Paths) != 1 {
		t.Fatalf("result %+v, want one path", r)
	}
	if !strings.EqualFold(filepath.Base(r.Paths[0]), "w8 saved ü.txt") {
		t.Fatalf("path %q, want the suggested name", r.Paths[0])
	}
	a, err1 := os.Stat(filepath.Dir(r.Paths[0]))
	b, err2 := os.Stat(dir)
	if err1 != nil || err2 != nil || !os.SameFile(a, b) {
		t.Fatalf("path %q is not in %q (%v %v)", r.Paths[0], dir, err1, err2)
	}
}

// A dialog the app withdraws closes with no result, and the window can open
// the next one. One withdrawn before the pump showed it never appears.
func TestCancelDialogDeliversNothing(t *testing.T) {
	lockThread(t)
	w := openW(t, "withdraw")
	sink := useDlgSink(t)
	tk, err := w.OpenPanel(OpenSpec{Dirs: true})
	if err != nil {
		t.Fatal(err)
	}
	var withdrew bool
	d := driveDialog(t, w, tk, func(uintptr) { withdrew = w.CancelDialog(tk) }, func() bool { return withdrew && dialogsIdle() })
	if !withdrew {
		t.Fatalf("CancelDialog of the dialog on screen = false: %+v", d)
	}
	for i := 0; i < 20; i++ {
		Pump(int64(5 * time.Millisecond))
		DispatchApp()
	}
	if len(sink.got) != 0 || w.CancelDialog(tk) {
		t.Fatalf("a withdrawn dialog delivered %+v", sink.got)
	}
	tk2, err := w.SavePanel(SaveSpec{})
	if err != nil {
		t.Fatalf("the next dialog: %v", err)
	}
	if !w.CancelDialog(tk2) { // withdrawn before the pump took it up
		t.Fatal("CancelDialog of a queued dialog = false")
	}
	for i := 0; i < 20; i++ {
		Pump(int64(5 * time.Millisecond))
		DispatchApp()
	}
	if len(sink.got) != 0 || liveDialogs != 0 {
		t.Fatalf("results %+v, %d dialogs alive", sink.got, liveDialogs)
	}
}

// A window that closes under its dialog gets the dialog's cancelled result,
// once.
func TestWindowClosedUnderItsDialog(t *testing.T) {
	lockThread(t)
	keep := openW(t, "keeps drawing") // the window driveDialog asks frames of
	w, err := Open(Options{Title: "closes", Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Skip(err)
	}
	sink := useDlgSink(t)
	tk, err := w.OpenPanel(OpenSpec{})
	if err != nil {
		w.Close()
		t.Fatal(err)
	}
	// The window closes from inside the dialog's loop.
	d := driveDialog(t, keep, tk, func(uintptr) { w.Close() }, func() bool { return len(sink.got) > 0 })
	for i := 0; i < 20; i++ {
		Pump(int64(5 * time.Millisecond))
		DispatchApp()
	}
	if len(sink.got) != 1 || sink.got[0].Ticket != tk || sink.got[0].Outcome != DialogCancelled {
		t.Fatalf("results %+v, want one cancelled (%+v)", sink.got, d)
	}
	if liveDialogs != 0 {
		t.Fatalf("%d dialog objects alive", liveDialogs)
	}
}

// The spec's types become file patterns; a spec whose types are all
// unknown is refused, as on macOS. Alerts are drawn on Windows.
func TestDialogSpecMapping(t *testing.T) {
	if got := strings.Join(dialogPatterns([]string{".PNG", "public.jpeg", "com.example.unknown", "*.exe"}), ";"); got != "*.png;*.jpg;*.jpeg" {
		t.Fatalf("patterns %q", got)
	}
	lockThread(t)
	w := openW(t, "spec")
	if _, err := w.OpenPanel(OpenSpec{Types: []string{"com.example.unknown"}}); !errors.Is(err, ErrDialogSpec) {
		t.Fatalf("unknown types: %v, want ErrDialogSpec", err)
	}
	if _, err := w.Alert(AlertSpec{Buttons: []string{"OK"}}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("Alert: %v, want ErrUnsupported", err)
	}
	if NativeDialogs || !NativeFilePanels {
		t.Fatal("Windows has native file panels and drawn alerts")
	}
}
