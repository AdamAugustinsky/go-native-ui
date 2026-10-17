//go:build linux

package platform

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/ffi"
	"gonativeui/internal/wl"
)

// Wayland backend: libwayland-client through dlopen, every proxy routed to
// one dispatcher callback (wl_proxy_add_dispatcher) that switches on the
// proxy pointer. Requests go through wl_proxy_marshal_array_flags with the
// argument union array in Window.wy.args.
//
// Opcodes below are the request/event indices in wayland.xml, xdg-shell.xml,
// xdg-decoration-unstable-v1.xml, fractional-scale-v1.xml and viewporter.xml
// (see internal/wl/protocol_linux.go for the extension tables).

// libwayland entry points.
var wlf struct {
	connect, disconnect, getFD, flush                     uintptr
	prepareRead, readEvents, cancelRead, dispatchPending  uintptr
	roundtrip, marshal, addDispatcher, proxyDestroy, getV uintptr
}

// Core interface tables exported by libwayland-client (wayland-client-protocol).
var wlc struct {
	registry, compositor, surface, callback, seat, pointer, keyboard, output, region uintptr
}

// libxkbcommon entry points (optional: without them there is no keyboard).
var xk struct {
	ctxNew, ctxUnref, keymapFromString, keymapUnref, stateNew, stateUnref uintptr
	updateMask, getOneSym, getUTF8, modIndex, keyRepeats                  uintptr
}

var (
	wlLoaded   bool
	xkLoaded   bool
	dispatchCB uintptr

	appID     = cstr("gonativeui")
	nameShift = cstr("Shift")
	nameCtrl  = cstr("Control")
	nameAlt   = cstr("Mod1")
	nameLogo  = cstr("Mod4")
)

const (
	wlMarshalFlagDestroy = 1 // WL_MARSHAL_FLAG_DESTROY
	xkbKeymapFormatV1    = 1 // XKB_KEYMAP_FORMAT_TEXT_V1
	xkbModInvalid        = 0xffffffff
	titleBarDragHeight   = 32 // logical px that start xdg_toplevel.move without server decorations
	maxOutputs           = 8
)

type wlOutput struct {
	proxy uintptr
	name  uint32
	scale int32
}

type wayland struct {
	display, registry, fd uintptr
	compositor, wmBase    uintptr
	seat, decoMgr         uintptr
	fracMgr, viewporter   uintptr
	compositorV, wmBaseV  uint32
	seatV                 uint32

	surface, xdgSurface, toplevel uintptr
	deco, frac, viewport          uintptr
	surfaceV, xdgSurfaceV         uint32
	toplevelV                     uint32
	frameCB                       uintptr

	configured   bool
	pendW, pendH int32
	fracPref     int32 // wp_fractional_scale preferred scale, 1/120 units
	bufScale     int32 // wl_surface.preferred_buffer_scale
	decoMode     uint32
	suspended    bool
	outputs      [maxOutputs]wlOutput
	outMask      uint32
	dead         bool

	// input
	pointer, keyboard uintptr
	ptrX, ptrY        float32 // physical pixels
	btnSerial         uint32
	axisSource        uint32
	xkbCtx            uintptr
	keymap, xkbState  uintptr
	modIdx            [4]uint32
	repeatRate        int32  // chars/s; 0 disables
	repeatDelay       int32  // ms
	repCode           uint32 // evdev code of the key being repeated, 0 = none
	repAt             int64

	args   [8]uint64 // union wl_argument array for marshal calls
	title  [128]byte
	keyBuf [32]byte
}

