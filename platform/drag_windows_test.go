//go:build windows

package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/com"
	"gonativeui/internal/win32"
)

// The test drives the window's IDropTarget through its vtable, as OLE does,
// with a data object that is a second object of ours (so the drop reads it
// through GetData like any foreign source). Memory OLE-style calls write
// through is in package variables, as internal/com requires.
var (
	testEffect uint32
	testSource oleObj // a foreign IDataObject: the vtable of ours, another address
	testFE     formatEtc
	testSM     stgMedium
)

func openDragWindow(t *testing.T) *Window {
	t.Helper()
	runtime.LockOSThread()
	t.Cleanup(runtime.UnlockOSThread)
	w, err := Open(Options{Title: "gnui drag test", Width: 320, Height: 200, Hidden: true})
	if err != nil {
		t.Skipf("no window: %v", err)
	}
	t.Cleanup(w.Close)
	return w
}

func packPoint(x, y int32) uintptr { return uintptr(uint32(x)) | uintptr(uint32(y))<<32 }

// drag calls IDropTarget slot with the source and the effects.
func dragCall(w *Window, slot uintptr, args ...uintptr) {
	com.Call(uintptr(unsafe.Pointer(&w.drag.target)), slot, args...)
}

func eventsOf(w *Window, kinds ...EventKind) []Event {
	var out []Event
	for _, e := range w.Pending() {
		for _, k := range kinds {
			if e.Kind == k {
				out = append(out, e)
			}
		}
	}
	return out
}

func TestDragThresholdFromTheSystem(t *testing.T) {
	if got := DragThreshold(); got < 1 || got > 64 {
		t.Fatalf("DragThreshold() = %v", got)
	}
}

func TestOLEDropTargetTakesFilesTextAndImages(t *testing.T) {
	w := openDragWindow(t)
	var offered DragKinds
	w.SetDragHandler(func(x, y float32, k DragKinds, allowed DragOp) DragAnswer {
		offered = k
		return DragAnswer{Op: DragOpCopy, Accept: DragAny, Target: 7}
	})
	w.SetDropKinds(DragFiles | DragText | DragImage)
	if !w.drag.registered {
		t.Skip("OLE cannot be used on this thread")
	}
	png, _ := testPNG(t, 8, 6)
	w.drag.own = []DragItem{{Path: `C:\tmp\a b.txt`}, {Path: `C:\tmp\é.png`}, {Text: "line1\nline2"}, {PNG: png}}
	testSource.vtbl = uintptr(unsafe.Pointer(&dataObjectVtbl[0]))
	src := uintptr(unsafe.Pointer(&testSource))
	enter := func() {
		testEffect = effectCopy | effectMove
		dragCall(w, 3, src, 0, packPoint(10, 10), uintptr(unsafe.Pointer(&testEffect)))
	}

	enter()
	if testEffect != effectCopy {
		t.Fatalf("DragEnter answered effect %d, want copy", testEffect)
	}
	ev := eventsOf(w, EvDragEnter, EvDragMove, EvDragLeave)
	if len(ev) != 1 || ev[0].Kind != EvDragEnter || DragKinds(ev[0].Code) != DragAny || ev[0].SelStart != int32(DragOpCopy|DragOpMove) || ev[0].SelLen != 2 {
		t.Fatalf("enter events %+v (offered %d)", ev, offered)
	}
	tag := ev[0].Tag

	testEffect = effectCopy
	dragCall(w, 4, mkShift, packPoint(12, 11), uintptr(unsafe.Pointer(&testEffect)))
	ev = eventsOf(w, EvDragMove)
	if len(ev) != 1 || ev[0].Tag != tag || ev[0].Mods != ModShift {
		t.Fatalf("move events %+v", ev)
	}

	dragCall(w, 5)
	if ev = eventsOf(w, EvDragLeave); len(ev) != 1 || ev[0].Tag != tag {
		t.Fatalf("leave events %+v", ev)
	}

	// Dropped as it stands: the files come first.
	enter()
	w.Pending()
	testEffect = effectCopy | effectMove
	dragCall(w, 6, src, 0, packPoint(10, 10), uintptr(unsafe.Pointer(&testEffect)))
	if testEffect != effectCopy {
		t.Fatalf("Drop answered effect %d", testEffect)
	}
	ev = eventsOf(w, EvDrop)
	if len(ev) != 1 || DragKinds(ev[0].Code) != DragFiles || ev[0].Flags&FlagFailed != 0 || ev[0].Tag != 7 || ev[0].SelStart != 2 {
		t.Fatalf("drop events %+v", ev)
	}
	if got := string(w.Text(ev[0].Text)); got != "C:\\tmp\\a b.txt\x00C:\\tmp\\é.png\x00" {
		t.Fatalf("paths %q", got)
	}

	// A handler that takes text only gets the text, with its line ends as \n.
	w.SetDragHandler(func(float32, float32, DragKinds, DragOp) DragAnswer {
		return DragAnswer{Op: DragOpCopy, Accept: DragText}
	})
	enter()
	w.Pending()
	testEffect = effectCopy
	dragCall(w, 6, src, 0, packPoint(10, 10), uintptr(unsafe.Pointer(&testEffect)))
	ev = eventsOf(w, EvDrop)
	if len(ev) != 1 || DragKinds(ev[0].Code) != DragText || string(w.Text(ev[0].Text)) != "line1\nline2" {
		t.Fatalf("text drop %+v", ev)
	}

	// An image is the PNG.
	w.SetDragHandler(func(float32, float32, DragKinds, DragOp) DragAnswer {
		return DragAnswer{Op: DragOpCopy, Accept: DragImage}
	})
	enter()
	w.Pending()
	testEffect = effectCopy
	dragCall(w, 6, src, 0, packPoint(10, 10), uintptr(unsafe.Pointer(&testEffect)))
	ev = eventsOf(w, EvDrop)
	if len(ev) != 1 || DragKinds(ev[0].Code) != DragImage || !bytes.Equal(w.Text(ev[0].Text), png) {
		t.Fatalf("image drop %+v", ev)
	}

	// A refusal leaves nothing behind: the effect is none and a leave is queued.
	w.SetDragHandler(func(float32, float32, DragKinds, DragOp) DragAnswer { return DragAnswer{} })
	enter()
	w.Pending()
	if testEffect != effectNone {
		t.Fatalf("a refusing handler answered effect %d", testEffect)
	}
	testEffect = effectCopy
	dragCall(w, 6, src, 0, packPoint(10, 10), uintptr(unsafe.Pointer(&testEffect)))
	if ev = eventsOf(w, EvDrop); len(ev) != 0 || testEffect != effectNone {
		t.Fatalf("a refused drop: %+v, effect %d", ev, testEffect)
	}

	// Kinds the window does not take are not entered at all.
	w.SetDropKinds(DragText)
	w.drag.own = []DragItem{{Path: `C:\x`}}
	enter()
	if testEffect != effectNone || len(eventsOf(w, EvDragEnter)) != 0 {
		t.Fatalf("a files-only drag entered a text-only window (effect %d)", testEffect)
	}
}

