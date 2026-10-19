//go:build darwin

package gpu

import (
	"errors"
	"fmt"
	"math"
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
}

// stagedCopy is one Stage call waiting for the next Encode.
type stagedCopy struct {
	tex             TextureID
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
	retiring          []retiredTex
	empty             objc.ID // 1×1 transparent texture bound for a freed id
	draws, totalDraws int
	offscreen         objc.ID
	offW, offH        int
	readback          []byte
	destroyed         bool
}

// NewRenderer builds the pipeline and instance rings on an existing device
// and command queue, which the caller keeps owning. Setup allocates.
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

	nsErrorOut = 0
	lib := objc.Send3(r.device, rsNewLibrary, uintptr(objc.Str(quadMSL)), 0, uintptr(unsafe.Pointer(&nsErrorOut)))
	if lib == 0 {
		return nil, fmt.Errorf("gpu: compiling shaders: %s", nsErrorText())
	}
	defer objc.Release(lib)
	vs := objc.Send1(lib, rsNewFunction, uintptr(objc.Str("vs_main")))
	fs := objc.Send1(lib, rsNewFunction, uintptr(objc.Str("fs_main")))
	if vs == 0 || fs == 0 {
		return nil, errors.New("gpu: shader entry points missing")
	}
	defer objc.Release(vs)
	defer objc.Release(fs)

	pd := objc.Send(objc.Send(clsPipelineDesc, rsAlloc), rsInit)
	defer objc.Release(pd)
	objc.Send1(pd, rsSetVertexFn, uintptr(vs))
	objc.Send1(pd, rsSetFragFn, uintptr(fs))
	ca := objc.Send1(objc.Send(pd, rsColorAttach), rsObjectAt, 0)
	objc.Send1(ca, rsSetPixelFmt, mtlBGRA8Unorm)
	objc.Send1(ca, rsSetBlendOn, 1)
	// Premultiplied alpha: src + dst × (1 − src.a), for colour and alpha.
	objc.Send1(ca, rsSetSrcRGB, 1)   // One
	objc.Send1(ca, rsSetDstRGB, 5)   // OneMinusSourceAlpha
	objc.Send1(ca, rsSetSrcAlpha, 1) // One
	objc.Send1(ca, rsSetDstAlpha, 5) // OneMinusSourceAlpha
	nsErrorOut = 0
	r.pipeline = objc.Send2(r.device, rsNewPipeline, uintptr(pd), uintptr(unsafe.Pointer(&nsErrorOut)))
	if r.pipeline == 0 {
		return nil, fmt.Errorf("gpu: creating pipeline: %s", nsErrorText())
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

	for i := range r.slots {
		if err := r.growSlot(&r.slots[i], initialQuads); err != nil {
			r.Destroy()
			return nil, err
		}
	}
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
	tex := r.newMTLTexture(kind, w, h)
	i, id := r.ids.add()
	if i == len(r.textures) {
		r.textures = append(r.textures, 0)
		r.kinds = append(r.kinds, 0)
		r.lastUse = append(r.lastUse, 0)
	}
	r.textures[i], r.kinds[i], r.lastUse[i] = tex, kind, 0
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
// life; C7's external textures should revisit the id's width if they churn.
func (r *Renderer) FreeTexture(t TextureID) {
	i, ok := r.ids.lookup(t)
	if !ok || i == 0 {
		return
	}
	r.retiring = append(r.retiring, retiredTex{r.textures[i], r.serial})
	r.textures[i] = 0
	r.ids.remove(i)
	r.collect()
}

// TextureCounts reports the live textures (texture 0 included) and the freed
// ones whose release still waits for the GPU.
func (r *Renderer) TextureCounts() (live, retiring int) { return r.ids.n, len(r.retiring) }

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
// The struct is 48 bytes: on arm64 it is passed by reference; on amd64 SysV
// it is MEMORY class and is copied to the stack word by word (ArgRect pushes
// raw 8-byte words, so integers are smuggled through as float64 bit
// patterns).
func argRegion(f *ffi.Frame, x, y, w, h int) *ffi.Frame {
	if runtime.GOARCH == "arm64" {
		regionWords = [6]uintptr{uintptr(x), uintptr(y), 0, uintptr(w), uintptr(h), 1}
		return f.Arg(uintptr(unsafe.Pointer(&regionWords)))
	}
	bits := func(v uint64) float64 { return math.Float64frombits(v) }
	return f.ArgRect(bits(uint64(x)), bits(uint64(y)), bits(0), bits(uint64(w))).
		ArgRect(bits(uint64(h)), bits(1), 0, 0) // the last two words are padding the callee never reads
}

// Upload copies a w×h block of pixels into texture t at (x, y). pixels[0] is
// the block's first texel and stride is the byte distance between rows. An
// id that was freed is ignored.
func (r *Renderer) Upload(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok {
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
// An id freed before the next Encode is skipped. The staging buffer grows to
// the most bytes staged for one frame (one per ring slot); it is Metal
// memory, not Go heap.
func (r *Renderer) Stage(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok {
		return
	}
	bpp := 1
	if r.kinds[i] == TexRGBA8 {
		bpp = 4
	}
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic("gpu: Stage: pixels too short for region")
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
	s.copies = append(s.copies, stagedCopy{t, off, row, x, y, w, h, bpp})
}

// growStage gives slot s a staging buffer of at least n bytes, keeping what
// is staged in it. The slot is retired: no GPU work reads the old one.
func (r *Renderer) growStage(s *ringSlot, n int) {
	size := max(n, 2*s.stageCap, 1<<20)
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
		F.Reset().Arg(uintptr(blit)).Arg(uintptr(rsCopyBufTex)).Arg(uintptr(s.stage)).Arg(uintptr(c.off)).
			Arg(uintptr(c.bpr)).Arg(uintptr(c.bpr * c.h))
		if runtime.GOARCH == "arm64" { // MTLSize and MTLOrigin (24 bytes) by reference
			copySize, copyOrigin = [3]uintptr{uintptr(c.w), uintptr(c.h), 1}, [3]uintptr{uintptr(c.x), uintptr(c.y), 0}
			F.Arg(uintptr(unsafe.Pointer(&copySize))).Arg(uintptr(r.textures[k])).Arg(0).Arg(0).
				Arg(uintptr(unsafe.Pointer(&copyOrigin)))
		} else { // amd64: the integer registers are used up, so every word goes to the stack in order
			F.Arg(uintptr(c.w)).Arg(uintptr(c.h)).Arg(1).Arg(uintptr(r.textures[k])).Arg(0).Arg(0).
				Arg(uintptr(c.x)).Arg(uintptr(c.y)).Arg(0)
		}
		F.Call(objc.MsgSend)
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
	if s.cb == 0 {
		return
	}
	if st := objc.Send(s.cb, rsStatus); st >= 2 && st < 4 { // Committed, Scheduled
		objc.Send(s.cb, rsWait)
	}
	objc.Release(s.cb)
	s.cb = 0
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
	r.flushStaged(s)
	var quads []Quad
	if list != nil {
		quads = list.Quads
	}
	if len(quads) > s.quads {
		if err := r.growSlot(s, len(quads)); err != nil {
			panic(err)
		}
	}
	if len(quads) > 0 {
		copy(unsafe.Slice((*Quad)(s.mem), len(quads)), quads)
	}
	s.cb = objc.Retain(objc.ID(cmdBuf))

	viewportArg[0], viewportArg[1] = float32(width), float32(height)

	pool := F.Call0(objc.PoolPush)
	objc.Send1(r.color0, rsSetTexture, target)
	F.Reset().Arg(uintptr(r.color0)).Arg(uintptr(rsSetClear)).
		ArgRect(float64(clear[0]), float64(clear[1]), float64(clear[2]), float64(clear[3])).Call(objc.MsgSend)
	enc := objc.Send1(objc.ID(cmdBuf), rsEncoder, uintptr(r.rpd))
	objc.Send1(enc, rsSetPipeline, uintptr(r.pipeline))
	objc.Send3(enc, rsSetVBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetFBuffer, uintptr(s.buf), 0, 0)
	objc.Send3(enc, rsSetVBytes, uintptr(unsafe.Pointer(&viewportArg)), 8, 1)
	objc.Send2(enc, rsSetFSampler, uintptr(r.sampler), 0)

	// Shape quads don't read the bound texture, so they join whatever run is
	// open; only a texture quad with a different texture ends it.
	bound := TextureID(0)
	objc.Send2(enc, rsSetFTexture, uintptr(r.textures[0]), 0)
	r.use(s, 0)
	runStart := 0
	for i := range quads {
		q := &quads[i]
		if q.Mode == ModeShape || q.Tex == bound {
			continue
		}
		if i > runStart {
			r.draw(enc, runStart, i-runStart)
		}
		runStart = i
		bound = q.Tex
		tex := r.empty // a freed or unknown id draws nothing
		if k, ok := r.ids.lookup(bound); ok {
			tex = r.textures[k]
			r.use(s, k)
		}
		objc.Send2(enc, rsSetFTexture, uintptr(tex), 0)
	}
	if len(quads) > runStart {
		r.draw(enc, runStart, len(quads)-runStart)
	}
	objc.Send(enc, rsEndEncoding)
	objc.Send1(r.color0, rsSetTexture, 0)
	F.Call1(objc.PoolPop, pool)
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
	for _, t := range r.textures {
		if t != 0 {
			objc.Release(t)
		}
	}
	for _, x := range r.retiring {
		objc.Release(x.tex)
	}
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