func loadWayland() error {
	if wlLoaded {
		return nil
	}
	h, err := dlopen("libwayland-client.so.0", "libwayland-client.so")
	if err != nil {
		return fmt.Errorf("wayland: %w", err)
	}
	if err := dlsyms(h, "libwayland-client", []symRef{
		{"wl_display_connect", &wlf.connect},
		{"wl_display_disconnect", &wlf.disconnect},
		{"wl_display_get_fd", &wlf.getFD},
		{"wl_display_flush", &wlf.flush},
		{"wl_display_prepare_read", &wlf.prepareRead},
		{"wl_display_read_events", &wlf.readEvents},
		{"wl_display_cancel_read", &wlf.cancelRead},
		{"wl_display_dispatch_pending", &wlf.dispatchPending},
		{"wl_display_roundtrip", &wlf.roundtrip},
		{"wl_proxy_marshal_array_flags", &wlf.marshal}, // libwayland >= 1.20
		{"wl_proxy_add_dispatcher", &wlf.addDispatcher},
		{"wl_proxy_destroy", &wlf.proxyDestroy},
		{"wl_proxy_get_version", &wlf.getV},
		{"wl_registry_interface", &wlc.registry},
		{"wl_compositor_interface", &wlc.compositor},
		{"wl_surface_interface", &wlc.surface},
		{"wl_callback_interface", &wlc.callback},
		{"wl_seat_interface", &wlc.seat},
		{"wl_pointer_interface", &wlc.pointer},
		{"wl_keyboard_interface", &wlc.keyboard},
		{"wl_output_interface", &wlc.output},
		{"wl_region_interface", &wlc.region},
	}); err != nil {
		return err
	}
	if err := wl.Link(func(name string) uintptr {
		p, _ := purego.Dlsym(h, name) // core interface tables ("wl_seat_interface", ...)
		return p
	}); err != nil {
		return err
	}
	dispatchCB = ffi.NewCallback(wlDispatch)

	if xh, err := dlopen("libxkbcommon.so.0", "libxkbcommon.so"); err == nil {
		xkLoaded = dlsyms(xh, "libxkbcommon", []symRef{
			{"xkb_context_new", &xk.ctxNew},
			{"xkb_context_unref", &xk.ctxUnref},
			{"xkb_keymap_new_from_string", &xk.keymapFromString},
			{"xkb_keymap_unref", &xk.keymapUnref},
			{"xkb_state_new", &xk.stateNew},
			{"xkb_state_unref", &xk.stateUnref},
			{"xkb_state_update_mask", &xk.updateMask},
			{"xkb_state_key_get_one_sym", &xk.getOneSym},
			{"xkb_state_key_get_utf8", &xk.getUTF8},
			{"xkb_keymap_mod_get_index", &xk.modIndex},
			{"xkb_keymap_key_repeats", &xk.keyRepeats},
		}) == nil
	}
	wlLoaded = true
	return nil
}

// ---- request helpers ----

// req sends a request through wl_proxy_marshal_array_flags using w.wy.args
// and returns the new proxy for requests with a new_id argument.
func (w *Window) req(proxy uintptr, opcode uint32, iface uintptr, version, flags uint32) uintptr {
	return fr.Reset().Arg(proxy).Arg32(opcode).Arg(iface).Arg32(version).Arg32(flags).
		Arg(uintptr(unsafe.Pointer(&w.wy.args))).Call(wlf.marshal).R0()
}

func version(proxy uintptr) uint32 { return uint32(fr.Call1(wlf.getV, proxy)) }

// listen routes the proxy's events to the shared dispatcher.
func listen(proxy uintptr) { fr.Call4(wlf.addDispatcher, proxy, dispatchCB, 0, 0) }

// release sends a destructor request when the proxy's version has it (since
// <= version) and frees the proxy; otherwise it just frees the proxy.
func (w *Window) release(proxy uintptr, opcode, since uint32) {
	if proxy == 0 {
		return
	}
	if v := version(proxy); v >= since {
		w.req(proxy, opcode, 0, v, wlMarshalFlagDestroy)
		return
	}
	fr.Call1(wlf.proxyDestroy, proxy)
}

func ifaceVersion(iface uintptr) uint32 { return peekU32(iface + 8) }

func minU32(a, b, c uint32) uint32 { return min(a, min(b, c)) }

// bind wl_registry.bind (opcode 0, signature "usun").
func (w *Window) bind(name uint32, iface, ifaceName uintptr, v uint32) uintptr {
	y := &w.wy
	y.args[0], y.args[1], y.args[2], y.args[3] = uint64(name), uint64(ifaceName), uint64(v), 0
	p := w.req(y.registry, 0, iface, v, 0)
	listen(p)
	return p
}

// ---- Open / Close ----

