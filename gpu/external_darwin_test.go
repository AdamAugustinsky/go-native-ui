//go:build darwin

package gpu

import (
	"errors"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

var (
	red   = [4]int{255, 0, 0, 255}
	green = [4]int{0, 255, 0, 255}
	blue  = [4]int{0, 0, 255, 255}
)

// stripes is a w×h YUV image of three vertical stripes, red, green and blue,
// each w/3 wide, in space sp: its planes in format f (U holds the
// interleaved pairs for NV12).
func stripes(f YUVFormat, sp YUVSpace, w, h int) *YUVImage {
	cw, ch := chromaSize(w, h)
	cols := [3][3]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}}
	im := &YUVImage{Y: make([]byte, w*h), YStride: w}
	if f == YUVNV12 {
		im.U, im.UStride = make([]byte, cw*2*ch), cw*2
	} else {
		im.U, im.UStride, im.V, im.VStride = make([]byte, cw*ch), cw, make([]byte, cw*ch), cw
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := cols[x*3/w]
			l, cb, cr := toYUV(sp, c[0], c[1], c[2])
			im.Y[y*w+x] = l
			if x%2 == 0 && y%2 == 0 {
				cx, cy := x/2, y/2
				if f == YUVNV12 {
					im.U[cy*im.UStride+2*cx], im.U[cy*im.UStride+2*cx+1] = cb, cr
				} else {
					im.U[cy*cw+cx], im.V[cy*cw+cx] = cb, cr
				}
			}
		}
	}
	return im
}

// yuvQuad draws a w×h-texel texture t over (0, 0)-(dw, dh).
func yuvQuad(t TextureID, w, h, dw, dh float32) Quad {
	return Quad{Rect: [4]float32{0, 0, dw, dh}, UV: [4]float32{0, 0, w, h}, Mode: ModeRGBA, Tex: t,
		Color: [4]uint32{0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF, 0xFFFFFFFF}}
}

// checkStripes checks the middle of each stripe of a dw-wide render.
func checkStripes(t *testing.T, what string, m img, dw int) {
	t.Helper()
	y := m.h / 2
	near(t, what+": red", m.at(dw/6, y), red, 3)
	near(t, what+": green", m.at(dw/2, y), green, 3)
	near(t, what+": blue", m.at(dw*5/6, y), blue, 3)
}

// TestStageYUV: I420 in BT.709 video range and NV12 in BT.601 full range,
// staged from memory, draw their stripes in RGB.
func TestStageYUV(t *testing.T) {
	r := newTestRenderer(t)
	for _, c := range []struct {
		f  YUVFormat
		sp YUVSpace
	}{{YUVI420, BT709Video}, {YUVNV12, BT601Full}, {YUVI420, BT601Video}, {YUVNV12, BT709Full}} {
		tex, err := r.NewYUVTexture(c.f, 24, 4, c.sp)
		if err != nil {
			t.Fatal(err)
		}
		r.StageYUV(tex, stripes(c.f, c.sp, 24, 4))
		m := render(r, 48, 8, black, yuvQuad(tex, 24, 4, 48, 8))
		checkStripes(t, "format "+string(rune('0'+c.f))+" space "+string(rune('0'+c.sp)), m, 48)
		r.FreeTexture(tex)
	}
	// A second image replaces the first in GPU order.
	tex, _ := r.NewYUVTexture(YUVI420, 24, 4, BT709Video)
	r.StageYUV(tex, stripes(YUVI420, BT709Video, 24, 4))
	render(r, 48, 8, black, yuvQuad(tex, 24, 4, 48, 8))
	flipped := stripes(YUVI420, BT709Video, 24, 4)
	flipped.Turns = 2 // upside down: blue on the left
	r.StageYUV(tex, flipped)
	m := render(r, 48, 8, black, yuvQuad(tex, 24, 4, 48, 8))
	near(t, "turned twice: left", m.at(8, 4), blue, 3)
	near(t, "turned twice: right", m.at(40, 4), red, 3)
	// A plain texture drawn after a YUV one in the same frame is not converted.
	plain := texel(r, 10, 200, 30, 255)
	m = render(r, 48, 8, black, yuvQuad(tex, 24, 4, 48, 8), rgbaQuad(0, plain))
	near(t, "plain after YUV", m.at(0, 0), [4]int{10, 200, 30, 255}, 1)
}

