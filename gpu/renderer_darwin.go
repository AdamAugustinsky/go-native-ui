//go:build darwin

package gpu

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
	"gonativeui/platform"
)

// TextureKind is the pixel format of a renderer texture.
type TextureKind uint8

const (
	TexR8    TextureKind = iota // 1 byte per texel coverage
	TexRGBA8                    // 4 bytes per texel, premultiplied alpha
)

// ErrNoDevice is returned when the system has no Metal device.
var ErrNoDevice = errors.New("gpu: no Metal device")

// ringSlots is the number of frames in flight. Each has its own instance
// buffer.
const ringSlots = 3

// stageFloor is the smallest staging buffer: glyph updates are small.
const stageFloor = 64 << 10

// stageCalmFrames is how many frames in a row a slot must stage at most a
// quarter of its staging buffer before the buffer shrinks (each slot has
// every third frame: about three seconds at 60 Hz).
const stageCalmFrames = 60

var (
	rsNewBuffer    = objc.Sel("newBufferWithLength:options:")
	rsContents     = objc.Sel("contents")
	rsAlloc        = objc.Sel("alloc")
	rsInit         = objc.Sel("init")
	rsNewLibrary   = objc.Sel("newLibraryWithSource:options:error:")
	rsNewFunction  = objc.Sel("newFunctionWithName:")
	rsNewPipeline  = objc.Sel("newRenderPipelineStateWithDescriptor:error:")
	rsNewSampler   = objc.Sel("newSamplerStateWithDescriptor:")
	rsNewTexture   = objc.Sel("newTextureWithDescriptor:")
	rsTexDesc      = objc.Sel("texture2DDescriptorWithPixelFormat:width:height:mipmapped:")
	rsSetUsage     = objc.Sel("setUsage:")
	rsSetStorage   = objc.Sel("setStorageMode:")
	rsSetVertexFn  = objc.Sel("setVertexFunction:")
	rsSetFragFn    = objc.Sel("setFragmentFunction:")
	rsColorAttach  = objc.Sel("colorAttachments")
	rsObjectAt     = objc.Sel("objectAtIndexedSubscript:")
	rsSetPixelFmt  = objc.Sel("setPixelFormat:")
	rsSetBlendOn   = objc.Sel("setBlendingEnabled:")
	rsSetSrcRGB    = objc.Sel("setSourceRGBBlendFactor:")
	rsSetDstRGB    = objc.Sel("setDestinationRGBBlendFactor:")
	rsSetSrcAlpha  = objc.Sel("setSourceAlphaBlendFactor:")
	rsSetDstAlpha  = objc.Sel("setDestinationAlphaBlendFactor:")
	rsSetMinFilter = objc.Sel("setMinFilter:")
	rsSetMagFilter = objc.Sel("setMagFilter:")
	rsSetSAddress  = objc.Sel("setSAddressMode:")
	rsSetTAddress  = objc.Sel("setTAddressMode:")
	rsRPD          = objc.Sel("renderPassDescriptor")
	rsSetLoad      = objc.Sel("setLoadAction:")
	rsSetScissor   = objc.Sel("setScissorRect:")
	rsCopyTex      = objc.Sel("copyFromTexture:toTexture:")
	rsWidth        = objc.Sel("width")
	rsHeight       = objc.Sel("height")
	rsCopyRegion   = objc.Sel("copyFromTexture:sourceSlice:sourceLevel:sourceOrigin:sourceSize:toTexture:destinationSlice:destinationLevel:destinationOrigin:")
	rsSetStore     = objc.Sel("setStoreAction:")
	rsSetClear     = objc.Sel("setClearColor:")
	rsSetTexture   = objc.Sel("setTexture:")
	rsCommandBuf   = objc.Sel("commandBuffer")
	rsEncoder      = objc.Sel("renderCommandEncoderWithDescriptor:")
	rsEndEncoding  = objc.Sel("endEncoding")
	rsCommit       = objc.Sel("commit")
	rsStatus       = objc.Sel("status")
	rsWait         = objc.Sel("waitUntilCompleted")
	rsSetPipeline  = objc.Sel("setRenderPipelineState:")
	rsSetVBuffer   = objc.Sel("setVertexBuffer:offset:atIndex:")
	rsSetFBuffer   = objc.Sel("setFragmentBuffer:offset:atIndex:")
	rsSetVBytes    = objc.Sel("setVertexBytes:length:atIndex:")
	rsSetFSampler  = objc.Sel("setFragmentSamplerState:atIndex:")
	rsSetFTexture  = objc.Sel("setFragmentTexture:atIndex:")
	rsSetFTextures = objc.Sel("setFragmentTextures:withRange:")
	rsDraw         = objc.Sel("drawPrimitives:vertexStart:vertexCount:instanceCount:baseInstance:")
	rsReplace      = objc.Sel("replaceRegion:mipmapLevel:withBytes:bytesPerRow:")
	rsGetBytes     = objc.Sel("getBytes:bytesPerRow:fromRegion:mipmapLevel:")
	rsLocalizedErr = objc.Sel("localizedDescription")
	rsCBError      = objc.Sel("error")
	rsCode         = objc.Sel("code")
	rsNewQueue     = objc.Sel("newCommandQueue")
	rsBlitEncoder  = objc.Sel("blitCommandEncoder")
	rsCopyBufTex   = objc.Sel("copyFromBuffer:sourceOffset:sourceBytesPerRow:sourceBytesPerImage:sourceSize:toTexture:destinationSlice:destinationLevel:destinationOrigin:")

	metalOnce       sync.Once
	metalErr        error
	createDevice    uintptr
	clsPipelineDesc objc.ID
	clsSamplerDesc  objc.ID
	clsTexDesc      objc.ID
	clsRPD          objc.ID

	// Globals handed to C by pointer (never stack memory, see package ffi).
	nsErrorOut  uintptr    // NSError** out parameter
	regionWords [6]uintptr // MTLRegion for arm64 (passed by reference)
	viewportArg [2]float32 // vertex buffer 1
	pinnedBytes *byte      // keeps an Upload source on the heap for the duration of the call
	copySize    [3]uintptr // MTLSize and MTLOrigin of a staged copy, for arm64 (passed by reference)
	copyOrigin  [3]uintptr
	// MTLScissorRect of a partial frame's pass, and the MTLOrigin and
	// MTLSize of a clear tile's copy (clearRects)
	scissorWords [4]uintptr
	tileSrc      [3]uintptr
	tileSize     [3]uintptr
	tileDst      [3]uintptr
)

func loadMetal() error {
	metalOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/Metal.framework/Metal", purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			metalErr = err
			return
		}
		if createDevice, err = purego.Dlsym(lib, "MTLCreateSystemDefaultDevice"); err != nil {
			metalErr = err
			return
		}
		clsPipelineDesc = objc.Cls("MTLRenderPipelineDescriptor")
		clsSamplerDesc = objc.Cls("MTLSamplerDescriptor")
		clsTexDesc = objc.Cls("MTLTextureDescriptor")
		clsRPD = objc.Cls("MTLRenderPassDescriptor")
	})
	return metalErr
}

