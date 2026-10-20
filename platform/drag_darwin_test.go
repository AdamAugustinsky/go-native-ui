//go:build darwin

package platform

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// The drag tests drive the real dragging methods of GNUIView with a fake
// NSDraggingInfo (a class defined here with objc.Define) whose pasteboard is
// a real, private NSPasteboard, so the data is read through AppKit as a drop
// from another application would be. The drag out tests replace the call that
// starts the session (it needs a pointer) and call the source's and the file
// promise delegate's methods the way AppKit does.

var (
	fakeOnce sync.Once
	fakeInfo struct {
		pb   objc.ID
		x, y float64
		mask uintptr
		seq  int64
	}
	fakeInfoObj objc.ID

	selWriteObjects   = objc.Sel("writeObjects:")
	selUniquePB       = objc.Sel("pasteboardWithUniqueName")
	selReleaseGlobal  = objc.Sel("releaseGlobally")
	selTIFFRep        = objc.Sel("TIFFRepresentation")
	selItem           = objc.Sel("item")
	selDraggingFrame  = objc.Sel("draggingFrame")
	selRegisteredType = objc.Sel("registeredDraggedTypes")
	selMouseEvent     = objc.Sel("mouseEventWithType:location:modifierFlags:timestamp:windowNumber:context:eventNumber:clickCount:pressure:")
)

func fakeInfoInit() {
	fakeOnce.Do(func() {
		def := objc.Define("GNUIFakeDragInfo", objc.Cls("NSObject"), nil)
		def.Method(selDragLocation, "{CGPoint=dd}@:", func(c *ffi.CB) { c.RetF64(0, fakeInfo.x); c.RetF64(1, fakeInfo.y) })
		def.Method(selDragPasteboard, "@@:", func(c *ffi.CB) { c.Ret(uintptr(fakeInfo.pb)) })
		def.Method(selDragSourceMask, "Q@:", func(c *ffi.CB) { c.Ret(fakeInfo.mask) })
		def.Method(selDragSeq, "q@:", func(c *ffi.CB) { c.Ret(uintptr(fakeInfo.seq)) })
		fakeInfoObj = objc.Send(objc.Send(def.Register(), selAlloc), selInit)
	})
}

// dragRig is a GNUIView without a window, registered as the Window's view.
type dragRig struct {
	t    *testing.T
	w    *Window
	view objc.ID
	pb   objc.ID
	asks []dragAsk
}

type dragAsk struct {
	x, y    float32
	offered DragKinds
	allowed DragOp
}

func newDragRig(t *testing.T) *dragRig {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	if theWindow.Load() != nil {
		t.Skip("a window is open")
	}
	loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	classesOnce.Do(registerClasses)
	dragInit()
	fakeInfoInit()
	v := objc.Send(viewClass, selAlloc)
	v = objc.ID(objc.F.Reset().Arg(uintptr(v)).Arg(uintptr(selInitWithFrame)).ArgRect(0, 0, 100, 100).Call(objc.MsgSend).R0())
	if v == 0 {
		t.Skip("cannot create a view")
	}
	w := &Window{q: newEventQueue(), view: v, win: v, scale: 2, wPt: 100, hPt: 100}
	theWindow.Store(w)
	pb := objc.Retain(objc.Send(clsPasteboard, selUniquePB))
	if pb == 0 {
		t.Skip("no private pasteboard here")
	}
	fakeInfo.pb, fakeInfo.mask, fakeInfo.seq = pb, nsOpCopy|nsOpLink|nsOpMove, 41
	r := &dragRig{t: t, w: w, view: v, pb: pb}
	t.Cleanup(func() {
		w.closeDrag()
		theWindow.Store(nil)
		objc.Send(pb, selReleaseGlobal)
		objc.Release(pb)
	})
	return r
}

// answer makes the window answer every drag with a, and records the asks.
func (r *dragRig) answer(a DragAnswer) {
	r.w.SetDragHandler(func(x, y float32, offered DragKinds, allowed DragOp) DragAnswer {
		r.asks = append(r.asks, dragAsk{x, y, offered, allowed})
		return a
	})
}

// put replaces the pasteboard's contents with one NSPasteboardItem per
// element of items, each a map of type to bytes.
func (r *dragRig) put(items ...map[string][]byte) {
	r.t.Helper()
	objc.Send(r.pb, selClearContents)
	arr := objc.Send(clsNSMutableArray, selArrayClass)
	for _, m := range items {
		it := objc.Send(objc.Send(clsNSPasteboardItem, selAlloc), selInit)
		for typ, b := range m {
			ty := objc.Str(typ)
			data := dataOf(b)
			objc.Send2(it, selSetData, uintptr(data), uintptr(ty))
		}
		objc.Send1(arr, selAddObject, uintptr(it))
		objc.Send(it, selAutorelease)
	}
	if !objc.SendBool1(r.pb, selWriteObjects, uintptr(arr)) {
		r.t.Fatal("writeObjects failed")
	}
}

var dragTestBytes []byte