func (w *Window) openWayland(o Options) (err error) {
	if err := loadWayland(); err != nil {
		return err
	}
	w.wl = true
	y := &w.wy
	defer func() {
		if err != nil {
			w.closeWayland()
			w.wl = false
		}
	}()

	w.lw, w.lh, w.s120 = int32(o.Width), int32(o.Height), 120
	w.pw, w.ph = w.physical(w.lw), w.physical(w.lh)
	copy(y.title[:len(y.title)-1], o.Title)

	y.display = fr.Call1(wlf.connect, 0)
	if y.display == 0 {
		return errors.New("wayland: wl_display_connect failed")
	}
	y.fd = uintptr(int32(fr.Call1(wlf.getFD, y.display)))
	w.pfd[0] = pollfd{fd: int32(y.fd), events: pollIn}
	if xkLoaded {
		y.xkbCtx = fr.Call1(xk.ctxNew, 0)
	}
	y.repeatRate, y.repeatDelay = 25, 600 // until wl_keyboard.repeat_info

	// wl_display.get_registry (opcode 1): the inline wrapper passes the
	// display's version and a NULL new_id.
	y.args[0] = 0
	y.registry = w.req(y.display, 1, wlc.registry, version(y.display), 0)
	listen(y.registry)
	if int32(fr.Call1(wlf.roundtrip, y.display)) < 0 {
		return errors.New("wayland: registry roundtrip failed")
	}
	if y.compositor == 0 || y.wmBase == 0 {
		return errors.New("wayland: compositor lacks wl_compositor or xdg_wm_base")
	}
	if int32(fr.Call1(wlf.roundtrip, y.display)) < 0 { // seat capabilities, output scales
		return errors.New("wayland: roundtrip failed")
	}

	// wl_compositor.create_surface (0)
	y.args[0] = 0
	y.surface = w.req(y.compositor, 0, wlc.surface, y.compositorV, 0)
	y.surfaceV = version(y.surface)
	listen(y.surface)

	// xdg_wm_base.get_xdg_surface (2), xdg_surface.get_toplevel (1)
	y.args[0], y.args[1] = 0, uint64(y.surface)
	y.xdgSurface = w.req(y.wmBase, 2, wl.XdgSurface.Addr(), y.wmBaseV, 0)
	y.xdgSurfaceV = version(y.xdgSurface)
	listen(y.xdgSurface)
	y.args[0] = 0
	y.toplevel = w.req(y.xdgSurface, 1, wl.XdgToplevel.Addr(), y.xdgSurfaceV, 0)
	y.toplevelV = version(y.toplevel)
	listen(y.toplevel)

	// xdg_toplevel.set_title (2), set_app_id (3)
	y.args[0] = uint64(uintptr(unsafe.Pointer(&y.title[0])))
	w.req(y.toplevel, 2, 0, y.toplevelV, 0)
	y.args[0] = uint64(uintptr(unsafe.Pointer(&appID[0])))
	w.req(y.toplevel, 3, 0, y.toplevelV, 0)
	if o.MinWidth > 0 || o.MinHeight > 0 { // xdg_toplevel.set_min_size (8), surface-local units
		y.args[0], y.args[1] = uint64(uint32(o.MinWidth)), uint64(uint32(o.MinHeight))
		w.req(y.toplevel, 8, 0, y.toplevelV, 0)
	}

	// zxdg_decoration_manager_v1.get_toplevel_decoration (1) + set_mode(server_side=2) (1)
	if y.decoMgr != 0 {
		y.args[0], y.args[1] = 0, uint64(y.toplevel)
		y.deco = w.req(y.decoMgr, 1, wl.ZxdgToplevelDecorationV1.Addr(), version(y.decoMgr), 0)
		listen(y.deco)
		y.args[0] = 2
		w.req(y.deco, 1, 0, version(y.deco), 0)
	}
	// wp_viewporter.get_viewport (1), wp_fractional_scale_manager_v1.get_fractional_scale (1)
	if y.viewporter != 0 {
		y.args[0], y.args[1] = 0, uint64(y.surface)
		y.viewport = w.req(y.viewporter, 1, wl.WpViewport.Addr(), version(y.viewporter), 0)
		if y.fracMgr != 0 {
			y.args[0], y.args[1] = 0, uint64(y.surface)
			y.frac = w.req(y.fracMgr, 1, wl.WpFractionalScaleV1.Addr(), version(y.fracMgr), 0)
			listen(y.frac)
		}
	}
	// Initial empty commit; the compositor answers with the first configure.
	w.req(y.surface, 6, 0, y.surfaceV, 0)
	for i := 0; i < 20 && !y.configured; i++ {
		if int32(fr.Call1(wlf.roundtrip, y.display)) < 0 {
			return errors.New("wayland: roundtrip failed waiting for the first configure")
		}
	}
	if !y.configured {
		return errors.New("wayland: compositor never sent xdg_surface.configure")
	}
	// Pick up preferred scale / output enter that followed the configure.
	if int32(fr.Call1(wlf.roundtrip, y.display)) < 0 {
		return errors.New("wayland: roundtrip failed")
	}
	w.q.reset() // everything so far is reflected in Size()
	return nil
}