type ringSlot struct {
	demand instanceDemand
	buf    objc.ID
	mem    unsafe.Pointer // contents of buf (persistently mapped)
	quads  int            // capacity in quads
	cb     objc.ID        // retained command buffer that last read buf
	serial uint64         // the frame encoded into the slot (Renderer.serial)
	uses   []int32        // texture indices its frame binds or its staged blit writes

	// Stage's copies for the slot's next frame: their pixels in the staging
	// buffer, written by a blit in ucb, which Encode commits ahead of the
	// frame. The staging buffer is reused once ucb has completed.
	stage    objc.ID
	stageMem unsafe.Pointer
	stageCap int
	stageLen int
	copies   []stagedCopy
	ucb      objc.ID
	// The staging buffer's recent demand (noteStage): the most a frame of
	// the slot staged while every one staged at most a quarter of the
	// buffer, for calm frames in a row; shrink, when not 0, is the size it
	// goes down to once its upload has completed (retire).
	stagePeak, stageCalm, stageShrink int

	// The pixel-buffer imports its frame drew (external_darwin.go), released
	// once its command buffer has completed or is known never to run.
	leases  [MaxImports]lease
	nLeases int
}

// stagedCopy is one Stage call waiting for the next Encode.
type stagedCopy struct {
	tex             TextureID
	plane           uint8 // 0, or a YUV texture's chroma plane 1 or 2
	off, bpr        int
	x, y, w, h, bpp int
}

// retiredTex is a freed texture waiting for the frames that may sample it.
type retiredTex struct {
	tex    objc.ID
	serial uint64 // the last frame encoded when it was freed
	bytes  int64  // its memory, for TextureBytes (a YUV texture's on its first plane)
}

// Renderer draws DrawLists with Metal. All methods share objc.F, so they
// must be called from one thread at a time, and that goroutine must be locked
// to its OS thread (runtime.LockOSThread): Encode and RenderToRGBA push and
// pop an autorelease pool, and pools are per OS thread.
type Renderer struct {
	lossState // DeviceLost, Epoch: see devloss.go

	device, queue     objc.ID
	ownsDevice        bool
	pipeline, sampler objc.ID
	rpd, color0       objc.ID
	slots             [ringSlots]ringSlot
	next              int    // ring slot used by the next Encode
	serial            uint64 // frames encoded so far: the serial of the last one
	ids               texTable
	textures          []objc.ID     // by index
	kinds             []TextureKind // by index
	texW, texH        []int         // by index: the size of a texture made by NewTexture
	lastUse           []uint64      // by index: serial of the last frame that bound it (0 = none)
	xtex              []xtex        // by index: a YUV texture's planes (external_darwin.go)
	ext               extKit        // pixel-buffer imports
	retiring          []retiredTex
	waker             objc.ID // an empty command buffer whose completion gives the dispatcher a turn (Collect), 0 for none
	empty             objc.ID // 1×1 transparent texture bound for a freed id
	draws, totalDraws int
	offscreen         objc.ID
	offW, offH        int
	readback          []byte
	bd                backdropKit
	bindings          [3 + runTextures]objc.ID // stable memory for setFragmentTextures:withRange:
	destroyed         bool

	// texLog records the texture writes for partial frames (damage.go,
	// TextureWrites); partial holds the frame's damage while Encode draws
	// a partial frame (EncodeDamaged), sc the rectangle being drawn.
	texLog  textureLog
	partial *Damage
	sc      [4]uintptr // MTLScissorRect: x, y, width, height
	// clear tiles a partial frame's damaged rectangles are reset from
	// (clearRects), and the colour they hold
	clearTile   objc.ID
	clearTileRP objc.ID
	clearOf     [4]float32
	clearOK     bool
}

