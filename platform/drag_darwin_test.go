//go:build darwin

package platform

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
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
		pb        objc.ID
		pbQueries int
		x, y      float64
		mask      uintptr
		seq       int64
		source    objc.ID
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
		def.Method(selDragPasteboard, "@@:", func(c *ffi.CB) { fakeInfo.pbQueries++; c.Ret(uintptr(fakeInfo.pb)) })
		def.Method(selDragSourceMask, "Q@:", func(c *ffi.CB) { c.Ret(fakeInfo.mask) })
		def.Method(selDragSeq, "q@:", func(c *ffi.CB) { c.Ret(uintptr(fakeInfo.seq)) })
		def.Method(selDragSource, "@@:", func(c *ffi.CB) { c.Ret(uintptr(fakeInfo.source)) })
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
	if reg.n > 0 {
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
	remove := testInstall(w)
	pb := objc.Retain(objc.Send(clsPasteboard, selUniquePB))
	if pb == 0 {
		t.Skip("no private pasteboard here")
	}
	fakeInfo.pb, fakeInfo.mask, fakeInfo.seq = pb, nsOpCopy|nsOpLink|nsOpMove, 41
	r := &dragRig{t: t, w: w, view: v, pb: pb}
	t.Cleanup(func() {
		w.closeDrag()
		remove()
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
	if got := strings.Join(registeredTypes(r), " "); !strings.Contains(got, "public.file-url") || !strings.Contains(got, "public.utf8-plain-text") {
		t.Fatalf("registered types: %q", got)
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
	ev := []Event{r.next()}
	if ev[0].Kind != EvDrop || DragKinds(ev[0].Code) != DragFiles || ev[0].SelStart != 2 || ev[0].Flags != 0 || ev[0].X != 20 || ev[0].Y != 40 || ev[0].Tag != 0 {
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
	ev = []Event{r.next()}
	if DragKinds(ev[0].Code) != DragText || r.text(ev[0]) != "a b.txt" {
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
	ev := []Event{r.next()}
	if ev[0].Kind != EvDrop || DragKinds(ev[0].Code) != DragText || ev[0].Flags != 0 || r.text(ev[0]) != txt {
		t.Fatalf("events %+v %q", ev, r.text(ev[0]))
	}
	// Bounded like a paste: one byte over fails, with nothing kept.
	r.w.q.reset()
	r.put(map[string][]byte{"public.utf8-plain-text": bytes.Repeat([]byte("a"), MaxPaste+1)})
	if !r.drop() {
		t.Fatal("refused")
	}
	ev = []Event{r.next()}
	if ev[0].Flags&FlagFailed == 0 || ev[0].Text.Len != 0 || len(r.w.q.text) != 0 {
		t.Errorf("oversized text: %+v (%d bytes kept)", ev, len(r.w.q.text))
	}
}

func TestDropImageIsReadAndConvertedOnAWorker(t *testing.T) {
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
	if e.Kind != EvDrop || DragKinds(e.Code) != DragImage || e.Flags != 0 || !bytes.Equal(r.w.q.textOf(e.Text), want) {
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
		e := r.next()
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
		err                                error
		ours, theirs, hostTypes, textTypes int
		isDescendant                       bool
	)
	onMain(func() {
		dragInit() // arrText is made here: before it, the host would register for nothing
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
		dragInit()
		textTypes = int(objc.Send(arrText, selURLCount))
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
	if ours != 1 || theirs != hostTypes || hostTypes != textTypes || isDescendant {
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
	if paths, n := r.readPaths(MaxPaste); n != 1 || string(paths) != file+"\x00" {
		t.Errorf("file items: %d, %q", n, paths)
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

// readPaths reads the file URLs of the rig's pasteboard as the worker does.
func (r *dragRig) readPaths(limit int) ([]byte, int) {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	return readPaths(new(ffi.Frame), objc.Send(r.pb, selPBItems), limit)
}

// ---- copying the dropped data before the drop returns ----

// TestPBOwnerChild is the other application of TestDropDataIsCopiedBeforeTheDropReturns:
// it owns a named pasteboard with one item of text and a PNG, until it is killed.
func TestPBOwnerChild(t *testing.T) {
	name := os.Getenv("GNUI_PB_OWNER")
	if name == "" {
		t.Skip("child of TestDropDataIsCopiedBeforeTheDropReturns")
	}
	onMain(func() {
		dragInit()
		pb := objc.Send1(clsPasteboard, selPBNamed, uintptr(objc.Str(name)))
		objc.Send(pb, selClearContents)
		it := objc.Send(objc.Send(clsNSPasteboardItem, selAlloc), selInit)
		objc.Send2(it, selSetData, uintptr(dataOf([]byte("eager text"))), uintptr(tText))
		objc.Send2(it, selSetData, uintptr(dataOf(pngOf(16, 8, color.RGBA{0, 0, 255, 255}))), uintptr(tPNG))
		arr := objc.Send(clsNSMutableArray, selArrayClass)
		objc.Send1(arr, selAddObject, uintptr(it))
		objc.Send(it, selAutorelease)
		if !objc.SendBool1(pb, selWriteObjects, uintptr(arr)) {
			os.Exit(1)
		}
		os.Stdout.WriteString("READY\n")
		time.Sleep(60 * time.Second)
	})
}

var selPBNamed = objc.Sel("pasteboardWithName:")

// pbOwner starts another process that owns a pasteboard with an item of text
// and a PNG, and makes it the pasteboard of the rig's drags.
func (r *dragRig) pbOwner() objc.ID {
	r.t.Helper()
	name := "dev.gonativeui.test.owner." + strconv.Itoa(os.Getpid())
	cmd := exec.Command(os.Args[0], "-test.run=^TestPBOwnerChild$")
	cmd.Env = append(os.Environ(), "GNUI_PB_OWNER="+name)
	out, err := cmd.StdoutPipe()
	if err != nil {
		r.t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	ready := make(chan bool, 1)
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "READY") {
				ready <- true
				io.Copy(io.Discard, out)
				return
			}
		}
		ready <- false
	}()
	select {
	case ok := <-ready:
		if !ok {
			r.t.Skip("the pasteboard owner did not start")
		}
	case <-time.After(20 * time.Second):
		r.t.Skip("the pasteboard owner did not start in time")
	}
	pb := objc.Retain(objc.Send1(clsPasteboard, selPBNamed, uintptr(objc.Str(name))))
	old := fakeInfo.pb
	fakeInfo.pb = pb
	r.t.Cleanup(func() { fakeInfo.pb = old; objc.Send(pb, selReleaseGlobal); objc.Release(pb) })
	return pb
}

// takeOver makes the pasteboard someone else's: the owner changes, as when
// another drag starts, and the items read before are stale.
func takeOver(t *testing.T, pb objc.ID) {
	t.Helper()
	objc.Send(pb, selClearContents)
	arr := objc.Send(clsNSMutableArray, selArrayClass)
	it := objc.Send(objc.Send(clsNSPasteboardItem, selAlloc), selInit)
	objc.Send2(it, selSetData, uintptr(dataOf([]byte("someone else"))), uintptr(tText))
	objc.Send1(arr, selAddObject, uintptr(it))
	objc.Send(it, selAutorelease)
	if !objc.SendBool1(pb, selWriteObjects, uintptr(arr)) {
		t.Fatal("writeObjects failed")
	}
}

func TestDropDataIsCopiedBeforeTheDropReturns(t *testing.T) {
	r := newDragRig(t)
	pb := r.pbOwner()
	r.w.SetDropKinds(DragAny)
	r.at(5, 5)

	// Text is queued by the time the drop returns, whatever happens to the
	// pasteboard after.
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	if !r.drop() {
		t.Fatal("refused")
	}
	ev := r.events()
	takeOver(t, pb)
	if len(ev) != 1 || ev[0].Kind != EvDrop || ev[0].Flags != 0 || r.text(ev[0]) != "eager text" {
		t.Fatalf("text drop: %+v", ev)
	}

	// An image's bytes are copied before the return too: the pasteboard
	// changes owner straight after, and the decode (on a worker, later) still
	// has the original.
	r.w.q.reset()
	pb = r.pbOwner()
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragImage})
	if !r.drop() {
		t.Fatal("refused")
	}
	takeOver(t, pb)
	e := r.next()
	if e.Kind != EvDrop || e.Flags != 0 || DragKinds(e.Code) != DragImage {
		t.Fatalf("image drop: %+v", e)
	}
	got, err := png.Decode(bytes.NewReader(r.w.q.textOf(e.Text)))
	if err != nil || got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Fatalf("decoded %v, %v", got, err)
	}
	if c := color.RGBAModel.Convert(got.At(3, 3)).(color.RGBA); c.B < 250 || c.R > 5 {
		t.Errorf("pixel %+v: not the owner's blue image", c)
	}
}

// A drag of our own session is answered from its spec, not from the pasteboard
// (whose data a lazy provider would fulfil through the very thread that is in
// the drop).
func TestOwnDragIsAnsweredFromTheSpec(t *testing.T) {
	r := newDragRig(t)
	s := r.stubSession()
	r.press()
	file := filepath.Join(t.TempDir(), "a b.txt")
	pic := pngOf(6, 4, color.RGBA{9, 200, 9, 255})
	if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Path: file}, {Text: "own t\u00e9xt"}, {PNG: pic}}}); err != nil || s.calls != 1 {
		t.Fatalf("StartDrag: %v", err)
	}
	pic[0] = 0 // the caller's slice is its own again
	// The pasteboard holds something else, which must not be read.
	r.put(map[string][]byte{"public.utf8-plain-text": []byte("pasteboard text"), "public.png": pngOf(2, 2, color.RGBA{255, 0, 0, 255}),
		string(objc.AppendUTF8(nil, tFileURL)): fileURLBytes("/pasteboard/file")})
	fakeInfo.source = dragSource
	t.Cleanup(func() { fakeInfo.source = 0 })
	r.w.SetDropKinds(DragAny)
	r.at(5, 5)

	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragFiles})
	if !r.drop() {
		t.Fatal("refused")
	}
	if ev := r.events(); len(ev) != 1 || ev[0].SelStart != 1 || r.text(ev[0]) != file+"\x00" {
		t.Errorf("files: %+v %q", ev, r.text(ev[0]))
	}
	r.w.q.reset()
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	if !r.drop() {
		t.Fatal("refused")
	}
	if ev := r.events(); len(ev) != 1 || r.text(ev[0]) != "own t\u00e9xt" {
		t.Errorf("text: %+v", ev)
	}
	r.w.q.reset()
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragImage})
	if !r.drop() {
		t.Fatal("refused")
	}
	ev := r.events()
	if len(ev) != 1 || ev[0].Flags != 0 {
		t.Fatalf("image: %+v", ev)
	}
	if got, err := png.Decode(bytes.NewReader(r.w.q.textOf(ev[0].Text))); err != nil || got.Bounds().Dx() != 6 {
		t.Errorf("image: %v %v", got, err)
	}

	// After the session the spec's data is gone: a later drag from "us" reads the pasteboard.
	r.endSession(0)
	r.w.q.reset()
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	if !r.drop() {
		t.Fatal("refused")
	}
	if ev := r.events(); len(ev) != 1 || r.text(ev[0]) != "pasteboard text" {
		t.Errorf("after the session: %+v", ev)
	}
}

