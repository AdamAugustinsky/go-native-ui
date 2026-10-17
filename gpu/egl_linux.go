//go:build linux

package gpu

import (
	"errors"
	"fmt"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/platform"
)

// OpenGL 3.3 core (or GLES 3.0) through EGL, on a wl_egl_window (Wayland) or
// an X11 Window. Everything is bound with dlopen; calls go through
// internal/ffi so Present does not allocate.

// EGL constants (Khronos egl.h / eglext.h).
const (
	eglNone                  = 0x3038
	eglSurfaceType           = 0x3033
	eglWindowBit             = 0x0004
	eglRenderableType        = 0x3040
	eglOpenGLBit             = 0x0008
	eglOpenGLES3Bit          = 0x0040
	eglRedSize               = 0x3024
	eglGreenSize             = 0x3023
	eglBlueSize              = 0x3022
	eglAlphaSize             = 0x3021
	eglOpenGLAPI             = 0x30A2
	eglOpenGLESAPI           = 0x30A0
	eglContextMajorVersion   = 0x3098 // == EGL_CONTEXT_CLIENT_VERSION
	eglContextMinorVersion   = 0x30FB
	eglContextProfileMask    = 0x30FD
	eglContextCoreProfileBit = 0x0001
	eglPlatformWaylandKHR    = 0x31D8
	eglPlatformX11KHR        = 0x31D5
	eglNativeVisualID        = 0x302E

	glColorBufferBit = 0x4000
	maxConfigs       = 64
)

// Attribute lists live in package variables: ffi frames hold their addresses.
var (
	cfgGL   = [...]int32{eglSurfaceType, eglWindowBit, eglRenderableType, eglOpenGLBit, eglRedSize, 8, eglGreenSize, 8, eglBlueSize, 8, eglAlphaSize, 8, eglNone}
	cfgGLES = [...]int32{eglSurfaceType, eglWindowBit, eglRenderableType, eglOpenGLES3Bit, eglRedSize, 8, eglGreenSize, 8, eglBlueSize, 8, eglAlphaSize, 8, eglNone}
	// X11 windows use the default (usually 24-bit, alpha-less) visual, so we
	// don't insist on destination alpha and match the visual id instead.
	cfgGLX11   = [...]int32{eglSurfaceType, eglWindowBit, eglRenderableType, eglOpenGLBit, eglRedSize, 8, eglGreenSize, 8, eglBlueSize, 8, eglNone}
	cfgGLESX11 = [...]int32{eglSurfaceType, eglWindowBit, eglRenderableType, eglOpenGLES3Bit, eglRedSize, 8, eglGreenSize, 8, eglBlueSize, 8, eglNone}
	ctxGL      = [...]int32{eglContextMajorVersion, 3, eglContextMinorVersion, 3, eglContextProfileMask, eglContextCoreProfileBit, eglNone}
	ctxGLES    = [...]int32{eglContextMajorVersion, 3, eglNone}
)

var fr = new(ffi.Frame)

var eglf struct {
	getProcAddress, getPlatformDisplay, getPlatformDisplayEXT, getDisplay uintptr
	initialize, terminate, bindAPI, chooseConfig, getConfigAttrib         uintptr
	createContext, destroyContext, createWindowSurface, destroySurface    uintptr
	makeCurrent, swapInterval, swapBuffers, getError                      uintptr
}

var wlegl struct{ create, resize, destroy uintptr }

var glf struct{ viewport, clearColor, clear uintptr }

var xlib struct{ defaultScreen, defaultVisual, visualID uintptr }

var eglLoaded, wlEglLoaded, x11Loaded bool

