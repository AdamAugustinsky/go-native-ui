//go:build darwin

package platform

import (
	"bytes"
	"encoding/binary"
	"image/color"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"
	"unsafe"

	"gonativeui/internal/objc"
)

// pbTypes returns the type names on the general pasteboard.
func pbTypes() []string {
	pbImageInit()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	types := objc.Send(pb, selPBTypes)
	var out []string
	for i, n := 0, int(objc.Send(types, selPBCount)); i < n; i++ {
		out = append(out, string(objc.AppendUTF8(nil, objc.Send1(types, selPBObjectAt, uintptr(i)))))
	}
	return out
}

// pbPutData replaces the pasteboard with b under the type typ, as another
// program would.
func pbPutData(typ string, b []byte) {
	pbImageInit()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	p := unsafe.Pointer(unsafe.SliceData(append(b, 0)))
	typeStr := objc.Str(typ)
	data := objc.ID(objc.F.Reset().Arg(uintptr(clsPBNSData)).Arg(uintptr(selPBDataWithBytes)).ArgPtr(p).Arg(uintptr(len(b))).Call(objc.MsgSend).R0())
	objc.Send(pb, selClearContents)
	objc.Send2(pb, selPBSetDataFT, uintptr(data), uintptr(typeStr))
	runtime.KeepAlive(b)
}

// pbPutFiles puts file URLs on the pasteboard the way Finder does: one item
// per file (writeObjects:).
func pbPutFiles(t *testing.T, paths ...string) {
	t.Helper()
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	urls := make([]uintptr, len(paths))
	for i, p := range paths {
		sel, str := objc.Sel("fileURLWithPath:"), objc.Str(p)
		urls[i] = uintptr(objc.Send1(clsPBNSURL, sel, uintptr(str)))
	}
	clsArr, selArr := objc.Cls("NSArray"), objc.Sel("arrayWithObjects:count:") // before the chain: they use the frame too
	arr := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).
		ArgPtr(unsafe.Pointer(&urls[0])).Arg(uintptr(len(urls))).Call(objc.MsgSend).R0())
	objc.Send(pb, selClearContents)
	selWrite := objc.Sel("writeObjects:")
	if !objc.SendBool1(pb, selWrite, uintptr(arr)) {
		t.Skip("the pasteboard cannot take file URLs here")
	}
}

// tiffRGB is an uncompressed little-endian RGB TIFF of the pixels (3 bytes
// each, row by row).
func tiffRGB(w, h int, px []byte) []byte {
	const nTags = 9
	ifd := 8
	bps := ifd + 2 + nTags*12 + 4
	data := bps + 6
	le := binary.LittleEndian
	b := make([]byte, data, data+len(px))
	copy(b, "II*\x00")
	le.PutUint32(b[4:], uint32(ifd))
	le.PutUint16(b[ifd:], nTags)
	tag := func(i int, id, typ uint16, n, v uint32) {
		o := ifd + 2 + i*12
		le.PutUint16(b[o:], id)
		le.PutUint16(b[o+2:], typ)
		le.PutUint32(b[o+4:], n)
		le.PutUint32(b[o+8:], v)
	}
	tag(0, 256, 4, 1, uint32(w))
	tag(1, 257, 4, 1, uint32(h))
	tag(2, 258, 3, 3, uint32(bps))
	tag(3, 259, 3, 1, 1)
	tag(4, 262, 3, 1, 2)
	tag(5, 273, 4, 1, uint32(data))
	tag(6, 277, 3, 1, 3)
	tag(7, 278, 4, 1, uint32(h))
	tag(8, 279, 4, 1, uint32(len(px)))
	le.PutUint16(b[bps:], 8)
	le.PutUint16(b[bps+2:], 8)
	le.PutUint16(b[bps+4:], 8)
	return append(b, px...)
}

// waitConverted lets the pasteboard worker finish and returns its answer.
func waitConverted(t *testing.T, w *Window) Event {
	t.Helper()
	awaitPaste(t, w, false)
	for _, e := range w.q.ev {
		if e.Kind == EvPaste {
			return e
		}
	}
	return Event{}
}