// An item that offers a file URL as well as image data: the file is the image.
func TestImageDropTakesTheFileURLWhenThereIsOne(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragImage})
	r.w.SetDropKinds(DragImage)
	r.at(5, 5)
	dir := t.TempDir()
	var jbuf bytes.Buffer
	src, _ := png.Decode(bytes.NewReader(pngOf(16, 8, color.RGBA{200, 100, 0, 255})))
	if err := jpeg.Encode(&jbuf, src, nil); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "photo.jpg")
	if err := os.WriteFile(file, jbuf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	url := string(objc.AppendUTF8(nil, tFileURL))
	r.put(map[string][]byte{url: fileURLBytes(file), "public.png": pngOf(2, 2, color.RGBA{255, 0, 0, 255})})
	if !r.drop() {
		t.Fatal("refused")
	}
	e := r.next()
	got, err := png.Decode(bytes.NewReader(r.w.q.textOf(e.Text)))
	if e.Flags != 0 || err != nil || got.Bounds().Dx() != 16 || got.Bounds().Dy() != 8 {
		t.Errorf("the file's image was not the drop: %+v %v %v", e, got, err)
	}

	// A file that is not an image, a directory, a file over MaxPaste, and a
	// FIFO (opening one would block the worker) all fail.
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o644); err != nil {
		t.Fatal(err)
	}
	big := filepath.Join(dir, "big.png")
	if err := os.WriteFile(big, make([]byte, MaxPaste+1), 0o644); err != nil {
		t.Fatal(err)
	}
	text := filepath.Join(dir, "a.txt")
	os.WriteFile(text, []byte("not an image"), 0o644)
	for name, path := range map[string]string{"text file": text, "directory": dir, "big": big, "fifo": fifo, "missing": filepath.Join(dir, "none.png")} {
		r.w.q.reset()
		r.put(map[string][]byte{url: fileURLBytes(path), "public.png": pngOf(2, 2, color.RGBA{255, 0, 0, 255})})
		if !r.drop() {
			t.Fatalf("%s: refused", name)
		}
		if e := r.next(); e.Flags&FlagFailed == 0 || e.Text.Len != 0 {
			t.Errorf("%s: %+v", name, e)
		}
	}
	r.waitWorkers()
}

// waitWorkers waits for the decoding goroutines of the rig's window to end.
func (r *dragRig) waitWorkers() {
	r.t.Helper()
	for end := time.Now().Add(10 * time.Second); r.w.drag.workers.Load() != 0; time.Sleep(2 * time.Millisecond) {
		if time.Now().After(end) {
			r.t.Fatal("a decoding goroutine never ended")
		}
	}
}

func TestDropFilesListThatDoesNotFitIsRefusedWhole(t *testing.T) {
	r := newDragRig(t)
	dir := t.TempDir()
	var names [3]string
	var items []map[string][]byte
	for i := range names {
		names[i] = filepath.Join(dir, strings.Repeat(string(rune('a'+i)), 20))
		items = append(items, map[string][]byte{string(objc.AppendUTF8(nil, tFileURL)): fileURLBytes(names[i])})
	}
	r.put(items...)
	all := len(names[0]) + len(names[1]) + len(names[2]) + 3
	if paths, n := r.readPaths(all); n != 3 || len(paths) != all {
		t.Fatalf("a list that fits exactly: %d paths, %d bytes", n, len(paths))
	}
	// One byte short: not the first two, nothing.
	if paths, n := r.readPaths(all - 1); n != 0 || paths != nil {
		t.Errorf("a list one byte over: %d paths, %q", n, paths)
	}
	// Whatever came before in the queue is no part of the measure.
	r.w.q.text = bytes.Repeat([]byte("x"), MaxPaste)
	if _, n := r.readPaths(MaxPaste); n != 3 {
		t.Errorf("with a full queue: %d paths", n)
	}
}