// dataOf returns an autoreleased NSData with a copy of b.
func dataOf(b []byte) objc.ID {
	dragTestBytes = append(dragTestBytes[:0], b...)
	dragTestBytes = append(dragTestBytes, 0)
	return objc.ID(objc.F.Reset().Arg(uintptr(clsNSData)).Arg(uintptr(selDataWithBytes)).
		ArgPtr(unsafe.Pointer(&dragTestBytes[0])).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
}

func (r *dragRig) at(xPt, yPt float64) {
	fakeInfo.x, fakeInfo.y = xPt, yPt
}

func (r *dragRig) send(sel objc.SEL) uintptr {
	return uintptr(objc.Send1(r.view, sel, uintptr(fakeInfoObj)))
}

func (r *dragRig) drop() bool {
	return objc.SendBool1(r.view, selPerformDrag, uintptr(fakeInfoObj))
}

func (r *dragRig) events() []Event {
	ev := append([]Event(nil), r.w.q.ev...)
	r.w.q.ev = r.w.q.ev[:0]
	return ev
}

func (r *dragRig) text(e Event) string { return string(r.w.q.textOf(e.Text)) }

// next waits for the event a worker queues, through reapDrags.
func (r *dragRig) next() Event {
	r.t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		r.w.reapDrags()
		if len(r.w.q.ev) > 0 {
			ev := r.events()
			if len(ev) != 1 {
				r.t.Fatalf("events %+v", ev)
			}
			return ev[0]
		}
	}
	r.t.Fatal("no event from the worker")
	return Event{}
}

func pngOf(w, h int, c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func fileURLBytes(path string) []byte {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	url := objc.Send1(clsNSURL, selFileURLPath, uintptr(objc.Str(path)))
	return objc.AppendUTF8(nil, objc.Send(url, objc.Sel("absoluteString")))
}

// ---- drag in ----

func TestDragInEnterMoveLeave(t *testing.T) {
	r := newDragRig(t)
	dir := t.TempDir()
	f1 := filepath.Join(dir, "a.txt")
	r.put(map[string][]byte{string(objc.AppendUTF8(nil, tFileURL)): fileURLBytes(f1)})

	// Not registered for anything yet: the view takes no drag.
	if got := r.send(selDragEntered); got != 0 || len(r.w.q.ev) != 0 {
		t.Fatalf("unregistered: op %d events %+v", got, r.w.q.ev)
	}
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragFiles})
	r.w.SetDropKinds(DragFiles | DragText)
	if got := objc.Send(r.view, selRegisteredType); got == 0 || int(objc.Send(got, selURLCount)) != 2 {
		t.Fatalf("registered types: %v", got)
	}

	r.at(10, 80) // points, origin bottom left: pixel (20, 40) from the top left at scale 2, height 100
	if got := r.send(selDragEntered); got != nsOpCopy {
		t.Fatalf("entered answered %d, want copy", got)
	}
	r.at(15, 70)
	if got := r.send(selDragUpdated); got != nsOpCopy {
		t.Fatalf("updated answered %d", got)
	}
	r.send(selDragExited)
	ev := r.events()
	if len(ev) != 3 || ev[0].Kind != EvDragEnter || ev[1].Kind != EvDragMove || ev[2].Kind != EvDragLeave {
		t.Fatalf("events %+v", ev)
	}
	if e := ev[0]; e.X != 20 || e.Y != 40 || DragKinds(e.Code) != DragFiles || DragOp(e.SelStart) != DragOpAll || e.SelLen != 1 || e.Tag != 41 {
		t.Errorf("enter %+v", e)
	}
	if e := ev[1]; e.X != 30 || e.Y != 60 {
		t.Errorf("move %+v", e)
	}
	if len(r.asks) != 2 || r.asks[0] != (dragAsk{20, 40, DragFiles, DragOpAll}) {
		t.Errorf("asks %+v", r.asks)
	}

	// The source allows only a link: the answer is the handler's, clipped to it.
	fakeInfo.mask = nsOpLink
	r.answer(DragAnswer{Op: DragOpCopy | DragOpLink, Accept: DragFiles})
	if got := r.send(selDragEntered); got != nsOpLink {
		t.Errorf("link-only source answered %d", got)
	}
	r.events()

	// A refusal: no operation, and a drag of a kind nobody takes is not reported at all.
	r.answer(DragAnswer{Op: DragOpNone, Accept: DragFiles})
	fakeInfo.mask = nsOpCopy
	if got := r.send(selDragEntered); got != 0 {
		t.Errorf("refusal answered %d", got)
	}
	r.events()
	r.put(map[string][]byte{"public.png": pngOf(2, 2, color.RGBA{255, 0, 0, 255})})
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragAny})
	// (The drag that was over the window has left it.)
	if got := r.send(selDragEntered); got != 0 {
		t.Errorf("an image on a window that takes files and text: op %d", got)
	}
	if ev := r.events(); len(ev) != 1 || ev[0].Kind != EvDragLeave {
		t.Errorf("events %+v", ev)
	}
	if got := r.send(selDragEntered); got != 0 || len(r.w.q.ev) != 0 {
		t.Errorf("a second time: op %d events %+v", got, r.w.q.ev)
	}
}

