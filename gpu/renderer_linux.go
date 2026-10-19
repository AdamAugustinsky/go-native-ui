//go:build linux

package gpu

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
)

// The OpenGL 3.3 core renderer (OpenGL ES 3.0 where only that exists). It
// mirrors renderer_darwin.go; the shader is shaders_linux.go.
//
// All GL entry points are resolved once (eglGetProcAddress, then dlsym of
// the GL library) and called through internal/ffi, so Encode, like Metal's,
// makes no Go allocations. OpenGL contexts belong to one thread: create the
// renderer, call Encode/RenderToRGBA/Upload/NewTexture and Destroy on the
// thread whose context is current, locked with runtime.LockOSThread.
//
// Instance data: the 108-byte Quad records are vertex attributes with
// divisor 1 (one record per instance; the vertex shader builds the strip's
// four corners from gl_VertexID). OpenGL 3.3 has neither
// glDrawArraysInstancedBaseInstance (4.2) nor persistent mapping
// (ARB_buffer_storage, 4.4), so:
//   - a batch that starts at instance `first` re-points the attributes at
//     byte offset first*108 instead of using a base instance;
//   - each Encode re-specifies its ring slot's buffer storage with
//     glBufferData(NULL) (orphaning: the driver hands out fresh memory and
//     the GPU keeps reading the old), then maps it with
//     glMapBufferRange(WRITE|INVALIDATE_BUFFER) and copies the quads in.
//     The ring of three slots gives the driver room to pipeline even when it
//     cannot rename buffers. Nothing ever waits on the GPU.

// TextureKind is the pixel format of a renderer texture.
type TextureKind uint8

const (
	TexR8    TextureKind = iota // 1 byte per texel coverage
	TexRGBA8                    // 4 bytes per texel, premultiplied alpha
)

// ErrNoDevice is returned when no EGL display with a usable OpenGL context
// can be created without a window.
var ErrNoDevice = errors.New("gpu: no EGL display with an OpenGL 3.3 core (or ES 3.0) context")

// ringSlots is the number of instance buffers used in rotation.
const ringSlots = 3

// initialQuads is the starting instance capacity of every ring slot.
const initialQuads = 4096

// GL constants (Khronos gl.h / glcorearb.h).
const (
	glArrayBuffer         = 0x8892
	glStreamDraw          = 0x88E0
	glVertexShader        = 0x8B31
	glFragmentShader      = 0x8B30
	glCompileStatus       = 0x8B81
	glLinkStatus          = 0x8B82
	glInfoLogLength       = 0x8B84
	glTexture2D           = 0x0DE1
	glTexture0            = 0x84C0
	glTextureMagFilter    = 0x2800
	glTextureMinFilter    = 0x2801
	glTextureWrapS        = 0x2802
	glTextureWrapT        = 0x2803
	glLinear              = 0x2601
	glClampToEdge         = 0x812F
	glR8                  = 0x8229
	glRGBA8               = 0x8058
	glRed                 = 0x1903
	glRGBA                = 0x1908
	glUnsignedByte        = 0x1401
	glUnsignedInt         = 0x1405
	glFloat               = 0x1406
	glUnpackRowLength     = 0x0CF2
	glUnpackAlignment     = 0x0CF5
	glPackAlignment       = 0x0D05
	glFramebuffer         = 0x8D40
	glRenderbuffer        = 0x8D41
	glColorAttachment0    = 0x8CE0
	glFramebufferComplete = 0x8CD5
	glBlend               = 0x0BE2
	glScissorTest         = 0x0C11
	glDepthTest           = 0x0B71
	glCullFace            = 0x0B44
	glOne                 = 1
	glOneMinusSrcAlpha    = 0x0303
	glTriangleStrip       = 5
	glMapWriteBit         = 0x0002
	glMapInvalidateBuffer = 0x0008
	glVersionEnum         = 0x1F02
	glNoError             = 0
	glSyncGPUCommands     = 0x9117 // GL_SYNC_GPU_COMMANDS_COMPLETE
	glAlreadySignaled     = 0x911A
	glConditionSatisfied  = 0x911C
	glWaitFailed          = 0x911D
)

// glx holds the resolved GL entry points.
var glx struct {
	getString, getError, enable, disable, blendFuncSeparate, colorMask uintptr
	flush, finish, getIntegerv                                         uintptr
	createShader, shaderSource, compileShader, getShaderiv             uintptr
	getShaderInfoLog, createProgram, attachShader, linkProgram         uintptr
	getProgramiv, getProgramInfoLog, deleteShader, deleteProgram       uintptr
	useProgram, getUniformLocation, uniform1i, uniform2f               uintptr
	genVertexArrays, bindVertexArray, deleteVertexArrays               uintptr
	genBuffers, bindBuffer, bufferData, bufferSubData                  uintptr
	mapBufferRange, unmapBuffer, deleteBuffers                         uintptr
	enableVertexAttribArray, vertexAttribPointer                       uintptr
	vertexAttribIPointer, vertexAttribDivisor, drawArraysInstanced     uintptr
	genTextures, bindTexture, activeTexture, texParameteri             uintptr
	texImage2D, texSubImage2D, pixelStorei, deleteTextures             uintptr
	genFramebuffers, bindFramebuffer, deleteFramebuffers               uintptr
	genRenderbuffers, bindRenderbuffer, renderbufferStorage            uintptr
	framebufferRenderbuffer, checkFramebufferStatus                    uintptr
	deleteRenderbuffers, readPixels                                    uintptr
	fenceSync, clientWaitSync, deleteSync                              uintptr
}