// NewRenderer builds the pipeline on an existing device and command queue,
// which the caller keeps owning. Instance ring slots allocate on their first
// non-empty frame. Setup allocates.
func NewRenderer(device, queue uintptr) (*Renderer, error) {
	if err := loadMetal(); err != nil {
		return nil, err
	}
	if device == 0 {
		return nil, ErrNoDevice
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	defer F.Call1(objc.PoolPop, pool)

	r := &Renderer{device: objc.ID(device), queue: objc.ID(queue)}
	r.ids = newTexTable()
	r.textures = make([]objc.ID, 0, initialTextures)
	r.kinds = make([]TextureKind, 0, initialTextures)
	r.texW = make([]int, 0, initialTextures)
	r.texH = make([]int, 0, initialTextures)
	r.lastUse = make([]uint64, 0, initialTextures)
	r.xtex = make([]xtex, 0, initialTextures)
	if err := r.build(); err != nil {
		return nil, err
	}

	// Texture 0: 1×1 white RGBA texel that shape quads bind.
	t0 := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(t0, 0, 0, 1, 1, []byte{255, 255, 255, 255}, 4)
	return r, nil
}

// build creates what the device holds besides the textures: the quad
// pipeline, the sampler, the render pass descriptor and the texture a freed
// id samples. The caller has an autorelease pool.
func (r *Renderer) build() error {
	F := objc.F
	// the quad pipeline: Prewarm's, or built here
	if r.pipeline = warmPipeline(r.device); r.pipeline == 0 {
		var fail buildFail
		switch r.pipeline, fail = quadPipeline(F, r.device, &nsErrorOut); fail {
		case buildCompile:
			return fmt.Errorf("gpu: compiling shaders: %s", nsErrorText())
		case buildEntry:
			return errors.New("gpu: shader entry points missing")
		case buildPipeline:
			return fmt.Errorf("gpu: creating pipeline: %s", nsErrorText())
		}
	}

	sd := objc.Send(objc.Send(clsSamplerDesc, rsAlloc), rsInit)
	defer objc.Release(sd)
	objc.Send1(sd, rsSetMinFilter, 1) // Linear
	objc.Send1(sd, rsSetMagFilter, 1)
	objc.Send1(sd, rsSetSAddress, 0) // ClampToEdge
	objc.Send1(sd, rsSetTAddress, 0)
	r.sampler = objc.Send1(r.device, rsNewSampler, uintptr(sd))

	r.rpd = objc.Retain(objc.Send(clsRPD, rsRPD))
	r.color0 = objc.Send1(objc.Send(r.rpd, rsColorAttach), rsObjectAt, 0)
	objc.Send1(r.color0, rsSetLoad, 2)  // Clear
	objc.Send1(r.color0, rsSetStore, 1) // Store

	// The texture a quad naming a freed texture samples: one transparent texel.
	r.empty = r.newMTLTexture(TexRGBA8, 1, 1)
	argRegion(objc.F.Reset().Arg(uintptr(r.empty)).Arg(uintptr(rsReplace)), 0, 0, 1, 1).
		Arg(0).Arg(uintptr(unsafe.Pointer(&emptyTexel[0]))).Arg(4).Call(objc.MsgSend)
	return nil
}

// emptyTexel is the content of Renderer.empty (a global: C reads it).
var emptyTexel [4]byte

// NewOffscreenRenderer creates the system default device and a command
// queue and a Renderer on them; Destroy releases all three. It returns
// ErrNoDevice if the machine has no Metal device.
func NewOffscreenRenderer() (*Renderer, error) {
	if err := loadMetal(); err != nil {
		return nil, err
	}
	dev := objc.F.Call0(createDevice)
	if dev == 0 {
		return nil, ErrNoDevice
	}
	q := uintptr(objc.Send(objc.ID(dev), rsNewQueue))
	r, err := NewRenderer(dev, q)
	if err != nil {
		objc.Release(objc.ID(q))
		objc.Release(objc.ID(dev))
		return nil, err
	}
	r.ownsDevice = true
	return r, nil
}

func nsErrorText() string {
	if nsErrorOut == 0 {
		return "unknown error"
	}
	return string(objc.AppendUTF8(nil, objc.Send(objc.ID(nsErrorOut), rsLocalizedErr)))
}

const (
	mtlR8Unorm    = 10
	mtlRGBA8Unorm = 70
	mtlBGRA8Unorm = 80
)

// NewTexture creates a w×h texture. Contents are undefined until Upload. It
// reuses the index of a freed texture under a new generation (see
// FreeTexture); the table grows only past the most textures ever live at
// once, so creating textures in the frame path allocates on the Go heap only
// then (Metal allocates its own memory).
func (r *Renderer) NewTexture(kind TextureKind, w, h int) TextureID {
	i, id := r.addTexture(r.newMTLTexture(kind, w, h), kind)
	r.texW[i], r.texH[i] = w, h
	r.ids.addBytes(i, texBytes(kind, w, h))
	r.texLog.note(id, 0, 0, 0, 0)
	return id
}

func (r *Renderer) newMTLTexture(kind TextureKind, w, h int) objc.ID {
	format := uintptr(mtlRGBA8Unorm)
	if kind == TexR8 {
		format = mtlR8Unorm
	}
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

// FreeTexture frees texture t. Its id stops resolving at once: Upload and
// FreeTexture ignore it, and quads that still name it draw nothing. The Metal
// texture is released when the frames encoded so far have finished on the
// GPU (see textures.go); FreeTexture itself never waits. Freeing texture 0,
// an unknown id or an id already freed does nothing.
// An index is reused under 4096 generations and then retired for good, so a
// renderer can create about 4.29 billion textures (2^20 indices × 4096) in its
// life. Pixel-buffer imports do not churn it: they reuse MaxImports indices
// (external_darwin.go).
func (r *Renderer) FreeTexture(t TextureID) {
	i, ok := r.ids.lookup(t)
	if !ok || i == 0 || (r.kinds[i] == texYUV && !r.xtex[i].owned) { // an import is the renderer's to end
		return
	}
	r.texLog.note(t, 0, 0, 0, 0)
	// Poll once when a batch of frees begins. Collect at the safe point (or
	// the next Encode) retires the rest, even if no further free follows.
	collect := len(r.retiring) == 0
	r.retiring = append(r.retiring, retiredTex{r.textures[i], r.serial, r.ids.remove(i)})
	for _, p := range r.xtex[i].planes {
		if p != 0 {
			r.retiring = append(r.retiring, retiredTex{p, r.serial, 0})
		}
	}
	r.textures[i], r.xtex[i] = 0, xtex{}
	if collect {
		r.collect()
	}
}

// TextureCounts reports the live textures (texture 0 included) and the freed
// ones whose release still waits for the GPU.
func (r *Renderer) TextureCounts() (live, retiring int) {
	return r.ids.n - r.ext.reserved, len(r.retiring)
}

// TextureBytes reports the memory of the renderer's textures: the live ones
// (atlas pages and standalone textures) and the freed ones that wait for the
// GPU, as width × height × bytes per texel (no mip levels, no driver padding).
// Texture 0 (4 bytes) is counted; the renderer's own targets and a pixel-buffer
// import's memory are not.
func (r *Renderer) TextureBytes() int64 {
	n := r.ids.used
	for _, x := range r.retiring {
		n += x.bytes
	}
	return n
}

// collect retires the ring slots whose command buffers have completed,
// without waiting, and releases the freed textures no unfinished frame may
// sample. A command buffer that is not committed yet is unfinished: it
// retires only when Encode reuses its slot.
func (r *Renderer) collect() (committed bool) {
	pending := r.serial + 1 // the oldest frame still to finish
	for i := range r.slots {
		s := &r.slots[i]
		for k, cb := range [2]objc.ID{s.ucb, s.cb} {
			if cb == 0 {
				continue
			}
			st := objc.Send(cb, rsStatus)
			if st >= 4 { // Completed, Error
				r.noteCompleted(cb, st)
				objc.Release(cb)
				if k == 0 {
					s.ucb, s.stageLen = 0, 0
				} else {
					s.cb = 0
					r.releaseLeases(s)
				}
			} else {
				pending = min(pending, s.serial)
				committed = committed || st >= 2
			}
		}
	}
	if r.waker != 0 && objc.Send(r.waker, rsStatus) >= 4 {
		objc.Release(r.waker)
		r.waker = 0
	}
	n := 0
	for _, x := range r.retiring {
		if x.serial < pending {
			objc.Release(x.tex)
			continue
		}
		r.retiring[n] = x
		n++
	}
	clear(r.retiring[n:])
	r.retiring = r.retiring[:n]
	return committed
}

// mtlErrorDeviceRemoved is MTLCommandBufferErrorDeviceRemoved: the GPU was
// removed while the command buffer ran.
const mtlErrorDeviceRemoved = 11

// noteCompleted reads the outcome of a finished command buffer: one that
// failed because the device was removed makes the renderer lost.
func (r *Renderer) noteCompleted(cb objc.ID, status objc.ID) {
	if status != 5 { // Error
		return
	}
	if e := objc.Send(cb, rsCBError); e != 0 && objc.Send(e, rsCode) == mtlErrorDeviceRemoved {
		r.lost = true
	}
}

// Collect releases, without waiting, what finished frames held: the leases
// of their pixel-buffer imports and the textures freed while they might
// sample them. The app's safe point calls it. While freed textures wait for
// frames committed to the GPU, it arranges one wake for when those have
// finished: an empty command buffer, which the queue completes after them,
// whose completion handler gives the dispatcher a turn (and so this safe
// point), so they go even if no other frame follows. Frames not committed
// yet keep theirs until Encode reuses their slot. Nothing polls or redraws.
func (r *Renderer) Collect() {
	if r.ext.leased == 0 && len(r.retiring) == 0 && r.waker == 0 {
		return
	}
	committed := r.collect()
	if len(r.retiring) > 0 && r.waker == 0 && committed {
		r.armWake()
	}
}

var (
	// wakeBlock is the completion handler of Renderer.waker:
	// void (^)(id<MTLCommandBuffer>). It only gives the dispatcher a turn.
	wakeBlock     objc.ID
	wakeBlockOnce sync.Once
	// wakeCompletions counts the handler's calls (tests).
	wakeCompletions atomic.Int64
)

// armWake commits an empty command buffer whose completion, after every
// command buffer committed before it, wakes the dispatcher.
func (r *Renderer) armWake() {
	wakeBlockOnce.Do(func() {
		wakeBlock = objc.NewGlobalBlock("v16@?0@8", func(*ffi.CB) {
			wakeCompletions.Add(1)
			platform.SignalDispatch() // the safe point collects (Collect); no window redraws for it
		})
	})
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	cb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
	objc.Send1(cb, rsAddCompleted, uintptr(wakeBlock))
	objc.Send(cb, rsCommit)
	F.Call1(objc.PoolPop, pool)
	r.waker = cb
}

// argRegion appends an MTLRegion (origin x, y, 0; size w, h, 1) by value.
// The struct is 48 bytes, so arm64 passes it by reference.
func argRegion(f *ffi.Frame, x, y, w, h int) *ffi.Frame {
	regionWords = [6]uintptr{uintptr(x), uintptr(y), 0, uintptr(w), uintptr(h), 1}
	return f.Arg(uintptr(unsafe.Pointer(&regionWords)))
}

// Upload copies a w×h block of pixels into texture t at (x, y). pixels[0] is
// the block's first texel and stride is the byte distance between rows. An
// id that was freed, or names a YUV texture (StageYUV fills those), is
// ignored.
func (r *Renderer) Upload(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok || r.kinds[i] == texYUV {
		return
	}
	bpp := 1
	if r.kinds[i] == TexRGBA8 {
		bpp = 4
	}
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic("gpu: Upload: pixels too short for region")
	}
	r.texLog.note(t, x, y, w, h)
	// replaceRegion doesn't synchronise with the GPU: wait until frames
	// already submitted have stopped sampling the texture.
	r.waitSubmitted(i)
	pinnedBytes = &pixels[0] // force the backing array onto the heap while C reads it
	if stride%bpp == 0 {
		argRegion(objc.F.Reset().Arg(uintptr(r.textures[i])).Arg(uintptr(rsReplace)), x, y, w, h).
			Arg(0).Arg(uintptr(unsafe.Pointer(&pixels[0]))).Arg(uintptr(stride)).Call(objc.MsgSend)
	} else { // Metal needs bytesPerRow to be a multiple of the pixel size: copy row by row
		for row := 0; row < h; row++ {
			argRegion(objc.F.Reset().Arg(uintptr(r.textures[i])).Arg(uintptr(rsReplace)), x, y+row, w, 1).
				Arg(0).Arg(uintptr(unsafe.Pointer(&pixels[row*stride]))).Arg(uintptr(w * bpp)).Call(objc.MsgSend)
		}
	}
	pinnedBytes = nil
	runtime.KeepAlive(pixels)
}

// Stage copies a w×h block of pixels into texture t at (x, y), like Upload,
// in GPU order and without waiting for the GPU: the pixels are copied now
// into a staging buffer of the next ring slot (pixels may change as soon as
// Stage returns), and the next Encode commits a blit of them into the
// texture ahead of its frame, so the frames already submitted keep sampling
// the old contents and that frame and every later one see the new. Use it to
// fill part of a texture that frames in flight sample (an atlas page);
// Upload, which waits for those frames, suits a texture no frame has used.
// An id freed before the next Encode is skipped, and a YUV texture's is
// ignored (StageYUV fills those). The staging buffer grows to the most bytes
// staged for one frame (one per ring slot); it is Metal memory, not Go heap.
func (r *Renderer) Stage(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok || r.kinds[i] == texYUV {
		return
	}
	bpp := 1
	if r.kinds[i] == TexRGBA8 {
		bpp = 4
	}
	r.texLog.note(t, x, y, w, h)
	r.stage(t, 0, x, y, w, h, pixels, stride, bpp, "gpu: Stage: pixels too short for region")
}

// stage is Stage into plane plane of texture t (a YUV texture's chroma
// planes are 1 and 2), bpp bytes per texel; it panics with short if pixels
// is too short for the region.
func (r *Renderer) stage(t TextureID, plane uint8, x, y, w, h int, pixels []byte, stride, bpp int, short string) {
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic(short)
	}
	s := &r.slots[r.next]
	r.retire(s) // the slot's last frame and upload are done with its staging buffer (Encode waits for the same)
	row := w * bpp
	off := (s.stageLen + 15) &^ 15
	if off+row*h > s.stageCap {
		r.growStage(s, off+row*h)
	}
	dst := unsafe.Slice((*byte)(unsafe.Add(s.stageMem, off)), row*h)
	for y := 0; y < h; y++ {
		copy(dst[y*row:(y+1)*row], pixels[y*stride:])
	}
	s.stageLen = off + row*h
	s.copies = append(s.copies, stagedCopy{t, plane, off, row, x, y, w, h, bpp})
}