func TestDropFiles(t *testing.T) {
	r := newDragRig(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a b.txt"), filepath.Join(dir, "\u65e5\u672c \U0001F600.txt")
	r.put(
		map[string][]byte{string(objc.AppendUTF8(nil, tFileURL)): fileURLBytes(a), "public.utf8-plain-text": []byte("a b.txt")},
		map[string][]byte{string(objc.AppendUTF8(nil, tFileURL)): fileURLBytes(b)},
	)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragFiles})
	r.w.SetDropKinds(DragAny)
	r.at(10, 80)
	r.send(selDragEntered)
	r.events()
	if !objc.SendBool1(r.view, selPrepareDrag, uintptr(fakeInfoObj)) || !r.drop() {
		t.Fatal("the drop was refused")
	}
	ev := r.events()
	if len(ev) != 1 || ev[0].Kind != EvDrop || DragKinds(ev[0].Code) != DragFiles || ev[0].SelStart != 2 || ev[0].Flags != 0 || ev[0].X != 20 || ev[0].Y != 40 || ev[0].Tag != 41 {
		t.Fatalf("events %+v", ev)
	}
	if got := r.text(ev[0]); got != a+"\x00"+b+"\x00" {
		t.Errorf("paths %q", got)
	}
	if r.w.drag.inside {
		t.Error("the drag is still over the window after the drop")
	}

	// The target takes text only: the drop is the text, though files are offered.
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.q.reset()
	if !r.drop() {
		t.Fatal("the text drop was refused")
	}
	ev = r.events()
	if len(ev) != 1 || DragKinds(ev[0].Code) != DragText || r.text(ev[0]) != "a b.txt" {
		t.Errorf("text of a file drag: %+v", ev)
	}

	// Refusals end the drag: no data, EvDragLeave when it was over the window.
	r.answer(DragAnswer{Op: DragOpNone, Accept: DragFiles})
	r.send(selDragEntered)
	r.events()
	if r.drop() {
		t.Error("a refusing target took the drop")
	}
	if ev = r.events(); len(ev) != 1 || ev[0].Kind != EvDragLeave {
		t.Errorf("after a refused drop: %+v", ev)
	}
}

func TestDropText(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.SetDropKinds(DragText)
	txt := "h\u00e9llo \U0001F468\u200D\U0001F469 \u65e5\u672c\nline 2"
	r.put(map[string][]byte{"public.utf8-plain-text": []byte(txt)})
	r.at(5, 5)
	if !r.drop() {
		t.Fatal("refused")
	}
	ev := r.events()
	if len(ev) != 1 || ev[0].Kind != EvDrop || DragKinds(ev[0].Code) != DragText || ev[0].Flags != 0 || r.text(ev[0]) != txt {
		t.Fatalf("events %+v %q", ev, r.text(ev[0]))
	}
	// Bounded like a paste: one byte over fails, with nothing kept.
	r.w.q.reset()
	r.put(map[string][]byte{"public.utf8-plain-text": bytes.Repeat([]byte("a"), MaxPaste+1)})
	if !r.drop() {
		t.Fatal("refused")
	}
	ev = r.events()
	if len(ev) != 1 || ev[0].Flags&FlagFailed == 0 || ev[0].Text.Len != 0 || len(r.w.q.text) != 0 {
		t.Errorf("oversized text: %+v (%d bytes kept)", ev, len(r.w.q.text))
	}
}

func TestDropImageConvertsOnAWorker(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragImage})
	r.w.SetDropKinds(DragImage)
	r.at(5, 5)
	want := pngOf(16, 8, color.RGBA{0, 128, 255, 255})

	// A PNG is delivered as it is.
	r.put(map[string][]byte{"public.png": want})
	if !r.drop() {
		t.Fatal("refused")
	}
	if len(r.w.q.ev) != 0 {
		t.Fatalf("the event came before the worker: %+v", r.w.q.ev)
	}
	e := r.next()
	if e.Kind != EvDrop || DragKinds(e.Code) != DragImage || e.Flags != 0 || e.Tag != 41 || !bytes.Equal(r.w.q.textOf(e.Text), want) {
		t.Errorf("png drop: %+v", e)
	}

	// TIFF and JPEG are converted to PNG.
	tiff := tiffOf(t, want)
	var jbuf bytes.Buffer
	src, _ := png.Decode(bytes.NewReader(want))
	if err := jpeg.Encode(&jbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	for name, put := range map[string]map[string][]byte{"tiff": {"public.tiff": tiff}, "jpeg": {"public.jpeg": jbuf.Bytes()}} {
		r.w.q.reset()
		r.put(put)
		if !r.drop() {
			t.Fatalf("%s: refused", name)
		}
		e := r.next()
		if e.Kind != EvDrop || e.Flags != 0 {
			t.Fatalf("%s: %+v", name, e)
		}
		got, err := png.Decode(bytes.NewReader(r.w.q.textOf(e.Text)))
		if err != nil || got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
			t.Errorf("%s: decoded %v, %v", name, got, err)
		}
	}

	// Not an image, and too much data, fail without keeping anything.
	for name, put := range map[string]map[string][]byte{
		"garbage": {"public.tiff": []byte("not an image")},
		"huge":    {"public.png": make([]byte, MaxPaste+1)},
	} {
		r.w.q.reset()
		r.put(put)
		if !r.drop() {
			t.Fatalf("%s: refused", name)
		}
		var e Event
		if name == "huge" {
			ev := r.events()
			if len(ev) != 1 {
				t.Fatalf("%s: %+v", name, ev)
			}
			e = ev[0]
		} else {
			e = r.next()
		}
		if e.Kind != EvDrop || e.Flags&FlagFailed == 0 || e.Text.Len != 0 {
			t.Errorf("%s: %+v", name, e)
		}
	}
	if n := r.w.drag.workers.Load(); n != 0 {
		t.Errorf("%d workers still counted", n)
	}
}