// TestYUVTurns: a quarter turn clockwise draws the left of the image at the
// top, and three at the bottom.
func TestYUVTurns(t *testing.T) {
	r := newTestRenderer(t)
	tex, _ := r.NewYUVTexture(YUVI420, 24, 4, BT709Video)
	for _, c := range []struct {
		turns       int
		top, bottom [4]int
	}{{1, red, blue}, {3, blue, red}} {
		im := stripes(YUVI420, BT709Video, 24, 4)
		im.Turns = c.turns
		r.StageYUV(tex, im)
		m := render(r, 8, 48, black, yuvQuad(tex, 4, 24, 8, 48)) // turned: 4×24 texels
		near(t, "top", m.at(4, 8), c.top, 3)
		near(t, "middle", m.at(4, 24), green, 3)
		near(t, "bottom", m.at(4, 40), c.bottom, 3)
	}
}

// ---- Pixel buffers ----

var cvt struct {
	create, lock, unlock, base, bpr, setAttach, release uintptr
	ioKey, metalKey, matrixKey, m601, m709              uintptr
	out                                                 uintptr
}

func loadTestCV(t *testing.T) {
	t.Helper()
	if err := loadCoreVideo(); err != nil {
		t.Fatal(err)
	}
	h, err := purego.Dlopen("/System/Library/Frameworks/CoreVideo.framework/CoreVideo", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Fatal(err)
	}
	fn := func(name string) uintptr {
		p, err := purego.Dlsym(h, name)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	val := func(name string) uintptr { return *(*uintptr)(ffi.PtrFromC(fn(name))) }
	cvt.create, cvt.lock, cvt.unlock = fn("CVPixelBufferCreate"), fn("CVPixelBufferLockBaseAddress"), fn("CVPixelBufferUnlockBaseAddress")
	cvt.base, cvt.bpr = fn("CVPixelBufferGetBaseAddressOfPlane"), fn("CVPixelBufferGetBytesPerRowOfPlane")
	cvt.setAttach, cvt.release = fn("CVBufferSetAttachment"), fn("CVPixelBufferRelease")
	cvt.ioKey, cvt.metalKey = val("kCVPixelBufferIOSurfacePropertiesKey"), val("kCVPixelBufferMetalCompatibilityKey")
	cvt.matrixKey, cvt.m601, cvt.m709 = val("kCVImageBufferYCbCrMatrixKey"), val("kCVImageBufferYCbCrMatrix_ITU_R_601_4"), val("kCVImageBufferYCbCrMatrix_ITU_R_709_2")
}

// newPixelBuffer makes an IOSurface-backed w×h NV12 pixel buffer of the
// stripes in space sp, tagged with sp's matrix; release it with cvt.release.
func newPixelBuffer(t *testing.T, sp YUVSpace, w, h int) uintptr {
	t.Helper()
	loadTestCV(t)
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)
	attrs := objc.Send(objc.Cls("NSMutableDictionary"), objc.Sel("dictionary"))
	set := objc.Sel("setObject:forKey:")
	objc.Send2(attrs, set, uintptr(objc.Send(objc.Cls("NSDictionary"), objc.Sel("dictionary"))), cvt.ioKey)
	objc.Send2(attrs, set, uintptr(objc.Send1(objc.Cls("NSNumber"), objc.Sel("numberWithBool:"), 1)), cvt.metalKey)
	format := uintptr(pixFmt420v)
	if sp == BT601Full || sp == BT709Full {
		format = pixFmt420f
	}
	cvt.out = 0
	if st := int32(F.Call6(cvt.create, 0, uintptr(w), uintptr(h), format, uintptr(attrs), uintptr(unsafe.Pointer(&cvt.out)))); st != 0 {
		t.Fatalf("CVPixelBufferCreate: %d", st)
	}
	pb := cvt.out
	im := stripes(YUVNV12, sp, w, h)
	F.Call2(cvt.lock, pb, 0)
	for p, src := range [2][]byte{im.Y, im.U} {
		stride := []int{im.YStride, im.UStride}[p]
		rows := []int{h, (h + 1) / 2}[p]
		base, bpr := F.Call2(cvt.base, pb, uintptr(p)), int(F.Call2(cvt.bpr, pb, uintptr(p)))
		dst := unsafe.Slice((*byte)(ffi.PtrFromC(base)), bpr*rows)
		for y := 0; y < rows; y++ {
			copy(dst[y*bpr:y*bpr+stride], src[y*stride:])
		}
	}
	F.Call2(cvt.unlock, pb, 0)
	m := cvt.m709
	if sp == BT601Video || sp == BT601Full {
		m = cvt.m601
	}
	F.Call4(cvt.setAttach, pb, cvt.matrixKey, m, 1) // kCVAttachmentMode_ShouldPropagate
	return pb
}