// growStage gives slot s a staging buffer of at least n bytes, keeping what
// is staged in it. The slot is retired: no GPU work reads the old one.
func (r *Renderer) growStage(s *ringSlot, n int) {
	r.newStage(s, max(n, 2*s.stageCap, stageFloor)) // glyph updates are small; large uploads still grow directly
	s.stagePeak, s.stageCalm, s.stageShrink = 0, 0, 0
}

// noteStage records the bytes slot s stages for the frame being encoded.
// After stageCalmFrames frames of the slot that each staged at most a
// quarter of its buffer, the buffer is marked to shrink to twice the most
// they staged (stageFloor at least): demand would have to double to grow it
// again, so a steady load does not churn it.
func (r *Renderer) noteStage(s *ringSlot) {
	if s.stageCap <= stageFloor || s.stageShrink > 0 {
		return
	}
	if s.stageLen*4 > s.stageCap {
		s.stagePeak, s.stageCalm = 0, 0
		return
	}
	s.stagePeak = max(s.stagePeak, s.stageLen)
	if s.stageCalm++; s.stageCalm >= stageCalmFrames {
		s.stageShrink = max(stageFloor, (2*s.stagePeak+stageFloor-1)/stageFloor*stageFloor)
		s.stagePeak, s.stageCalm = 0, 0
	}
}

// newStage replaces slot s's staging buffer with one of size bytes, keeping
// what is staged in it (which must fit). No GPU work may read the old one.
func (r *Renderer) newStage(s *ringSlot, size int) {
	buf := objc.Send2(r.device, rsNewBuffer, uintptr(size), 0) // MTLResourceStorageModeShared
	if buf == 0 {
		panic("gpu: staging buffer allocation failed")
	}
	mem := ffi.PtrFromC(uintptr(objc.Send(buf, rsContents)))
	if s.stage != 0 {
		copy(unsafe.Slice((*byte)(mem), s.stageLen), unsafe.Slice((*byte)(s.stageMem), s.stageLen))
		objc.Release(s.stage)
	}
	s.stage, s.stageMem, s.stageCap = buf, mem, size
}