// tiffOf returns the TIFF data of a PNG, made by AppKit.
func tiffOf(t *testing.T, pngData []byte) []byte {
	t.Helper()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	data := dataOf(pngData)
	img := objc.Send1(objc.Send(objc.Send(clsNSImage2, selAlloc), selAutorelease), selInitWithData, uintptr(data))
	tiff := objc.Send(img, selTIFFRep)
	if tiff == 0 {
		t.Fatal("no TIFF")
	}
	p := uintptr(objc.Send(tiff, selBytes))
	n := int(objc.Send(tiff, selDataLength))
	return append([]byte(nil), unsafe.Slice((*byte)(ffi.PtrFromC(p)), n)...)
}

// Hosted native views are siblings of the app's view, not its children: the
// drag types the view registers for are not theirs, and theirs are not touched.
func TestDragTypesDoNotReachHosts(t *testing.T) {
	var (
		err                     error
		ours, theirs, hostTypes int
		isDescendant            bool
	)
	onMain(func() {
		w, e := Open(Options{Title: "drag hosts", Width: 300, Height: 200, Hidden: true})
		if e != nil {
			err = e
			return
		}
		defer w.Close()
		id, e := w.NewHost(HostView, HostOptions{})
		if e != nil {
			err = e
			return
		}
		view := objc.ID(w.HostView(id))
		sel := objc.Sel("isDescendantOf:")
		// A host that registered for dragged types of its own (a text view does).
		objc.Send1(view, selRegisterTypes, uintptr(arrText))
		before := int(objc.Send(objc.Send(view, selRegisteredType), selURLCount))
		w.SetDropKinds(DragFiles)
		ours = int(objc.Send(objc.Send(w.view, selRegisteredType), selURLCount))
		theirs = int(objc.Send(objc.Send(view, selRegisteredType), selURLCount))
		hostTypes = before
		isDescendant = objc.SendBool1(view, sel, uintptr(w.view))
		w.SetDropKinds(0)
		if n := int(objc.Send(objc.Send(w.view, selRegisteredType), selURLCount)); n != 0 {
			ours = -n
		}
	})
	if err != nil {
		t.Skip(err)
	}
	if ours != 1 || theirs != hostTypes || hostTypes != 1 || isDescendant {
		t.Errorf("view registered for %d types, host for %d (had %d), host under the view: %v", ours, theirs, hostTypes, isDescendant)
	}
}

// ---- drag out ----

// pressEvent makes an NSEvent of a left-button press and gives it to the
// window, as pointerEvent does.
func (r *dragRig) press() {
	r.t.Helper()
	pool := objc.F.Call0(objc.PoolPush)
	ev := objc.ID(objc.F.Reset().Arg(uintptr(clsNSEvent)).Arg(uintptr(selMouseEvent)).Arg(1).ArgPoint(50, 50).Arg(0).ArgF64(0).Arg(0).Arg(0).Arg(1).Arg(1).ArgF32(1).Call(objc.MsgSend).R0())
	if ev == 0 {
		r.t.Skip("cannot make a mouse event")
	}
	r.w.notePress(ev)
	objc.F.Call1(objc.PoolPop, pool)
	r.w.drag.skipBtn = true // there is no pointer to ask
}

// session replaces the call that starts the session, and collects its items.
type session struct {
	items objc.ID
	event objc.ID
	calls int
}

func (r *dragRig) stubSession() *session {
	s := new(session)
	old := beginDragSession
	t := r.t
	beginDragSession = func(view, items, event, source objc.ID) objc.ID {
		s.calls++
		if view != r.view || source != dragSource || event != r.w.drag.press {
			t.Errorf("session started with view %v source %v event %v", view, source, event)
		}
		s.items = objc.Retain(items)
		return 1 // a session object: the tests never use it
	}
	t.Cleanup(func() { beginDragSession = old })
	return s
}

func (s *session) count() int { return int(objc.Send(s.items, selURLCount)) }

func (s *session) item(i int) objc.ID { return objc.Send1(s.items, selURLAt, uintptr(i)) }