// hugePNG is a 1-bit PNG of w by h pixels: a few tens of kilobytes that decode
// to w*h*4 bytes in a 32-bit bitmap (a decompression bomb).
func hugePNG(w, h uint32) []byte {
	var out bytes.Buffer
	out.WriteString("\x89PNG\r\n\x1a\n")
	chunk := func(typ string, data []byte) {
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(data)))
		out.Write(n[:])
		out.WriteString(typ)
		out.Write(data)
		binary.BigEndian.PutUint32(n[:], crc32.ChecksumIEEE(append([]byte(typ), data...)))
		out.Write(n[:])
	}
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 1 // bit depth 1, colour type 0 (grey)
	chunk("IHDR", ihdr)
	var z bytes.Buffer
	zw, _ := zlib.NewWriterLevel(&z, zlib.BestCompression)
	row := make([]byte, 1+(w+7)/8) // a filter byte, then zeros
	for y := uint32(0); y < h; y++ {
		zw.Write(row)
	}
	zw.Close()
	chunk("IDAT", z.Bytes())
	chunk("IEND", nil)
	return out.Bytes()
}

func TestDropImageOverTheLimitsIsRefusedBeforeDecoding(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragImage})
	r.w.SetDropKinds(DragImage)
	r.at(5, 5)

	pool := objc.F.Call0(objc.PoolPush)
	w, h, ok := imageSize(new(ffi.Frame), dataOf(hugePNG(20000, 20000)))
	objc.F.Call1(objc.PoolPop, pool)
	if !ok || w != 20000 || h != 20000 {
		t.Fatalf("the header read as %d x %d (%v): the test would prove nothing", w, h, ok)
	}

	cases := []struct {
		name string
		typ  string
		data []byte
	}{
		{"over 100 MP, as a PNG", "public.png", hugePNG(20000, 20000)},
		{"over 100 MP, as a TIFF", "public.tiff", hugePNG(20000, 20000)},
		{"under 100 MP, over the conversion limit", "public.tiff", hugePNG(10000, 10000)},
		{"under 100 MP, over the conversion limit, JPEG", "public.jpeg", hugePNG(10000, 10000)},
	}
	for _, c := range cases {
		before := convertCalls.Load()
		r.w.q.reset()
		r.put(map[string][]byte{c.typ: c.data})
		if !r.drop() {
			t.Fatalf("%s: refused", c.name)
		}
		e := r.next()
		if e.Kind != EvDrop || e.Flags&FlagFailed == 0 || e.Text.Len != 0 {
			t.Errorf("%s: %+v", c.name, e)
		}
		if convertCalls.Load() != before {
			t.Errorf("%s: the image was decoded", c.name)
		}
	}

	// Conversions run one at a time: while another holds the turn, this one waits.
	r.w.q.reset()
	r.put(map[string][]byte{"public.tiff": tiffOf(t, pngOf(4, 4, color.RGBA{9, 9, 9, 255}))})
	before := convertCalls.Load()
	convertMu.Lock()
	if !r.drop() {
		convertMu.Unlock()
		t.Fatal("refused")
	}
	time.Sleep(150 * time.Millisecond)
	r.w.reapDrags()
	waiting := len(r.w.q.ev) == 0 && convertCalls.Load() == before
	convertMu.Unlock()
	if !waiting {
		t.Error("a conversion ran while another had the turn")
	}
	if e := r.next(); e.Flags != 0 || convertCalls.Load() != before+1 {
		t.Errorf("after the turn: %+v", e)
	}
}

// ---- drag out: names, pictures, records ----

func TestPromiseNamesMustBeBaseNames(t *testing.T) {
	r := newDragRig(t)
	s := r.stubSession()
	r.press()
	write := func(context.Context, string) error { return nil }
	bad := []string{"", ".", "..", "a/b", "/abs", "../x", "a:b", "nul\x00byte", "\xff\xfe", strings.Repeat("x", MaxPromiseName+1)}
	for _, name := range bad {
		_, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: name, Write: write}}}})
		if !errors.Is(err, ErrDragSpec) {
			t.Errorf("name %q: %v", name, err)
		}
	}
	if s.calls != 0 {
		t.Errorf("%d sessions started for bad names", s.calls)
	}
	promiseMu.Lock()
	n := len(promises)
	promiseMu.Unlock()
	if n != 0 {
		t.Errorf("%d promise records made for bad names", n)
	}
	for _, name := range []string{"a.txt", "caf\u00e9 \u65e5\u672c.mp4", strings.Repeat("x", MaxPromiseName)} {
		if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: name, Write: write}}}}); err != nil {
			t.Errorf("name %q: %v", name, err)
		}
		r.endSession(0)
	}
}