func TestDataObjectOfASession(t *testing.T) {
	w := openDragWindow(t)
	if !w.oleSetup() {
		t.Skip("OLE cannot be used on this thread")
	}
	png, _ := testPNG(t, 8, 6)
	w.drag.own = []DragItem{{Path: `C:\one.txt`}, {Text: "héllo\nworld"}, {PNG: png}}
	obj := uintptr(unsafe.Pointer(&w.drag.data))
	get := func(cf uint32) (uintptr, []byte) {
		testFE = formatEtc{cfFormat: uint16(cf), dwAspect: dvAspectContent, lindex: -1, tymed: tymedHGlobal}
		testSM = stgMedium{}
		if hr := uint32(com.Call(obj, vtGetData, uintptr(unsafe.Pointer(&testFE)), uintptr(unsafe.Pointer(&testSM)))); hr != 0 {
			return uintptr(hr), nil
		}
		p := win32.Call(pGlobalLock, testSM.hglobal)
		out := append([]byte(nil), unsafe.Slice((*byte)(win32.Ptr(p)), int(win32.Call(pGlobalSize, testSM.hglobal)))...)
		win32.Call(pGlobalUnlock, testSM.hglobal)
		win32.Call(pReleaseStgMedium, uintptr(unsafe.Pointer(&testSM)))
		return 0, out
	}

	hr, blk := get(cfHDrop)
	if hr != 0 {
		t.Fatalf("GetData(CF_HDROP) = %#x", hr)
	}
	if got, n, ok := appendDropFiles(nil, blk, MaxPaste, nil); !ok || n != 1 || string(got) != "C:\\one.txt\x00" {
		t.Fatalf("CF_HDROP holds %q (%d)", got, n)
	}
	if hr, blk = get(cfUnicodeText); hr != 0 {
		t.Fatalf("GetData(CF_UNICODETEXT) = %#x", hr)
	}
	u := unsafe.Slice((*uint16)(unsafe.Pointer(unsafe.SliceData(blk))), len(blk)/2)
	if got, _ := utf16Decode(nil, u, 100); string(got) != "héllo\nworld" {
		t.Fatalf("CF_UNICODETEXT holds %q", got)
	}
	if hr, blk = get(clipFormats().png); hr != 0 || pngEnd(blk) != len(png) {
		t.Fatalf("PNG: %#x, %d bytes", hr, len(blk))
	}
	if hr, blk = get(cfDIBV5); hr != 0 || len(blk) < 40 {
		t.Fatalf("CF_DIBV5: %#x, %d bytes", hr, len(blk))
	}
	if out, err := dibToPNG(nil, blk, MaxPaste); err != nil || pngEnd(out) == 0 {
		t.Fatalf("the DIBV5 does not convert back: %v", err)
	}
	if hr, _ = get(cfDIB); hr != hrFormatEtc {
		t.Fatalf("GetData(CF_DIB) = %#x, want DV_E_FORMATETC", hr)
	}

	// QueryGetData agrees; a stream is not offered.
	testFE = formatEtc{cfFormat: cfHDrop, dwAspect: dvAspectContent, lindex: -1, tymed: 4 /* TYMED_ISTREAM */}
	if hr := uint32(com.Call(obj, vtQueryGetData, uintptr(unsafe.Pointer(&testFE)))); hr != hrTymed {
		t.Fatalf("QueryGetData(stream) = %#x, want DV_E_TYMED", hr)
	}

	// EnumFormatEtc lists the four formats the session offers.
	var enum uintptr
	hrEnum := win32.HR(com.Call(obj, 8, dataDirGet, uintptr(unsafe.Pointer(&enum))))
	if hrEnum != 0 || enum == 0 {
		t.Fatalf("EnumFormatEtc = %#x", hrEnum)
	}
	defer com.Release(enum)
	n := 0
	var fetched uint32
	for ; n < 8; n++ {
		testFE = formatEtc{}
		fetched = 0
		if com.Call(enum, 3 /* Next */, 1, uintptr(unsafe.Pointer(&testFE)), uintptr(unsafe.Pointer(&fetched))) != 0 || fetched != 1 {
			break
		}
	}
	if n != 4 {
		t.Fatalf("EnumFormatEtc lists %d formats, want 4 (files, PNG, DIBV5, text)", n)
	}
}