func (r *pasteRig) request(tag uint64, kinds PasteKinds) Event {
	r.t.Helper()
	r.w.q.reset()
	r.w.RequestPasteKinds(tag, kinds)
	awaitPaste(r.t, r.w, false)
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Kind != EvPaste || r.w.q.ev[0].Tag != tag {
		r.t.Fatalf("events after RequestPasteKinds: %+v", r.w.q.ev)
	}
	return r.w.q.ev[0]
}

func TestClipboardImageRoundTrip(t *testing.T) {
	r := newPasteRig(t)
	src, _ := testPNG(t, 40, 30)
	if err := SetClipboardImage(src); err != nil {
		t.Fatal(err)
	}
	e := r.request(1, WantImage)
	if e.Flags != 0 || e.Paste != PasteImage || !bytes.Equal(r.w.q.textOf(e.Text), src) {
		t.Errorf("image: flags %v kind %v, %d bytes (want %d)", e.Flags, e.Paste, e.Text.Len, len(src))
	}
	if e := r.request(2, WantText|WantFiles); e.Flags&FlagFailed == 0 {
		t.Errorf("an image is not text or files: %+v", e)
	}
	if e := r.request(3, WantText|WantImage); e.Paste != PasteImage || e.Flags != 0 {
		t.Errorf("text or image: %+v", e)
	}
	if ts := pbTypes(); slices.Contains(ts, "org.nspasteboard.ConcealedType") {
		t.Errorf("a plain copy is concealed: %v", ts)
	}
	// What is not a PNG is refused before the pasteboard is touched.
	if err := SetClipboardImage([]byte("text")); err != ErrNotPNG {
		t.Errorf("text: %v", err)
	}
	if err := SetClipboardImage(append(append([]byte{}, src[:33]...), make([]byte, MaxPaste)...)); err == nil {
		t.Error("garbage over MaxPaste accepted")
	}
	if e := r.request(4, WantImage); e.Flags != 0 {
		t.Errorf("a refused copy changed the clipboard: %+v", e)
	}
	// An image from another program (the PNG type, as a screenshot has it).
	pbPutData("public.png", src)
	if e := r.request(5, WantImage); e.Flags != 0 || !bytes.Equal(r.w.q.textOf(e.Text), src) {
		t.Errorf("foreign PNG: %+v", e)
	}
}

func TestClipboardConcealed(t *testing.T) {
	r := newPasteRig(t)
	_ = r
	has := func(ts []string) (c, tr bool) {
		return slices.Contains(ts, "org.nspasteboard.ConcealedType"), slices.Contains(ts, "org.nspasteboard.TransientType")
	}
	src, _ := testPNG(t, 4, 4)
	for _, tc := range []struct {
		name string
		set  func(concealed bool)
	}{
		{"text", func(c bool) { SetClipboardTextWith([]byte("secret"), ClipOptions{Concealed: c}) }},
		{"image", func(c bool) { SetClipboardImage(src, ClipOptions{Concealed: c}) }},
	} {
		tc.set(true)
		ts := pbTypes()
		if c, tr := has(ts); !c || !tr {
			t.Errorf("%s concealed: types %v", tc.name, ts)
		}
		if tc.name == "text" {
			if e := r.request(1, WantText); string(r.w.q.textOf(e.Text)) != "secret" {
				t.Errorf("the concealed text must still paste: %+v", e)
			}
		}
		tc.set(false)
		if c, tr := has(pbTypes()); c || tr {
			t.Errorf("%s: the next, plain copy keeps the markers: %v", tc.name, pbTypes())
		}
	}
}

