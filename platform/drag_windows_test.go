//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// OLE drag and drop on Windows (W8). A drop is simulated by calling the
// window's IDropTarget through its vtable, as OLE does, with our own
// IDataObject (the one a drag out offers); a real drag out runs DoDragDrop
// over our own window with the mouse driven by SendInput.

var (
	pClientToScreenT  = win32.Proc(user32, "ClientToScreen")
	pSendInputT       = win32.Proc(user32, "SendInput")
	pGetSystemMetricT = win32.Proc(user32, "GetSystemMetrics")

	tEffect uint32
	tPt     point
	tEnum   uintptr
	tFmts   [4]formatEtc
	tGot    uint32
	tInput  mouseInput
)

// mouseInput is INPUT with a MOUSEINPUT (40 bytes on 64-bit Windows).
type mouseInput struct {
	typ               uint32
	_                 uint32
	dx, dy            int32
	data, flags, time uint32
	_                 uint32
	extra             uintptr
}

// IDropTarget's methods after IUnknown.
const (
	vtDTEnter = 3
	vtDTOver  = 4
	vtDTLeave = 5
	vtDTDrop  = 6
)

// screenPt is the POINTL word OLE passes for client point (x, y) of w.
func screenPt(w *Window, x, y int32) uintptr {
	tPt = point{x, y}
	win32.Call(pClientToScreenT, w.hwnd, uintptr(unsafe.Pointer(&tPt)))
	return uintptr(uint32(tPt.x)) | uintptr(uint32(tPt.y))<<32
}

// dropTargetOf is w's registered IDropTarget, or 0.
func dropTargetOf(w *Window) uintptr {
	if s := w.oleOf(false); s != nil && s.target != nil {
		return s.target.Ptr()
	}
	return 0
}

// newTestData makes the data object a drag of spec would offer (dragOut);
// release it with dragOut.release.
func newTestData(t *testing.T, w *Window, spec *DragSpec) uintptr {
	t.Helper()
	if !ensureOLE() {
		t.Skip("no OLE apartment")
	}
	dragInit()
	if err := dragOut.fill(w, spec); err != nil {
		t.Fatal(err)
	}
	return dragOut.data.Ptr()
}

func takeEvents(w *Window) []Event {
	Pump(0)
	return append([]Event(nil), w.Events()...)
}

// A simulated drop: enter, move and drop through the window's IDropTarget
// queue EvDragEnter, EvDragMove and EvDrop with the client positions, the
// kinds offered, the items, the handler's target and the paths; the effect
// answered is the handler's. Leave, a refused drag and a text-only window
// follow.
func TestDropThroughIDropTarget(t *testing.T) {
	lockThread(t)
	w := openW(t, "drop")
	var asked []DragKinds
	w.SetDragHandler(func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
		asked = append(asked, offered)
		if x < 10 {
			return DragAnswer{} // a strip that takes nothing
		}
		return DragAnswer{Op: DragOpCopy, Accept: DragAny, Target: 77}
	})
	w.SetDropKinds(DragAny)
	target := dropTargetOf(w)
	if target == 0 {
		t.Fatal("no drop target registered")
	}
	paths := []string{`C:\Users\runner\a b.txt`, `C:\tmp\ünï.png`}
	data := newTestData(t, w, &DragSpec{Items: []DragItem{{Path: paths[0]}, {Path: paths[1]}, {Text: "hello"}}})
	defer dragOut.release()

	tEffect = dropEffectCopy | dropEffectMove
	win32.Com(target, vtDTEnter, data, 0, screenPt(w, 30, 40), uintptr(unsafe.Pointer(&tEffect)))
	if tEffect != dropEffectCopy {
		t.Fatalf("enter effect %#x, want copy", tEffect)
	}
	tEffect = dropEffectCopy | dropEffectMove
	win32.Com(target, vtDTOver, mkShift, screenPt(w, 50, 60), uintptr(unsafe.Pointer(&tEffect)))
	if tEffect != 0 { // Shift asks to move; the handler copies only
		t.Fatalf("over effect with Shift %#x, want none", tEffect)
	}
	tEffect = dropEffectCopy | dropEffectMove | dropEffectLink
	win32.Com(target, vtDTDrop, data, 0, screenPt(w, 70, 80), uintptr(unsafe.Pointer(&tEffect)))
	if tEffect != dropEffectCopy {
		t.Fatalf("drop effect %#x, want copy", tEffect)
	}
	evs := takeEvents(w)
	var enter, move, drop *Event
	for i := range evs {
		switch evs[i].Kind {
		case EvDragEnter:
			enter = &evs[i]
		case EvDragMove:
			move = &evs[i]
		case EvDrop:
			drop = &evs[i]
		case EvDragLeave:
			t.Fatal("EvDragLeave after a drop")
		}
	}
	if enter == nil || move == nil || drop == nil {
		t.Fatalf("events %+v", evs)
	}
	if enter.X != 30 || enter.Y != 40 || DragKinds(enter.Code) != DragFiles|DragText || enter.SelLen != 2 || DragOp(enter.SelStart) != DragOpCopy|DragOpMove || enter.Tag == 0 {
		t.Fatalf("EvDragEnter %+v", *enter)
	}
	if move.X != 50 || move.Y != 60 || DragOp(move.SelStart) != DragOpMove || move.Mods != ModShift || move.Tag != enter.Tag {
		t.Fatalf("EvDragMove %+v", *move)
	}
	want := []byte(paths[0] + "\x00" + paths[1] + "\x00")
	if drop.X != 70 || drop.Y != 80 || DragKinds(drop.Code) != DragFiles || drop.SelStart != 2 || drop.Tag != 77 || drop.Flags&FlagFailed != 0 || !bytes.Equal(w.Text(drop.Text), want) {
		t.Fatalf("EvDrop %+v %q", *drop, w.Text(drop.Text))
	}

	// Over the strip that takes nothing: no effect, and leaving ends it.
	tEffect = dropEffectCopy
	win32.Com(target, vtDTEnter, data, 0, screenPt(w, 5, 5), uintptr(unsafe.Pointer(&tEffect)))
	if tEffect != 0 {
		t.Fatalf("refused enter effect %#x", tEffect)
	}
	win32.Com(target, vtDTLeave)
	evs = takeEvents(w)
	if countKind(evs, EvDragEnter) != 1 || countKind(evs, EvDragLeave) != 1 {
		t.Fatalf("enter and leave: %+v", evs)
	}

	// A window that takes text only gets the text of the same drag.
	w.SetDropKinds(DragText)
	tEffect = dropEffectCopy
	win32.Com(target, vtDTEnter, data, 0, screenPt(w, 20, 20), uintptr(unsafe.Pointer(&tEffect)))
	win32.Com(target, vtDTDrop, data, 0, screenPt(w, 20, 20), uintptr(unsafe.Pointer(&tEffect)))
	evs = takeEvents(w)
	got := false
	for _, e := range evs {
		if e.Kind == EvDrop {
			got = DragKinds(e.Code) == DragText && string(w.Text(e.Text)) == "hello"
		}
	}
	if !got {
		t.Fatalf("text drop: %+v", evs)
	}
	if len(asked) == 0 || asked[0] != DragFiles|DragText {
		t.Fatalf("the handler was asked %v", asked)
	}
}