// flushStaged commits slot s's staged copies in a command buffer of their
// own, ahead of the frame being encoded (the queue runs command buffers in
// commit order, and Metal orders the blit's writes after the reads of the
// frames committed before it).
func (r *Renderer) flushStaged(s *ringSlot) {
	if len(s.copies) == 0 {
		return
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	ucb := objc.Retain(objc.Send(r.queue, rsCommandBuf))
	blit := objc.Send(ucb, rsBlitEncoder)
	for _, c := range s.copies {
		k, ok := r.ids.lookup(c.tex)
		if !ok {
			continue // freed since it was staged
		}
		r.use(s, k) // an Upload into it waits for this blit
		dst := r.textures[k]
		if c.plane > 0 {
			dst = r.xtex[k].planes[c.plane-1]
		}
		F.Reset().Arg(uintptr(blit)).Arg(uintptr(rsCopyBufTex)).Arg(uintptr(s.stage)).Arg(uintptr(c.off)).
			Arg(uintptr(c.bpr)).Arg(uintptr(c.bpr * c.h))
		// MTLSize and MTLOrigin (24 bytes) by reference
		copySize, copyOrigin = [3]uintptr{uintptr(c.w), uintptr(c.h), 1}, [3]uintptr{uintptr(c.x), uintptr(c.y), 0}
		F.Arg(uintptr(unsafe.Pointer(&copySize))).Arg(uintptr(dst)).Arg(0).Arg(0).
			Arg(uintptr(unsafe.Pointer(&copyOrigin))).Call(objc.MsgSend)
	}
	objc.Send(blit, rsEndEncoding)
	objc.Send(ucb, rsCommit)
	F.Call1(objc.PoolPop, pool)
	s.ucb = ucb
	s.copies = s.copies[:0]
}

// use records that slot s's frame (Renderer.serial) binds texture index k,
// or that its staged blit writes it.
func (r *Renderer) use(s *ringSlot, k int) {
	if r.lastUse[k] != r.serial {
		r.lastUse[k] = r.serial
		s.uses = append(s.uses, int32(k))
	}
}

// waitSubmitted waits for the committed command buffers of every ring slot
// whose frame bound texture index i or whose staged blit wrote it: each may
// still read it. A texture no frame has bound (lastUse 0) waits for nothing,
// so filling a new texture never stalls. A buffer that isn't committed yet
// (the frame being built) is left alone: it reads the new contents. Every
// slot is checked, not only the latest reader: an older frame that is
// committed still samples the texture while a newer one waits for its commit.
func (r *Renderer) waitSubmitted(i int) {
	if r.lastUse[i] == 0 {
		return
	}
	for k := range r.slots {
		s := &r.slots[k]
		if !slices.Contains(s.uses, int32(i)) {
			continue
		}
		for _, cb := range [2]objc.ID{s.ucb, s.cb} {
			if cb != 0 {
				if st := objc.Send(cb, rsStatus); st >= 2 && st < 4 { // Committed, Scheduled
					objc.Send(cb, rsWait)
				}
			}
		}
	}
}

// growSlot (re)allocates a ring slot's instance buffer. The caller must have
// retired the slot's previous command buffer first.
func (r *Renderer) growSlot(s *ringSlot, quads int) error {
	n := sizeClass(quads, initialQuads)
	buf := objc.Send2(r.device, rsNewBuffer, uintptr(n*quadSize), 0) // MTLResourceStorageModeShared
	if buf == 0 {
		return errors.New("gpu: instance buffer allocation failed")
	}
	if s.buf != 0 {
		objc.Release(s.buf)
	}
	s.buf, s.quads = buf, n
	s.mem = ffi.PtrFromC(uintptr(objc.Send(buf, rsContents)))
	return nil
}

// retire waits until the GPU has finished reading slot s (its frame's
// command buffer if it was committed, and its staged upload) and drops the
// references. Waiting on a command buffer's status needs no completion
// handler, so no callbacks and no Go allocation. A command buffer that was
// never committed is dropped without waiting.
func (r *Renderer) retire(s *ringSlot) {
	for _, cb := range [2]objc.ID{s.ucb, s.cb} {
		if cb == 0 {
			continue
		}
		st := objc.Send(cb, rsStatus)
		if st >= 2 && st < 4 { // Committed, Scheduled
			objc.Send(cb, rsWait)
			st = objc.Send(cb, rsStatus)
		}
		r.noteCompleted(cb, st)
		objc.Release(cb)
	}
	if s.ucb != 0 {
		s.stageLen = 0
	}
	s.ucb, s.cb = 0, 0
	r.releaseLeases(s)
	if s.stageShrink > 0 && s.stageLen == 0 { // its upload is done and nothing is staged
		if s.stageShrink < s.stageCap {
			r.newStage(s, s.stageShrink)
		}
		s.stageShrink = 0
	}
}

// Encode records one render pass into cmdBuf: it clears target (an
// id<MTLTexture> with render-target usage, BGRA8Unorm) to clear, then draws
// list. The caller commits cmdBuf afterwards (and presents, if relevant).
//
// clear is written to the target as is, so it should be premultiplied. The
// instance data is copied into the next ring slot; if that slot's previous
// frame is still on the GPU, Encode waits for it, so at most ringSlots
// frames are in flight. A cmdBuf that is not committed yet keeps its slot,
// and the textures freed since it was encoded, until Encode reuses the slot;
// then it is dropped without waiting, so one that is never committed must
// not be committed later.
//
// Encode allocates nothing on the Go heap once the renderer and list are
// warm. Instance capacity follows demand with hysteresis (capacity.go).
func (r *Renderer) Encode(cmdBuf, target uintptr, width, height int, clear [4]float32, list *DrawList) {
	r.draws = 0
	if width <= 0 || height <= 0 {
		r.dropPending() // never encoded: nothing will sample them
		return
	}
	F := objc.F
	s := &r.slots[r.next]
	if r.next++; r.next == ringSlots {
		r.next = 0
	}
	r.retire(s)
	r.serial++
	s.serial = r.serial
	s.uses = s.uses[:0]
	if len(r.retiring) > 0 {
		r.collect()
	}
	r.noteStage(s)
	r.flushStaged(s)
	var quads []Quad
	extra := 0 // room after them for the backdrops' fills (Backdrop.Base)
	if list != nil {
		quads = list.Quads
		if len(quads) > 0 {
			extra = len(list.Backdrops)
		}
	}
	if n := s.demand.capacity(s.quads, len(quads)+extra); n != s.quads {
		if err := r.growSlot(s, n); err != nil {
			panic(err)
		}
	}
	if len(quads) > 0 {
		copy(unsafe.Slice((*Quad)(s.mem), len(quads)), quads)
	}
	s.cb = objc.Retain(objc.ID(cmdBuf))

	viewportArg[0], viewportArg[1] = float32(width), float32(height)

	pool := F.Call0(objc.PoolPush)
	// Backdrops (backdrop_darwin.go) end the pass, blur and resume it; or,
	// without the blur, are drawn as shapes of their fallback colour. Their
	// regions' fills (Backdrop.Base) are drawn first, after the list's quads
	// in the slot.
	inst := unsafe.Slice((*Quad)(s.mem), len(quads))
	blur, pre := false, 0
	if extra > 0 {
		all := unsafe.Slice((*Quad)(s.mem), len(quads)+extra)
		inst = all[:len(quads)]
		blur, pre = r.prepareBackdrops(target, width, height, list, inst, all[len(quads):])
	} else {
		r.sizeBackdrops(0, 0, width, height)
	}
	objc.Send1(r.color0, rsSetTexture, target)
	F.Reset().Arg(uintptr(r.color0)).Arg(uintptr(rsSetClear)).
		ArgRect(float64(clear[0]), float64(clear[1]), float64(clear[2]), float64(clear[3])).Call(objc.MsgSend)
	if d := r.partial; d != nil {
		// a partial frame (EncodeDamaged): the target keeps the previous
		// frame and is drawn again only inside each damaged rectangle
		objc.Send1(r.color0, rsSetLoad, 1) // Load
		for _, rc := range d.Rects[:d.N] {
			r.sc = [4]uintptr{uintptr(rc[0]), uintptr(rc[1]), uintptr(rc[2] - rc[0]), uintptr(rc[3] - rc[1])}
			r.encodeRun(cmdBuf, target, width, height, list, quads, inst, s, blur, pre)
		}
		r.sc = [4]uintptr{}
		objc.Send1(r.color0, rsSetLoad, 2) // Clear
	} else {
		r.encodeRun(cmdBuf, target, width, height, list, quads, inst, s, blur, pre)
	}
	objc.Send1(r.color0, rsSetTexture, 0)
	if blur {
		objc.Send1(r.bd.loadCol0, rsSetTexture, 0)
	}
	if r.ext.nPending > 0 {
		r.leaseImports(s, cmdBuf)
	}
	F.Call1(objc.PoolPop, pool)
}

// encodeRun encodes the main pass (one, or several split by backdrops) of
// the frame's quads, already in slot s, inside r.sc when a partial frame
// sets it.
func (r *Renderer) encodeRun(cmdBuf, target uintptr, width, height int, list *DrawList, quads, inst []Quad, s *ringSlot, blur bool, pre int) {
	enc := r.beginPass(cmdBuf, r.rpd, s, r.textures[0])

	// Shape quads join the open run. Plain textures share a small table;
	// only table overflow, YUV conversion changes and backdrops end a run.
	var table [runTextures]TextureID
	ntex := 0
	bound, yuv := TextureID(0), false
	r.use(s, 0)
	if pre > 0 {
		r.draw(enc, len(quads), pre)
	}
	runStart := 0
nextQuad:
	for i := range quads {
		q := &quads[i]
		if q.Mode == ModeBackdrop {
			if !blur || inst[i].Mode != ModeBackdrop {
				continue // its fallback, a shape
			}
			if i > runStart {
				r.draw(enc, runStart, i-runStart)
			}
			if r.sc[2] > 0 && !r.scissorMeets(q, width, height) {
				// a partial frame's rectangle it draws nothing in: no blur,
				// but the pass splits here as for one drawn, so the texture
				// table restarts at the same quads in every rectangle's pass
				// (the slots are written into the frame's one instance copy,
				// which the GPU reads after all of them are encoded)
				objc.Send(enc, rsEndEncoding)
				objc.Send1(r.bd.loadCol0, rsSetTexture, target)
				enc = r.beginPass(cmdBuf, r.bd.load, s, r.bd.tex[0])
				bound, yuv = 0, false
				ntex = 0
				objc.Send2(enc, rsSetFTexture, uintptr(r.textures[0]), 0)
				runStart = i + 1
				continue
			}
			objc.Send(enc, rsEndEncoding)
			b := list.Backdrops[q.Tex]
			r.encodeBackdrop(objc.ID(cmdBuf), target, width, height, q, &b, &inst[i])
			objc.Send1(r.bd.loadCol0, rsSetTexture, target)
			enc = r.beginPass(cmdBuf, r.bd.load, s, r.bd.tex[0])
			r.draw(enc, i, 1)
			bound, yuv = 0, false
			ntex = 0
			objc.Send2(enc, rsSetFTexture, uintptr(r.textures[0]), 0)
			runStart = i + 1
			continue
		}
		if q.Mode == ModeShape || q.Mode == ModeCapsule || yuv && q.Tex == bound {
			continue
		}
		if !yuv {
			for slot := range ntex {
				if table[slot] == q.Tex {
					inst[i].Tex = TextureID(slot)
					continue nextQuad
				}
			}
		}
		k, ok := r.ids.lookup(q.Tex)
		isYUV := ok && r.xtex[k].format != 0
		if yuv || isYUV || ntex == len(table) {
			if i > runStart {
				r.draw(enc, runStart, i-runStart)
			}
			runStart, ntex = i, 0
		}
		if isYUV {
			bound = q.Tex
			r.bindRun(enc, s, bound, &yuv, 0)
		} else {
			inst[i].Tex = TextureID(ntex) // only our ring copy, never list.Quads
			table[ntex] = q.Tex
			r.bindRun(enc, s, q.Tex, &yuv, uintptr(3+ntex))
			ntex++
		}
	}
	if len(quads) > runStart {
		r.draw(enc, runStart, len(quads)-runStart)
	}
	objc.Send(enc, rsEndEncoding)
}

// scissorMeets reports whether backdrop q draws or reads anything inside
// r.sc: a backdrop elsewhere is not blurred again in a partial frame.
func (r *Renderer) scissorMeets(q *Quad, width, height int) bool {
	b := quadBounds(q, width, height)
	if x0, y0, x1, y1, ok := backdropRegion(q, width, height); ok {
		b = union(b, [4]int32{int32(x0), int32(y0), int32(x1), int32(y1)})
	}
	sc := [4]int32{int32(r.sc[0]), int32(r.sc[1]), int32(r.sc[0] + r.sc[2]), int32(r.sc[1] + r.sc[3])}
	return overlaps(b, sc)
}

// beginPass begins an encoder of the main pass with descriptor rpd (its
// target set) and binds the quad pipeline and slot s's instances.
func (r *Renderer) beginPass(cmdBuf uintptr, rpd objc.ID, s *ringSlot, texture objc.ID) objc.ID {
	enc := objc.Send1(objc.ID(cmdBuf), rsEncoder, uintptr(rpd))
	if r.sc[2] > 0 {
		scissorWords = r.sc
		objc.Send1(enc, rsSetScissor, uintptr(unsafe.Pointer(&scissorWords)))
	}
	objc.Send1(enc, rsSetPipeline, uintptr(r.pipeline))
	objc.Send3(enc, rsSetVBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetFBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetVBytes, uintptr(unsafe.Pointer(&viewportArg)), 8, 1)
	objc.Send2(enc, rsSetFSampler, uintptr(r.sampler), 0)
	r.beginYUV(enc)
	r.bindings[0] = texture
	for slot := 1; slot < len(r.bindings); slot++ {
		r.bindings[slot] = r.empty
	}
	// NSRange is two integer registers on arm64.
	objc.Send3(enc, rsSetFTextures, uintptr(unsafe.Pointer(&r.bindings[0])), 0, uintptr(len(r.bindings)))
	return enc
}

// draw issues one instanced triangle-strip draw of count quads starting at
// instance first (baseInstance; [[instance_id]] includes it).
func (r *Renderer) draw(enc objc.ID, first, count int) {
	objc.F.Reset().Arg(uintptr(enc)).Arg(uintptr(rsDraw)).
		Arg(4). // MTLPrimitiveTypeTriangleStrip
		Arg(0).Arg(4).Arg(uintptr(count)).Arg(uintptr(first)).Call(objc.MsgSend)
	r.draws++
	r.totalDraws++
}

// DrawCalls is the number of draw calls the last Encode issued.
func (r *Renderer) DrawCalls() int { return r.draws }

// RenderToRGBA renders list into an offscreen BGRA8 target of the given
// size and writes the result to out as RGBA8 (premultiplied, row-major, top
// row first; len(out) must be at least width*height*4). It uses its own
// command buffer and waits for completion: for tests and PNG output.
func (r *Renderer) RenderToRGBA(width, height int, clear [4]float32, list *DrawList, out []byte) {
	if width <= 0 || height <= 0 {
		return
	}
	n := width * height * 4
	if len(out) < n {
		panic("gpu: RenderToRGBA: out too short")
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	if r.offscreen == 0 || r.offW != width || r.offH != height {
		r.newTarget(width, height)
	}
	cb := objc.Send(r.queue, rsCommandBuf)
	r.Encode(uintptr(cb), uintptr(r.offscreen), width, height, clear, list)
	objc.Send(cb, rsCommit)
	objc.Send(cb, rsWait)
	r.readTarget(out)
	F.Call1(objc.PoolPop, pool)
}

// readTarget copies the offscreen target into out as RGBA8.
func (r *Renderer) readTarget(out []byte) {
	width, height := r.offW, r.offH
	n := width * height * 4
	if cap(r.readback) < n {
		r.readback = make([]byte, n)
	}
	r.readback = r.readback[:n]
	argRegion(objc.F.Reset().Arg(uintptr(r.offscreen)).Arg(uintptr(rsGetBytes)).
		Arg(uintptr(unsafe.Pointer(&r.readback[0]))).Arg(uintptr(width*4)), 0, 0, width, height).
		Arg(0).Call(objc.MsgSend)
	for i := 0; i < n; i += 4 { // BGRA → RGBA
		out[i], out[i+1], out[i+2], out[i+3] = r.readback[i+2], r.readback[i+1], r.readback[i], r.readback[i+3]
	}
}

// newTarget (re)creates the offscreen BGRA8 render target.
func (r *Renderer) newTarget(w, h int) {
	if r.offscreen != 0 {
		objc.Release(r.offscreen)
	}
	d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, uintptr(w), uintptr(h), 0)
	objc.Send1(d, rsSetUsage, 1|4) // ShaderRead | RenderTarget
	objc.Send1(d, rsSetStorage, 0) // Shared: CPU-readable without a blit
	r.offscreen = objc.Send1(r.device, rsNewTexture, uintptr(d))
	r.offW, r.offH = w, h
	if r.offscreen == 0 {
		panic("gpu: offscreen target creation failed")
	}
}

// DropDevice does nothing on this OS: the old device's objects are released by
// Rebuild. (Direct3D 11 needs it before Surface.Recover.)
func (r *Renderer) DropDevice() {}

// Rebuild makes the renderer work again after its device was lost
// (DeviceLost): device and queue are the new id<MTLDevice> and
// id<MTLCommandQueue> (Surface.Device and Surface.Queue after
// Surface.Recover), which the caller keeps owning. A renderer made by
// NewOffscreenRenderer creates a device of its own again and ignores them
// (pass 0).
//
// It waits for the frames in flight, releases what the renderer made on the
// old device and builds the pipeline on the new one. Every live texture is
// created again under the same id, size and kind, empty except for texture 0
// (a YUV texture of NewYUVTexture too: StageYUV it again): the owners of the
// others upload them again when Epoch changes (see devloss.go). Pixel-buffer
// imports of the frame being built are dropped. On an error the renderer stays
// lost and Rebuild may be tried again.
func (r *Renderer) Rebuild(device, queue uintptr) error {
	if r.destroyed {
		return errors.New("gpu: Rebuild: the renderer is destroyed")
	}
	if err := loadMetal(); err != nil {
		return err
	}
	F := objc.F
	pool := F.Call0(objc.PoolPush)
	err := r.rebuild(device, queue)
	F.Call1(objc.PoolPop, pool)
	r.lost = err != nil
	if err == nil {
		r.epoch++
	}
	return err
}

func (r *Renderer) rebuild(device, queue uintptr) error {
	for i := range r.slots {
		s := &r.slots[i]
		s.stageShrink = 0
		r.retire(s)
		if s.buf != 0 {
			objc.Release(s.buf)
		}
		if s.stage != 0 {
			objc.Release(s.stage)
		}
		*s = ringSlot{uses: s.uses[:0], copies: s.copies[:0]}
	}
	if r.waker != 0 {
		objc.Send(r.waker, rsWait)
		objc.Release(r.waker)
		r.waker = 0
	}
	r.dropPending()
	if r.ext.cache != 0 {
		objc.F.Call1(cv.cfRelease, r.ext.cache)
		r.ext.cache = 0
	}
	for i := range r.textures {
		if !r.ids.live[i] {
			continue
		}
		x := &r.xtex[i]
		if r.kinds[i] == texYUV && !x.owned { // an import index: nothing of ours
			continue
		}
		for _, p := range x.planes {
			if p != 0 {
				objc.Release(p)
			}
		}
		x.planes = [2]objc.ID{}
		if r.textures[i] != 0 {
			objc.Release(r.textures[i])
			r.textures[i] = 0
		}
	}
	for _, x := range r.retiring {
		objc.Release(x.tex)
	}
	clear(r.retiring)
	r.retiring = r.retiring[:0]
	clear(r.lastUse)
	r.destroyBackdrops()
	r.bd = backdropKit{}
	for _, o := range []objc.ID{r.offscreen, r.pipeline, r.sampler, r.rpd, r.empty} {
		if o != 0 {
			objc.Release(o)
		}
	}
	r.offscreen, r.pipeline, r.sampler, r.rpd, r.empty, r.offW, r.offH = 0, 0, 0, 0, 0, 0, 0
	if r.ownsDevice {
		objc.Release(r.queue)
		objc.Release(r.device)
		r.queue, r.device = 0, 0
		dev := objc.F.Call0(createDevice)
		if dev == 0 {
			return ErrNoDevice
		}
		r.device = objc.ID(dev)
		r.queue = objc.Send(r.device, rsNewQueue)
	} else {
		if device == 0 || queue == 0 {
			return ErrNoDevice
		}
		r.device, r.queue = objc.ID(device), objc.ID(queue)
	}
	if err := r.build(); err != nil {
		return err
	}
	if r.ext.reserved > 0 { // imports were used: a texture cache on the new device
		cv.out = 0
		if st := int32(objc.F.Call5(cv.cacheCreate, 0, 0, uintptr(r.device), 0, uintptr(unsafe.Pointer(&cv.out)))); st != 0 || cv.out == 0 {
			return errors.New("gpu: CVMetalTextureCacheCreate failed")
		}
		r.ext.cache = cv.out
	}
	for i := range r.textures {
		if !r.ids.live[i] {
			continue
		}
		switch x := &r.xtex[i]; {
		case r.kinds[i] == texYUV && !x.owned:
		case r.kinds[i] == texYUV:
			r.textures[i] = r.newPlane(mtlR8Unorm, int(x.w), int(x.h))
			r.newChromaPlanes(x)
		default:
			r.textures[i] = r.newMTLTexture(r.kinds[i], r.texW[i], r.texH[i])
		}
	}
	r.Upload(0, 0, 0, 1, 1, []byte{255, 255, 255, 255}, 4)
	return nil
}

// Destroy waits for in-flight frames and releases everything the renderer
// created. A device and queue passed to NewRenderer are not released.
func (r *Renderer) Destroy() {
	if r.destroyed {
		return
	}
	r.destroyed = true
	for i := range r.slots {
		s := &r.slots[i]
		r.retire(s)
		if s.buf != 0 {
			objc.Release(s.buf)
			s.buf = 0
		}
		if s.stage != 0 {
			objc.Release(s.stage)
			s.stage, s.stageMem, s.stageCap, s.stageLen = 0, nil, 0, 0
		}
		s.copies = nil
	}
	if r.waker != 0 {
		objc.Send(r.waker, rsWait)
		objc.Release(r.waker)
		r.waker = 0
	}
	r.destroyExternal()
	if r.clearTile != 0 {
		objc.Release(r.clearTile)
		objc.Release(r.clearTileRP)
		r.clearTile, r.clearTileRP = 0, 0
	}
	for _, t := range r.textures {
		if t != 0 {
			objc.Release(t)
		}
	}
	for _, x := range r.retiring {
		objc.Release(x.tex)
	}
	r.destroyBackdrops()
	r.textures, r.retiring, r.ids = nil, nil, texTable{} // ids freed after Destroy resolve to nothing
	for _, o := range []objc.ID{r.offscreen, r.pipeline, r.sampler, r.rpd, r.empty} {
		if o != 0 {
			objc.Release(o)
		}
	}
	r.offscreen, r.pipeline, r.sampler, r.rpd, r.empty = 0, 0, 0, 0, 0
	if r.ownsDevice {
		objc.Release(r.queue)
		objc.Release(r.device)
	}
}

// InstanceCapacity is the retained GPU instance-ring capacity in quads.
func (r *Renderer) InstanceCapacity() int {
	n := 0
	for i := range r.slots {
		n += r.slots[i].quads
	}
	return n
}

// TextureWrites appends to dst the texture writes (Upload, Stage, StageYUV,
// NewTexture, FreeTexture, ImportPixelBuffer) made since serial, for a
// DamageTracker, and returns the serial to pass next time; complete is
// false when the log no longer holds them all (draw the frame whole).
func (r *Renderer) TextureWrites(since uint64, dst []TextureWrite) ([]TextureWrite, uint64, bool) {
	return r.texLog.since(since, dst)
}

// Partial frames (damage.go, D92). A window keeps a back buffer of its
// size (NewBackBuffer) that holds its last frame; each frame draws into it
// only where the frame's Damage says it changed, and copies it whole to the
// window's drawable, whose contents Metal does not keep from frame to frame.

// clearTileSize is the side of the tile a partial frame's damaged
// rectangles are reset from.
const clearTileSize = 256

// NewBackBuffer makes a w×h back buffer for EncodeDamaged (a private BGRA8
// render target the backdrops can read): about 4 bytes a pixel of GPU
// memory, 27 MB at 3360×2040. ReleaseBackBuffer frees it.
func (r *Renderer) NewBackBuffer(w, h int) uintptr {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	d := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, uintptr(w), uintptr(h), 0)
	objc.Send1(d, rsSetUsage, 1|4) // ShaderRead | RenderTarget
	objc.Send1(d, rsSetStorage, 2) // Private
	return uintptr(objc.Send1(r.device, rsNewTexture, uintptr(d)))
}