func (r *dragRig) endSession(op uintptr) {
	objc.F.Reset().Arg(uintptr(dragSource)).Arg(uintptr(selSrcEnded)).Arg(0).ArgPoint(60, 60).Arg(op).Call(objc.MsgSend)
}

func TestStartDragBuildsTheSession(t *testing.T) {
	r := newDragRig(t)
	s := r.stubSession()
	dir := t.TempDir()
	file := filepath.Join(dir, "doc.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	pic := pngOf(4, 4, color.RGBA{9, 9, 9, 255})
	spec := &DragSpec{
		Items: []DragItem{
			{Path: file}, {Text: "t\u00e9xt"}, {PNG: pic},
			{Promise: &FilePromise{Name: "clip.mp4", Type: "public.mpeg-4", Write: func(context.Context, string) error { return nil }}},
		},
		Ops:     DragOpCopy | DragOpMove,
		Preview: &DragPreview{PNG: pngOf(100, 60, color.RGBA{255, 0, 0, 255}), Rect: [4]float32{20, 40, 120, 100}},
	}

	if _, err := r.w.StartDrag(spec); !errors.Is(err, ErrNoPress) {
		t.Fatalf("without a press: %v", err)
	}
	r.press()
	r.w.drag.skipBtn = false
	if _, err := r.w.StartDrag(spec); !errors.Is(err, ErrNoPress) { // the button is not down
		t.Fatalf("with the button up: %v", err)
	}
	r.w.drag.skipBtn = true
	if _, err := r.w.StartDrag(&DragSpec{}); !errors.Is(err, ErrDragSpec) {
		t.Fatalf("empty spec: %v", err)
	}
	if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Text: "a", Path: "/b"}}}); !errors.Is(err, ErrDragSpec) {
		t.Fatalf("two payloads: %v", err)
	}
	if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: "x"}}}}); !errors.Is(err, ErrDragSpec) {
		t.Fatalf("promise without a writer: %v", err)
	}
	if s.calls != 0 {
		t.Fatalf("a refused spec started %d sessions", s.calls)
	}

	ticket, err := r.w.StartDrag(spec)
	if err != nil || ticket == 0 || s.calls != 1 {
		t.Fatalf("StartDrag: %d, %v, %d sessions", ticket, err, s.calls)
	}
	if _, err := r.w.StartDrag(spec); !errors.Is(err, ErrDragBusy) {
		t.Errorf("a second drag: %v", err)
	}
	if s.count() != 4 {
		t.Fatalf("%d dragging items", s.count())
	}

	// AppKit rejects an item with no frame; the first has the preview's, in
	// points from the bottom left: 20..120 x 40..100 px at scale 2, view height 100.
	f := objc.SendRect(s.item(0), selDraggingFrame)
	if f != (objc.Rect{X: 10, Y: 50, W: 50, H: 30}) {
		t.Errorf("preview frame %+v", f)
	}
	for i := 1; i < 4; i++ {
		if f := objc.SendRect(s.item(i), selDraggingFrame); f.W <= 0 || f.H <= 0 {
			t.Errorf("item %d has no frame %+v", i, f)
		}
	}

	// What the items write: the kinds a destination reads back.
	writers := objc.Send(clsNSMutableArray, selArrayClass)
	for i := 0; i < 4; i++ {
		objc.Send1(writers, selAddObject, uintptr(objc.Send(s.item(i), selItem)))
	}
	objc.Send(r.pb, selClearContents)
	if !objc.SendBool1(r.pb, selWriteObjects, uintptr(writers)) {
		t.Fatal("the items cannot be written to a pasteboard")
	}
	if k, n := offeredKinds(r.pb); k != DragAny || n != 4 {
		t.Errorf("the pasteboard offers %v in %d items", k, n)
	}
	q := &eventQueue{}
	if n := appendDropPaths(q, r.pb); n != 1 || string(q.text) != file+"\x00" {
		t.Errorf("file items: %d, %q", n, q.text)
	}
	if got := string(objc.AppendUTF8(nil, objc.Send1(r.pb, selStringForType, uintptr(tText)))); got != "t\u00e9xt" {
		t.Errorf("text item %q", got)
	}

	// The source answers with the spec's operations, and reports the end.
	mask := objc.F.Reset().Arg(uintptr(dragSource)).Arg(uintptr(selSrcMask)).Arg(0).Arg(0).Call(objc.MsgSend).R0()
	if mask != nsOpCopy|nsOpMove {
		t.Errorf("source mask %d", mask)
	}
	r.endSession(nsOpMove)
	ev := r.events()
	if len(ev) != 1 || ev[0].Kind != EvDragEnd || ev[0].Tag != uint64(ticket) || DragOp(ev[0].Code) != DragOpMove {
		t.Fatalf("end: %+v", ev)
	}
	if r.w.drag.active {
		t.Error("still active after the end")
	}
	// The next drag may start, and one nobody took says so.
	ticket2, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Text: "again"}}})
	if err != nil || ticket2 == ticket {
		t.Fatalf("second drag: %d %v", ticket2, err)
	}
	r.endSession(0)
	if ev = r.events(); len(ev) != 1 || ev[0].Code != uint32(DragOpNone) || ev[0].Tag != uint64(ticket2) {
		t.Errorf("cancelled drag: %+v", ev)
	}
	// The press stays valid for no one else's drag: it is released with the window.
	r.w.closeDrag()
	if r.w.drag.press != 0 {
		t.Error("the press is kept after close")
	}
	if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Text: "late"}}}); !errors.Is(err, ErrDragSpec) {
		t.Errorf("drag on a closed window: %v", err)
	}
}