// An image dragged as PNG arrives as the same PNG.
func TestDropOfAnImage(t *testing.T) {
	lockThread(t)
	w := openW(t, "image drop")
	w.SetDropKinds(DragImage)
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	img := buf.Bytes()
	data := newTestData(t, w, &DragSpec{Items: []DragItem{{PNG: img}}})
	defer dragOut.release()
	target := dropTargetOf(w)
	tEffect = dropEffectCopy
	win32.Com(target, vtDTEnter, data, 0, screenPt(w, 10, 10), uintptr(unsafe.Pointer(&tEffect)))
	win32.Com(target, vtDTDrop, data, 0, screenPt(w, 10, 10), uintptr(unsafe.Pointer(&tEffect)))
	for _, e := range takeEvents(w) {
		if e.Kind == EvDrop {
			if DragKinds(e.Code) != DragImage || !bytes.Equal(w.Text(e.Text), img) {
				t.Fatalf("image drop %+v %q", e, w.Text(e.Text))
			}
			return
		}
	}
	t.Fatal("no EvDrop")
}

// Our IDataObject answers QueryGetData, GetData and EnumFormatEtc for the
// formats of its items.
func TestDragDataObjectFormats(t *testing.T) {
	lockThread(t)
	w := openW(t, "data")
	data := newTestData(t, w, &DragSpec{Items: []DragItem{{Path: `C:\x\y.txt`}, {Text: "line one\nline two"}}})
	defer dragOut.release()
	if !hasFormat(data, cfHDrop) || !hasFormat(data, cfUnicodeText) || hasFormat(data, clipFormats().png) {
		t.Fatal("QueryGetData answers the wrong formats")
	}
	if p, n := readDropFiles(data); n != 1 || string(p) != "C:\\x\\y.txt\x00" {
		t.Fatalf("CF_HDROP: %q (%d)", p, n)
	}
	if s := readDropText(data); string(s) != "line one\nline two" {
		t.Fatalf("CF_UNICODETEXT: %q", s)
	}
	if hr := win32.HR(win32.Com(data, 8 /* EnumFormatEtc */, datadirGet, uintptr(unsafe.Pointer(&tEnum)))); hr < 0 || tEnum == 0 {
		t.Fatalf("EnumFormatEtc = %#x", uint32(hr))
	}
	win32.Com(tEnum, 3 /* Next */, uintptr(len(tFmts)), uintptr(unsafe.Pointer(&tFmts[0])), uintptr(unsafe.Pointer(&tGot)))
	win32.Release(tEnum)
	tEnum = 0
	if tGot != 2 || uint32(tFmts[0].cf) != cfHDrop || uint32(tFmts[1].cf) != cfUnicodeText {
		t.Fatalf("enumerated %d formats: %+v", tGot, tFmts[:tGot])
	}
}