func TestPasteFilesFromPasteboard(t *testing.T) {
	r := newPasteRig(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a file.txt"), filepath.Join(dir, "b\u00e9.png")
	for _, p := range []string{a, b} {
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pbPutFiles(t, a, b)
	e := r.request(1, WantFiles)
	got := PasteFilePaths(r.w.q.textOf(e.Text))
	if e.Flags != 0 || e.Paste != PasteFiles || e.Code != 2 || len(got) != 2 || got[0] != a {
		t.Fatalf("files: flags %v kind %v code %d: %q (want %q %q)", e.Flags, e.Paste, e.Code, got, a, b)
	}
	// (AppKit hands the accented name back decomposed: the same file, other bytes.)
	if fa, fb := mustStat(t, got[1]), mustStat(t, b); !os.SameFile(fa, fb) {
		t.Errorf("second path %q is not the file %q", got[1], b)
	}
	// A file copy that comes with text: files win when asked for, text otherwise.
	if e := r.request(2, WantText|WantImage); e.Flags&FlagFailed == 0 && e.Paste == PasteFiles {
		t.Errorf("files delivered to a request that did not ask: %+v", e)
	}
	// The list older programs write.
	pool := objc.F.Call0(objc.PoolPush)
	pb := objc.Send(clsPasteboard, selGeneralPB)
	objc.Send(pb, selClearContents)
	clsArr, selArr, selSet, str := objc.Cls("NSArray"), objc.Sel("arrayWithObject:"), objc.Sel("setPropertyList:forType:"), objc.Str(a)
	list := objc.ID(objc.F.Reset().Arg(uintptr(clsArr)).Arg(uintptr(selArr)).Arg(uintptr(str)).Call(objc.MsgSend).R0())
	objc.Send2(pb, selSet, uintptr(list), uintptr(pbFilenames))
	objc.F.Call1(objc.PoolPop, pool)
	if e := r.request(3, WantFiles); e.Flags != 0 || string(r.w.q.textOf(e.Text)) != a+"\x00" {
		t.Errorf("NSFilenamesPboardType: %+v %q", e, r.w.q.textOf(e.Text))
	}
	// Not files: the request fails.
	SetClipboardText([]byte("text"))
	if e := r.request(4, WantFiles); e.Flags&FlagFailed == 0 {
		t.Errorf("text is not files: %+v", e)
	}
}

func TestPasteTIFFConvertedOnWorker(t *testing.T) {
	r := newPasteRig(t)
	const w, h = 16, 9
	px := make([]byte, 3*w*h)
	for i := range px {
		px[i] = byte(i * 7)
	}
	pbPutData("public.tiff", tiffRGB(w, h, px))
	r.w.q.reset()
	r.w.RequestPasteKinds(9, WantImage)
	if len(r.w.q.ev) != 0 {
		t.Fatalf("the answer came during the call: %+v", r.w.q.ev)
	}
	e := waitConverted(t, r.w)
	if e.Flags != 0 || e.Paste != PasteImage || e.Tag != 9 || pastePending() {
		t.Fatalf("answer %+v", e)
	}
	out := r.w.q.textOf(e.Text)
	if w0, h0, ok := pngHeader(out); !ok || w0 != w || h0 != h || pngEnd(out) != len(out) {
		t.Fatalf("not the PNG of the image: %d x %d", w0, h0)
	}
	img := decodeNRGBA(t, out)
	for _, p := range [][2]int{{0, 0}, {5, 3}, {w - 1, h - 1}} {
		i := 3 * (p[1]*w + p[0])
		if got, want := img.NRGBAAt(p[0], p[1]), (color.NRGBA{px[i], px[i+1], px[i+2], 255}); got != want {
			t.Errorf("pixel %v: %v, want %v", p, got, want)
		}
	}
	// The PNG type, when there is one, is used as it is.
	src, _ := testPNG(t, 3, 3)
	pbPutData("public.png", src)
	if e := r.request(10, WantImage); e.Flags != 0 {
		t.Errorf("png: %+v", e)
	}

	// A newer request replaces the conversion in flight: its answer is never delivered.
	pbPutData("public.tiff", tiffRGB(w, h, px))
	r.w.q.reset()
	r.w.RequestPasteKinds(11, WantImage)
	SetClipboardText([]byte("newer"))
	r.w.RequestPaste(12)
	time.Sleep(300 * time.Millisecond)
	r.w.reapPaste()
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Tag != 12 || pastePending() {
		t.Errorf("after a newer request: %+v", r.w.q.ev)
	}
}

func TestPasteTIFFRefusals(t *testing.T) {
	r := newPasteRig(t)
	// A small file that declares 100 million pixels is not decoded.
	pbPutData("public.tiff", tiffRGB(10000, 10000, make([]byte, 30)))
	r.w.q.reset()
	r.w.RequestPasteKinds(1, WantImage)
	if e := waitConverted(t, r.w); e.Flags&FlagFailed == 0 {
		t.Errorf("a huge declared size: %+v", e)
	}
	// Not an image at all.
	pbPutData("public.tiff", []byte("this is not a tiff, just text that is long enough"))
	r.w.q.reset()
	r.w.RequestPasteKinds(2, WantImage)
	if e := waitConverted(t, r.w); e.Flags&FlagFailed == 0 {
		t.Errorf("garbage: %+v", e)
	}
	// An image whose PNG would be over MaxPaste (noise does not compress).
	if testing.Short() {
		return
	}
	const w, h = 2400, 3000
	px := make([]byte, 3*w*h)
	rand.New(rand.NewSource(3)).Read(px)
	pbPutData("public.tiff", tiffRGB(w, h, px))
	r.w.q.reset()
	r.w.RequestPasteKinds(3, WantImage)
	if e := waitConverted(t, r.w); e.Flags&(FlagFailed|FlagTooBig) != FlagFailed|FlagTooBig || len(r.w.q.text) != 0 {
		t.Errorf("a PNG over MaxPaste: %+v, %d bytes kept", e, len(r.w.q.text))
	}
}

// A PNG over MaxPaste on the pasteboard is refused by its size; none of it is
// copied into the Go heap.
func TestPasteImageOverMaxIsRefused(t *testing.T) {
	r := newPasteRig(t)
	big := make([]byte, MaxPaste+1<<20)
	copy(big, pngSig[:])
	pbPutData("public.png", big)
	var before, after runtime.MemStats
	r.w.q.reset()
	runtime.ReadMemStats(&before)
	r.w.RequestPasteKinds(4, WantImage)
	awaitPaste(t, r.w, false)
	runtime.ReadMemStats(&after)
	if len(r.w.q.ev) != 1 || r.w.q.ev[0].Flags != FlagFailed|FlagTooBig || r.w.q.ev[0].Tag != 4 || len(r.w.q.text) != 0 {
		t.Errorf("events %+v, %d text bytes", r.w.q.ev, len(r.w.q.text))
	}
	if grown := after.TotalAlloc - before.TotalAlloc; grown > 1<<20 {
		t.Errorf("refusing a %d-byte image allocated %d bytes on the Go heap", len(big), grown)
	}
	// With text also requested, the text is delivered instead.
	SetClipboardText([]byte("t"))
	if e := r.request(5, WantImage|WantText); e.Paste != PasteText || e.Flags != 0 {
		t.Errorf("text after the image was refused: %+v", e)
	}
}

// The paste poll allocates nothing in steady state for any kind.
func TestPasteKindsZeroAllocs(t *testing.T) {
	r := newPasteRig(t)
	dir := t.TempDir()
	f := filepath.Join(dir, "f.txt")
	os.WriteFile(f, nil, 0o644)
	src, _ := testPNG(t, 64, 64)
	for _, c := range []struct {
		name  string
		put   func()
		kinds PasteKinds
	}{
		{"png", func() { SetClipboardImage(src) }, WantImage},
		{"png or text", func() { SetClipboardImage(src) }, WantImage | WantText},
		{"files", func() { pbPutFiles(t, f) }, WantFiles},
		{"nothing", func() { SetClipboardText(nil) }, WantImage | WantFiles},
	} {
		c.put()
		poll := func() {
			r.w.q.reset()
			r.w.RequestPasteKinds(1, c.kinds)
			awaitPaste(t, r.w, true)
		}
		poll()
		poll()
		if n := testing.AllocsPerRun(50, poll); n != 0 {
			t.Errorf("%s: %v allocations per poll", c.name, n)
		}
	}
}

func mustStat(t *testing.T, p string) os.FileInfo {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return fi
}