func (w *Window) closeWayland() {
	y := &w.wy
	if y.display == 0 {
		return
	}
	w.release(y.frameCB, 0, 99) // wl_callback has no destructor request
	w.release(y.keyboard, 0, 3) // wl_keyboard.release
	w.release(y.pointer, 1, 3)  // wl_pointer.release
	w.release(y.seat, 3, 5)     // wl_seat.release
	for i := range y.outputs {
		w.release(y.outputs[i].proxy, 0, 3) // wl_output.release
	}
	w.release(y.viewport, 0, 1)
	w.release(y.frac, 0, 1)
	w.release(y.deco, 0, 1)
	w.release(y.toplevel, 0, 1)
	w.release(y.xdgSurface, 0, 1)
	w.release(y.surface, 0, 1) // wl_surface.destroy
	w.release(y.viewporter, 0, 1)
	w.release(y.fracMgr, 0, 1)
	w.release(y.decoMgr, 0, 1)
	w.release(y.wmBase, 0, 1)
	w.release(y.compositor, 2, 7) // wl_compositor.release
	w.release(y.registry, 0, 99)  // no destructor
	if y.xkbState != 0 {
		fr.Call1(xk.stateUnref, y.xkbState)
	}
	if y.keymap != 0 {
		fr.Call1(xk.keymapUnref, y.keymap)
	}
	if y.xkbCtx != 0 {
		fr.Call1(xk.ctxUnref, y.xkbCtx)
	}
	fr.Call1(wlf.flush, y.display)
	fr.Call1(wlf.disconnect, y.display)
	y.display = 0
}

// ---- frame pacing ----

// wlRequestFrame attaches a wl_callback to the next surface commit
// (wl_surface.frame, opcode 3).
func (w *Window) wlRequestFrame() {
	y := &w.wy
	if y.frameCB != 0 {
		return
	}
	y.args[0] = 0
	y.frameCB = w.req(y.surface, 3, wlc.callback, y.surfaceV, 0)
	listen(y.frameCB)
}

// ---- event loop ----

func (w *Window) wlFail() {
	y := &w.wy
	if !y.dead {
		y.dead = true
		w.q.push(Event{Kind: EvClose, Time: nanotime()})
	}
}

func (w *Window) waitWayland(timeoutNs int64) {
	y := &w.wy
	deadline := int64(-1)
	if timeoutNs >= 0 {
		deadline = nanotime() + timeoutNs
	}
	for {
		if y.dead {
			return
		}
		// Standard libwayland read protocol: drain queued events, then
		// announce the read, flush and poll.
		for int32(fr.Call1(wlf.prepareRead, y.display)) != 0 {
			if int32(fr.Call1(wlf.dispatchPending, y.display)) < 0 {
				w.wlFail()
				return
			}
		}
		flushed := int32(fr.Call1(wlf.flush, y.display)) >= 0 // false: socket full (EAGAIN) or broken

		now := nanotime()
		tmo := int64(-1)
		if deadline >= 0 {
			tmo = max(deadline-now, 0)
		}
		if len(w.q.ev) > 0 || w.pw != w.repW || w.ph != w.repH || w.s120 != w.repS {
			tmo = 0
		}
		if y.repCode != 0 {
			if d := max(y.repAt-now, 0); tmo < 0 || d < tmo {
				tmo = d
			}
		}
		w.pfd[0].events = pollIn
		if !flushed {
			w.pfd[0].events |= pollOut
		}
		w.pollWait(tmo)

		if w.pfd[0].revents&(pollIn|pollErr|pollHup) != 0 {
			if int32(fr.Call1(wlf.readEvents, y.display)) < 0 {
				w.wlFail()
				return
			}
		} else {
			fr.Call1(wlf.cancelRead, y.display)
		}
		if int32(fr.Call1(wlf.dispatchPending, y.display)) < 0 {
			w.wlFail()
			return
		}
		w.drainWake()
		w.wlRepeat(nanotime())
		w.flushResize()

		if len(w.q.ev) > 0 || (deadline >= 0 && nanotime() >= deadline) {
			return
		}
	}
}

// wlRepeat emits client-side key repeats (Wayland leaves repeat to clients).
func (w *Window) wlRepeat(now int64) {
	y := &w.wy
	if y.repCode == 0 || now < y.repAt || y.xkbState == 0 {
		return
	}
	w.wlKeyEvent(y.repCode, true, true, now)
	interval := int64(1e9) / int64(max(y.repeatRate, 1))
	y.repAt += interval
	if y.repAt <= now {
		y.repAt = now + interval
	}
}