func TestDragPictureIsTheSystemIconWithoutAPreview(t *testing.T) {
	r := newDragRig(t)
	r.press()
	write := func(context.Context, string) error { return nil }
	for name, it := range map[string]DragItem{
		"text":    {Text: "hello"},
		"promise": {Promise: &FilePromise{Name: "a.mp4", Type: "public.mpeg-4", Write: write}},
		"png":     {PNG: pngOf(200, 100, color.RGBA{1, 2, 3, 255})},
	} {
		s := r.stubSession()
		if _, err := r.w.StartDrag(&DragSpec{Items: []DragItem{it}}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		f := objc.SendRect(s.item(0), selDraggingFrame)
		switch {
		case name == "png" && (f.W != 64 || f.H != 32):
			t.Errorf("png: the picture is %+v, want the image scaled to fit 64 points", f)
		case name != "png" && (f.W != 32 || f.H != 32):
			t.Errorf("%s: the picture is %+v, want a 32 point icon (not the 16 point placeholder)", name, f)
		}
		r.endSession(0)
	}
}

func TestRemovedPromisesAreNotKeptAlive(t *testing.T) {
	r := newDragRig(t)
	mk := func(name string) *promiseRec {
		return newPromise(r.w, 1, &FilePromise{Name: name, Write: func(context.Context, string) error { return nil }})
	}
	a, b, c := mk("a"), mk("b"), mk("c")
	promiseMu.Lock()
	backing := promises[:cap(promises)]
	n := len(promises)
	promiseMu.Unlock()
	removePromise(b)
	promiseMu.Lock()
	tail := backing[len(promises):n]
	live := len(promises)
	promiseMu.Unlock()
	if live != n-1 {
		t.Fatalf("%d records, want %d", live, n-1)
	}
	for i, x := range tail {
		if x != nil {
			t.Errorf("slot %d past the end still holds %q's record", i, x.name)
		}
	}
	// closeDrag drops the window's records the same way.
	r.w.closeDrag()
	promiseMu.Lock()
	for i, x := range backing[:cap(backing)] {
		if x == a || x == b || x == c {
			t.Errorf("slot %d still holds a record after the window closed", i)
		}
	}
	promiseMu.Unlock()
}

// ---- repaint during our own drag session ----

func TestFramesRunDuringOurOwnDragSession(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.SetDropKinds(DragText)
	r.put(map[string][]byte{"public.utf8-plain-text": []byte("x")})
	r.at(10, 80)
	calls, moves, nested := 0, 0, 0
	r.w.SetFrameHandler(func() {
		calls++
		for _, e := range r.w.Pending() {
			if e.Kind == EvDragMove || e.Kind == EvDragEnter {
				moves++
			}
		}
		r.w.frameArmed = true
		r.w.linkTick(nil, false) // a tick inside the handler: no nested frame
		nested = calls
		r.send(selDragUpdated) // AppKit's next draggingUpdated:, as the pointer moves: it arms the next frame
	})
	tick := func() { r.w.frameArmed = true; r.w.linkTick(nil, false) }

	// A drag from another application over the window: Wait is running, the
	// handler is not for it.
	r.send(selDragEntered)
	tick()
	if calls != 0 {
		t.Fatalf("the frame handler ran %d times for a drag in", calls)
	}
	r.w.q.reset()

	// Our own session: the drag's events arm a frame, and a tick runs the
	// handler, which sees them. Each handler call's own draggingUpdated: arms
	// the next frame, so frames keep coming while the pointer moves.
	r.w.drag.active = true
	r.send(selDragUpdated)
	if !r.w.frameArmed {
		t.Fatal("a drag event during our session asked for no frame")
	}
	for i := 0; i < 4; i++ {
		r.w.linkTick(nil, false)
		if i < 3 && !r.w.frameArmed {
			t.Fatalf("tick %d: the handler's drag event did not arm the next frame", i)
		}
	}
	if calls != 4 || moves < 4 {
		t.Errorf("handler ran %d times and saw %d drag events", calls, moves)
	}
	if nested != calls {
		t.Errorf("a tick inside the handler ran a nested frame (%d of %d)", nested, calls)
	}
	// Without a requested frame, a tick does nothing (an idle drag costs no frames).
	r.w.frameArmed = false
	r.w.linkTick(nil, false)
	if calls != 4 {
		t.Errorf("an unrequested tick ran the handler (%d)", calls)
	}
}

// ---- decoding ----

// tiffPage is one image of a TIFF the tests build. Without pix, its strips are
// zeros (compressed with deflate, so a huge image is a small file).
type tiffPage struct {
	w, h, bits, samples int
	orient              int // 0: no tag
	pix                 []byte
}

// tiffFile builds a little-endian baseline TIFF of the pages, in one IFD chain.
func tiffFile(t *testing.T, pages ...tiffPage) []byte {
	t.Helper()
	le := binary.LittleEndian
	var b bytes.Buffer
	b.Write([]byte{'I', 'I', 42, 0, 0, 0, 0, 0})
	patch := 4 // where the offset of the next IFD goes
	align := func() {
		if b.Len()%2 != 0 {
			b.WriteByte(0)
		}
	}
	for _, p := range pages {
		rowBytes := (p.w*p.samples*p.bits + 7) / 8
		rows := 64
		if p.pix == nil {
			rows = max(1, (8<<20)/rowBytes)
		} else {
			rows = p.h
		}
		var offs, counts []uint32
		for y := 0; y < p.h; y += rows {
			n := min(rows, p.h-y)
			var strip []byte
			if p.pix != nil {
				strip = p.pix[y*rowBytes : (y+n)*rowBytes]
			} else {
				var z bytes.Buffer
				zw, _ := zlib.NewWriterLevel(&z, zlib.BestSpeed)
				zw.Write(make([]byte, n*rowBytes))
				zw.Close()
				strip = z.Bytes()
			}
			align()
			offs, counts = append(offs, uint32(b.Len())), append(counts, uint32(len(strip)))
			b.Write(strip)
		}
		type entry struct {
			tag, typ uint16
			count    uint32
			val      []byte
		}
		u16 := func(vs ...int) []byte {
			o := make([]byte, 2*len(vs))
			for i, v := range vs {
				le.PutUint16(o[2*i:], uint16(v))
			}
			return o
		}
		u32 := func(vs ...uint32) []byte {
			o := make([]byte, 4*len(vs))
			for i, v := range vs {
				le.PutUint32(o[4*i:], v)
			}
			return o
		}
		bits := make([]int, p.samples)
		for i := range bits {
			bits[i] = p.bits
		}
		photometric := 2
		if p.samples == 1 {
			photometric = 1
		}
		compression := 8
		if p.pix != nil {
			compression = 1
		}
		es := []entry{
			{256, 4, 1, u32(uint32(p.w))}, {257, 4, 1, u32(uint32(p.h))},
			{258, 3, uint32(p.samples), u16(bits...)}, {259, 3, 1, u16(compression)}, {262, 3, 1, u16(photometric)},
			{273, 4, uint32(len(offs)), u32(offs...)},
		}
		if p.orient != 0 {
			es = append(es, entry{274, 3, 1, u16(p.orient)})
		}
		es = append(es, entry{277, 3, 1, u16(p.samples)}, entry{278, 4, 1, u32(uint32(rows))},
			entry{279, 4, uint32(len(counts)), u32(counts...)}, entry{284, 3, 1, u16(1)})
		if p.samples == 4 {
			es = append(es, entry{338, 3, 1, u16(2)})
		}
		for i := range es { // the values that do not fit the entry go before the IFD
			if len(es[i].val) > 4 {
				align()
				off := uint32(b.Len())
				b.Write(es[i].val)
				es[i].val = u32(off)
			}
		}
		align()
		le.PutUint32(b.Bytes()[patch:], uint32(b.Len()))
		var n [2]byte
		le.PutUint16(n[:], uint16(len(es)))
		b.Write(n[:])
		for _, e := range es {
			var rec [12]byte
			le.PutUint16(rec[0:], e.tag)
			le.PutUint16(rec[2:], e.typ)
			le.PutUint32(rec[4:], e.count)
			copy(rec[8:], e.val)
			b.Write(rec[:])
		}
		patch = b.Len()
		b.Write([]byte{0, 0, 0, 0})
	}
	return b.Bytes()
}

// convert runs the worker's conversion on the test thread.
func convert(b []byte) []byte { return imagePNG(new(ffi.Frame), b) }

func withLimits(t *testing.T, pixels, decoded int64, pngBytes int) {
	t.Helper()
	op, od, oz := maxDropPixels, maxDropDecoded, maxDropPNG
	maxDropPixels, maxDropDecoded, maxDropPNG = pixels, decoded, pngBytes
	t.Cleanup(func() { maxDropPixels, maxDropDecoded, maxDropPNG = op, od, oz })
}

func TestDecodeBoundsTheBytesOfADeepImage(t *testing.T) {
	newDragRig(t)
	if !loadIIO() {
		t.Skip("no ImageIO")
	}
	// A 16-bit RGBA TIFF of 8000×8000: 64 MP, under the pixel limit and four
	// bytes a pixel at 8 bits (244 MiB), but eight at 16 bits (488 MiB), over
	// MaxConvertBytes. A few kilobytes of file.
	deep := tiffFile(t, tiffPage{w: 8000, h: 8000, bits: 16, samples: 4})
	pool := objc.F.Call0(objc.PoolPush)
	w, h, ok := imageSize(new(ffi.Frame), dataOf(deep))
	objc.F.Call1(objc.PoolPop, pool)
	if !ok || w != 8000 || h != 8000 {
		t.Fatalf("the fixture reads as %d×%d (%v): the test would prove nothing", w, h, ok)
	}
	if convert(tiffFile(t, tiffPage{w: 100, h: 50, bits: 16, samples: 4})) == nil {
		t.Fatal("a small 16-bit TIFF does not decode: the fixture is wrong")
	}
	before := convertCalls.Load()
	if convert(deep) != nil || convertCalls.Load() != before {
		t.Errorf("a 16-bit 8000×8000 TIFF (%d bytes) was decoded", len(deep))
	}
	// The same shape at 8 bits fits the byte limit: it is the depth that refuses
	// the other (checked at lower limits, to keep the decode small).
	withLimits(t, MaxImagePixels, 20<<20, MaxPaste)
	if convert(tiffFile(t, tiffPage{w: 2000, h: 2000, bits: 16, samples: 4})) != nil { // 32 MB decoded
		t.Error("a 16-bit 2000×2000 TIFF passed a 20 MiB limit")
	}
	if got := convert(tiffFile(t, tiffPage{w: 2000, h: 2000, bits: 8, samples: 4})); got == nil { // 16 MB decoded
		t.Error("the same image at 8 bits was refused")
	}
}

func TestDecodeReadsOnlyTheFirstImage(t *testing.T) {
	newDragRig(t)
	if !loadIIO() {
		t.Skip("no ImageIO")
	}
	pix := []byte{
		255, 0, 0, 0, 255, 0, 0, 0, 255,
		10, 20, 30, 40, 50, 60, 70, 80, 90,
	}
	// Image 1 is 30000×30000: 900 MP, if anything looked at it.
	multi := tiffFile(t, tiffPage{w: 3, h: 2, bits: 8, samples: 3, pix: pix}, tiffPage{w: 30000, h: 30000, bits: 1, samples: 1})
	before := convertCalls.Load()
	out := convert(multi)
	if out == nil || convertCalls.Load() != before+1 {
		t.Fatalf("a multi-image TIFF with a small first image: %d bytes, %d decodes", len(out), convertCalls.Load()-before)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil || img.Bounds().Dx() != 3 || img.Bounds().Dy() != 2 {
		t.Fatalf("decoded %v, %v", img, err)
	}
	if c := color.NRGBAModel.Convert(img.At(1, 0)).(color.NRGBA); c.G < 250 || c.R > 5 || c.B > 5 {
		t.Errorf("pixel (1,0) %+v, want green", c)
	}
	// And the other way round: a huge first image is refused though the second is small.
	rev := tiffFile(t, tiffPage{w: 30000, h: 30000, bits: 1, samples: 1}, tiffPage{w: 3, h: 2, bits: 8, samples: 3, pix: pix})
	if convert(rev) != nil {
		t.Error("a huge first image was decoded")
	}
}

func TestDecodeAppliesTheOrientation(t *testing.T) {
	newDragRig(t)
	if !loadIIO() {
		t.Skip("no ImageIO")
	}
	// 4×2, stored: row 0 red, green, blue, white; row 1 black, grey, yellow, magenta.
	px := [][3]byte{{255, 0, 0}, {0, 255, 0}, {0, 0, 255}, {255, 255, 255}, {0, 0, 0}, {128, 128, 128}, {255, 255, 0}, {255, 0, 255}}
	var pix []byte
	for _, p := range px {
		pix = append(pix, p[0], p[1], p[2])
	}
	at := func(o int) (image.Image, []byte) {
		out := convert(tiffFile(t, tiffPage{w: 4, h: 2, bits: 8, samples: 3, orient: o, pix: pix}))
		if out == nil {
			t.Fatalf("orientation %d: not decoded", o)
		}
		img, err := png.Decode(bytes.NewReader(out))
		if err != nil {
			t.Fatal(err)
		}
		return img, out
	}
	same := func(img image.Image, x, y int, want [3]byte, name string) {
		t.Helper()
		c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
		d := func(a, b uint8) int { return max(int(a)-int(b), int(b)-int(a)) }
		if d(c.R, want[0]) > 3 || d(c.G, want[1]) > 3 || d(c.B, want[2]) > 3 {
			t.Errorf("%s: pixel (%d,%d) is %v, want %v", name, x, y, c, want)
		}
	}
	img, _ := at(0) // no tag: as stored
	if b := img.Bounds(); b.Dx() != 4 || b.Dy() != 2 {
		t.Fatalf("size %v", b)
	}
	same(img, 0, 0, px[0], "as stored")
	same(img, 3, 1, px[7], "as stored")
	img, _ = at(3) // rotated 180°
	same(img, 0, 0, px[7], "rotated 180")
	same(img, 3, 1, px[0], "rotated 180")
	img, _ = at(6) // shown rotated 90° clockwise: 2 wide, 4 tall; the stored top-left is the top-right
	if b := img.Bounds(); b.Dx() != 2 || b.Dy() != 4 {
		t.Fatalf("orientation 6: size %v", b)
	}
	same(img, 1, 0, px[0], "rotated 90 cw")
	same(img, 0, 0, px[4], "rotated 90 cw")
	same(img, 0, 3, px[7], "rotated 90 cw")
}

// noiseJPEG is a JPEG of w×h noise that stays within the input limit.
func noiseJPEG(t *testing.T, w, h, quality int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	rnd := uint32(12345)
	for i := 0; i < len(img.Pix); i += 4 {
		rnd = rnd*1664525 + 1013904223
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = byte(rnd>>8), byte(rnd>>16), byte(rnd>>24), 255
	}
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: quality}); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestEncodingThePNGIsBounded(t *testing.T) {
	newDragRig(t)
	if !loadIIO() {
		t.Skip("no ImageIO")
	}
	check := func(name string, jpg []byte, limit int) {
		t.Helper()
		withLimits(t, MaxImagePixels, MaxConvertBytes, limit)
		sinkPeak.Store(0)
		sinkRefused.Store(false)
		if got := convert(jpg); got != nil {
			t.Errorf("%s: a %d byte PNG came out under a %d limit", name, len(got), limit)
		}
		if !sinkRefused.Load() {
			t.Errorf("%s: the encoder was never refused", name)
		}
		if peak := sinkPeak.Load(); peak > int64(limit) {
			t.Errorf("%s: the output grew to %d bytes, past %d", name, peak, limit)
		}
	}
	// Noise does not compress as a PNG: a JPEG of 512×512 is some 100 KB and
	// its PNG about a megabyte.
	small := noiseJPEG(t, 512, 512, 60)
	check("small limit", small, 64<<10)
	if got := func() []byte { withLimits(t, MaxImagePixels, MaxConvertBytes, 8<<20); return convert(small) }(); got == nil {
		t.Error("the same JPEG was refused under a limit it fits")
	}
	if testing.Short() {
		return
	}
	// At the real limits: a JPEG under MaxPaste whose PNG is over it.
	big := noiseJPEG(t, 3000, 3000, 50)
	if len(big) > MaxPaste {
		t.Skipf("the noise JPEG is %d bytes, over MaxPaste", len(big))
	}
	check("MaxPaste", big, MaxPaste)
}

func TestLeavingOurWindowDuringOurSessionPresentsAFrame(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.SetDropKinds(DragText)
	r.put(map[string][]byte{"public.utf8-plain-text": []byte("x")})
	r.at(10, 80)
	enters, leaves := 0, 0
	r.w.SetFrameHandler(func() {
		for _, e := range r.w.Pending() {
			switch e.Kind {
			case EvDragEnter, EvDragMove:
				enters++
			case EvDragLeave:
				leaves++
			}
		}
	})
	tick := func() { r.w.linkTick(nil, false) }

	// Not our session: leaving asks for no frame (Wait is running).
	r.send(selDragEntered)
	r.send(selDragExited)
	if r.w.frameArmed {
		t.Fatal("a drag from another application asked for a frame on leaving")
	}
	r.w.q.reset()

	// Our session: the highlight the enter drew must be cleared by a frame the
	// leave asks for, since no Wait turn will deliver the event.
	r.w.drag.active = true
	r.send(selDragEntered)
	tick()
	if enters != 1 || leaves != 0 {
		t.Fatalf("enter: %d enters, %d leaves", enters, leaves)
	}
	r.send(selDragExited)
	if !r.w.frameArmed {
		t.Fatal("leaving our own window during our session asked for no frame")
	}
	tick()
	if leaves != 1 {
		t.Errorf("the frame handler saw %d leaves", leaves)
	}
}

// A drag session of another window of ours over an idle window: AppKit's
// drag loop runs, the app's pump does not return, so the window under the
// drag asks for frames itself (its link paused, nothing else would wake it):
// on entering, moving and leaving, and the tick draws them through its frame
// handler.
func TestADragFromAnotherWindowOfOursDrawsTheTarget(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.SetDropKinds(DragText)
	r.put(map[string][]byte{"public.utf8-plain-text": []byte("x")})
	r.at(10, 80)
	source := &Window{q: newEventQueue()} // the window whose session it is
	remove := testInstall(source)
	defer remove()
	calls := 0
	r.w.SetFrameHandler(func() { calls++ })

	source.drag.active = true
	defer func() { source.drag.active = false }()
	for i, sel := range []objc.SEL{selDragEntered, selDragUpdated, selDragExited} {
		r.w.frameArmed = false
		r.send(sel)
		if !r.w.frameArmed {
			t.Fatalf("event %d of a drag from another window's session asked for no frame", i)
		}
		r.w.linkTick(nil, false)
		if calls != i+1 {
			t.Fatalf("event %d: the tick ran the frame handler %d times, want %d", i, calls, i+1)
		}
	}
}

// ---- a source that fulfils lazily and slowly (#13) ----

// slowTextOwner starts another process that owns a named pasteboard with a text
// it provides after delay, and makes it the pasteboard of the rig's drags.
func (r *dragRig) slowTextOwner(delay string) objc.ID {
	r.t.Helper()
	name := "dev.gonativeui.test.slow." + strconv.Itoa(os.Getpid())
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), "GNUI_PB_SLOW="+delay, "GNUI_PB_SLOW_NAME="+name)
	out, err := cmd.StdoutPipe()
	if err != nil {
		r.t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })
	ready := make(chan string, 1)
	go func() { l, _ := bufio.NewReader(out).ReadString('\n'); ready <- l }()
	select {
	case l := <-ready:
		if strings.TrimSpace(l) != "ready" {
			r.t.Skipf("the slow owner did not start: %q", l)
		}
	case <-time.After(20 * time.Second):
		r.t.Skip("the slow owner did not start")
	}
	pb := objc.Retain(objc.Send1(clsPasteboard, selPBNamed, uintptr(objc.Str(name))))
	old := fakeInfo.pb
	fakeInfo.pb = pb
	r.t.Cleanup(func() { fakeInfo.pb = old; objc.Release(pb) })
	return pb
}

