//go:build darwin

package gpu

import (
	"errors"
	"fmt"
	"math"
	"runtime"
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
	buf   objc.ID
	mem   unsafe.Pointer // contents of buf (persistently mapped)
	quads int            // capacity in quads
	cb    objc.ID        // retained command buffer that last read buf
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
	next              int // ring slot used by the next Encode
	textures          []objc.ID
	kinds             []TextureKind
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
	return r, nil
}

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

// NewTexture creates a w×h texture (setup path: it allocates). Contents are
// undefined until Upload.
func (r *Renderer) NewTexture(kind TextureKind, w, h int) TextureID {
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
	r.textures = append(r.textures, tex)
	r.kinds = append(r.kinds, kind)
	return TextureID(len(r.textures) - 1)
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
// the block's first texel and stride is the byte distance between rows.
func (r *Renderer) Upload(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	bpp := 1
	if r.kinds[t] == TexRGBA8 {
		bpp = 4
	}
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic("gpu: Upload: pixels too short for region")
	}
	// replaceRegion doesn't synchronise with the GPU: wait until frames
	// already submitted have stopped sampling the texture.
	r.waitSubmitted()
	pinnedBytes = &pixels[0] // force the backing array onto the heap while C reads it
	if stride%bpp == 0 {
		argRegion(objc.F.Reset().Arg(uintptr(r.textures[t])).Arg(uintptr(rsReplace)), x, y, w, h).
			Arg(0).Arg(uintptr(unsafe.Pointer(&pixels[0]))).Arg(uintptr(stride)).Call(objc.MsgSend)
	} else { // Metal needs bytesPerRow to be a multiple of the pixel size: copy row by row
		for row := 0; row < h; row++ {
			argRegion(objc.F.Reset().Arg(uintptr(r.textures[t])).Arg(uintptr(rsReplace)), x, y+row, w, 1).
				Arg(0).Arg(uintptr(unsafe.Pointer(&pixels[row*stride]))).Arg(uintptr(w * bpp)).Call(objc.MsgSend)
		}
	}
	pinnedBytes = nil
	runtime.KeepAlive(pixels)
}

// waitSubmitted waits for every committed command buffer that may still read
// the renderer's textures. A buffer that isn't committed yet (the frame being
// built) is left alone: it reads the new contents.
func (r *Renderer) waitSubmitted() {
	for i := range r.slots {
		if cb := r.slots[i].cb; cb != 0 {
			if st := objc.Send(cb, rsStatus); st >= 2 && st < 4 { // Committed, Scheduled
				objc.Send(cb, rsWait)
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

// retire waits until the GPU has finished reading slot s (if its command
// buffer was committed) and drops the reference. Waiting on the command
// buffer's status needs no completion handler, so no callbacks and no Go
// allocation. A command buffer that was never committed is dropped without
// waiting.
func (s *ringSlot) retire() {
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
// frames are in flight. A cmdBuf that is never committed must not be
// expected to protect its slot.
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
	s.retire()
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
		if int(bound) >= len(r.textures) {
			bound = 0
		}
		objc.Send2(enc, rsSetFTexture, uintptr(r.textures[bound]), 0)
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
	if cap(r.readback) < n {
		r.readback = make([]byte, n)
	}
	r.readback = r.readback[:n]
	argRegion(F.Reset().Arg(uintptr(r.offscreen)).Arg(uintptr(rsGetBytes)).
		Arg(uintptr(unsafe.Pointer(&r.readback[0]))).Arg(uintptr(width*4)), 0, 0, width, height).
		Arg(0).Call(objc.MsgSend)
	F.Call1(objc.PoolPop, pool)
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
		s.retire()
		if s.buf != 0 {
			objc.Release(s.buf)
			s.buf = 0
		}
	}
	for _, t := range r.textures {
		objc.Release(t)
	}
	r.textures = nil
	for _, o := range []objc.ID{r.offscreen, r.pipeline, r.sampler, r.rpd} {
		if o != 0 {
			objc.Release(o)
		}
	}
	r.offscreen, r.pipeline, r.sampler, r.rpd = 0, 0, 0, 0
	if r.ownsDevice {
		objc.Release(r.queue)
		objc.Release(r.device)
	}
}