// wlKeyEvent pushes an EvKey (and EvText on press) for an evdev keycode.
func (w *Window) wlKeyEvent(code uint32, down, repeat bool, now int64) {
	y := &w.wy
	if y.xkbState == 0 {
		return
	}
	kc := code + 8 // evdev -> xkb keycode
	sym := uint32(fr.Call2(xk.getOneSym, y.xkbState, uintptr(kc)))
	ev := Event{Kind: EvKey, Key: linuxKey(code, sym), Code: code, Mods: w.mods, Time: now}
	if down {
		ev.Flags |= FlagDown
	}
	if repeat {
		ev.Flags |= FlagRepeat
	}
	w.q.push(ev)
	if !down {
		return
	}
	n := int32(fr.Reset().Arg(y.xkbState).Arg32(kc).Arg(uintptr(unsafe.Pointer(&y.keyBuf[0]))).Arg(uintptr(len(y.keyBuf))).Call(xk.getUTF8).R0())
	if n > 0 && int(n) < len(y.keyBuf) && y.keyBuf[0] >= 0x20 && y.keyBuf[0] != 0x7f {
		w.q.pushText(y.keyBuf[:n], now)
	}
}

// ---- dispatcher ----

// wlDispatch is the dispatcher for every proxy:
//
//	int dispatcher(const void *data, void *proxy, uint32_t opcode,
//	               const struct wl_message *msg, union wl_argument *args)
func wlDispatch(c *ffi.CB) {
	c.Ret(0)
	theWindow.wlEvent(c.Int(1), uint32(c.Int(2)), c.Ptr(4))
}

// Argument accessors for `union wl_argument args[]` (8-byte unions).
func argU(a unsafe.Pointer, i int) uint32  { return *(*uint32)(unsafe.Add(a, i*8)) }
func argP(a unsafe.Pointer, i int) uintptr { return *(*uintptr)(unsafe.Add(a, i*8)) }
func argFixed(a unsafe.Pointer, i int) float32 { // wl_fixed_t is 24.8
	return float32(int32(argU(a, i))) / 256
}

func (w *Window) wlEvent(proxy uintptr, op uint32, a unsafe.Pointer) {
	y := &w.wy
	switch proxy {
	case y.frameCB:
		w.onFrameDone()
	case y.registry:
		w.onRegistry(op, a)
	case y.surface:
		w.onSurface(op, a)
	case y.xdgSurface:
		w.onXdgSurface(op, a)
	case y.toplevel:
		w.onToplevel(op, a)
	case y.wmBase:
		if op == 0 { // ping(serial) -> pong (3)
			y.args[0] = uint64(argU(a, 0))
			w.req(y.wmBase, 3, 0, y.wmBaseV, 0)
		}
	case y.deco:
		if op == 0 { // configure(mode)
			y.decoMode = argU(a, 0)
		}
	case y.frac:
		if op == 0 { // preferred_scale(scale)
			y.fracPref = int32(argU(a, 0))
			w.wlUpdateScale()
		}
	case y.seat:
		if op == 0 {
			w.onSeatCaps(argU(a, 0))
		}
	case y.pointer:
		w.onPointer(op, a)
	case y.keyboard:
		w.onKeyboard(op, a)
	default:
		w.onOutput(proxy, op, a)
	}
}

func (w *Window) onFrameDone() {
	y := &w.wy
	cb := y.frameCB
	y.frameCB = 0
	fr.Call1(wlf.proxyDestroy, cb) // wl_callback.done is a destructor event
	now := nanotime()
	if !w.visible {
		w.visible = true
		w.q.push(Event{Kind: EvVisible, Flags: FlagDown, Time: now})
	}
	w.q.push(Event{Kind: EvFrame, Time: now})
}