func shortBudget(t *testing.T, d time.Duration) {
	old := dropSyncBudget
	dropSyncBudget = d
	t.Cleanup(func() { dropSyncBudget = old })
}

// A source that takes 1.5 s to provide its text does not hold the main thread:
// the drop returns when the budget ends, and the EvDrop comes out of a later
// Wait with the text.
func TestSlowSourceDoesNotHoldTheMainThread(t *testing.T) {
	r := newDragRig(t)
	shortBudget(t, 100*time.Millisecond)
	r.slowTextOwner("1500ms")
	r.w.SetDropKinds(DragAny)
	r.at(5, 5)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})

	start := time.Now()
	if !r.drop() {
		t.Fatal("refused")
	}
	if took := time.Since(start); took > time.Second {
		t.Errorf("the drop held the main thread for %v", took)
	}
	if ev := r.events(); len(ev) != 0 {
		t.Fatalf("the text arrived before the source provided it: %+v", ev)
	}
	e := r.next()
	if e.Kind != EvDrop || e.Flags != 0 || r.text(e) != "slow text" {
		t.Errorf("late drop: %+v %q", e, r.text(e))
	}
	r.waitWorkers()
}

// The same drag, but the pasteboard changes owner (another drag starts) before
// the slow source answers: the read is thrown away, and the drop fails rather
// than delivering whatever the new owner holds.
func TestSlowSourceReadAcrossAnOwnerChangeFails(t *testing.T) {
	r := newDragRig(t)
	shortBudget(t, 100*time.Millisecond)
	pb := r.slowTextOwner("1500ms")
	r.w.SetDropKinds(DragAny)
	r.at(5, 5)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	if !r.drop() {
		t.Fatal("refused")
	}
	takeOver(t, pb)
	e := r.next()
	if e.Kind != EvDrop || e.Flags&FlagFailed == 0 || r.text(e) != "" {
		t.Errorf("a drop across an owner change: %+v %q", e, r.text(e))
	}
	r.waitWorkers()
}