// ReleaseBackBuffer frees a back buffer of NewBackBuffer's once the frames
// that use it are done (Metal keeps it until then).
func (r *Renderer) ReleaseBackBuffer(b uintptr) {
	if b != 0 {
		objc.Release(objc.ID(b))
	}
}

// EncodeDamaged encodes list as Encode does, into back (a back buffer of
// NewBackBuffer's, of the target's size, holding the previous frame drawn
// into it) where d says the frame changed, the whole of it when d is Full,
// and then copies back to target (a drawable of the same size; none when 0
// or back). A frame with no damage only copies. Every pixel it leaves is the
// same as a whole frame's would be: d must be a DamageTracker's for this
// back buffer.
func (r *Renderer) EncodeDamaged(cmdBuf, target, back uintptr, width, height int, clear [4]float32, list *DrawList, d *Damage) {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	if d.Full {
		r.Encode(cmdBuf, back, width, height, clear, list)
	} else {
		if d.N > 0 {
			r.clearRects(cmdBuf, back, clear, d)
		}
		r.partial = d
		r.Encode(cmdBuf, back, width, height, clear, list)
		r.partial = nil
	}
	if target != 0 && target != back {
		blit := objc.Send(objc.ID(cmdBuf), rsBlitEncoder)
		objc.Send2(blit, rsCopyTex, back, target)
		objc.Send(blit, rsEndEncoding)
	}
}