// gls is scratch memory handed to C by pointer: package-level, so never on a
// goroutine stack (see package ffi).
var gls struct {
	name   [128]byte
	src    [1]*byte
	i32    [4]int32
	u32    [4]uint32
	devs   [8]uintptr
	cfg    [1]uintptr
	num    int32
	maj    int32
	min    int32
	pinned *byte // keeps an Upload source on the heap while C reads it
}

var (
	glLoadOnce sync.Once
	glLoadErr  error
	glLibs     []uintptr
)

// getProc resolves a function by name: eglGetProcAddress first (every GL and
// extension function with EGL 1.5, extensions only before), then the GL
// libraries' exported symbols.
func getProc(name string) uintptr {
	n := copy(gls.name[:len(gls.name)-1], name)
	gls.name[n] = 0
	if p := fr.Call1(eglf.getProcAddress, uintptr(unsafe.Pointer(&gls.name[0]))); p != 0 {
		return p
	}
	for _, lib := range glLibs {
		if p, err := purego.Dlsym(lib, name); err == nil && p != 0 {
			return p
		}
	}
	return 0
}

func loadGLFuncs() error {
	glLoadOnce.Do(func() {
		for _, n := range []string{"libOpenGL.so.0", "libGL.so.1", "libGLESv2.so.2"} {
			if h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL); err == nil {
				glLibs = append(glLibs, h)
			}
		}
		for _, e := range []struct {
			name string
			dst  *uintptr
		}{
			{"glGetString", &glx.getString}, {"glGetError", &glx.getError}, {"glEnable", &glx.enable},
			{"glDisable", &glx.disable}, {"glBlendFuncSeparate", &glx.blendFuncSeparate}, {"glColorMask", &glx.colorMask},
			{"glFlush", &glx.flush}, {"glFinish", &glx.finish}, {"glGetIntegerv", &glx.getIntegerv},
			{"glCreateShader", &glx.createShader}, {"glShaderSource", &glx.shaderSource},
			{"glCompileShader", &glx.compileShader}, {"glGetShaderiv", &glx.getShaderiv},
			{"glGetShaderInfoLog", &glx.getShaderInfoLog}, {"glCreateProgram", &glx.createProgram},
			{"glAttachShader", &glx.attachShader}, {"glLinkProgram", &glx.linkProgram},
			{"glGetProgramiv", &glx.getProgramiv}, {"glGetProgramInfoLog", &glx.getProgramInfoLog},
			{"glDeleteShader", &glx.deleteShader}, {"glDeleteProgram", &glx.deleteProgram},
			{"glUseProgram", &glx.useProgram}, {"glGetUniformLocation", &glx.getUniformLocation},
			{"glUniform1i", &glx.uniform1i}, {"glUniform2f", &glx.uniform2f},
			{"glGenVertexArrays", &glx.genVertexArrays}, {"glBindVertexArray", &glx.bindVertexArray},
			{"glDeleteVertexArrays", &glx.deleteVertexArrays},
			{"glGenBuffers", &glx.genBuffers}, {"glBindBuffer", &glx.bindBuffer}, {"glBufferData", &glx.bufferData},
			{"glBufferSubData", &glx.bufferSubData}, {"glMapBufferRange", &glx.mapBufferRange},
			{"glUnmapBuffer", &glx.unmapBuffer}, {"glDeleteBuffers", &glx.deleteBuffers},
			{"glEnableVertexAttribArray", &glx.enableVertexAttribArray}, {"glVertexAttribPointer", &glx.vertexAttribPointer},
			{"glVertexAttribIPointer", &glx.vertexAttribIPointer}, {"glVertexAttribDivisor", &glx.vertexAttribDivisor},
			{"glDrawArraysInstanced", &glx.drawArraysInstanced},
			{"glGenTextures", &glx.genTextures}, {"glBindTexture", &glx.bindTexture}, {"glActiveTexture", &glx.activeTexture},
			{"glTexParameteri", &glx.texParameteri}, {"glTexImage2D", &glx.texImage2D},
			{"glTexSubImage2D", &glx.texSubImage2D}, {"glPixelStorei", &glx.pixelStorei},
			{"glDeleteTextures", &glx.deleteTextures},
			{"glGenFramebuffers", &glx.genFramebuffers}, {"glBindFramebuffer", &glx.bindFramebuffer},
			{"glDeleteFramebuffers", &glx.deleteFramebuffers},
			{"glGenRenderbuffers", &glx.genRenderbuffers}, {"glBindRenderbuffer", &glx.bindRenderbuffer},
			{"glRenderbufferStorage", &glx.renderbufferStorage}, {"glFramebufferRenderbuffer", &glx.framebufferRenderbuffer},
			{"glCheckFramebufferStatus", &glx.checkFramebufferStatus}, {"glDeleteRenderbuffers", &glx.deleteRenderbuffers},
			{"glReadPixels", &glx.readPixels},
			{"glFenceSync", &glx.fenceSync}, {"glClientWaitSync", &glx.clientWaitSync}, {"glDeleteSync", &glx.deleteSync},
		} {
			if *e.dst = getProc(e.name); *e.dst == 0 && glLoadErr == nil {
				glLoadErr = fmt.Errorf("gpu: cannot resolve %s", e.name)
			}
		}
		// glViewport/glClear/glClearColor come from Surface.loadGL when a
		// window exists; resolve them here too for offscreen use.
		for _, e := range []struct {
			name string
			dst  *uintptr
		}{{"glViewport", &glf.viewport}, {"glClearColor", &glf.clearColor}, {"glClear", &glf.clear}} {
			if *e.dst == 0 {
				if *e.dst = getProc(e.name); *e.dst == 0 && glLoadErr == nil {
					glLoadErr = fmt.Errorf("gpu: cannot resolve %s", e.name)
				}
			}
		}
	})
	return glLoadErr
}