// ---- file promises (#13) ----

// The call that starts a reception (receivePromisedFilesAtDestination:) is only
// allowed inside a real drag's performDragOperation:, so these tests start a
// reception by hand and call its reader block as AppKit would, from a queue of
// its own: what they check is the bookkeeping, not AppKit.
func TestReceptionCollectsTheFilesAndDeliversOnce(t *testing.T) {
	r := newDragRig(t)
	promiseInit()
	if receiveKind == nil {
		t.Skip("no NSFilePromiseReceiver here")
	}
	dir := t.TempDir()
	files := []string{filepath.Join(dir, "a.txt"), filepath.Join(dir, "b b.txt")}
	d := &r.w.drag
	id, rc, _ := newReception(d, Event{Kind: EvDrop, Code: uint32(DragFiles), SelStart: 1}, dir, 2)
	req, err := receiveKind.New(id)
	if err != nil {
		t.Fatal(err)
	}
	rc.reqs = append(rc.reqs, req)
	call := func(path string, fail bool) {
		pool := objc.F.Call0(objc.PoolPush)
		defer objc.F.Call1(objc.PoolPop, pool)
		var url, nserr uintptr
		if fail {
			nserr = uintptr(objc.Send(objc.Send(clsNSError, selAlloc), objc.Sel("init")))
		} else {
			url = uintptr(objc.Send1(clsNSURL, selFileURLPath, uintptr(objc.Str(path))))
		}
		done := make(chan struct{})
		go func() { // the reader runs on the reception's queue, not the main thread
			defer close(done)
			objc.CallBlock(new(ffi.Frame), req.Block(), url, nserr)
		}()
		<-done
	}
	call(files[0], false)
	if r.w.drag.nDone.Load() != 0 {
		t.Fatal("delivered before every file arrived")
	}
	call(files[1], false)
	req.Sent()
	e := r.next()
	if e.Kind != EvDrop || e.Flags != 0 || e.SelStart != 2 || r.text(e) != files[0]+"\x00"+files[1]+"\x00" {
		t.Fatalf("reception: %+v %q", e, r.text(e))
	}
	call(files[0], false) // a late call after the delivery changes nothing
	if r.w.drag.nDone.Load() != 0 {
		t.Error("delivered twice")
	}
	r.waitWorkers()
}