// TextureSize is the size of a texture (a drawable's, a back buffer's) in
// pixels.
func TextureSize(t uintptr) (w, h int) {
	return int(objc.Send(objc.ID(t), rsWidth)), int(objc.Send(objc.ID(t), rsHeight))
}

// CheckBackBuffer compares a back buffer of NewBackBuffer's with list drawn
// whole into the offscreen target, once the frames committed before it are
// done: for a debug mode that checks each partial frame. ref and got hold
// width×height×4 bytes each. It returns false and the first pixel that
// differs.
func (r *Renderer) CheckBackBuffer(back uintptr, width, height int, clear [4]float32, list *DrawList, ref, got []byte) (x, y int, ok bool) {
	r.RenderToRGBA(width, height, clear, list, ref)
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	cb := objc.Send(r.queue, rsCommandBuf)
	blit := objc.Send(cb, rsBlitEncoder)
	objc.Send2(blit, rsCopyTex, back, uintptr(r.offscreen))
	objc.Send(blit, rsEndEncoding)
	objc.Send(cb, rsCommit)
	objc.Send(cb, rsWait)
	r.readTarget(got)
	n := width * height * 4
	for i := 0; i < n; i += 4 {
		if ref[i] != got[i] || ref[i+1] != got[i+1] || ref[i+2] != got[i+2] || ref[i+3] != got[i+3] {
			return i / 4 % width, i / 4 / width, false
		}
	}
	return 0, 0, true
}