// glString reads a NUL-terminated string returned by C.
func glString(p uintptr) string {
	if p == 0 {
		return ""
	}
	n := 0
	for *(*byte)(unsafe.Add(ffi.PtrFromC(p), n)) != 0 {
		n++
	}
	return string(unsafe.Slice((*byte)(ffi.PtrFromC(p)), n))
}

type ringSlot struct {
	buf    uint32
	quads  int     // capacity in quads
	sync   uintptr // GLsync fenced after the slot's last frame; 0 = none pending
	serial uint64  // the frame the sync follows
}

// retiredTex is a freed texture waiting for the frames that may sample it.
type retiredTex struct {
	name   uint32
	serial uint64 // the last frame encoded when it was freed
}

// Attribute layout of a Quad record (offsets checked against the struct).
var attribs = [...]struct {
	loc     uint32
	size    uint32
	typ     uint32
	off     uintptr
	integer bool
}{
	{0, 4, glFloat, 0, false},        // Rect
	{1, 4, glFloat, 16, false},       // Clip
	{2, 4, glFloat, 32, false},       // UV
	{3, 4, glUnsignedInt, 48, true},  // Color[4]
	{4, 4, glFloat, 64, false},       // Radii
	{5, 1, glFloat, 80, false},       // Border
	{6, 1, glUnsignedInt, 84, true},  // BorderColor
	{7, 1, glFloat, 88, false},       // Soft
	{8, 2, glFloat, 92, false},       // FadeX
	{9, 1, glUnsignedInt, 100, true}, // Mode
}

var (
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Clip)-16]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.UV)-32]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Color)-48]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Radii)-64]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Border)-80]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.BorderColor)-84]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Soft)-88]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.FadeX)-92]
	_ = [1]struct{}{}[unsafe.Offsetof(Quad{}.Mode)-100]
)

// Renderer draws DrawLists with OpenGL. All methods share the package's ffi
// frame and the thread's current context, so call them from the one locked
// thread that owns the context (see above).
type Renderer struct {
	dpy, ctx     uintptr
	ownsCtx      bool    // NewOffscreenRenderer: created here, destroyed by Destroy
	pbuf         uintptr // pbuffer surface made current when the display has no surfaceless support
	es           bool
	prog, vao    uint32
	locView      int32
	slots        [ringSlots]ringSlot
	next         int    // ring slot used by the next Encode
	serial       uint64 // frames encoded so far: the serial of the last one
	done         uint64 // every frame up to this serial has finished on the GPU
	ids          texTable
	tex          []uint32 // by index
	kinds        []TextureKind
	empty        uint32 // 1×1 transparent texture bound for a freed id
	retiring     []retiredTex
	draws        int
	totalDraw    int
	fbo, rbo     uint32
	offW         int
	offH         int
	readback     []byte
	stage        []Quad
	destroyed    bool
	setupGLError uint32 // first GL error raised while building the renderer (0 = none)
}

// NewRenderer builds the pipeline and instance rings in the context that is
// current on the calling thread. device and queue are the EGLDisplay and
// EGLContext (Surface.Device and Surface.Queue); the caller keeps owning
// them. Setup allocates.
func NewRenderer(device, queue uintptr) (*Renderer, error) {
	if err := loadEGL(); err != nil {
		return nil, err
	}
	if device == 0 || queue == 0 {
		return nil, ErrNoDevice
	}
	if fr.Call0(eglf.getCurrentContext) != queue {
		return nil, errors.New("gpu: NewRenderer: the context is not current on this thread (call it from the render thread)")
	}
	r := &Renderer{dpy: device, ctx: queue}
	if err := r.init(); err != nil {
		r.Destroy()
		return nil, err
	}
	return r, nil
}