// Every file failing is a failed drop, and its folder is removed.
func TestReceptionOfNothingFails(t *testing.T) {
	r := newDragRig(t)
	promiseInit()
	if receiveKind == nil {
		t.Skip("no NSFilePromiseReceiver here")
	}
	dir := filepath.Join(t.TempDir(), "gnui-drop-x")
	os.Mkdir(dir, 0o700)
	id, _, _ := newReception(&r.w.drag, Event{Kind: EvDrop, Code: uint32(DragFiles)}, dir, 1)
	req, err := receiveKind.New(id)
	if err != nil {
		t.Fatal(err)
	}
	pool := objc.F.Call0(objc.PoolPush)
	nserr := uintptr(objc.Send(objc.Send(clsNSError, selAlloc), objc.Sel("init")))
	done := make(chan struct{})
	go func() { defer close(done); objc.CallBlock(new(ffi.Frame), req.Block(), 0, nserr) }()
	<-done
	req.Sent()
	objc.F.Call1(objc.PoolPop, pool)
	e := r.next()
	if e.Kind != EvDrop || e.Flags&FlagFailed == 0 {
		t.Errorf("failed reception: %+v", e)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("the empty folder is still there: %v", err)
	}
	r.waitWorkers()
}

// ---- links and rich text are text ----

func TestDropLinksAndRichTextAreText(t *testing.T) {
	r := newDragRig(t)
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragText})
	r.w.SetDropKinds(DragText)
	r.at(5, 5)
	for _, c := range []struct {
		name string
		item map[string][]byte
		want string
	}{
		{"a link", map[string][]byte{"public.url": []byte("https://example.com/a?b=1")}, "https://example.com/a?b=1"},
		{"RTF", map[string][]byte{"public.rtf": []byte(`{\rtf1\ansi\deff0 {\fonttbl{\f0 Helvetica;}}Hello \b world\b0 !}`)}, "Hello world!"},
		{"HTML", map[string][]byte{"public.html": []byte("<html><body><p>Hi <b>there</b></p><script>x()</script></body></html>")}, "Hi there"},
		{"plain text before a link", map[string][]byte{"public.utf8-plain-text": []byte("the title"), "public.url": []byte("https://example.com/")}, "the title"},
		{"a link before rich text", map[string][]byte{"public.url": []byte("https://example.com/x"), "public.html": []byte("<b>bold</b>")}, "https://example.com/x"},
	} {
		r.w.q.reset()
		r.put(c.item)
		if !r.drop() {
			t.Errorf("%s: refused", c.name)
			continue
		}
		ev := r.next()
		if ev.Kind != EvDrop || DragKinds(ev.Code) != DragText || ev.Flags&FlagFailed != 0 || r.text(ev) != c.want {
			t.Errorf("%s: %+v %q, want %q", c.name, ev, r.text(ev), c.want)
		}
	}

	// The enter names text as a kind for them.
	r.w.q.reset()
	r.put(map[string][]byte{"public.url": []byte("https://example.com/")})
	r.send(selDragEntered)
	if ev := r.events(); len(ev) != 1 || DragKinds(ev[0].Code) != DragText {
		t.Errorf("enter events %+v, want text offered", ev)
	}

	// A file's URL, also offered as public.url, is a file and not text.
	dir := t.TempDir()
	path := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.w.q.reset()
	r.put(map[string][]byte{"public.file-url": fileURLBytes(path), "public.url": fileURLBytes(path)})
	if r.drop() {
		t.Error("a file was taken as text by a window that takes only text")
	}
	r.answer(DragAnswer{Op: DragOpCopy, Accept: DragAny})
	r.w.SetDropKinds(DragAny)
	r.w.q.reset()
	if !r.drop() {
		t.Fatal("refused")
	}
	if ev := r.next(); DragKinds(ev.Code) != DragFiles || r.text(ev) != path+"\x00" {
		t.Errorf("file drop %+v %q", ev, r.text(ev))
	}
}

// ---- receiving file promises ----

var (
	promiseSrcOnce sync.Once
	promiseSrcCls  objc.ID
	promiseSrcName = []byte("promised.txt")
	promiseSrcData = []byte("promised data")
	promiseSrcMode atomic.Int32 // 0: write the file, 1: never answer
	promiseSrcSeen atomic.Int32
)

// promiseSource is a source of file promises like another application's: a
// provider whose delegate writes a file when the destination asks.
func promiseSource(t *testing.T) objc.ID {
	t.Helper()
	promiseSrcOnce.Do(func() {
		def := objc.Define("GNUIFakePromiseSource", objc.Cls("NSObject"), []string{"NSFilePromiseProviderDelegate"})
		def.Method(selPromiseName, "@@:@@", func(c *ffi.CB) {
			f := c.Frame()
			alloc := wsend(f, clsNSString, selAllocStr)
			str := wsend(f, alloc, selInitBytes, uintptr(unsafe.Pointer(&promiseSrcName[0])), uintptr(len(promiseSrcName)), 4)
			c.Ret(uintptr(wsend(f, str, selAutorelease)))
		})
		def.Method(selPromiseWrit, "v@:@@@?", func(c *ffi.CB) {
			f := c.Frame()
			promiseSrcSeen.Add(1)
			if promiseSrcMode.Load() == 1 {
				return
			}
			path := string(appendCString(nil, uintptr(wsend(f, objc.ID(c.Int(3)), selFSRep))))
			if err := os.WriteFile(path, promiseSrcData, 0o644); err != nil {
				panic(err)
			}
			objc.CallBlock(f, objc.ID(c.Int(4)), 0)
		})
		def.Method(selPromiseQ, "@@:@", func(c *ffi.CB) { c.Ret(uintptr(promiseQueue)) })
		promiseSrcCls = def.Register()
	})
	delegate := objc.Retain(objc.Send(objc.Send(promiseSrcCls, selAlloc), selInit))
	prov := objc.Send(objc.Send(clsNSFilePromise, selAlloc), selInit)
	fileType := objc.Str("public.plain-text") // (made before the call is built: they share the frame)
	prov = objc.Send2(prov, selInitFilePromise, uintptr(fileType), uintptr(delegate))
	return objc.Retain(prov)
}

func (r *dragRig) putProvider(prov objc.ID) {
	r.t.Helper()
	objc.Send(r.pb, selClearContents)
	arr := objc.Send(clsNSMutableArray, selArrayClass)
	objc.Send1(arr, selAddObject, uintptr(prov))
	if !objc.SendBool1(r.pb, selWriteObjects, uintptr(arr)) {
		r.t.Fatal("writeObjects failed")
	}
}

