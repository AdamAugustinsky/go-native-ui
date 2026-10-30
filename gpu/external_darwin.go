//go:build darwin

package gpu

import (
	"errors"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// YUV textures and pixel-buffer imports (see yuv.go).
//
// A YUV texture is an entry of the texture table whose Metal texture is the
// luma plane and whose chroma planes are in xtex. The run of quads that
// binds it binds the chroma planes too (textures 1 and 2) and the
// conversion (fragment buffer 1), and the shader's ModeRGBA path converts.
//
// Imports (ImportPixelBuffer) use MaxImports table indices reserved with
// the first. An import holds a lease: a retain of the CVPixelBuffer and the
// two CVMetalTextures of its planes, whose Metal textures alias the pixel
// buffer's memory (Core Video recycles that memory once the buffer is
// released). Encode moves the frame's leases into its ring slot and stops
// their ids resolving; the slot releases them once its command buffer has
// completed: when Encode reuses the slot (after the wait that paces
// frames), or earlier when a collect sees the command buffer completed.
// When no frame comes to reuse the slot (the window went idle or closed), a
// command buffer that carries leases has a completion handler, a
// process-lifetime global block that only gives the dispatcher a turn
// (platform.SignalDispatch), and the app's safe point calls CollectLeases,
// which reads the slots' status without waiting. A frame that is never
// committed releases its leases at once (Abandon). Nothing is released on a
// timer: a GPU that never completes keeps them, counted by ExternalCounts.

const (
	mtlRG8Unorm = 30
	texYUV      = TextureKind(255) // kinds[i] of a YUV texture or an import index

	pixFmt420v = 0x34323076 // kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange
	pixFmt420f = 0x34323066 // kCVPixelFormatType_420YpCbCr8BiPlanarFullRange
)

var (
	errPixelFormat = errors.New("gpu: ImportPixelBuffer: not a biplanar 4:2:0 (420v or 420f) pixel buffer")
	errNoBuffer    = errors.New("gpu: ImportPixelBuffer: no pixel buffer")
)

// xtex is what a texture-table entry has beyond its Metal texture: the
// chroma planes and conversion of a YUV texture (format 0 for others).
type xtex struct {
	planes [2]objc.ID // CbCr (NV12), or Cb and Cr (I420)
	w, h   int32      // luma size
	format YUVFormat
	space  YUVSpace
	turns  uint8
	owned  bool // the renderer's (NewYUVTexture), not an import's
}

// lease is one import's hold on Core Video objects: the CVPixelBuffer and
// the CVMetalTextures of its luma and chroma planes.
type lease struct{ pb, y, c uintptr }

// extKit is the renderer's import state.
type extKit struct {
	cache    uintptr // CVMetalTextureCacheRef, made with the first import
	cacheErr error
	reserved int               // table indices reserved for imports (0 or MaxImports)
	idx      [MaxImports]int32 // those indices
	pending  [MaxImports]lease // the imports of the frame being built
	nPending int
	leased   int // leases held by ring slots
}

// yuvRun mirrors quadMSL's YUVRun (64 bytes): the conversion rows, the
// number of planes (0: not YUV) and the quarter turns.
type yuvRun struct {
	m             [12]float32
	planes, turns uint32
	_             [2]uint32
}

var _ = [1]struct{}{}[unsafe.Sizeof(yuvRun{})-64]

var (
	rsAddCompleted = objc.Sel("addCompletedHandler:")

	yuvOff  yuvRun // bound at the start of every pass: not YUV
	yuvBind yuvRun // a YUV run's (C reads it while setFragmentBytes copies it)

	cv struct {
		once sync.Once
		err  error

		cacheCreate, cacheTexture, cacheFlush, textureGet uintptr
		pbRetain, pbRelease, pbFormat, pbPlaneW, pbPlaneH uintptr
		copyAttachment, cfRelease, cfEqual, cfRetainCount uintptr
		matrixKey, matrix601                              uintptr
		out                                               uintptr // a CF object out parameter (C writes it)
	}

	// leaseBlock is the completion handler of a command buffer that carries
	// leases: void (^)(id<MTLCommandBuffer>).
	leaseBlock     objc.ID
	leaseBlockOnce sync.Once
	// leaseCompletions counts the handler's calls (tests).
	leaseCompletions atomic.Int64
)

func loadCoreVideo() error {
	cv.once.Do(func() {
		vid, err := purego.Dlopen("/System/Library/Frameworks/CoreVideo.framework/CoreVideo", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cv.err = err
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			cv.err = err
			return
		}
		fn := func(h uintptr, name string) uintptr {
			p, err := purego.Dlsym(h, name)
			if err != nil && cv.err == nil {
				cv.err = err
			}
			return p
		}
		val := func(h uintptr, name string) uintptr { // a data symbol holding a CFStringRef
			if p := fn(h, name); p != 0 {
				return *(*uintptr)(ffi.PtrFromC(p))
			}
			return 0
		}
		cv.cacheCreate = fn(vid, "CVMetalTextureCacheCreate")
		cv.cacheTexture = fn(vid, "CVMetalTextureCacheCreateTextureFromImage")
		cv.cacheFlush = fn(vid, "CVMetalTextureCacheFlush")
		cv.textureGet = fn(vid, "CVMetalTextureGetTexture")
		cv.pbRetain = fn(vid, "CVPixelBufferRetain")
		cv.pbRelease = fn(vid, "CVPixelBufferRelease")
		cv.pbFormat = fn(vid, "CVPixelBufferGetPixelFormatType")
		cv.pbPlaneW = fn(vid, "CVPixelBufferGetWidthOfPlane")
		cv.pbPlaneH = fn(vid, "CVPixelBufferGetHeightOfPlane")
		cv.copyAttachment = fn(vid, "CVBufferCopyAttachment")
		cv.matrixKey = val(vid, "kCVImageBufferYCbCrMatrixKey")
		cv.matrix601 = val(vid, "kCVImageBufferYCbCrMatrix_ITU_R_601_4")
		cv.cfRelease = fn(cf, "CFRelease")
		cv.cfEqual = fn(cf, "CFEqual")
		cv.cfRetainCount = fn(cf, "CFGetRetainCount")
	})
	return cv.err
}

func initLeaseBlock() {
	leaseBlock = objc.NewGlobalBlock("v16@?0@8", func(*ffi.CB) {
		leaseCompletions.Add(1)
		platform.SignalDispatch() // the safe point collects (CollectLeases); no window redraws for it
	})
}

// addTexture puts Metal texture tex of kind into the table (a setup path:
// the slices grow past the most textures ever live at once).
func (r *Renderer) addTexture(tex objc.ID, kind TextureKind) (int, TextureID) {
	i, id := r.ids.add()
	if i == len(r.textures) {
		r.textures = append(r.textures, 0)
		r.kinds = append(r.kinds, 0)
		r.lastUse = append(r.lastUse, 0)
		r.xtex = append(r.xtex, xtex{})
		r.texW = append(r.texW, 0)
		r.texH = append(r.texH, 0)
	}
	r.textures[i], r.kinds[i], r.lastUse[i], r.xtex[i] = tex, kind, 0, xtex{}
	r.texW[i], r.texH[i] = 0, 0
	return i, id
}

// newPlane creates a w×h texture of Metal pixel format format.
func (r *Renderer) newPlane(format uintptr, w, h int) objc.ID {
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	d := objc.Send4(clsTexDesc, rsTexDesc, format, uintptr(w), uintptr(h), 0)
	tex := objc.Send1(r.device, rsNewTexture, uintptr(d))
	F.Call1(objc.PoolPop, pool)
	if tex == 0 {
		panic("gpu: texture creation failed")
	}
	return tex
}

// NewYUVTexture creates the planes of a w×h YUV image of format f whose
// values are in space sp, to fill with StageYUV and draw with ModeRGBA
// quads (see yuv.go). Its contents are undefined until then. FreeTexture
// frees it, as any texture. It never fails on macOS.
func (r *Renderer) NewYUVTexture(f YUVFormat, w, h int, sp YUVSpace) (TextureID, error) {
	if w <= 0 || h <= 0 || (f != YUVNV12 && f != YUVI420) {
		return 0, errors.New("gpu: NewYUVTexture: bad format or size")
	}
	x := xtex{w: int32(w), h: int32(h), format: f, space: sp, owned: true}
	r.newChromaPlanes(&x)
	i, id := r.addTexture(r.newPlane(mtlR8Unorm, w, h), texYUV)
	r.xtex[i] = x
	cw, ch := chromaSize(w, h)
	r.ids.addBytes(i, int64(w)*int64(h)+2*int64(cw)*int64(ch)) // Y, and the chroma (NV12's RG8 plane or two R8 ones)
	return id, nil
}

// newChromaPlanes creates the chroma planes of owned YUV texture x.
func (r *Renderer) newChromaPlanes(x *xtex) {
	cw, ch := chromaSize(int(x.w), int(x.h))
	if x.format == YUVNV12 {
		x.planes[0] = r.newPlane(mtlRG8Unorm, cw, ch)
	} else {
		x.planes[0], x.planes[1] = r.newPlane(mtlR8Unorm, cw, ch), r.newPlane(mtlR8Unorm, cw, ch)
	}
}

// StageYUV fills YUV texture t (NewYUVTexture) with image im, staged like
// Stage: in GPU order and without waiting, so the frames in flight keep the
// previous image and the next frame shows this one (a video frame each
// frame). The planes are copied now; im may change as soon as it returns.
// It panics if a plane is too short for the texture's size; an id that is
// not a YUV texture of the renderer's is ignored.
func (r *Renderer) StageYUV(t TextureID, im *YUVImage) {
	i, ok := r.ids.lookup(t)
	if !ok || r.kinds[i] != texYUV || !r.xtex[i].owned {
		return
	}
	x := &r.xtex[i]
	w, h := int(x.w), int(x.h)
	cw, ch := chromaSize(w, h)
	r.stage(t, 0, 0, 0, w, h, im.Y, im.YStride, 1, "gpu: StageYUV: Y plane too short")
	if x.format == YUVNV12 {
		r.stage(t, 1, 0, 0, cw, ch, im.U, im.UStride, 2, "gpu: StageYUV: UV plane too short")
	} else {
		r.stage(t, 1, 0, 0, cw, ch, im.U, im.UStride, 1, "gpu: StageYUV: U plane too short")
		r.stage(t, 2, 0, 0, cw, ch, im.V, im.VStride, 1, "gpu: StageYUV: V plane too short")
	}
	x.turns = uint8(im.Turns & 3)
}

// initImports makes the texture cache and reserves the import indices,
// with the first import (setup: it allocates).
func (r *Renderer) initImports() error {
	e := &r.ext
	if e.reserved > 0 {
		return nil
	}
	if e.cacheErr != nil {
		return e.cacheErr
	}
	if err := loadCoreVideo(); err != nil {
		e.cacheErr = err
		return err
	}
	leaseBlockOnce.Do(initLeaseBlock)
	cv.out = 0
	if st := int32(objc.F.Call5(cv.cacheCreate, 0, 0, uintptr(r.device), 0, uintptr(unsafe.Pointer(&cv.out)))); st != 0 || cv.out == 0 {
		e.cacheErr = errors.New("gpu: CVMetalTextureCacheCreate failed")
		return e.cacheErr
	}
	e.cache = cv.out
	for k := range e.idx {
		i, _ := r.addTexture(0, texYUV)
		e.idx[k] = int32(i)
		r.invalidate(i)
	}
	e.reserved = MaxImports
	return nil
}

// invalidate bumps the generation of import index i: the id an import got
// stops resolving. The index stays live, so its generations wrap.
func (r *Renderer) invalidate(i int) {
	r.ids.gen[i] = (r.ids.gen[i] + 1) & texGenMask
	r.textures[i] = 0
	r.xtex[i] = xtex{}
}

func (r *Renderer) importID(k int) TextureID {
	i := r.ext.idx[k]
	return TextureID(uint32(i) | r.ids.gen[i]<<texIndexBits)
}

// ImportPixelBuffer imports pb, a CVPixelBufferRef in a biplanar 4:2:0
// format ('420v' or '420f', IOSurface-backed: what AVPlayerItemVideoOutput
// and VideoToolbox give), for the frame being built: the id it returns is
// drawn by ModeRGBA quads of the next Encode (UV in luma texels, turned by
// turns quarter turns clockwise, see YUVImage.Turns) and names nothing after
// it (ids are reused after 4,096 frames: keep none). Its planes are used in
// place, with no copy (CVMetalTextureCache). The range comes from the format
// and the matrix from the buffer's kCVImageBufferYCbCrMatrixKey (BT.709
// without one).
//
// The renderer retains pb and its plane textures until the GPU has finished
// the frame (see external_darwin.go): the caller may release pb at once.
// Importing the same pb again in a frame returns the same id. After
// MaxImports in one frame it returns ErrBusy. A frame that will not be
// committed must call Abandon. It allocates nothing once the first import
// has set the cache up.
func (r *Renderer) ImportPixelBuffer(pb uintptr, turns int) (TextureID, error) {
	if pb == 0 {
		return 0, errNoBuffer
	}
	e := &r.ext
	for k := 0; k < e.nPending; k++ {
		if e.pending[k].pb == pb {
			return r.importID(k), nil
		}
	}
	if e.nPending == MaxImports {
		return 0, ErrBusy
	}
	if err := r.initImports(); err != nil {
		return 0, err
	}
	F := objc.F
	sp := BT709Video
	switch uint32(F.Call1(cv.pbFormat, pb)) {
	case pixFmt420v:
	case pixFmt420f:
		sp = BT709Full
	default:
		return 0, errPixelFormat
	}
	if m := F.Call3(cv.copyAttachment, pb, cv.matrixKey, 0); m != 0 {
		if cv.matrix601 != 0 && F.Call2(cv.cfEqual, m, cv.matrix601)&0xFF != 0 {
			sp -= BT709Video // the same range in BT.601
		}
		F.Call1(cv.cfRelease, m)
	}
	w, h := F.Call2(cv.pbPlaneW, pb, 0), F.Call2(cv.pbPlaneH, pb, 0)
	y := r.cvTexture(pb, mtlR8Unorm, w, h, 0)
	if y == 0 {
		return 0, errors.New("gpu: ImportPixelBuffer: no luma texture")
	}
	c := r.cvTexture(pb, mtlRG8Unorm, F.Call2(cv.pbPlaneW, pb, 1), F.Call2(cv.pbPlaneH, pb, 1), 1)
	if c == 0 {
		F.Call1(cv.cfRelease, y)
		return 0, errors.New("gpu: ImportPixelBuffer: no chroma texture")
	}
	F.Call1(cv.pbRetain, pb)
	k := e.nPending
	e.pending[k] = lease{pb, y, c}
	e.nPending++
	i := int(e.idx[k])
	r.textures[i] = objc.ID(F.Call1(cv.textureGet, y))
	r.xtex[i] = xtex{
		planes: [2]objc.ID{objc.ID(F.Call1(cv.textureGet, c))},
		w:      int32(w), h: int32(h),
		format: YUVNV12, space: sp, turns: uint8(turns & 3),
	}
	return r.importID(k), nil
}

// cvTexture is a CVMetalTextureRef of plane plane of pb (0 on failure).
func (r *Renderer) cvTexture(pb, format, w, h, plane uintptr) uintptr {
	cv.out = 0
	st := objc.F.Reset().Arg(0).Arg(r.ext.cache).Arg(pb).Arg(0).Arg(format).Arg(w).Arg(h).Arg(plane).
		Arg(uintptr(unsafe.Pointer(&cv.out))).Call(cv.cacheTexture).R0()
	if int32(st) != 0 {
		return 0
	}
	return cv.out
}

// ExternalCounts reports the imports of the frame being built (pending) and
// the leases ring slots hold until their frames complete (leased; each a
// pixel buffer and two plane textures). Both return to 0 once the GPU has
// finished and the safe point or the next Encode has collected.
func (r *Renderer) ExternalCounts() (pending, leased int) { return r.ext.nPending, r.ext.leased }

// CollectLeases releases the leases of the ring slots whose frames have
// completed, reading their status without waiting. The app's safe point
// calls it (the completion handler of a frame with leases gives the
// dispatcher a turn).
func (r *Renderer) CollectLeases() {
	if r.ext.leased > 0 {
		r.collect()
	}
}

// Abandon releases what a frame that will not be committed holds: the
// imports not encoded yet, and, if cmdBuf (that frame's command buffer, or
// 0) was encoded and not committed, its slot's leases, at once, since the
// GPU never sees them. cmdBuf must then never be committed.
func (r *Renderer) Abandon(cmdBuf uintptr) {
	r.dropPending()
	if cmdBuf == 0 {
		return
	}
	for i := range r.slots {
		s := &r.slots[i]
		if s.cb == objc.ID(cmdBuf) && objc.Send(s.cb, rsStatus) < 2 { // NotEnqueued, Enqueued
			objc.Release(s.cb)
			s.cb = 0
			r.releaseLeases(s)
		}
	}
}

// dropPending releases the imports not encoded (a frame that ended before
// Encode).
func (r *Renderer) dropPending() {
	e := &r.ext
	for k := 0; k < e.nPending; k++ {
		releaseLease(&e.pending[k])
		r.invalidate(int(e.idx[k]))
	}
	e.nPending = 0
}

func releaseLease(l *lease) {
	F := objc.F
	F.Call1(cv.cfRelease, l.y)
	F.Call1(cv.cfRelease, l.c)
	F.Call1(cv.pbRelease, l.pb)
	*l = lease{}
}

// leaseImports gives slot s the frame's imports, encoded into cmdBuf: their
// ids stop resolving, and cmdBuf calls the completion handler.
func (r *Renderer) leaseImports(s *ringSlot, cmdBuf uintptr) {
	e := &r.ext
	n := e.nPending
	if s.nLeases+n > len(s.leases) {
		panic("gpu: ring slot leases overflow")
	}
	copy(s.leases[s.nLeases:], e.pending[:n])
	s.nLeases += n
	e.leased += n
	for k := 0; k < n; k++ {
		e.pending[k] = lease{}
		r.invalidate(int(e.idx[k]))
	}
	e.nPending = 0
	objc.Send1(objc.ID(cmdBuf), rsAddCompleted, uintptr(leaseBlock))
}

// releaseLeases releases slot s's leases: its frame has completed, or will
// never run.
func (r *Renderer) releaseLeases(s *ringSlot) {
	if s.nLeases == 0 {
		return
	}
	for i := 0; i < s.nLeases; i++ {
		releaseLease(&s.leases[i])
	}
	r.ext.leased -= s.nLeases
	s.nLeases = 0
	objc.F.Call2(cv.cacheFlush, r.ext.cache, 0)
}

// beginYUV disables conversion; beginPass binds the empty chroma textures.
func (r *Renderer) beginYUV(enc objc.ID) {
	objc.Send3(enc, rsSetFBytes, uintptr(unsafe.Pointer(&yuvOff)), unsafe.Sizeof(yuvOff), 1)
}

// bindRun binds texture id for the run of quads that starts with it: its
// Metal texture, and a YUV texture's planes and conversion. yuv says
// whether the conversion is bound (so that a later plain texture unbinds
// it). Plain textures bind into the run's table; YUV uses slot 0.
func (r *Renderer) bindRun(enc objc.ID, s *ringSlot, id TextureID, yuv *bool, slot uintptr) {
	tex := r.empty // a freed or unknown id draws nothing
	var x *xtex
	if k, ok := r.ids.lookup(id); ok && r.textures[k] != 0 {
		tex = r.textures[k]
		r.use(s, k)
		if r.xtex[k].format != 0 {
			x = &r.xtex[k]
		}
	}
	switch {
	case x != nil:
		yuvBind.m = yuvMatrix(x.space)
		yuvBind.planes, yuvBind.turns = 2, uint32(x.turns)
		objc.Send2(enc, rsSetFTexture, uintptr(x.planes[0]), 1)
		if x.format == YUVI420 {
			yuvBind.planes = 3
			objc.Send2(enc, rsSetFTexture, uintptr(x.planes[1]), 2)
		}
		objc.Send3(enc, rsSetFBytes, uintptr(unsafe.Pointer(&yuvBind)), unsafe.Sizeof(yuvBind), 1)
		*yuv = true
	case *yuv:
		objc.Send3(enc, rsSetFBytes, uintptr(unsafe.Pointer(&yuvOff)), unsafe.Sizeof(yuvOff), 1)
		*yuv = false
	}
	objc.Send2(enc, rsSetFTexture, uintptr(tex), slot)
}

// destroyExternal releases the imports and the YUV planes (Destroy, after
// the slots have retired).
func (r *Renderer) destroyExternal() {
	r.dropPending()
	for i := range r.xtex {
		if x := &r.xtex[i]; x.owned {
			for _, p := range x.planes {
				if p != 0 {
					objc.Release(p)
				}
			}
		}
	}
	r.xtex = nil
	if r.ext.cache != 0 {
		objc.F.Call1(cv.cfRelease, r.ext.cache)
	}
	r.ext = extKit{}
}

// pixelBufferRetainCount is pb's retain count (tests).
func pixelBufferRetainCount(pb uintptr) int {
	if loadCoreVideo() != nil {
		return -1
	}
	return int(objc.F.Call1(cv.cfRetainCount, pb))
}