func (r *Renderer) init() error {
	if err := loadGLFuncs(); err != nil {
		return err
	}
	ver := glString(fr.Call1(glx.getString, glVersionEnum))
	r.es = strings.HasPrefix(ver, "OpenGL ES")
	if !r.es {
		var maj, min int
		if _, err := fmt.Sscanf(ver, "%d.%d", &maj, &min); err != nil || maj < 3 || (maj == 3 && min < 3) {
			return fmt.Errorf("gpu: OpenGL 3.3 required, have %q", ver)
		}
	}
	for fr.Call0(glx.getError) != glNoError { // drain errors of earlier users
	}

	header := glslHeader330
	if r.es {
		header = glslHeaderES300
	}
	vs, err := compileShader(glVertexShader, header+quadVS)
	if err != nil {
		return fmt.Errorf("gpu: vertex shader: %w", err)
	}
	defer fr.Call1(glx.deleteShader, uintptr(vs))
	fs, err := compileShader(glFragmentShader, header+quadFS)
	if err != nil {
		return fmt.Errorf("gpu: fragment shader: %w", err)
	}
	defer fr.Call1(glx.deleteShader, uintptr(fs))
	r.prog = uint32(fr.Call0(glx.createProgram))
	fr.Call2(glx.attachShader, uintptr(r.prog), uintptr(vs))
	fr.Call2(glx.attachShader, uintptr(r.prog), uintptr(fs))
	fr.Call1(glx.linkProgram, uintptr(r.prog))
	gls.i32[0] = 0
	fr.Call3(glx.getProgramiv, uintptr(r.prog), glLinkStatus, uintptr(unsafe.Pointer(&gls.i32[0])))
	if gls.i32[0] == 0 {
		return fmt.Errorf("gpu: linking shaders: %s", infoLog(r.prog, true))
	}
	fr.Call1(glx.useProgram, uintptr(r.prog))
	r.locView = r.uniform("uViewport")
	if loc := r.uniform("uTex"); loc >= 0 {
		fr.Call2(glx.uniform1i, uintptr(loc), 0)
	}

	fr.Call2(glx.genVertexArrays, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
	r.vao = gls.u32[0]
	fr.Call1(glx.bindVertexArray, uintptr(r.vao))
	for i := range attribs {
		fr.Call1(glx.enableVertexAttribArray, uintptr(attribs[i].loc))
		fr.Call2(glx.vertexAttribDivisor, uintptr(attribs[i].loc), 1)
	}

	for i := range r.slots {
		s := &r.slots[i]
		fr.Call2(glx.genBuffers, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
		s.buf, s.quads = gls.u32[0], initialQuads
		fr.Call2(glx.bindBuffer, glArrayBuffer, uintptr(s.buf))
		fr.Call4(glx.bufferData, glArrayBuffer, initialQuads*quadSize, 0, glStreamDraw)
	}
	// Texture 0: 1×1 white RGBA texel that shape quads bind.
	t0 := r.NewTexture(TexRGBA8, 1, 1)
	r.Upload(t0, 0, 0, 1, 1, []byte{255, 255, 255, 255}, 4)
	// The texture a quad naming a freed texture samples: one transparent texel.
	r.empty = r.newGLTexture(TexRGBA8, 1, 1)
	gls.pinned = &emptyTexel[0]
	fr.Call2(glx.pixelStorei, glUnpackAlignment, 1)
	r.subImage(0, 0, 1, 1, glRGBA, unsafe.Pointer(&emptyTexel[0]))
	gls.pinned = nil
	// A GL error during setup is not fatal by itself (the shaders linked and
	// the buffers exist); TestNoGLErrors reports them. Leave none pending.
	r.setupGLError = r.glErr()
	return nil
}

func (r *Renderer) uniform(name string) int32 {
	n := copy(gls.name[:len(gls.name)-1], name)
	gls.name[n] = 0
	return int32(fr.Call2(glx.getUniformLocation, uintptr(r.prog), uintptr(unsafe.Pointer(&gls.name[0]))))
}

func compileShader(kind uintptr, src string) (uint32, error) {
	sh := uint32(fr.Call1(glx.createShader, kind))
	if sh == 0 {
		return 0, errors.New("glCreateShader failed")
	}
	buf := append([]byte(src), 0)
	gls.src[0] = &buf[0]
	fr.Call4(glx.shaderSource, uintptr(sh), 1, uintptr(unsafe.Pointer(&gls.src[0])), 0)
	fr.Call1(glx.compileShader, uintptr(sh))
	gls.i32[0] = 0
	fr.Call3(glx.getShaderiv, uintptr(sh), glCompileStatus, uintptr(unsafe.Pointer(&gls.i32[0])))
	if gls.i32[0] == 0 {
		msg := infoLog(sh, false)
		fr.Call1(glx.deleteShader, uintptr(sh))
		return 0, errors.New(msg)
	}
	gls.src[0] = nil
	return sh, nil
}

// infoLogBuf is the buffer glGet*InfoLog writes into (heap, kept reachable).
var infoLogBuf []byte

func infoLog(obj uint32, program bool) string {
	get, getLog := glx.getShaderiv, glx.getShaderInfoLog
	if program {
		get, getLog = glx.getProgramiv, glx.getProgramInfoLog
	}
	gls.i32[0] = 0
	fr.Call3(get, uintptr(obj), glInfoLogLength, uintptr(unsafe.Pointer(&gls.i32[0])))
	n := int(gls.i32[0])
	if n <= 1 {
		return "no info log"
	}
	infoLogBuf = make([]byte, n)
	gls.i32[1] = 0
	fr.Call4(getLog, uintptr(obj), uintptr(n), uintptr(unsafe.Pointer(&gls.i32[1])), uintptr(unsafe.Pointer(&infoLogBuf[0])))
	m := min(int(gls.i32[1]), n)
	return strings.TrimSpace(string(infoLogBuf[:m]))
}

// glErr returns the first pending GL error (0 if none) and clears the rest.
func (r *Renderer) glErr() uint32 {
	first := uint32(fr.Call0(glx.getError))
	for e := first; e != glNoError; e = uint32(fr.Call0(glx.getError)) {
	}
	return first
}

// emptyTexel is the content of Renderer.empty (a global: C reads it).
var emptyTexel [4]byte

// NewTexture creates a w×h texture. Contents are undefined until Upload. It
// reuses the index of a freed texture under a new generation (see
// FreeTexture); the table grows only past the most textures ever live at
// once.
func (r *Renderer) NewTexture(kind TextureKind, w, h int) TextureID {
	name := r.newGLTexture(kind, w, h)
	i, id := r.ids.add()
	if i == len(r.tex) {
		r.tex = append(r.tex, 0)
		r.kinds = append(r.kinds, 0)
	}
	r.tex[i], r.kinds[i] = name, kind
	return id
}

// FreeTexture frees texture t. Its id stops resolving at once: Upload and
// FreeTexture ignore it, and quads that still name it draw nothing. The GL
// texture is deleted when the sync object of the last frame encoded so far
// has signalled (see textures.go); FreeTexture itself never waits. Freeing
// texture 0, an unknown id or an id already freed does nothing.
// An index is reused under 4096 generations and then retired for good, so a
// renderer can create about 4.29 billion textures (2^20 indices × 4096) in its
// life; C7's external textures should revisit the id's width if they churn.
func (r *Renderer) FreeTexture(t TextureID) {
	i, ok := r.ids.lookup(t)
	if !ok || i == 0 {
		return
	}
	r.retiring = append(r.retiring, retiredTex{r.tex[i], r.serial})
	r.tex[i] = 0
	r.ids.remove(i)
	r.collect()
}

// TextureCounts reports the live textures (texture 0 included) and the freed
// ones whose deletion still waits for the GPU.
func (r *Renderer) TextureCounts() (live, retiring int) { return r.ids.n, len(r.retiring) }

// syncDone reads a sync object without waiting (a zero timeout, no flush:
// the swap flushes). A failed wait counts as done.
func syncDone(s uintptr) bool {
	switch uint32(fr.Reset().Arg(s).Arg32(0).Arg(0).Call(glx.clientWaitSync).R0()) {
	case glAlreadySignaled, glConditionSatisfied, glWaitFailed:
		return true
	}
	return false
}

// collect advances done from the slots' sync objects that have signalled,
// without waiting, and deletes the freed textures no unfinished frame may
// sample. GL runs a context's commands in order, so a signalled sync
// completes every earlier frame too.
func (r *Renderer) collect() {
	for i := range r.slots {
		s := &r.slots[i]
		if s.sync != 0 && syncDone(s.sync) {
			r.done = max(r.done, s.serial)
			fr.Call1(glx.deleteSync, s.sync)
			s.sync = 0
		}
	}
	n := 0
	for _, x := range r.retiring {
		if x.serial <= r.done {
			gls.u32[0] = x.name
			fr.Call2(glx.deleteTextures, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
			continue
		}
		r.retiring[n] = x
		n++
	}
	r.retiring = r.retiring[:n]
}

// fence follows the frame just encoded in slot s with a sync object. A
// previous sync of the slot that has not signalled is given up: the new one,
// later in the same command stream, signals after it.
func (r *Renderer) fence(s *ringSlot) {
	if s.sync != 0 {
		fr.Call1(glx.deleteSync, s.sync)
	}
	s.sync = fr.Reset().Arg32(glSyncGPUCommands).Arg32(0).Call(glx.fenceSync).R0()
	s.serial = r.serial
}

func (r *Renderer) newGLTexture(kind TextureKind, w, h int) uint32 {
	internal, format := uintptr(glRGBA8), uintptr(glRGBA)
	if kind == TexR8 {
		internal, format = glR8, glRed
	}
	fr.Call2(glx.genTextures, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
	name := gls.u32[0]
	if name == 0 {
		panic("gpu: texture creation failed")
	}
	fr.Call1(glx.activeTexture, glTexture0)
	fr.Call2(glx.bindTexture, glTexture2D, uintptr(name))
	for _, p := range [...][2]uintptr{{glTextureMinFilter, glLinear}, {glTextureMagFilter, glLinear},
		{glTextureWrapS, glClampToEdge}, {glTextureWrapT, glClampToEdge}} {
		fr.Call3(glx.texParameteri, glTexture2D, p[0], p[1])
	}
	fr.Reset().Arg32(glTexture2D).Arg32(0).Arg32(uint32(internal)).Arg(uintptr(w)).Arg(uintptr(h)).Arg32(0).
		Arg32(uint32(format)).Arg32(glUnsignedByte).Arg(0).Call(glx.texImage2D)
	return name
}

// Upload copies a w×h block of pixels into texture t at (x, y). pixels[0] is
// the block's first texel and stride is the byte distance between rows. An
// id that was freed is ignored. glTexSubImage2D is ordered with the draws, so
// frames already submitted read the old contents.
func (r *Renderer) Upload(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	if w <= 0 || h <= 0 {
		return
	}
	i, ok := r.ids.lookup(t)
	if !ok {
		return
	}
	bpp, format := 1, uintptr(glRed)
	if r.kinds[i] == TexRGBA8 {
		bpp, format = 4, glRGBA
	}
	if stride < w*bpp || len(pixels) < (h-1)*stride+w*bpp {
		panic("gpu: Upload: pixels too short for region")
	}
	gls.pinned = &pixels[0] // force the backing array onto the heap while C reads it
	fr.Call1(glx.activeTexture, glTexture0)
	fr.Call2(glx.bindTexture, glTexture2D, uintptr(r.tex[i]))
	fr.Call2(glx.pixelStorei, glUnpackAlignment, 1)
	if stride%bpp == 0 {
		fr.Call2(glx.pixelStorei, glUnpackRowLength, uintptr(stride/bpp))
		r.subImage(x, y, w, h, format, unsafe.Pointer(&pixels[0]))
		fr.Call2(glx.pixelStorei, glUnpackRowLength, 0)
	} else {
		for row := 0; row < h; row++ {
			r.subImage(x, y+row, w, 1, format, unsafe.Pointer(&pixels[row*stride]))
		}
	}
	gls.pinned = nil
}

func (r *Renderer) subImage(x, y, w, h int, format uintptr, p unsafe.Pointer) {
	fr.Reset().Arg32(glTexture2D).Arg32(0).Arg(uintptr(x)).Arg(uintptr(y)).Arg(uintptr(w)).Arg(uintptr(h)).
		Arg32(uint32(format)).Arg32(glUnsignedByte).ArgPtr(p).Call(glx.texSubImage2D)
}

// uploadInstances copies quads into slot s: orphan the old storage, map the
// new, copy, unmap.
func (r *Renderer) uploadInstances(s *ringSlot, quads []Quad) {
	for s.quads < len(quads) {
		s.quads *= 2
	}
	fr.Call2(glx.bindBuffer, glArrayBuffer, uintptr(s.buf))
	fr.Call4(glx.bufferData, glArrayBuffer, uintptr(s.quads*quadSize), 0, glStreamDraw)
	if len(quads) == 0 {
		return
	}
	n := len(quads) * quadSize
	p := fr.Reset().Arg32(glArrayBuffer).Arg(0).Arg(uintptr(n)).Arg32(glMapWriteBit | glMapInvalidateBuffer).
		Call(glx.mapBufferRange).R0()
	if p != 0 {
		copy(unsafe.Slice((*Quad)(ffi.PtrFromC(p)), len(quads)), quads)
		fr.Call1(glx.unmapBuffer, glArrayBuffer)
		return
	}
	// Mapping failed (should not happen): upload from a heap staging copy.
	r.stage = append(r.stage[:0], quads...)
	fr.Reset().Arg32(glArrayBuffer).Arg(0).Arg(uintptr(n)).ArgPtr(unsafe.Pointer(&r.stage[0])).Call(glx.bufferSubData)
}

// Stage is Upload: glTexSubImage2D is already ordered with the frames in flight,
// which keep reading the old contents, and does not wait for them. (On Metal
// Stage defers the copy to the next Encode to get the same ordering.)
func (r *Renderer) Stage(t TextureID, x, y, w, h int, pixels []byte, stride int) {
	r.Upload(t, x, y, w, h, pixels, stride)
}

// Encode records one render pass: it clears target (a framebuffer object
// name; 0 is the default framebuffer, which Surface.BeginFrame provides) to
// clear, then draws list. cmdBuf is unused (OpenGL has no command buffers;
// pass Frame.CmdBuf, which is 0). Commands are issued immediately; the
// caller swaps (Surface.EndFrame) or reads back.
//
// clear is written to the target as is, so it should be premultiplied. Encode
// changes GL state (program, VAO, array buffer, texture 0, blending,
// viewport, framebuffer binding) and leaves the default framebuffer bound.
//
// Encode allocates nothing on the Go heap once the renderer and list are
// warm. It grows the ring slot only if the list is larger than any before.
func (r *Renderer) Encode(cmdBuf, target uintptr, width, height int, clear [4]float32, list *DrawList) {
	r.draws = 0
	if width <= 0 || height <= 0 {
		return
	}
	s := &r.slots[r.next]
	if r.next++; r.next == ringSlots {
		r.next = 0
	}
	r.serial++
	if len(r.retiring) > 0 {
		r.collect()
	}
	var quads []Quad
	if list != nil {
		quads = list.Quads
	}
	defer r.fence(s)

	fr.Call2(glx.bindFramebuffer, glFramebuffer, target)
	fr.Reset().Arg32(0).Arg32(0).Arg32(uint32(width)).Arg32(uint32(height)).Call(glf.viewport)
	fr.Call1(glx.disable, glScissorTest)
	fr.Call1(glx.disable, glDepthTest)
	fr.Call1(glx.disable, glCullFace)
	fr.Reset().Arg32(1).Arg32(1).Arg32(1).Arg32(1).Call(glx.colorMask)
	fr.Reset().ArgF32(clear[0]).ArgF32(clear[1]).ArgF32(clear[2]).ArgF32(clear[3]).Call(glf.clearColor)
	fr.Call1(glf.clear, glColorBufferBit)
	if len(quads) == 0 {
		fr.Call2(glx.bindFramebuffer, glFramebuffer, 0)
		return
	}

	r.uploadInstances(s, quads)
	fr.Call1(glx.useProgram, uintptr(r.prog))
	fr.Call1(glx.bindVertexArray, uintptr(r.vao))
	fr.Reset().Arg32(uint32(r.locView)).ArgF32(float32(width)).ArgF32(float32(height)).Call(glx.uniform2f)
	fr.Call1(glx.enable, glBlend)
	// Premultiplied alpha: src + dst × (1 − src.a), for colour and alpha.
	fr.Reset().Arg32(glOne).Arg32(glOneMinusSrcAlpha).Arg32(glOne).Arg32(glOneMinusSrcAlpha).Call(glx.blendFuncSeparate)
	fr.Call1(glx.activeTexture, glTexture0)

	// Shape quads don't read the bound texture, so they join whatever run is
	// open; only a texture quad with a different texture ends it.
	bound := TextureID(0)
	fr.Call2(glx.bindTexture, glTexture2D, uintptr(r.tex[0]))
	runStart := 0
	for i := range quads {
		q := &quads[i]
		if q.Mode == ModeShape || q.Tex == bound {
			continue
		}
		if i > runStart {
			r.draw(s, runStart, i-runStart)
		}
		runStart = i
		bound = q.Tex
		name := r.empty // a freed or unknown id draws nothing
		if k, ok := r.ids.lookup(bound); ok {
			name = r.tex[k]
		}
		fr.Call2(glx.bindTexture, glTexture2D, uintptr(name))
	}
	if len(quads) > runStart {
		r.draw(s, runStart, len(quads)-runStart)
	}
	fr.Call2(glx.bindFramebuffer, glFramebuffer, 0)
}

// draw issues one instanced triangle-strip draw of count quads starting at
// instance first: the attributes are pointed at record `first` of the bound
// instance buffer (no base-instance draw in OpenGL 3.3).
func (r *Renderer) draw(s *ringSlot, first, count int) {
	base := uintptr(first) * quadSize
	for i := range attribs {
		a := &attribs[i]
		fr.Reset().Arg32(a.loc).Arg32(a.size).Arg32(a.typ)
		if a.integer {
			fr.Arg32(quadSize).Arg(base + a.off).Call(glx.vertexAttribIPointer)
		} else {
			fr.Arg32(0).Arg32(quadSize).Arg(base + a.off).Call(glx.vertexAttribPointer)
		}
	}
	fr.Reset().Arg32(glTriangleStrip).Arg32(0).Arg32(4).Arg32(uint32(count)).Call(glx.drawArraysInstanced)
	r.draws++
	r.totalDraw++
}

// DrawCalls is the number of draw calls the last Encode issued.
func (r *Renderer) DrawCalls() int { return r.draws }

// RenderToRGBA renders list into an offscreen RGBA8 target of the given
// size and writes the result to out as RGBA8 (premultiplied, row-major, top
// row first; len(out) must be at least width*height*4). It reads back with
// glReadPixels, which returns the bottom row first, and flips. For tests and
// PNG output.
func (r *Renderer) RenderToRGBA(width, height int, clear [4]float32, list *DrawList, out []byte) {
	if width <= 0 || height <= 0 {
		return
	}
	n := width * height * 4
	if len(out) < n {
		panic("gpu: RenderToRGBA: out too short")
	}
	if r.fbo == 0 || r.offW != width || r.offH != height {
		r.newTarget(width, height)
	}
	r.Encode(0, uintptr(r.fbo), width, height, clear, list)
	if cap(r.readback) < n {
		r.readback = make([]byte, n)
	}
	r.readback = r.readback[:n]
	fr.Call2(glx.bindFramebuffer, glFramebuffer, uintptr(r.fbo))
	fr.Call2(glx.pixelStorei, glPackAlignment, 1)
	fr.Reset().Arg32(0).Arg32(0).Arg(uintptr(width)).Arg(uintptr(height)).Arg32(glRGBA).Arg32(glUnsignedByte).
		ArgPtr(unsafe.Pointer(&r.readback[0])).Call(glx.readPixels)
	fr.Call2(glx.bindFramebuffer, glFramebuffer, 0)
	row := width * 4
	for y := 0; y < height; y++ {
		copy(out[y*row:(y+1)*row], r.readback[(height-1-y)*row:(height-y)*row])
	}
}

// newTarget (re)creates the offscreen RGBA8 render target.
func (r *Renderer) newTarget(w, h int) {
	if r.fbo == 0 {
		fr.Call2(glx.genFramebuffers, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
		r.fbo = gls.u32[0]
		fr.Call2(glx.genRenderbuffers, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
		r.rbo = gls.u32[0]
	}
	fr.Call2(glx.bindRenderbuffer, glRenderbuffer, uintptr(r.rbo))
	fr.Call4(glx.renderbufferStorage, glRenderbuffer, glRGBA8, uintptr(w), uintptr(h))
	fr.Call2(glx.bindFramebuffer, glFramebuffer, uintptr(r.fbo))
	fr.Call4(glx.framebufferRenderbuffer, glFramebuffer, glColorAttachment0, glRenderbuffer, uintptr(r.rbo))
	st := uint32(fr.Call1(glx.checkFramebufferStatus, glFramebuffer))
	fr.Call2(glx.bindFramebuffer, glFramebuffer, 0)
	r.offW, r.offH = w, h
	if st != glFramebufferComplete {
		panic(fmt.Sprintf("gpu: offscreen framebuffer incomplete (0x%x)", st))
	}
}

// drawFramebuffer returns the name bound as GL_DRAW_FRAMEBUFFER, and false if
// the query did not answer (the result slot is poisoned first, so a failed
// call cannot pass for a stale value).
func (r *Renderer) drawFramebuffer() (uint32, bool) {
	const poison = 0x7fffffff
	gls.i32[0] = poison
	fr.Reset().Arg32(0x8CA6).ArgPtr(unsafe.Pointer(&gls.i32[0])).Call(glx.getIntegerv) // GL_DRAW_FRAMEBUFFER_BINDING
	return uint32(gls.i32[0]), gls.i32[0] != poison
}

// flush submits queued GL commands (tests that encode without reading back).
func (r *Renderer) flush() { fr.Call0(glx.flush) }

// Destroy releases everything the renderer created. A display and context
// passed to NewRenderer are not destroyed. It must run on the thread whose
// context is current; if that context is not current the GL objects are left
// to be freed with the context.
func (r *Renderer) Destroy() {
	if r.destroyed {
		return
	}
	r.destroyed = true
	if fr.Call0(eglf.getCurrentContext) == r.ctx && r.ctx != 0 {
		del := func(fn uintptr, names ...uint32) {
			for _, n := range names {
				if n != 0 {
					gls.u32[0] = n
					fr.Call2(fn, 1, uintptr(unsafe.Pointer(&gls.u32[0])))
				}
			}
		}
		for i := range r.slots {
			del(glx.deleteBuffers, r.slots[i].buf)
			if s := r.slots[i].sync; s != 0 {
				fr.Call1(glx.deleteSync, s)
			}
		}
		del(glx.deleteTextures, r.tex...)
		for _, x := range r.retiring {
			del(glx.deleteTextures, x.name)
		}
		del(glx.deleteTextures, r.empty)
		del(glx.deleteFramebuffers, r.fbo)
		del(glx.deleteRenderbuffers, r.rbo)
		del(glx.deleteVertexArrays, r.vao)
		if r.prog != 0 {
			fr.Call1(glx.deleteProgram, uintptr(r.prog))
		}
	}
	r.tex, r.kinds, r.retiring, r.ids = nil, nil, nil, texTable{} // ids freed after Destroy resolve to nothing
	if r.ownsCtx {
		r.releaseOffscreen()
	}
}