func (w *Window) onRegistry(op uint32, a unsafe.Pointer) {
	y := &w.wy
	switch op {
	case 0: // global(name, interface, version)
		name, iface, adv := argU(a, 0), argP(a, 1), argU(a, 2)
		switch {
		case y.compositor == 0 && cstrIs(iface, "wl_compositor"):
			y.compositorV = minU32(adv, 6, ifaceVersion(wlc.compositor)) // 6: preferred_buffer_scale
			y.compositor = w.bind(name, wlc.compositor, peekPtr(wlc.compositor), y.compositorV)
		case y.wmBase == 0 && cstrIs(iface, "xdg_wm_base"):
			y.wmBaseV = minU32(adv, 6, uint32(wl.XdgWmBase.Version))
			y.wmBase = w.bind(name, wl.XdgWmBase.Addr(), wl.XdgWmBase.NameAddr(), y.wmBaseV)
		case y.seat == 0 && cstrIs(iface, "wl_seat"):
			y.seatV = minU32(adv, 8, ifaceVersion(wlc.seat)) // 8: axis_value120 era; newer events are ignored
			y.seat = w.bind(name, wlc.seat, peekPtr(wlc.seat), y.seatV)
		case y.decoMgr == 0 && cstrIs(iface, "zxdg_decoration_manager_v1"):
			y.decoMgr = w.bind(name, wl.ZxdgDecorationManagerV1.Addr(), wl.ZxdgDecorationManagerV1.NameAddr(), 1)
		case y.fracMgr == 0 && cstrIs(iface, "wp_fractional_scale_manager_v1"):
			y.fracMgr = w.bind(name, wl.WpFractionalScaleManagerV1.Addr(), wl.WpFractionalScaleManagerV1.NameAddr(), 1)
		case y.viewporter == 0 && cstrIs(iface, "wp_viewporter"):
			y.viewporter = w.bind(name, wl.WpViewporter.Addr(), wl.WpViewporter.NameAddr(), 1)
		case cstrIs(iface, "wl_output"):
			for i := range y.outputs {
				if y.outputs[i].proxy == 0 {
					v := minU32(adv, 3, ifaceVersion(wlc.output)) // 2: scale, 3: release
					y.outputs[i] = wlOutput{proxy: w.bind(name, wlc.output, peekPtr(wlc.output), v), name: name, scale: 1}
					break
				}
			}
		}
	case 1: // global_remove(name)
		name := argU(a, 0)
		for i := range y.outputs {
			if o := &y.outputs[i]; o.proxy != 0 && o.name == name {
				w.release(o.proxy, 0, 3)
				*o = wlOutput{}
				y.outMask &^= 1 << i
				w.wlUpdateScale()
			}
		}
	}
}

func (w *Window) onOutput(proxy uintptr, op uint32, a unsafe.Pointer) {
	y := &w.wy
	if op != 3 { // scale(factor)
		return
	}
	for i := range y.outputs {
		if y.outputs[i].proxy == proxy {
			y.outputs[i].scale = int32(argU(a, 0))
			w.wlUpdateScale()
			return
		}
	}
}

func (w *Window) onSurface(op uint32, a unsafe.Pointer) {
	y := &w.wy
	switch op {
	case 0, 1: // enter(output), leave(output)
		o := argP(a, 0)
		for i := range y.outputs {
			if y.outputs[i].proxy == o {
				if op == 0 {
					y.outMask |= 1 << i
				} else {
					y.outMask &^= 1 << i
				}
				w.wlUpdateScale()
				return
			}
		}
	case 2: // preferred_buffer_scale(factor), since 6
		y.bufScale = int32(argU(a, 0))
		w.wlUpdateScale()
	}
}

// wlUpdateScale picks the scale: fractional-scale (needs the viewport),
// then preferred_buffer_scale, then the largest entered wl_output scale.
func (w *Window) wlUpdateScale() {
	y := &w.wy
	s := int32(120)
	switch {
	case y.fracPref > 0 && y.viewport != 0:
		s = y.fracPref
	case y.bufScale > 0:
		s = y.bufScale * 120
	default:
		m := int32(1)
		for i := range y.outputs {
			if y.outMask&(1<<i) != 0 && y.outputs[i].scale > m {
				m = y.outputs[i].scale
			}
		}
		s = m * 120
	}
	if s == w.s120 {
		return
	}
	w.s120 = s
	w.wlApplyGeometry(true)
}

// wlApplyGeometry derives the pixel size from the logical size and scale and
// tells the compositor how the buffer maps onto the surface.
func (w *Window) wlApplyGeometry(scaleChanged bool) {
	y := &w.wy
	w.pw, w.ph = w.physical(w.lw), w.physical(w.lh)
	if y.viewport != 0 {
		// wp_viewport.set_destination (2): surface size in logical units.
		y.args[0], y.args[1] = uint64(uint32(w.lw)), uint64(uint32(w.lh))
		w.req(y.viewport, 2, 0, 1, 0)
	} else if scaleChanged && y.surfaceV >= 3 {
		y.args[0] = uint64(uint32(w.s120 / 120))
		w.req(y.surface, 8, 0, y.surfaceV, 0) // wl_surface.set_buffer_scale
	}
}