// Twenty registrations, drops and revocations leave no COM object alive.
func TestDropsLeakNothing(t *testing.T) {
	lockThread(t)
	w := openW(t, "drop leaks")
	if !ensureOLE() {
		t.Skip("no OLE apartment")
	}
	dragInit()
	before := com.Leaks()
	for i := 0; i < 20; i++ {
		w.SetDropKinds(DragAny)
		target := dropTargetOf(w)
		if target == 0 {
			t.Fatalf("drop %d: no target", i)
		}
		data := newTestData(t, w, &DragSpec{Items: []DragItem{{Text: "x"}}})
		tEffect = dropEffectCopy
		win32.Com(target, vtDTEnter, data, 0, screenPt(w, 10, 10), uintptr(unsafe.Pointer(&tEffect)))
		win32.Com(target, vtDTDrop, data, 0, screenPt(w, 10, 10), uintptr(unsafe.Pointer(&tEffect)))
		dragOut.release()
		w.SetDropKinds(0)
		if dropTargetOf(w) != 0 {
			t.Fatalf("drop %d: target still registered", i)
		}
		takeEvents(w)
	}
	if got := com.Leaks(); got.Live != before.Live || got.Stale != before.Stale {
		t.Fatalf("COM objects: before %+v after %+v", before, got)
	}
}

// StartDrag needs a press and refuses file promises; a drag out over our own
// window, driven by SendInput, drops its text there and ends with EvDragEnd.
func TestDragOutToOurOwnWindow(t *testing.T) {
	lockThread(t)
	if _, err := (&Window{}).StartDrag(&DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: "a"}}}}); err == nil {
		t.Fatal("an invalid spec started")
	}
	w, err := Open(Options{Title: "drag out", Width: 400, Height: 300})
	if err != nil {
		t.Skip(err)
	}
	defer w.Close()
	if _, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}}); !errors.Is(err, ErrNoPress) {
		t.Fatalf("StartDrag with no press: %v", err)
	}
	if _, err := w.StartDrag(&DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: "a.txt", Write: func(context.Context, string) error { return nil }}}}}); !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("a file promise: %v", err)
	}
	w.SetDropKinds(DragText)
	win32.Call(pSetForegroundWindow, w.hwnd)
	for i := 0; i < 20; i++ {
		takeEvents(w)
	}
	if win32.Call(pGetForegroundWindow) != w.hwnd {
		t.Skip("the window is not in the foreground: SendInput would go elsewhere")
	}
	sw, sh := int32(win32.Call(pGetSystemMetricT, 0)), int32(win32.Call(pGetSystemMetricT, 1))
	send := func(x, y int32, flags uint32) {
		screenPt(w, x, y)
		tInput = mouseInput{dx: tPt.x * 65535 / max(sw-1, 1), dy: tPt.y * 65535 / max(sh-1, 1), flags: flags | 0x8001 /* MOVE|ABSOLUTE */}
		win32.Call(pSendInputT, 1, uintptr(unsafe.Pointer(&tInput)), unsafe.Sizeof(tInput))
	}
	send(60, 60, 0)
	send(60, 60, 0x2) // left down
	var evs []Event
	for end := time.Now().Add(3 * time.Second); w.buttons&(1<<ButtonLeft) == 0 && time.Now().Before(end); {
		evs = append(evs, takeEvents(w)...)
	}
	if w.buttons&(1<<ButtonLeft) == 0 {
		send(60, 60, 0x4)
		t.Skipf("the press did not arrive: %+v", evs)
	}
	tk, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "dragged"}}})
	if err != nil {
		send(60, 60, 0x4)
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { // SendInput from any thread: DoDragDrop's loop has the mouse
		defer close(done)
		time.Sleep(300 * time.Millisecond)
		for i := int32(1); i <= 10; i++ {
			send(60+i*15, 60+i*10, 0)
			time.Sleep(30 * time.Millisecond)
		}
		time.Sleep(100 * time.Millisecond)
		send(210, 160, 0x4) // left up: the drop
	}()
	evs = nil
	var dropped string
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end) && countKind(evs, EvDragEnd) == 0; {
		Pump(int64(10 * time.Millisecond))
		for _, e := range w.Events() {
			if e.Kind == EvDrop {
				dropped = string(w.Text(e.Text))
			}
			evs = append(evs, e)
		}
	}
	<-done
	var drop, endEv *Event
	for i := range evs {
		switch evs[i].Kind {
		case EvDrop:
			drop = &evs[i]
		case EvDragEnd:
			endEv = &evs[i]
		}
	}
	if endEv == nil || endEv.Tag != uint64(tk) {
		t.Fatalf("no EvDragEnd for %d: %+v", tk, evs)
	}
	if drop == nil || dropped != "dragged" || DragOp(endEv.Code) != DragOpCopy {
		t.Fatalf("drop %+v, end %+v", drop, *endEv)
	}
	if dragOut.gen != 0 {
		t.Fatal("the drag's objects are still held")
	}
}