func loadEGL() error {
	if eglLoaded {
		return nil
	}
	h, err := dlopen("libEGL.so.1", "libEGL.so")
	if err != nil {
		return fmt.Errorf("egl: %w", err)
	}
	for _, s := range []struct {
		name     string
		dst      *uintptr
		optional bool
	}{
		{"eglGetProcAddress", &eglf.getProcAddress, false},
		{"eglGetPlatformDisplay", &eglf.getPlatformDisplay, true}, // EGL 1.5
		{"eglGetPlatformDisplayEXT", &eglf.getPlatformDisplayEXT, true},
		{"eglGetDisplay", &eglf.getDisplay, false},
		{"eglInitialize", &eglf.initialize, false},
		{"eglTerminate", &eglf.terminate, false},
		{"eglBindAPI", &eglf.bindAPI, false},
		{"eglChooseConfig", &eglf.chooseConfig, false},
		{"eglGetConfigAttrib", &eglf.getConfigAttrib, false},
		{"eglCreateContext", &eglf.createContext, false},
		{"eglDestroyContext", &eglf.destroyContext, false},
		{"eglCreateWindowSurface", &eglf.createWindowSurface, false},
		{"eglDestroySurface", &eglf.destroySurface, false},
		{"eglMakeCurrent", &eglf.makeCurrent, false},
		{"eglSwapInterval", &eglf.swapInterval, false},
		{"eglSwapBuffers", &eglf.swapBuffers, false},
		{"eglGetError", &eglf.getError, false},
	} {
		p, err := purego.Dlsym(h, s.name)
		if (err != nil || p == 0) && !s.optional {
			return fmt.Errorf("libEGL: missing symbol %s", s.name)
		}
		*s.dst = p
	}
	eglLoaded = true
	return nil
}

func loadWaylandEGL() error {
	if wlEglLoaded {
		return nil
	}
	h, err := dlopen("libwayland-egl.so.1", "libwayland-egl.so")
	if err != nil {
		return fmt.Errorf("wayland-egl: %w", err)
	}
	for name, dst := range map[string]*uintptr{
		"wl_egl_window_create":  &wlegl.create,
		"wl_egl_window_resize":  &wlegl.resize,
		"wl_egl_window_destroy": &wlegl.destroy,
	} {
		p, err := purego.Dlsym(h, name)
		if err != nil || p == 0 {
			return fmt.Errorf("libwayland-egl: missing symbol %s", name)
		}
		*dst = p
	}
	wlEglLoaded = true
	return nil
}

func loadX11Visual() {
	if x11Loaded {
		return
	}
	h, err := dlopen("libX11.so.6", "libX11.so")
	if err != nil {
		return
	}
	for name, dst := range map[string]*uintptr{
		"XDefaultScreen":      &xlib.defaultScreen,
		"XDefaultVisual":      &xlib.defaultVisual,
		"XVisualIDFromVisual": &xlib.visualID,
	} {
		p, err := purego.Dlsym(h, name)
		if err != nil || p == 0 {
			xlib.defaultScreen = 0
			return
		}
		*dst = p
	}
	x11Loaded = true
}