func (w *Window) onXdgSurface(op uint32, a unsafe.Pointer) {
	y := &w.wy
	if op != 0 { // configure(serial)
		return
	}
	// A zero dimension means "client's choice"; keep our current value for it.
	if y.pendW > 0 {
		w.lw = y.pendW
	}
	if y.pendH > 0 {
		w.lh = y.pendH
	}
	y.args[0] = uint64(argU(a, 0))
	w.req(y.xdgSurface, 4, 0, y.xdgSurfaceV, 0) // ack_configure
	y.configured = true
	w.wlApplyGeometry(false)
}

func (w *Window) onToplevel(op uint32, a unsafe.Pointer) {
	y := &w.wy
	switch op {
	case 0: // configure(width, height, states)
		y.pendW, y.pendH = int32(argU(a, 0)), int32(argU(a, 1))
		arr := argP(a, 2) // struct wl_array { size_t size; size_t alloc; void *data; }
		suspended := false
		if arr != 0 {
			size, data := peekPtr(arr), peekPtr(arr+16)
			for off := uintptr(0); off+4 <= size; off += 4 {
				if peekU32(data+off) == 9 { // XDG_TOPLEVEL_STATE_SUSPENDED (since 6)
					suspended = true
				}
			}
		}
		if suspended != y.suspended {
			y.suspended = suspended
			if w.visible {
				var f Flags
				if !suspended {
					f = FlagDown
				}
				w.q.push(Event{Kind: EvVisible, Flags: f, Time: nanotime()})
			}
		}
	case 1: // close
		w.q.push(Event{Kind: EvClose, Time: nanotime()})
	}
}

// ---- seat ----

func (w *Window) onSeatCaps(caps uint32) {
	y := &w.wy
	if caps&1 != 0 && y.pointer == 0 { // wl_seat.get_pointer (0)
		y.args[0] = 0
		y.pointer = w.req(y.seat, 0, wlc.pointer, y.seatV, 0)
		listen(y.pointer)
	} else if caps&1 == 0 && y.pointer != 0 {
		w.release(y.pointer, 1, 3)
		y.pointer = 0
	}
	if caps&2 != 0 && y.keyboard == 0 && xkLoaded { // wl_seat.get_keyboard (1)
		y.args[0] = 0
		y.keyboard = w.req(y.seat, 1, wlc.keyboard, y.seatV, 0)
		listen(y.keyboard)
	} else if caps&2 == 0 && y.keyboard != 0 {
		w.release(y.keyboard, 0, 3)
		y.keyboard, y.repCode = 0, 0
	}
}

// dragsAt reports whether a press at the pointer position (physical pixels)
// moves the window: the drag map if the app set one, else the top strip.
func (w *Window) dragsAt(x, y, scale float32) bool {
	if len(w.drags.regs) > 0 {
		return w.drags.at(x, y)
	}
	return y < titleBarDragHeight*scale
}

func (w *Window) onPointer(op uint32, a unsafe.Pointer) {
	y := &w.wy
	scale := float32(w.s120) / 120
	now := nanotime()
	switch op {
	case 0: // enter(serial, surface, x, y)
		y.ptrX, y.ptrY = argFixed(a, 2)*scale, argFixed(a, 3)*scale
		w.q.push(Event{Kind: EvPointerMove, X: y.ptrX, Y: y.ptrY, Mods: w.mods, Time: now})
	case 1: // leave(serial, surface)
		w.q.push(Event{Kind: EvPointerLeave, Time: now})
	case 2: // motion(time, x, y)
		y.ptrX, y.ptrY = argFixed(a, 1)*scale, argFixed(a, 2)*scale
		w.q.push(Event{Kind: EvPointerMove, X: y.ptrX, Y: y.ptrY, Mods: w.mods, Time: now})
	case 3: // button(serial, time, button, state)
		serial, code, st := argU(a, 0), argU(a, 2), argU(a, 3)
		b := evdevButton(code)
		if b == ButtonNone {
			return
		}
		if st != 0 && b == ButtonLeft && y.decoMode != 2 && w.dragsAt(y.ptrX, y.ptrY, scale) {
			// No server-side decoration: the app's drag map, or without one
			// the top strip, drags the window. xdg_toplevel.move (5)
			y.args[0], y.args[1] = uint64(y.seat), uint64(serial)
			w.req(y.toplevel, 5, 0, y.toplevelV, 0)
			return
		}
		ev := Event{Kind: EvPointerButton, Button: b, X: y.ptrX, Y: y.ptrY, Mods: w.mods, Time: now}
		if st != 0 {
			ev.Flags = FlagDown
		}
		w.q.push(ev)
	case 4: // axis(time, axis, value)
		v := -argFixed(a, 2) * scale // wl_pointer: positive = scrolling down/right = content moves up/left
		ev := Event{Kind: EvScroll, Mods: w.mods, Time: now}
		if y.axisSource == 1 || y.axisSource == 2 { // finger, continuous
			ev.Flags = FlagPrecise
		}
		if argU(a, 1) == 0 { // vertical_scroll
			ev.DY = v
		} else {
			ev.DX = v
		}
		w.q.push(ev)
	case 5: // frame
		y.axisSource = 0
	case 6: // axis_source(source)
		y.axisSource = argU(a, 0)
	}
}

