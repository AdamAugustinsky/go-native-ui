//go:build darwin

package gpu

import (
	"errors"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
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

// initialQuads is the starting instance capacity of every ring slot.
const initialQuads = 4096

// stageFloor is the smallest staging buffer: glyph updates are small.
const stageFloor = 64 << 10

// stageCalmFrames is how many frames in a row a slot must stage at most a
// quarter of its staging buffer before the buffer shrinks (each slot has
// every third frame: about three seconds at 60 Hz).
const stageCalmFrames = 60

// runTextures matches quadMSL's table at texture slots 3..10. Slots 0..2
// remain the backdrop/YUV bindings, whose runs cannot share conversions.
const runTextures = 8

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
	rsDraw         = objc.Sel("drawPrimitives:vertexStart:vertexCount:instanceCount:baseInstance:")
	rsReplace      = objc.Sel("replaceRegion:mipmapLevel:withBytes:bytesPerRow:")
	rsGetBytes     = objc.Sel("getBytes:bytesPerRow:fromRegion:mipmapLevel:")
	rsLocalizedErr = objc.Sel("localizedDescription")
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
}

// Renderer draws DrawLists with Metal. All methods share objc.F, so they
// must be called from one thread at a time, and that goroutine must be locked
// to its OS thread (runtime.LockOSThread): Encode and RenderToRGBA push and
// pop an autorelease pool, and pools are per OS thread.
type Renderer struct {
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
	lastUse           []uint64      // by index: serial of the last frame that bound it (0 = none)
	xtex              []xtex        // by index: a YUV texture's planes (external_darwin.go)
	ext               extKit        // pixel-buffer imports
	retiring          []retiredTex
	empty             objc.ID // 1×1 transparent texture bound for a freed id
	draws, totalDraws int
	offscreen         objc.ID
	offW, offH        int
	readback          []byte
	bd                backdropKit
	destroyed         bool
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
	r.lastUse = make([]uint64, 0, initialTextures)
	r.xtex = make([]xtex, 0, initialTextures)

	// the quad pipeline: Prewarm's, or built here
	if r.pipeline = warmPipeline(r.device); r.pipeline == 0 {
		var fail buildFail
		switch r.pipeline, fail = quadPipeline(F, r.device, &nsErrorOut); fail {
		case buildCompile:
			return nil, fmt.Errorf("gpu: compiling shaders: %s", nsErrorText())
		case buildEntry:
			return nil, errors.New("gpu: shader entry points missing")
		case buildPipeline:
			return nil, fmt.Errorf("gpu: creating pipeline: %s", nsErrorText())
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

	// Texture 0: 1×1 white RGBA texel that shape quads bind.
	t0 := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(t0, 0, 0, 1, 1, []byte{255, 255, 255, 255}, 4)
	// The texture a quad naming a freed texture samples: one transparent texel.
	r.empty = r.newMTLTexture(TexRGBA8, 1, 1)
	argRegion(objc.F.Reset().Arg(uintptr(r.empty)).Arg(uintptr(rsReplace)), 0, 0, 1, 1).
		Arg(0).Arg(uintptr(unsafe.Pointer(&emptyTexel[0]))).Arg(4).Call(objc.MsgSend)
	return r, nil
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
	_, id := r.addTexture(r.newMTLTexture(kind, w, h), kind)
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
	r.retiring = append(r.retiring, retiredTex{r.textures[i], r.serial})
	for _, p := range r.xtex[i].planes {
		if p != 0 {
			r.retiring = append(r.retiring, retiredTex{p, r.serial})
		}
	}
	r.textures[i], r.xtex[i] = 0, xtex{}
	r.ids.remove(i)
	r.collect()
}

// TextureCounts reports the live textures (texture 0 included) and the freed
// ones whose release still waits for the GPU.
func (r *Renderer) TextureCounts() (live, retiring int) {
	return r.ids.n - r.ext.reserved, len(r.retiring)
}

// collect retires the ring slots whose command buffers have completed,
// without waiting, and releases the freed textures no unfinished frame may
// sample. A command buffer that is not committed yet is unfinished: it
// retires only when Encode reuses its slot.
func (r *Renderer) collect() {
	pending := r.serial + 1 // the oldest frame still to finish
	for i := range r.slots {
		s := &r.slots[i]
		if s.ucb != 0 && objc.Send(s.ucb, rsStatus) >= 4 { // Completed, Error
			objc.Release(s.ucb)
			s.ucb = 0
			s.stageLen = 0
		}
		if s.cb != 0 && objc.Send(s.cb, rsStatus) >= 4 {
			objc.Release(s.cb)
			s.cb = 0
			r.releaseLeases(s)
		}
		if s.cb != 0 || s.ucb != 0 {
			pending = min(pending, s.serial)
		}
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
	n := s.quads
	if n == 0 {
		n = initialQuads
	}
	for n < quads {
		n *= 2
	}
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
	if s.ucb != 0 { // committed by flushStaged
		if st := objc.Send(s.ucb, rsStatus); st >= 2 && st < 4 {
			objc.Send(s.ucb, rsWait)
		}
		objc.Release(s.ucb)
		s.ucb = 0
		s.stageLen = 0
	}
	if s.cb != 0 {
		if st := objc.Send(s.cb, rsStatus); st >= 2 && st < 4 { // Committed, Scheduled
			objc.Send(s.cb, rsWait)
		}
		objc.Release(s.cb)
		s.cb = 0
	}
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
// warm. It grows the ring slot only if the list is larger than any before.
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
	if len(quads)+extra > s.quads {
		if err := r.growSlot(s, len(quads)+extra); err != nil {
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
	}
	objc.Send1(r.color0, rsSetTexture, target)
	F.Reset().Arg(uintptr(r.color0)).Arg(uintptr(rsSetClear)).
		ArgRect(float64(clear[0]), float64(clear[1]), float64(clear[2]), float64(clear[3])).Call(objc.MsgSend)
	enc := r.beginPass(cmdBuf, r.rpd, s)

	// Shape quads join the open run. Plain textures share a small table;
	// only table overflow, YUV conversion changes and backdrops end a run.
	var table [runTextures]TextureID
	ntex := 0
	bound, yuv := TextureID(0), false
	objc.Send2(enc, rsSetFTexture, uintptr(r.textures[0]), 0)
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
			objc.Send(enc, rsEndEncoding)
			b := list.Backdrops[q.Tex]
			r.encodeBackdrop(objc.ID(cmdBuf), target, width, height, q, &b, &inst[i])
			objc.Send1(r.bd.loadCol0, rsSetTexture, target)
			enc = r.beginPass(cmdBuf, r.bd.load, s)
			objc.Send2(enc, rsSetFTexture, uintptr(r.bd.tex[0]), 0)
			r.draw(enc, i, 1)
			bound, yuv = 0, false
			ntex = 0
			objc.Send2(enc, rsSetFTexture, uintptr(r.textures[0]), 0)
			runStart = i + 1
			continue
		}
		if q.Mode == ModeShape || yuv && q.Tex == bound {
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
	objc.Send1(r.color0, rsSetTexture, 0)
	if blur {
		objc.Send1(r.bd.loadCol0, rsSetTexture, 0)
	}
	if r.ext.nPending > 0 {
		r.leaseImports(s, cmdBuf)
	}
	F.Call1(objc.PoolPop, pool)
}

// beginPass begins an encoder of the main pass with descriptor rpd (its
// target set) and binds the quad pipeline and slot s's instances.
func (r *Renderer) beginPass(cmdBuf uintptr, rpd objc.ID, s *ringSlot) objc.ID {
	enc := objc.Send1(objc.ID(cmdBuf), rsEncoder, uintptr(rpd))
	objc.Send1(enc, rsSetPipeline, uintptr(r.pipeline))
	objc.Send3(enc, rsSetVBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetFBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetVBytes, uintptr(unsafe.Pointer(&viewportArg)), 8, 1)
	objc.Send2(enc, rsSetFSampler, uintptr(r.sampler), 0)
	r.beginYUV(enc)
	for slot := range runTextures {
		objc.Send2(enc, rsSetFTexture, uintptr(r.empty), uintptr(3+slot))
	}
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
	r.destroyExternal()
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