// clearRects resets d's rectangles of back to clear, from a tile that
// Metal's own clear filled, so they hold exactly what a whole frame's
// LoadActionClear writes (a colour with alpha 0 included, which no blended
// quad could write).
func (r *Renderer) clearRects(cmdBuf, back uintptr, clear [4]float32, d *Damage) {
	if r.clearTile == 0 {
		t := objc.Send4(clsTexDesc, rsTexDesc, mtlBGRA8Unorm, clearTileSize, clearTileSize, 0)
		objc.Send1(t, rsSetUsage, 4)   // RenderTarget
		objc.Send1(t, rsSetStorage, 2) // Private
		r.clearTile = objc.Send1(r.device, rsNewTexture, uintptr(t))
		r.clearTileRP = objc.Retain(objc.Send(clsRPD, rsRPD))
		c0 := objc.Send1(objc.Send(r.clearTileRP, rsColorAttach), rsObjectAt, 0)
		objc.Send1(c0, rsSetTexture, uintptr(r.clearTile))
		objc.Send1(c0, rsSetLoad, 2)  // Clear
		objc.Send1(c0, rsSetStore, 1) // Store
		r.clearOK = false
	}
	if !r.clearOK || r.clearOf != clear {
		c0 := objc.Send1(objc.Send(r.clearTileRP, rsColorAttach), rsObjectAt, 0)
		objc.F.Reset().Arg(uintptr(c0)).Arg(uintptr(rsSetClear)).
			ArgRect(float64(clear[0]), float64(clear[1]), float64(clear[2]), float64(clear[3])).Call(objc.MsgSend)
		enc := objc.Send1(objc.ID(cmdBuf), rsEncoder, uintptr(r.clearTileRP))
		objc.Send(enc, rsEndEncoding)
		r.clearOf, r.clearOK = clear, true
	}
	blit := objc.Send(objc.ID(cmdBuf), rsBlitEncoder)
	for _, rc := range d.Rects[:d.N] {
		for y := rc[1]; y < rc[3]; y += clearTileSize {
			for x := rc[0]; x < rc[2]; x += clearTileSize {
				tileSrc = [3]uintptr{0, 0, 0}
				tileSize = [3]uintptr{uintptr(min(clearTileSize, rc[2]-x)), uintptr(min(clearTileSize, rc[3]-y)), 1}
				tileDst = [3]uintptr{uintptr(x), uintptr(y), 0}
				objc.F.Reset().Arg(uintptr(blit)).Arg(uintptr(rsCopyRegion)).Arg(uintptr(r.clearTile)).Arg(0).Arg(0).
					Arg(uintptr(unsafe.Pointer(&tileSrc))).Arg(uintptr(unsafe.Pointer(&tileSize))).
					Arg(back).Arg(0).Arg(0).Arg(uintptr(unsafe.Pointer(&tileDst))).Call(objc.MsgSend)
			}
		}
	}
	objc.Send(blit, rsEndEncoding)
}