func (w *Window) onKeyboard(op uint32, a unsafe.Pointer) {
	y := &w.wy
	now := nanotime()
	switch op {
	case 0: // keymap(format, fd, size)
		w.loadKeymap(argU(a, 0), int(int32(argU(a, 1))), argU(a, 2))
	case 1: // enter(serial, surface, keys)
		w.q.push(Event{Kind: EvFocus, Flags: FlagDown, Time: now})
	case 2: // leave(serial, surface)
		y.repCode = 0
		w.q.push(Event{Kind: EvFocus, Time: now})
	case 3: // key(serial, time, key, state)
		code, st := argU(a, 2), argU(a, 3)
		down := st != 0
		w.wlKeyEvent(code, down, st == 2, now)
		switch {
		case down && st == 1 && y.repeatRate > 0 && y.xkbState != 0 && uint32(fr.Call2(xk.keyRepeats, y.keymap, uintptr(code+8))) != 0:
			y.repCode = code
			y.repAt = now + int64(y.repeatDelay)*1e6
		case !down && y.repCode == code:
			y.repCode = 0
		}
	case 4: // modifiers(serial, depressed, latched, locked, group)
		if y.xkbState == 0 {
			return
		}
		dep, lat, lock := argU(a, 1), argU(a, 2), argU(a, 3)
		fr.Reset().Arg(y.xkbState).Arg32(dep).Arg32(lat).Arg32(lock).Arg32(0).Arg32(0).Arg32(argU(a, 4)).Call(xk.updateMask)
		eff := dep | lat | lock
		var m Mods
		for i, bit := range [4]Mods{ModShift, ModCtrl, ModAlt, ModSuper} {
			if idx := y.modIdx[i]; idx != xkbModInvalid && eff&(1<<idx) != 0 {
				m |= bit
			}
		}
		w.mods = m
	case 5: // repeat_info(rate, delay)
		y.repeatRate, y.repeatDelay = int32(argU(a, 0)), int32(argU(a, 1))
		if y.repeatRate <= 0 {
			y.repCode = 0
		}
	}
}

func (w *Window) loadKeymap(format uint32, fd int, size uint32) {
	y := &w.wy
	defer syscall.Close(fd) // the fd is ours to close
	if format != xkbKeymapFormatV1 || y.xkbCtx == 0 || size == 0 {
		return
	}
	// MAP_PRIVATE is required for wl_seat v7+; the text is NUL-terminated.
	data, err := syscall.Mmap(fd, 0, int(size), syscall.PROT_READ, syscall.MAP_PRIVATE)
	if err != nil {
		return
	}
	km := fr.Call4(xk.keymapFromString, y.xkbCtx, uintptr(unsafe.Pointer(&data[0])), xkbKeymapFormatV1, 0)
	syscall.Munmap(data)
	if km == 0 {
		return
	}
	st := fr.Call1(xk.stateNew, km)
	if st == 0 {
		fr.Call1(xk.keymapUnref, km)
		return
	}
	if y.xkbState != 0 {
		fr.Call1(xk.stateUnref, y.xkbState)
		fr.Call1(xk.keymapUnref, y.keymap)
	}
	y.keymap, y.xkbState = km, st
	for i, n := range [4][]byte{nameShift, nameCtrl, nameAlt, nameLogo} {
		y.modIdx[i] = uint32(fr.Call2(xk.modIndex, km, uintptr(unsafe.Pointer(&n[0]))))
	}
}