// AppKit lets receivePromisedFilesAtDestination: be called only inside the
// dragging destination methods of a real session, so the receiving itself
// cannot be driven with a fake dragging info: it is checked by hand. What is
// tested here is the offer, which EnableFilePromises gates, and the bookkeeping
// that the reader block of a real receiver drives (the receptions above).
func TestPromisedFilesAreOfferedAsFilesOnlyWhenEnabled(t *testing.T) {
	for _, on := range []bool{false, true} {
		t.Run(map[bool]string{false: "off", true: "on"}[on], func(t *testing.T) {
			old := EnableFilePromises
			EnableFilePromises = on
			t.Cleanup(func() { EnableFilePromises = old })
			r := newDragRig(t)
			r.answer(DragAnswer{Op: DragOpCopy, Accept: DragFiles})
			r.w.SetDropKinds(DragFiles)
			r.putProvider(promiseSource(t))
			r.at(5, 5)
			r.send(selDragEntered)
			ev := r.events()
			got := strings.Join(registeredTypes(r), " ")
			registered := strings.Contains(got, "com.apple.pasteboard.promised-file-content-type")
			if !on {
				if len(ev) != 0 || registered {
					t.Errorf("promises offered with EnableFilePromises off: %+v, registered %q", ev, got)
				}
				return
			}
			if len(ev) != 1 || DragKinds(ev[0].Code) != DragFiles || ev[0].SelLen != 1 {
				t.Fatalf("a promise is offered as one file: %+v", ev)
			}
			if !registered {
				t.Errorf("the view is registered for %q, without the promise types", got)
			}
		})
	}
}

// registeredTypes are the types the rig's view is registered for.
func registeredTypes(r *dragRig) (out []string) {
	arr := objc.Send(r.view, selRegisteredType)
	for i, n := 0, int(objc.Send(arr, selURLCount)); i < n; i++ {
		out = append(out, string(objc.AppendUTF8(nil, objc.Send1(arr, selURLAt, uintptr(i)))))
	}
	return out
}

// callReader calls a reception's reader block as AppKit would, from a queue of
// its own, with the file at path or an error.
func callReader(req objc.Request, path string, fail bool) {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	var url, nserr uintptr
	if fail {
		nserr = uintptr(objc.Send(objc.Send(clsNSError, selAlloc), objc.Sel("init")))
	} else {
		url = uintptr(objc.Send1(clsNSURL, selFileURLPath, uintptr(objc.Str(path))))
	}
	done := make(chan struct{})
	go func() { defer close(done); objc.CallBlock(new(ffi.Frame), req.Block(), url, nserr) }()
	<-done
}

// startReception makes a reception of want files and its request, as
// receivePromises does after it has asked the promisers.
func startReception(t *testing.T, r *dragRig, want int) (*reception, objc.Request) {
	t.Helper()
	promiseInit()
	if receiveKind == nil {
		t.Skip("no NSFilePromiseReceiver here")
	}
	dir := filepath.Join(t.TempDir(), "gnui-drop-x")
	os.Mkdir(dir, 0o700)
	id, rc, ok := newReception(&r.w.drag, Event{Kind: EvDrop, Code: uint32(DragFiles), Tag: 4}, dir, want)
	if !ok {
		t.Fatal("no place for a reception")
	}
	req, err := receiveKind.New(id)
	if err != nil {
		t.Fatal(err)
	}
	rc.reqs = append(rc.reqs, req)
	return rc, req
}

// A file that failed counts as reported: the drop is delivered with the one that
// came, and a report after it finds nothing.
func TestReceptionDeliversWhatCameWhenOneFails(t *testing.T) {
	r := newDragRig(t)
	rc, req := startReception(t, r, 2)
	a := filepath.Join(rc.dir, "a.txt")
	callReader(req, a, false)
	r.w.reapDrags()
	if len(r.w.q.ev) != 0 {
		t.Fatalf("the drop completed with one of two files: %+v", r.w.q.ev)
	}
	callReader(req, "", true)
	ev := r.next()
	if ev.Kind != EvDrop || DragKinds(ev.Code) != DragFiles || ev.Tag != 4 || ev.Flags&FlagFailed != 0 || ev.SelStart != 1 || r.text(ev) != a+"\x00" {
		t.Fatalf("drop %+v %q", ev, r.text(ev))
	}
	if req.Live() {
		t.Error("the request is live after the drop")
	}
	callReader(req, a, false)
	r.w.reapDrags()
	if len(r.w.q.ev) != 0 {
		t.Fatalf("a late report made an event: %+v", r.w.q.ev)
	}
	r.waitWorkers()
}

func TestReceptionThatNeverArrivesFailsAtTheTimeout(t *testing.T) {
	r := newDragRig(t)
	old := receiveTimeout
	receiveTimeout = 100 * time.Millisecond
	defer func() { receiveTimeout = old }()
	rc, _ := startReception(t, r, 1)
	rc.arm()
	ev := r.next()
	if ev.Kind != EvDrop || ev.Flags&FlagFailed == 0 || ev.Text.Len != 0 {
		t.Fatalf("drop %+v, want a failed one", ev)
	}
	if _, err := os.Stat(rc.dir); !os.IsNotExist(err) {
		t.Errorf("the empty folder is still there: %v", err)
	}
	r.waitWorkers()
}

func TestClosingTheWindowAbandonsReceptions(t *testing.T) {
	r := newDragRig(t)
	rc, req := startReception(t, r, 1)
	rc.arm()
	r.w.closeDrag()
	if req.Live() {
		t.Error("the request survived the window")
	}
	callReader(req, filepath.Join(rc.dir, "x"), false)
	r.w.reapDrags()
	if len(r.w.q.ev) != 0 {
		t.Fatalf("a report after the window closed made an event: %+v", r.w.q.ev)
	}
	if n := r.w.drag.workers.Load(); n != 0 {
		t.Errorf("%d places still held", n)
	}
}

func TestReceptionsAreBounded(t *testing.T) {
	r := newDragRig(t)
	for i := 0; i < maxReceptions; i++ {
		if _, _, ok := newReception(&r.w.drag, Event{}, t.TempDir(), 1); !ok {
			t.Fatalf("reception %d refused", i)
		}
	}
	if _, _, ok := newReception(&r.w.drag, Event{}, t.TempDir(), 1); ok {
		t.Error("a reception past maxReceptions was taken")
	}
	r.w.closeDrag()
	if _, _, ok := newReception(&r.w.drag, Event{}, t.TempDir(), 1); !ok {
		t.Error("closing the window left the receptions in place")
	}
	r.w.closeDrag()
}

func TestDragCachesTypesBySequence(t *testing.T) {
	r := newDragRig(t)
	r.w.SetDropKinds(DragText | DragFiles)
	r.answer(DragAnswer{Accept: DragText | DragFiles, Op: DragOpCopy})
	r.put(map[string][]byte{"public.utf8-plain-text": []byte("hello")})
	r.send(selDragEntered)
	queries := fakeInfo.pbQueries
	r.send(selDragUpdated)
	r.send(selDragUpdated)
	if fakeInfo.pbQueries != queries {
		t.Error("drag update re-read the pasteboard")
	}
	// A new sequence refreshes metadata even if AppKit supplies updated first.
	r.put(map[string][]byte{"public.file-url": []byte("file:///tmp/item")})
	fakeInfo.seq++
	r.send(selDragUpdated)
	if fakeInfo.pbQueries != queries+1 || r.w.drag.offered&DragFiles == 0 {
		t.Error("new sequence kept old offered kinds")
	}
	queries = fakeInfo.pbQueries
	r.send(selDragEntered)
	if fakeInfo.pbQueries != queries+1 {
		t.Error("entry did not refresh its metadata")
	}
}