// ---- file promises ----

var (
	pOnce   sync.Once
	pBlocks *objc.BlockKind
	pCalls  [1024]atomic.Int32
	pErrs   [1024]atomic.Uintptr
	pNext   atomic.Int32
)

// promiseBlock is a completion handler (void (^)(NSError *)) for the test with
// index i: it counts its calls and keeps the error it was given.
func promiseBlock(t *testing.T) (req objc.Request, i int) {
	pOnce.Do(func() {
		pBlocks = objc.NewBlockKind("v16@?0@8", true, func(ctx uintptr, c *ffi.CB) {
			pErrs[ctx].Store(c.Int(1))
			pCalls[ctx].Add(1)
		}, nil)
	})
	i = int(pNext.Add(1) - 1)
	if i >= len(pCalls) {
		t.Fatal("no free test block")
	}
	req, err := pBlocks.New(uintptr(i))
	if err != nil {
		t.Fatal(err)
	}
	return req, i
}

type promiseCase struct {
	r        *dragRig
	provider objc.ID
	dir      string
}

// startPromise starts a drag with one promise and returns the provider the
// destination would be given.
func (r *dragRig) startPromise(p *FilePromise) *promiseCase {
	r.t.Helper()
	s := r.stubSession()
	r.press()
	if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Promise: p}}}); err != nil {
		r.t.Fatal(err)
	}
	prov := objc.Retain(objc.Send(s.item(0), selItem))
	if got := className(prov); got != "NSFilePromiseProvider" {
		r.t.Fatalf("the writer is a %s", got)
	}
	return &promiseCase{r: r, provider: prov, dir: r.t.TempDir()}
}

// request is the destination asking for the file: writePromiseToURL:completionHandler:.
func (c *promiseCase) request(name string) (i int) {
	c.r.t.Helper()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	url := objc.Send1(clsNSURL, selFileURLPath, uintptr(objc.Str(filepath.Join(c.dir, name))))
	req, i := promiseBlock(c.r.t)
	objc.F.Reset().Arg(uintptr(dragSource)).Arg(uintptr(selPromiseWrit)).Arg(uintptr(c.provider)).Arg(uintptr(url)).Arg(uintptr(req.Block())).Call(objc.MsgSend)
	req.Sent()
	return i
}

func waitCalls(t *testing.T, i int, r *dragRig) {
	t.Helper()
	for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(2 * time.Millisecond) {
		r.w.reapDrags()
		if pCalls[i].Load() > 0 {
			return
		}
	}
	t.Fatalf("completion %d never called", i)
}

func keptBlocks() int64 { return objc.Leaks().KeptBlocks }

func noPromises(t *testing.T) {
	t.Helper()
	promiseMu.Lock()
	n, q := len(promises), len(promiseReqs)
	promiseMu.Unlock()
	if n != 0 || q != 0 {
		t.Errorf("%d promises and %d requests are still recorded", n, q)
	}
}