// TestImportPixelBuffer: NV12 pixel buffers, video range in BT.709 and full
// range in BT.601 (read from the buffer), draw their stripes with no copy.
func TestImportPixelBuffer(t *testing.T) {
	r := newTestRenderer(t)
	for _, sp := range []YUVSpace{BT709Video, BT601Full} {
		pb := newPixelBuffer(t, sp, 48, 16)
		tex, err := r.ImportPixelBuffer(pb, 0)
		if err != nil {
			t.Fatal(err)
		}
		m := render(r, 48, 16, black, yuvQuad(tex, 48, 16, 48, 16))
		checkStripes(t, "space "+string(rune('0'+sp)), m, 48)
		r.CollectLeases()
		objc.F.Call1(cvt.release, pb)
	}
	// Turned a quarter: the left stripe at the top.
	pb := newPixelBuffer(t, BT709Video, 48, 16)
	defer objc.F.Call1(cvt.release, pb)
	tex, _ := r.ImportPixelBuffer(pb, 1)
	m := render(r, 16, 48, black, yuvQuad(tex, 16, 48, 16, 48))
	near(t, "turned: top", m.at(8, 8), red, 3)
	near(t, "turned: bottom", m.at(8, 40), blue, 3)
}

// waitCompleted waits until the lease completion handler has run n times
// in all (the GPU finished those frames).
func waitCompleted(t *testing.T, n int64) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); leaseCompletions.Load() < n; {
		if time.Now().After(deadline) {
			t.Fatalf("completion handler ran %d times, want %d", leaseCompletions.Load(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func encodeFrame(r *Renderer, w, h int, list *DrawList) objc.ID {
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	if r.offscreen == 0 || r.offW != w || r.offH != h {
		r.newTarget(w, h)
	}
	cb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
	r.Encode(uintptr(cb), uintptr(r.offscreen), w, h, black, list)
	F.Call1(objc.PoolPop, pool)
	return cb
}

// TestImportLeases: the lease rules. A lease holds the pixel buffer until
// the GPU has finished its frame; without another frame, the completion
// handler and a collect release it; a frame never committed releases at
// once; an import id names nothing after its frame; at most MaxImports per
// frame and 3 × MaxImports in the ring; resizes, frames that no longer draw
// the buffer (scrolled off) and an idle end all come back to zero.
func TestImportLeases(t *testing.T) {
	r := newTestRenderer(t)
	pb := newPixelBuffer(t, BT709Video, 48, 16)
	defer objc.F.Call1(cvt.release, pb)
	rc0 := pixelBufferRetainCount(pb)
	counts := func(what string, pending, leased int) {
		t.Helper()
		p, l := r.ExternalCounts()
		if p != pending || l != leased {
			t.Errorf("%s: %d pending, %d leased; want %d, %d", what, p, l, pending, leased)
		}
	}
	list := &DrawList{}

	// Committed, then collected once completed, with no frame after it.
	tex, _ := r.ImportPixelBuffer(pb, 0)
	if again, _ := r.ImportPixelBuffer(pb, 0); again != tex {
		t.Errorf("a second import of the buffer in the frame: id %#x, want %#x", again, tex)
	}
	counts("imported", 1, 0)
	if rc := pixelBufferRetainCount(pb); rc != rc0+1 {
		t.Errorf("imported: retain count %d, want %d", rc, rc0+1)
	}
	list.Quads = append(list.Quads[:0], yuvQuad(tex, 48, 16, 48, 16))
	done0 := leaseCompletions.Load()
	cb := encodeFrame(r, 48, 16, list)
	counts("encoded", 0, 1)
	objc.Send(cb, rsCommit)
	objc.Release(cb)
	waitCompleted(t, done0+1)
	counts("completed, not collected", 0, 1)
	r.CollectLeases()
	counts("collected", 0, 0)
	if rc := pixelBufferRetainCount(pb); rc != rc0 {
		t.Errorf("collected: retain count %d, want %d", rc, rc0)
	}

	// The id named nothing after its frame: drawn again, it is empty.
	m := render(r, 48, 16, black, yuvQuad(tex, 48, 16, 48, 16))
	near(t, "stale import id", m.at(24, 8), [4]int{0, 0, 0, 255}, 0)

	// Encoded, never committed: released at once by Abandon.
	tex, _ = r.ImportPixelBuffer(pb, 0)
	list.Quads[0].Tex = tex
	cb = encodeFrame(r, 48, 16, list)
	counts("encoded, not committed", 0, 1)
	r.Abandon(uintptr(cb))
	objc.Release(cb)
	counts("abandoned", 0, 0)
	// Imported, the frame ends before Encode.
	r.ImportPixelBuffer(pb, 0)
	r.Abandon(0)
	counts("abandoned before Encode", 0, 0)
	if rc := pixelBufferRetainCount(pb); rc != rc0 {
		t.Errorf("abandoned: retain count %d, want %d", rc, rc0)
	}

	// MaxImports per frame, 3 × MaxImports in the ring.
	var pbs [MaxImports + 1]uintptr
	for i := range pbs {
		pbs[i] = newPixelBuffer(t, BT709Video, 16, 16)
		defer objc.F.Call1(cvt.release, pbs[i])
	}
	maxLeased := 0
	for frame := 0; frame < 8; frame++ {
		list.Quads = list.Quads[:0]
		for i := 0; i < MaxImports; i++ {
			tex, err := r.ImportPixelBuffer(pbs[i], 0)
			if err != nil {
				t.Fatal(err)
			}
			list.Quads = append(list.Quads, yuvQuad(tex, 16, 16, 16, 16))
		}
		if _, err := r.ImportPixelBuffer(pbs[MaxImports], 0); !errors.Is(err, ErrBusy) {
			t.Fatalf("import %d of a frame: %v, want ErrBusy", MaxImports+1, err)
		}
		cb := encodeFrame(r, 32+frame, 16+frame, list) // and a new size every frame (a live resize)
		objc.Send(cb, rsCommit)
		objc.Release(cb)
		_, l := r.ExternalCounts()
		maxLeased = max(maxLeased, l)
	}
	if maxLeased > 3*MaxImports {
		t.Errorf("%d leases in the ring, want at most %d", maxLeased, 3*MaxImports)
	}
	// Scrolled off: frames that import nothing retire the ring's leases as
	// they reuse its slots.
	list.Quads = list.Quads[:0]
	for i := 0; i < ringSlots; i++ {
		cb := encodeFrame(r, 48, 16, list)
		objc.Send(cb, rsCommit)
		objc.Release(cb)
	}
	counts("after a ring of frames that draw no import", 0, 0) // each slot released its leases as it was reused
	for i := range pbs {
		if rc := pixelBufferRetainCount(pbs[i]); rc != 1 {
			t.Errorf("buffer %d: retain count %d after the ring retired, want 1", i, rc)
		}
	}
}

// waitIdle waits for every committed frame of the ring (tests).
func (r *Renderer) waitIdle() {
	for i := range r.slots {
		if cb := r.slots[i].cb; cb != 0 {
			if st := objc.Send(cb, rsStatus); st >= 2 && st < 4 {
				objc.Send(cb, rsWait)
			}
		}
	}
}

// TestFreeYUVTexture: a YUV texture's planes are released like a texture.
func TestFreeYUVTexture(t *testing.T) {
	r := newTestRenderer(t)
	live0, _ := r.TextureCounts()
	tex, _ := r.NewYUVTexture(YUVI420, 64, 32, BT709Video)
	r.StageYUV(tex, stripes(YUVI420, BT709Video, 64, 32))
	render(r, 64, 32, black, yuvQuad(tex, 64, 32, 64, 32))
	r.FreeTexture(tex)
	render(r, 4, 4, black) // a frame later, the GPU done with them
	r.collect()
	if live, retiring := r.TextureCounts(); live != live0 || retiring != 0 {
		t.Errorf("after free: %d live (were %d), %d retiring", live, live0, retiring)
	}
	m := render(r, 64, 32, black, yuvQuad(tex, 64, 32, 64, 32))
	near(t, "freed YUV texture", m.at(32, 16), [4]int{0, 0, 0, 255}, 0)
}

// TestExternalZeroAlloc: steady-state frames that stage an I420 image,
// import a pixel buffer and draw both, rounded and clipped, allocate nothing.
func TestExternalZeroAlloc(t *testing.T) {
	r := newTestRenderer(t)
	const w, h = 128, 64
	pb := newPixelBuffer(t, BT709Video, 48, 16)
	defer objc.F.Call1(cvt.release, pb)
	yt, _ := r.NewYUVTexture(YUVI420, 64, 32, BT709Video)
	im := stripes(YUVI420, BT709Video, 64, 32)
	list := &DrawList{}
	frame := func() {
		r.StageYUV(yt, im)
		it, err := r.ImportPixelBuffer(pb, 0)
		if err != nil {
			panic(err)
		}
		list.Reset()
		q := yuvQuad(yt, 64, 32, 64, 32)
		q.Radii = [4]float32{8, 8, 8, 8}
		list.Add(q)
		q = yuvQuad(it, 48, 16, 48, 16)
		q.Rect = [4]float32{64, 0, 112, 16}
		q.Clip = [4]float32{0, 0, 100, 64}
		list.Add(q)
		list.Add(solid(0, 40, 20, 60, 0xFF0000FF))
		cb := encodeFrame(r, w, h, list)
		objc.Send(cb, rsCommit)
		objc.Release(cb)
		r.CollectLeases()
	}
	for i := 0; i < 30; i++ {
		frame()
	}
	const frames, attempts = 600, 5
	var mallocs, bytes uint64
	for a := 0; a < attempts; a++ {
		runtime.GC()
		time.Sleep(50 * time.Millisecond)
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < frames; i++ {
			frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		t.Logf("attempt %d: %d frames: %d mallocs, %d bytes", a+1, frames, mallocs, bytes)
		if mallocs == 0 && bytes == 0 {
			r.waitIdle()
			r.CollectLeases()
			if p, l := r.ExternalCounts(); p != 0 || l != 0 {
				t.Errorf("after the frames: %d pending, %d leased", p, l)
			}
			return
		}
	}
	t.Errorf("frames allocated in all %d windows (last: %d mallocs, %d bytes over %d frames)", attempts, mallocs, bytes, frames)
}