func TestStartDragRefusals(t *testing.T) {
	w := openDragWindow(t)
	spec := &DragSpec{Items: []DragItem{{Text: "x"}}}
	if _, err := w.StartDrag(&DragSpec{}); !errors.Is(err, ErrDragSpec) {
		t.Errorf("an empty spec: %v", err)
	}
	if _, err := w.StartDrag(spec); !errors.Is(err, ErrNoPress) {
		t.Errorf("no press held: %v", err)
	}
	promise := &DragSpec{Items: []DragItem{{Promise: &FilePromise{Name: "a.txt", Write: func(context.Context, string) error { return nil }}}}}
	if _, err := w.StartDrag(promise); !errors.Is(err, ErrPromiseUnsupported) || !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("a promise: %v", err)
	}
}

// TestLiveDoDragDrop runs a real DoDragDrop with no button held: OLE asks the
// source at once and drops where the pointer is. It is off by default, because
// a pointer over some other window would drop there.
func TestLiveDoDragDrop(t *testing.T) {
	if os.Getenv("GNUI_LIVE_DRAG") == "" {
		t.Skip("set GNUI_LIVE_DRAG=1 to run a real DoDragDrop")
	}
	w := openDragWindow(t)
	w.buttons = 1 << ButtonLeft
	done := make(chan struct{})
	go func() {
		select {
		case <-done:
		case <-time.After(20 * time.Second):
			panic("DoDragDrop did not return")
		}
	}()
	ticket, err := w.StartDrag(&DragSpec{Items: []DragItem{{Text: "x"}}})
	close(done)
	if err != nil {
		t.Fatal(err)
	}
	if ev := eventsOf(w, EvDragEnd); len(ev) != 1 || ev[0].Tag != uint64(ticket) {
		t.Fatalf("EvDragEnd: %+v (ticket %d)", ev, ticket)
	}
}