func TestFilePromiseFulfilled(t *testing.T) {
	r := newDragRig(t)
	base := keptBlocks()
	gotDest := make(chan string, 1)
	pc := r.startPromise(&FilePromise{Name: "m\u00e9dia.bin", Type: "public.data", Write: func(ctx context.Context, dest string) error {
		gotDest <- dest
		return os.WriteFile(dest, []byte("downloaded"), 0o644)
	}})

	// The delegate names the file on its worker queue.
	name := objc.F.Reset().Arg(uintptr(dragSource)).Arg(uintptr(selPromiseName)).Arg(uintptr(pc.provider)).Arg(uintptr(tText)).Call(objc.MsgSend).R0()
	if got := string(objc.AppendUTF8(nil, objc.ID(name))); got != "m\u00e9dia.bin" {
		t.Errorf("file name %q", got)
	}
	i := pc.request("out.bin")
	if keptBlocks() != base+1 {
		t.Errorf("the completion block was not kept (%d, base %d)", keptBlocks(), base)
	}
	if pCalls[i].Load() != 0 {
		t.Fatal("completed before the writer ran")
	}
	waitCalls(t, i, r)
	if pErrs[i].Load() != 0 || pCalls[i].Load() != 1 {
		t.Errorf("completion: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	if d := <-gotDest; d != filepath.Join(pc.dir, "out.bin") {
		t.Errorf("writer was given %q", d)
	}
	if b, _ := os.ReadFile(filepath.Join(pc.dir, "out.bin")); string(b) != "downloaded" {
		t.Errorf("file %q", b)
	}
	if keptBlocks() != base {
		t.Errorf("block still kept: %d, base %d", keptBlocks(), base)
	}
	// A second request for the same promise is refused at once, on its own block.
	j := pc.request("again.bin")
	if pCalls[j].Load() != 1 || pErrs[j].Load() == 0 || pCalls[i].Load() != 1 {
		t.Errorf("second request: %d calls, error %#x; first %d calls", pCalls[j].Load(), pErrs[j].Load(), pCalls[i].Load())
	}
	r.endSession(nsOpCopy)
	noPromises(t)
}

func TestFilePromiseFailsAndTimesOut(t *testing.T) {
	r := newDragRig(t)
	base := keptBlocks()

	pc := r.startPromise(&FilePromise{Name: "x", Write: func(context.Context, string) error { return errors.New("offline") }})
	i := pc.request("x")
	waitCalls(t, i, r)
	if pCalls[i].Load() != 1 || pErrs[i].Load() == 0 {
		t.Errorf("a failed writer: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	r.endSession(nsOpCopy)

	panicked := r.startPromise(&FilePromise{Name: "p", Write: func(context.Context, string) error { panic("boom") }})
	i = panicked.request("p")
	waitCalls(t, i, r)
	if pCalls[i].Load() != 1 || pErrs[i].Load() == 0 {
		t.Errorf("a panicking writer: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	r.endSession(nsOpCopy)

	release := make(chan struct{})
	slow := r.startPromise(&FilePromise{Name: "s", Timeout: 30 * time.Millisecond, Write: func(ctx context.Context, dest string) error {
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			<-release // ignores the cancellation for a while, then returns success
			return nil
		}
	}})
	i = slow.request("s")
	waitCalls(t, i, r)
	if pCalls[i].Load() != 1 || pErrs[i].Load() == 0 {
		t.Errorf("a timeout: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	close(release) // the writer returns late: its success must not call the handler again
	time.Sleep(50 * time.Millisecond)
	if pCalls[i].Load() != 1 {
		t.Errorf("a late writer completed the promise again: %d calls", pCalls[i].Load())
	}
	r.endSession(nsOpCopy)
	if keptBlocks() != base {
		t.Errorf("blocks kept: %d, base %d", keptBlocks(), base)
	}
	noPromises(t)
}

func TestFilePromiseCancelled(t *testing.T) {
	// The window closes while the writer runs: the handler is called once, with
	// an error, at once; the writer's context is cancelled.
	r := newDragRig(t)
	base := keptBlocks()
	sawCancel := make(chan struct{})
	pc := r.startPromise(&FilePromise{Name: "c", Write: func(ctx context.Context, dest string) error {
		<-ctx.Done()
		close(sawCancel)
		return nil // a success after the cancellation does not count
	}})
	i := pc.request("c")
	r.w.reapDrags() // starts the writer
	r.w.closeDrag()
	if pCalls[i].Load() != 1 || pErrs[i].Load() == 0 {
		t.Fatalf("close: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	select {
	case <-sawCancel:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer's context was not cancelled")
	}
	time.Sleep(50 * time.Millisecond)
	if pCalls[i].Load() != 1 || keptBlocks() != base {
		t.Errorf("after the writer returned: %d calls, kept %d (base %d)", pCalls[i].Load(), keptBlocks(), base)
	}
	noPromises(t)

	// A request that arrives after the window closed is answered at once too.
	if j := pc.request("late"); pCalls[j].Load() != 1 || pErrs[j].Load() == 0 {
		t.Errorf("a request on a closed window: %d calls, error %#x", pCalls[j].Load(), pErrs[j].Load())
	}
}

func TestFilePromiseOfACancelledDrag(t *testing.T) {
	// The drag ended without a drop: nothing will ask for the file, the record
	// goes; a request that comes anyway is answered with an error, once.
	r := newDragRig(t)
	called := atomic.Bool{}
	pc := r.startPromise(&FilePromise{Name: "n", Write: func(context.Context, string) error { called.Store(true); return nil }})
	r.endSession(0)
	noPromises(t)
	i := pc.request("n")
	if pCalls[i].Load() != 1 || pErrs[i].Load() == 0 {
		t.Errorf("request after the cancel: %d calls, error %#x", pCalls[i].Load(), pErrs[i].Load())
	}
	r.w.reapDrags()
	time.Sleep(20 * time.Millisecond)
	if called.Load() {
		t.Error("the writer ran for a cancelled drag")
	}
}

// The promise delegate's queue is ours, and the real session starts in a child
// process (opt in): see TestRealDragSession.
func TestPromiseOperationQueue(t *testing.T) {
	newDragRig(t)
	q := uintptr(objc.F.Reset().Arg(uintptr(dragSource)).Arg(uintptr(selPromiseQ)).Arg(0).Call(objc.MsgSend).R0())
	if q == 0 || objc.ID(q) != promiseQueue {
		t.Errorf("queue %#x", q)
	}
}

// TestRealDragSession starts a real AppKit drag session in a child process:
// AppKit validates the dragging items (a zero frame throws), and the session
// is abandoned when the child exits at once. It is opt in (GNUI_LIVE_DRAG=1)
// because a session flashes a drag image on the desktop.
func TestRealDragSession(t *testing.T) {
	if os.Getenv("GNUI_LIVE_DRAG_CHILD") != "" {
		return // the child runs the body below from TestMain's m.Run
	}
	if os.Getenv("GNUI_LIVE_DRAG") == "" {
		t.Skip("set GNUI_LIVE_DRAG=1 to start a real drag session in a child process")
	}
	out, err := runSelf(t, "GNUI_LIVE_DRAG_CHILD=1", "-test.run=^TestRealDragChild$")
	if err != nil || !strings.Contains(out, "SESSION STARTED") {
		t.Fatalf("child: %v\n%s", err, out)
	}
}

func TestRealDragChild(t *testing.T) {
	if os.Getenv("GNUI_LIVE_DRAG_CHILD") == "" {
		t.Skip("child of TestRealDragSession")
	}
	var res string
	onMain(func() {
		w, err := Open(Options{Title: "drag", Width: 300, Height: 200, Hidden: true})
		if err != nil {
			res = "open: " + err.Error()
			return
		}
		pool := objc.F.Call0(objc.PoolPush)
		wn := uintptr(objc.Send(w.win, objc.Sel("windowNumber")))
		ev := objc.ID(objc.F.Reset().Arg(uintptr(clsNSEventLive())).Arg(uintptr(selMouseEvent)).Arg(1).ArgPoint(50, 50).Arg(0).ArgF64(0).Arg(wn).Arg(0).Arg(1).Arg(1).ArgF32(1).Call(objc.MsgSend).R0())
		w.notePress(ev)
		objc.F.Call1(objc.PoolPop, pool)
		w.drag.skipBtn = true
		_, err = w.StartDrag(&DragSpec{
			Items:   []DragItem{{Text: "hello"}, {Promise: &FilePromise{Name: "p.txt", Write: func(context.Context, string) error { return nil }}}},
			Preview: &DragPreview{PNG: pngOf(40, 20, color.RGBA{255, 0, 0, 255}), Rect: [4]float32{10, 10, 50, 30}},
		})
		if err != nil {
			res = "start: " + err.Error()
			return
		}
		res = "SESSION STARTED"
	})
	println(res)
	if res != "SESSION STARTED" {
		os.Exit(1)
	}
	os.Exit(0) // abandons the session: nothing may fetch events now, the session would hold the loop
}

func clsNSEventLive() objc.ID { dragInit(); return clsNSEvent }

// runSelf runs this test binary with the environment variable env (NAME=value)
// and the arguments, and returns its output.
func runSelf(t *testing.T, env string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(os.Args[0], args...)
	cmd.Env = append(os.Environ(), env)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// A press the application gets is the one a drag starts from; one on the
// window-drag map belongs to the window (a titlebar press moves the window, and
// a box that takes input inside that band is a Block region that keeps it).
func TestPressForDragFollowsTheDragMap(t *testing.T) {
	var (
		skip                      string
		plain, blocked, ownedByWM bool
		pressAfterBlock           bool
	)
	onMain(func() {
		w, err := Open(Options{Title: "press", Width: 300, Height: 200, Hidden: true})
		if err != nil {
			skip = err.Error()
			return
		}
		defer w.Close()
		dragInit()
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		wn := uintptr(objc.Send(w.win, objc.Sel("windowNumber")))
		mk := func(x, y float64) objc.ID {
			return objc.ID(objc.F.Reset().Arg(uintptr(clsNSEvent)).Arg(uintptr(selMouseEvent)).Arg(1).ArgPoint(x, y).Arg(0).ArgF64(0).Arg(wn).Arg(0).Arg(1).Arg(1).ArgF32(1).Call(objc.MsgSend).R0())
		}
		ev := mk(50, 170) // 50 pt from the left, 30 pt from the top
		if objc.Send(ev, selWindow) != w.win {
			skip = "a hidden window has no window number to make an event for"
			return
		}
		s := w.scale
		plain = !w.pointerEvent(ev, 1, Now()) && w.drag.press == ev
		w.drag.press = 0
		// A toolbar band 0..60 pt from the top with a button (a Block) over its middle.
		w.SetDragRegions([]DragRegion{
			{Rect: pxRect(s, 0, 0, 300, 60)},
			{Rect: pxRect(s, 40, 10, 80, 50), Block: true},
		})
		blocked = !w.pointerEvent(ev, 1, Now()) && w.drag.press == ev
		pressAfterBlock = w.drag.press != 0
		w.drag.press = 0
		// A press in the band outside the button is the window's: the app never sees it, so no drag starts from it
		// (checked on the map: the real press would start a window drag).
		ownedByWM = w.drags.at(10*s, 30*s) && !w.drags.at(50*s, 30*s)
	})
	if skip != "" {
		t.Skip(skip)
	}
	if !plain || !blocked || !pressAfterBlock || !ownedByWM {
		t.Errorf("plain press kept %v, press on a Block region kept %v, window-drag band owned by the window %v", plain, blocked, ownedByWM)
	}
}