func dlopen(names ...string) (uintptr, error) {
	var last error
	for _, n := range names {
		h, err := purego.Dlopen(n, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err == nil {
			return h, nil
		}
		last = err
	}
	return 0, last
}

func cstr(s string) []byte { return append([]byte(s), 0) }

// Surface is an EGL window surface with a current GL context.
type Surface struct {
	native platform.Native
	w, h   int32

	dpy, cfg, ctx, surf, win uintptr
	gl                       bool // desktop GL 3.3 core; otherwise GLES 3
	major, minor             int32
	num                      int32
	attr                     int32
	cfgs                     [maxConfigs]uintptr
}

// NewSurface creates the EGL display, context and window surface for the
// window's native handles. w and h are physical pixels.
func NewSurface(n platform.Native, w, h int32, scale float32) (*Surface, error) {
	if err := loadEGL(); err != nil {
		return nil, err
	}
	s := &Surface{native: n, w: w, h: h}
	var err error
	defer func() {
		if err != nil {
			s.Destroy()
		}
	}()

	var nativeWindow, platformKind uintptr
	if n.Wayland {
		if err = loadWaylandEGL(); err != nil {
			return nil, err
		}
		s.win = fr.Call3(wlegl.create, n.Surface, uintptr(w), uintptr(h))
		if s.win == 0 {
			err = errors.New("wl_egl_window_create failed")
			return nil, err
		}
		nativeWindow, platformKind = s.win, eglPlatformWaylandKHR
	} else {
		loadX11Visual()
		nativeWindow, platformKind = n.X11Window, eglPlatformX11KHR
	}

	// EGL 1.5 eglGetPlatformDisplay, then EGL_EXT_platform_base, then the
	// legacy call (which guesses the platform from the pointer).
	if eglf.getPlatformDisplay != 0 {
		s.dpy = fr.Call3(eglf.getPlatformDisplay, platformKind, n.Display, 0)
	}
	if s.dpy == 0 && eglf.getPlatformDisplayEXT != 0 {
		s.dpy = fr.Call3(eglf.getPlatformDisplayEXT, platformKind, n.Display, 0)
	}
	if s.dpy == 0 {
		s.dpy = fr.Call1(eglf.getDisplay, n.Display)
	}
	if s.dpy == 0 {
		err = errors.New("egl: no display for the window system connection")
		return nil, err
	}
	if !ok(fr.Call3(eglf.initialize, s.dpy, uintptr(unsafe.Pointer(&s.major)), uintptr(unsafe.Pointer(&s.minor)))) {
		err = s.eglErr("eglInitialize")
		s.dpy = 0
		return nil, err
	}

	errGL := s.makeContext(true)
	if errGL != nil {
		if errES := s.makeContext(false); errES != nil {
			err = fmt.Errorf("no usable EGL context: OpenGL 3.3 core: %v; OpenGL ES 3: %v", errGL, errES)
			return nil, err
		}
	}

	s.surf = fr.Call4(eglf.createWindowSurface, s.dpy, s.cfg, nativeWindow, 0)
	if s.surf == 0 {
		err = s.eglErr("eglCreateWindowSurface")
		return nil, err
	}
	if !ok(fr.Call4(eglf.makeCurrent, s.dpy, s.surf, s.surf, s.ctx)) {
		err = s.eglErr("eglMakeCurrent")
		return nil, err
	}
	// Wayland: frame callbacks pace us and a hidden surface must never block
	// in the swap. X11: the blocking swap is the vsync throttle.
	interval := uintptr(1)
	if n.Wayland {
		interval = 0
	}
	fr.Call2(eglf.swapInterval, s.dpy, interval)

	if err = s.loadGL(); err != nil {
		return nil, err
	}
	return s, nil
}

func ok(r uintptr) bool { return uint32(r) != 0 } // EGLBoolean is unsigned int

func (s *Surface) eglErr(what string) error {
	return fmt.Errorf("egl: %s failed (EGL error 0x%x)", what, uint32(fr.Call0(eglf.getError)))
}

// makeContext binds the API, picks a config and creates a context.
func (s *Surface) makeContext(gl bool) error {
	api, cfg, ctx := uintptr(eglOpenGLESAPI), &cfgGLES[0], &ctxGLES[0]
	switch {
	case gl && s.native.Wayland:
		api, cfg, ctx = eglOpenGLAPI, &cfgGL[0], &ctxGL[0]
	case gl:
		api, cfg, ctx = eglOpenGLAPI, &cfgGLX11[0], &ctxGL[0]
	case !s.native.Wayland:
		cfg = &cfgGLESX11[0]
	}
	if !ok(fr.Call1(eglf.bindAPI, api)) {
		return s.eglErr("eglBindAPI")
	}
	s.num = 0
	if !ok(fr.Call5(eglf.chooseConfig, s.dpy, uintptr(unsafe.Pointer(cfg)), uintptr(unsafe.Pointer(&s.cfgs[0])), maxConfigs, uintptr(unsafe.Pointer(&s.num)))) || s.num == 0 {
		return errors.New("no matching EGL config")
	}
	s.cfg = s.cfgs[0]
	if !s.native.Wayland && x11Loaded {
		// The X window uses the default visual; pick a config for it.
		vis := fr.Call2(xlib.defaultVisual, s.native.Display, fr.Call1(xlib.defaultScreen, s.native.Display))
		id := fr.Call1(xlib.visualID, vis)
		found := false
		for i := int32(0); i < s.num && i < maxConfigs; i++ {
			if ok(fr.Call4(eglf.getConfigAttrib, s.dpy, s.cfgs[i], eglNativeVisualID, uintptr(unsafe.Pointer(&s.attr)))) && uintptr(s.attr) == id {
				s.cfg, found = s.cfgs[i], true
				break
			}
		}
		if !found {
			// eglCreateWindowSurface would fail with EGL_BAD_MATCH; let the
			// caller try the next API instead.
			return errors.New("no EGL config matches the X11 default visual")
		}
	}
	s.ctx = fr.Call4(eglf.createContext, s.dpy, s.cfg, 0, uintptr(unsafe.Pointer(ctx)))
	if s.ctx == 0 {
		return s.eglErr("eglCreateContext")
	}
	s.gl = gl
	return nil
}

// loadGL resolves the three GL entry points phase 1 needs: eglGetProcAddress
// first, then the GL/GLES library for core symbols.
func (s *Surface) loadGL() error {
	var lib uintptr
	if s.gl {
		lib, _ = dlopen("libGL.so.1", "libOpenGL.so.0")
	} else {
		lib, _ = dlopen("libGLESv2.so.2")
	}
	for _, g := range []struct {
		name string
		dst  *uintptr
	}{{"glViewport", &glf.viewport}, {"glClearColor", &glf.clearColor}, {"glClear", &glf.clear}} {
		name := cstr(g.name)
		p := fr.Call1(eglf.getProcAddress, uintptr(unsafe.Pointer(&name[0])))
		if p == 0 && lib != 0 {
			p, _ = purego.Dlsym(lib, g.name)
		}
		if p == 0 {
			return fmt.Errorf("gl: cannot resolve %s", g.name)
		}
		*g.dst = p
	}
	return nil
}

// Resize updates the buffer size (physical pixels).
func (s *Surface) Resize(w, h int32, scale float32) {
	s.w, s.h = w, h
	if s.win != 0 { // wl_egl_window_resize(win, w, h, dx, dy)
		fr.Reset().Arg(s.win).Arg32(uint32(w)).Arg32(uint32(h)).Arg32(0).Arg32(0).Call(wlegl.resize)
	}
}

// Present clears to the colour and swaps. On Wayland the swap commits the
// surface, which also carries the frame callback requested before it.
func (s *Surface) Present(r, g, b, a float32) bool {
	fr.Reset().Arg32(0).Arg32(0).Arg32(uint32(s.w)).Arg32(uint32(s.h)).Call(glf.viewport)
	fr.Reset().ArgF32(r).ArgF32(g).ArgF32(b).ArgF32(a).Call(glf.clearColor)
	fr.Call1(glf.clear, glColorBufferBit)
	return ok(fr.Call2(eglf.swapBuffers, s.dpy, s.surf))
}

// FrameWaitable is 0 on Linux.
func (s *Surface) FrameWaitable() uintptr { return 0 }

// Destroy releases the EGL objects. Call it before platform.Window.Close.
func (s *Surface) Destroy() {
	if s.dpy != 0 {
		fr.Call4(eglf.makeCurrent, s.dpy, 0, 0, 0)
		if s.surf != 0 {
			fr.Call2(eglf.destroySurface, s.dpy, s.surf)
		}
		if s.ctx != 0 {
			fr.Call2(eglf.destroyContext, s.dpy, s.ctx)
		}
		fr.Call1(eglf.terminate, s.dpy)
	}
	if s.win != 0 {
		fr.Call1(wlegl.destroy, s.win)
	}
	*s = Surface{}
}
