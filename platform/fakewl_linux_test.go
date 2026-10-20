//go:build linux

package platform

import (
	"bufio"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/wl"
)

// A scripted Wayland compositor for tests. It runs as a helper process (the
// test binary itself, selected by GNUI_FAKE_WL in TestMain) so that nothing
// it allocates counts against the client in allocation tests.
//
// The wire protocol is decoded generically from the interface tables the
// client uses (core ones from libwayland-client, extensions from package wl).
// Every request is reported on stdout as "REQ iface.request args…". Stdin
// takes commands (focus, pointer, ext-copy, paste, deco, configure …), each
// answered by "OK <command>" once it has run.

type fobj struct {
	id   uint32
	name string
	ifc  *wl.Interface
	ver  uint32

	mimes      []string // wl_data_source: what it offers
	surface    *fobj    // xdg_surface, xdg_toplevel, decorations…: the wl_surface they belong to
	buffer     *fbuf    // wl_buffer, wl_surface (attached)
	pool       *fpool
	pendBuf    *fobj // wl_surface: attached, committed with the next commit
	frames     []*fobj
	hotX       int32 // cursor surface hotspot
	hotY       int32
	configured bool // xdg_surface: first configure sent
	ext        bool // wl_data_offer created by the server (an "other application")
}

type fpool struct {
	mem  []byte
	size int
	fd   int
}

type fbuf struct {
	pool              *fpool
	off, w, h, stride int32
}

type farg struct {
	kind byte // i u f s o n a h
	v    uint32
	s    string
	b    []byte
	fd   int
	ifc  *wl.Interface // typed new_id
}

type nid uint32 // an object id argument
type fx float64
type fdv int

type fakeComp struct {
	mu     sync.Mutex
	conn   *net.UnixConn
	objs   map[uint32]*fobj
	next   uint32
	fds    []int
	opts   map[string]bool
	byName map[string]*wl.Interface
	serial uint32
	out    *bufio.Writer

	seat, kb, ptr, dd, pinch          *fobj
	mainSurf, xdgSurf, toplevel, deco *fobj
	cursorSurf                        *fobj
	clientSrc                         *fobj // the client's selection source
	extOffer                          *fobj
	extText                           []byte
	extStall                          bool
	decoMode                          uint32 // 0: no decoration global behaviour, 1 client, 2 server
	exports                           int
	ti                                *fobj      // the text input
	outMu                             sync.Mutex // guards out (see say)
	quiet                             bool       // "quiet": requests are no longer logged
	tiSerial                          uint32
	maximized                         bool
	cfgW, cfgH                        int32
}

func fakeCompositorMain() {
	sock := os.Getenv("GNUI_FAKE_WL")
	s := &fakeComp{objs: map[uint32]*fobj{}, next: 0xff000000, opts: map[string]bool{}, out: bufio.NewWriter(os.Stdout), decoMode: 2}
	for _, o := range strings.Split(os.Getenv("GNUI_FAKE_OPTS"), ",") {
		if o != "" {
			s.opts[o] = true
		}
	}
	if s.opts["clientdeco"] {
		s.decoMode = 1
	}
	h, err := purego.Dlopen("libwayland-client.so.0", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		fmt.Println("FATAL", err)
		os.Exit(2)
	}
	sym := func(name string) uintptr { p, _ := purego.Dlsym(h, name); return p }
	if err := wl.Link(sym); err != nil {
		fmt.Println("FATAL", err)
		os.Exit(2)
	}
	if err := wl.LinkGestures(sym); err != nil {
		fmt.Println("FATAL", err)
		os.Exit(2)
	}
	s.byName = map[string]*wl.Interface{}
	for _, n := range []string{"wl_display", "wl_registry", "wl_callback", "wl_compositor", "wl_surface", "wl_region", "wl_shm", "wl_shm_pool",
		"wl_buffer", "wl_seat", "wl_pointer", "wl_keyboard", "wl_output", "wl_data_device_manager", "wl_data_device", "wl_data_source", "wl_data_offer"} {
		p := sym(n + "_interface")
		if p == 0 {
			fmt.Println("FATAL missing", n)
			os.Exit(2)
		}
		s.byName[n] = (*wl.Interface)(ptr(p))
	}
	for _, i := range []*wl.Interface{&wl.XdgWmBase, &wl.XdgSurface, &wl.XdgToplevel, &wl.XdgPopup, &wl.XdgPositioner,
		&wl.ZxdgDecorationManagerV1, &wl.ZxdgToplevelDecorationV1, &wl.WpFractionalScaleManagerV1, &wl.WpFractionalScaleV1,
		&wl.WpViewporter, &wl.WpViewport, &wl.WpCursorShapeManagerV1, &wl.WpCursorShapeDeviceV1,
		&wl.ZxdgExporterV2, &wl.ZxdgExportedV2, &wl.ZwpTextInputManagerV3, &wl.ZwpTextInputV3,
		&wl.ZwpPointerGesturesV1, &wl.ZwpPointerGesturePinchV1} {
		s.byName[goString(i.Name)] = i
	}

	os.Remove(sock)
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: sock, Net: "unix"})
	if err != nil {
		fmt.Println("FATAL", err)
		os.Exit(2)
	}
	s.say("READY")
	conn, err := l.AcceptUnix()
	if err != nil {
		os.Exit(2)
	}
	s.conn = conn
	go s.commands()
	s.run()
	os.Exit(0)
}

func goString(p *byte) string {
	var b []byte
	for ; *p != 0; p = (*byte)(unsafe.Add(unsafe.Pointer(p), 1)) {
		b = append(b, *p)
	}
	return string(b)
}

// say reports a line to the test. It is called from the request loop, from
// the command goroutine (after it let go of s.mu) and from the goroutines of
// pasteFromClient, so the writer has a lock of its own: an unsynchronized
// bufio.Writer garbled lines and lost acknowledgements.
func (s *fakeComp) say(format string, a ...any) {
	s.outMu.Lock()
	defer s.outMu.Unlock()
	fmt.Fprintf(s.out, format+"\n", a...)
	s.out.Flush()
}

func msgAt(base *wl.Message, i int) wl.Message {
	return *(*wl.Message)(unsafe.Add(unsafe.Pointer(base), i*int(unsafe.Sizeof(wl.Message{}))))
}

// ---- wire ----

func (s *fakeComp) run() {
	var buf []byte
	tmp := make([]byte, 64<<10)
	oob := make([]byte, 4096)
	for {
		n, oobn, _, _, err := s.conn.ReadMsgUnix(tmp, oob)
		if err != nil {
			return
		}
		if oobn > 0 {
			msgs, _ := syscall.ParseSocketControlMessage(oob[:oobn])
			for i := range msgs {
				if fds, err := syscall.ParseUnixRights(&msgs[i]); err == nil {
					s.fds = append(s.fds, fds...)
				}
			}
		}
		buf = append(buf, tmp[:n]...)
		for len(buf) >= 8 {
			size := int(binary.LittleEndian.Uint32(buf[4:]) >> 16)
			if size < 8 || len(buf) < size {
				break
			}
			msg := append([]byte(nil), buf[:size]...)
			buf = buf[size:]
			s.mu.Lock()
			s.dispatch(msg)
			s.mu.Unlock()
		}
	}
}

func (s *fakeComp) dispatch(msg []byte) {
	id := binary.LittleEndian.Uint32(msg)
	op := int(binary.LittleEndian.Uint32(msg[4:]) & 0xffff)
	o := s.objs[id]
	if id == 1 && o == nil {
		o = &fobj{id: 1, name: "wl_display", ifc: s.byName["wl_display"], ver: 1}
		s.objs[1] = o
	}
	if o == nil {
		s.say("WARN request for unknown object %d", id)
		return
	}
	if op >= int(o.ifc.MethodCount) {
		s.say("WARN %s: opcode %d out of range", o.name, op)
		return
	}
	m := msgAt(o.ifc.Methods, op)
	args := s.decode(goString(m.Signature), m.Types, msg[8:])
	name := goString(m.Name)
	var sb strings.Builder
	for _, a := range args {
		switch a.kind {
		case 's':
			fmt.Fprintf(&sb, " %q", a.s)
		case 'h':
			sb.WriteString(" fd")
		case 'a':
			fmt.Fprintf(&sb, " array(%d)", len(a.b))
		case 'n':
			fmt.Fprintf(&sb, " new:%d", a.v)
		case 'f':
			fmt.Fprintf(&sb, " %.2f", float64(int32(a.v))/256)
		case 'i':
			fmt.Fprintf(&sb, " %d", int32(a.v))
		default:
			fmt.Fprintf(&sb, " %d", a.v)
		}
	}
	if !s.quiet {
		s.say("REQ %s.%s%s", o.name, name, sb.String())
	}
	s.request(o, name, args)
}

func (s *fakeComp) decode(sig string, types *uintptr, b []byte) []farg {
	for len(sig) > 0 && sig[0] >= '0' && sig[0] <= '9' {
		sig = sig[1:]
	}
	var out []farg
	k := 0
	u32 := func() uint32 { v := binary.LittleEndian.Uint32(b); b = b[4:]; return v }
	str := func() string {
		n := int(u32())
		if n == 0 {
			return ""
		}
		v := string(b[:n-1])
		b = b[(n+3)&^3:]
		return v
	}
	for i := 0; i < len(sig); i++ {
		c := sig[i]
		if c == '?' {
			continue
		}
		a := farg{kind: c}
		switch c {
		case 'i', 'u', 'f', 'o':
			a.v = u32()
		case 's':
			a.s = str()
		case 'a':
			n := int(u32())
			a.b = append([]byte(nil), b[:n]...)
			b = b[(n+3)&^3:]
		case 'h':
			a.fd = s.fds[0]
			s.fds = s.fds[1:]
		case 'n':
			ti := *(*uintptr)(unsafe.Add(unsafe.Pointer(types), k*8))
			if ti != 0 {
				a.ifc = (*wl.Interface)(ptr(ti))
			}
			a.v = u32()
		}
		out = append(out, a)
		k++
	}
	return out
}

func (s *fakeComp) newObj(ifc *wl.Interface, ver uint32) *fobj {
	// libwayland requires server-created ids to start at 0xff000000 and
	// follow one another.
	o := &fobj{id: s.next, name: goString(ifc.Name), ifc: ifc, ver: ver}
	s.next++
	s.objs[o.id] = o
	return o
}

func (s *fakeComp) adopt(id uint32, ifc *wl.Interface, ver uint32) *fobj {
	o := &fobj{id: id, name: goString(ifc.Name), ifc: ifc, ver: ver}
	s.objs[id] = o
	return o
}

func toU32(v any) uint32 {
	switch x := v.(type) {
	case int:
		return uint32(x)
	case int32:
		return uint32(x)
	case uint32:
		return x
	case nid:
		return uint32(x)
	case *fobj:
		if x == nil {
			return 0
		}
		return x.id
	case bool:
		if x {
			return 1
		}
	}
	return 0
}

// event sends o's event name with args.
func (s *fakeComp) event(o *fobj, name string, args ...any) {
	op := -1
	for i := 0; i < int(o.ifc.EventCount); i++ {
		if goString(msgAt(o.ifc.Events, i).Name) == name {
			op = i
		}
	}
	if op < 0 {
		s.say("FATAL no event %s.%s", o.name, name)
		os.Exit(2)
	}
	sig := goString(msgAt(o.ifc.Events, op).Signature)
	for len(sig) > 0 && sig[0] >= '0' && sig[0] <= '9' {
		sig = sig[1:]
	}
	sig = strings.ReplaceAll(sig, "?", "")
	body := make([]byte, 0, 64)
	var fds []int
	put := func(v uint32) { body = binary.LittleEndian.AppendUint32(body, v) }
	for i, c := range []byte(sig) {
		a := args[i]
		switch c {
		case 'i', 'u', 'o', 'n':
			put(toU32(a))
		case 'f':
			put(uint32(int32(float64(a.(fx)) * 256)))
		case 's':
			str := a.(string)
			put(uint32(len(str) + 1))
			body = append(body, str...)
			body = append(body, 0)
			for len(body)%4 != 0 {
				body = append(body, 0)
			}
		case 'a':
			arr := a.([]byte)
			put(uint32(len(arr)))
			body = append(body, arr...)
			for len(body)%4 != 0 {
				body = append(body, 0)
			}
		case 'h':
			fds = append(fds, int(a.(fdv)))
		}
	}
	msg := binary.LittleEndian.AppendUint32(nil, o.id)
	msg = binary.LittleEndian.AppendUint32(msg, uint32(8+len(body))<<16|uint32(op))
	msg = append(msg, body...)
	var oob []byte
	if len(fds) > 0 {
		oob = syscall.UnixRights(fds...)
	}
	if _, _, err := s.conn.WriteMsgUnix(msg, oob, nil); err != nil {
		s.say("WARN write: %v", err)
	}
}

func (s *fakeComp) nextSerial() uint32 { s.serial++; return s.serial }

// ---- behaviour ----

func (s *fakeComp) global(reg *fobj, name uint32, iface string, ver uint32) {
	s.event(reg, "global", name, iface, ver)
}

func (s *fakeComp) request(o *fobj, name string, a []farg) {
	switch o.name + "." + name {
	case "wl_display.sync":
		cb := s.adopt(a[0].v, s.byName["wl_callback"], 1)
		s.event(cb, "done", uint32(0))
	case "wl_display.get_registry":
		reg := s.adopt(a[0].v, s.byName["wl_registry"], 1)
		g := []struct {
			iface string
			ver   uint32
		}{{"wl_compositor", 6}, {"wl_shm", 1}, {"wl_seat", 8}, {"xdg_wm_base", 6}, {"wl_data_device_manager", 3}}
		if !s.opts["nodeco"] {
			g = append(g, struct {
				iface string
				ver   uint32
			}{"zxdg_decoration_manager_v1", 1})
		}
		if s.opts["cursorshape"] {
			g = append(g, struct {
				iface string
				ver   uint32
			}{"wp_cursor_shape_manager_v1", 1})
		}
		if s.opts["gestures"] {
			g = append(g, struct {
				iface string
				ver   uint32
			}{"zwp_pointer_gestures_v1", 3})
		}
		if s.opts["foreign"] {
			g = append(g, struct {
				iface string
				ver   uint32
			}{"zxdg_exporter_v2", 1})
		}
		if s.opts["textinput"] {
			g = append(g, struct {
				iface string
				ver   uint32
			}{"zwp_text_input_manager_v3", 1})
		}
		for i, e := range g {
			s.global(reg, uint32(i+1), e.iface, e.ver)
		}
	case "wl_registry.bind":
		ifc := s.byName[a[1].s] // bind(name, interface, version, new_id): the signature is "usun"
		if ifc == nil {
			s.say("WARN bind of unknown %q", a[1].s)
			return
		}
		b := s.adopt(a[3].v, ifc, a[2].v)
		if b.name == "wl_seat" {
			s.seat = b
			s.event(b, "capabilities", uint32(3))
			if b.ver >= 2 {
				s.event(b, "name", "fake")
			}
		}
	case "wl_compositor.create_surface":
		s.adopt(a[0].v, s.byName["wl_surface"], o.ver)
	case "wl_surface.attach":
		o.pendBuf = s.objs[a[0].v]
	case "wl_surface.frame":
		cb := s.adopt(a[0].v, s.byName["wl_callback"], 1)
		o.frames = append(o.frames, cb)
	case "wl_surface.commit":
		if o.pendBuf != nil {
			o.buffer = o.pendBuf.buffer
			o.pendBuf = nil
		}
		if o == s.cursorSurf && o.buffer != nil {
			s.reportCursor(o)
		}
		for _, cb := range o.frames {
			s.event(cb, "done", uint32(0))
		}
		o.frames = nil
		if s.xdgSurf != nil && s.xdgSurf.surface == o && !s.xdgSurf.configured {
			s.xdgSurf.configured = true
			s.sendConfigure(0, 0)
		}
	case "wl_shm.create_pool":
		p := s.adopt(a[0].v, s.byName["wl_shm_pool"], 1)
		sz := int(int32(a[2].v))
		mem, err := syscall.Mmap(a[1].fd, 0, sz, syscall.PROT_READ, syscall.MAP_SHARED)
		if err != nil {
			s.say("WARN mmap: %v", err)
			return
		}
		p.pool = &fpool{mem: mem, size: sz, fd: a[1].fd}
	case "wl_shm_pool.resize": // the pool grows (libwayland-cursor): map the new size
		if o.pool != nil {
			syscall.Munmap(o.pool.mem)
			sz := int(int32(a[0].v))
			mem, err := syscall.Mmap(o.pool.fd, 0, sz, syscall.PROT_READ, syscall.MAP_SHARED)
			if err != nil {
				s.say("WARN remap: %v", err)
				return
			}
			o.pool.mem, o.pool.size = mem, sz
		}
	case "wl_shm_pool.create_buffer":
		b := s.adopt(a[0].v, s.byName["wl_buffer"], 1)
		b.buffer = &fbuf{pool: o.pool, off: int32(a[1].v), w: int32(a[2].v), h: int32(a[3].v), stride: int32(a[4].v)}
	case "wl_seat.get_pointer":
		s.ptr = s.adopt(a[0].v, s.byName["wl_pointer"], o.ver)
	case "wl_seat.get_keyboard":
		s.kb = s.adopt(a[0].v, s.byName["wl_keyboard"], o.ver)
	case "wl_pointer.set_cursor":
		s.cursorSurf = s.objs[a[1].v]
		if s.cursorSurf != nil {
			s.cursorSurf.hotX, s.cursorSurf.hotY = int32(a[2].v), int32(a[3].v)
		}
	case "wl_data_device_manager.create_data_source":
		s.adopt(a[0].v, s.byName["wl_data_source"], o.ver)
	case "wl_data_device_manager.get_data_device":
		s.dd = s.adopt(a[0].v, s.byName["wl_data_device"], o.ver)
	case "wl_data_source.offer":
		o.mimes = append(o.mimes, a[0].s)
	case "wl_data_source.destroy":
		if s.clientSrc == o {
			s.clientSrc = nil
		}
	case "wl_data_device.set_selection":
		src := s.objs[a[0].v]
		if s.clientSrc != nil && s.clientSrc != src {
			s.event(s.clientSrc, "cancelled")
		}
		s.clientSrc = src
		s.extOffer = nil
	case "wl_data_offer.receive":
		if o.ext {
			go s.serveExt(a[0].s, a[1].fd)
		} else {
			syscall.Close(a[1].fd)
		}
	case "xdg_wm_base.get_xdg_surface":
		x := s.adopt(a[0].v, &wl.XdgSurface, o.ver)
		x.surface = s.objs[a[1].v]
		s.xdgSurf = x
	case "xdg_surface.get_toplevel":
		s.toplevel = s.adopt(a[0].v, &wl.XdgToplevel, o.ver)
	case "xdg_toplevel.set_maximized":
		s.maximized = true
		s.sendConfigure(s.cfgW, s.cfgH)
	case "xdg_toplevel.unset_maximized":
		s.maximized = false
		s.sendConfigure(s.cfgW, s.cfgH)
	case "zxdg_decoration_manager_v1.get_toplevel_decoration":
		s.deco = s.adopt(a[0].v, &wl.ZxdgToplevelDecorationV1, o.ver)
		s.event(s.deco, "configure", s.decoMode)
	case "zwp_pointer_gestures_v1.get_pinch_gesture":
		s.pinch = s.adopt(a[0].v, &wl.ZwpPointerGesturePinchV1, o.ver)
	case "wp_cursor_shape_manager_v1.get_pointer":
		s.adopt(a[0].v, &wl.WpCursorShapeDeviceV1, o.ver)
	case "zwp_text_input_manager_v3.get_text_input":
		s.ti = s.adopt(a[0].v, &wl.ZwpTextInputV3, o.ver)
	case "zxdg_exporter_v2.export_toplevel":
		e := s.adopt(a[0].v, &wl.ZxdgExportedV2, o.ver)
		s.exports++
		s.event(e, "handle", fmt.Sprintf("fake-handle-%d", s.exports))
	}
}

// sendConfigure sends xdg_toplevel.configure and xdg_surface.configure with
// the current maximized state.
func (s *fakeComp) sendConfigure(w, h int32) {
	if s.toplevel == nil || s.xdgSurf == nil {
		return
	}
	var states []byte
	if s.maximized {
		states = binary.LittleEndian.AppendUint32(states, 1)
	}
	states = binary.LittleEndian.AppendUint32(states, 4) // activated
	s.event(s.toplevel, "configure", w, h, states)
	s.event(s.xdgSurf, "configure", s.nextSerial())
}

// reportCursor reports the cursor surface's buffer: its size, hotspot and how
// many pixels are not transparent.
func (s *fakeComp) reportCursor(o *fobj) {
	b := o.buffer
	if b == nil || b.pool == nil {
		return
	}
	n := 0
	for y := int32(0); y < b.h; y++ {
		for x := int32(0); x < b.w; x++ {
			i := int(b.off + y*b.stride + x*4)
			if i+4 <= len(b.pool.mem) && b.pool.mem[i+3] != 0 {
				n++
			}
		}
	}
	s.say("CURSOR %dx%d hot %d,%d opaque %d", b.w, b.h, o.hotX, o.hotY, n)
}

// serveExt writes the "other application's" clipboard text to fd.
func (s *fakeComp) serveExt(mime string, fd int) {
	s.mu.Lock()
	text, stall := s.extText, s.extStall
	s.mu.Unlock()
	f := os.NewFile(uintptr(fd), "pipe")
	if stall {
		f.Write(text[:len(text)/2])
		select {} // never finish
	}
	f.Write(text)
	f.Close()
}

// ---- commands from the test (stdin) ----

func (s *fakeComp) commands() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		line := sc.Text()
		s.mu.Lock()
		s.command(line)
		s.mu.Unlock()
		s.say("OK %s", strings.Fields(line)[0])
	}
	os.Exit(0)
}

func (s *fakeComp) command(line string) {
	f := strings.SplitN(line, " ", 2)
	arg := ""
	if len(f) > 1 {
		arg = f[1]
	}
	num := func(i int) float64 {
		p := strings.Fields(arg)
		if i >= len(p) {
			return 0
		}
		v, _ := strconv.ParseFloat(p[i], 64)
		return v
	}
	switch f[0] {
	case "spam": // n: n lines from a goroutine of their own, as the paste goroutines report
		go func(n int) {
			for i := 0; i < n; i++ {
				s.say("SPAM %d %s", i, strings.Repeat("x", 40))
			}
		}(int(num(0)))
	case "quiet": // stop logging requests (the test measures this process's allocations; the log reader would allocate)
		s.quiet = true
	case "focus":
		if s.kb != nil && s.mainSurface() != nil {
			s.event(s.kb, "enter", s.nextSerial(), s.mainSurface(), []byte{})
		}
	case "unfocus":
		if s.kb != nil {
			s.event(s.kb, "leave", s.nextSerial(), s.mainSurface())
		}
	case "pointer": // enter at x y
		if s.ptr != nil {
			ser := s.nextSerial()
			s.say("ENTER %d", ser)
			s.event(s.ptr, "enter", ser, s.mainSurface(), fx(num(0)), fx(num(1)))
		}
	case "ti_enter", "ti_leave": // the text input gets / loses the surface
		if s.ti != nil && s.mainSurface() != nil {
			s.event(s.ti, strings.TrimPrefix(f[0], "ti_"), s.mainSurface())
		}
	case "preedit": // text cursor_begin cursor_end
		if s.ti != nil {
			s.event(s.ti, "preedit_string", strings.Fields(arg)[0], int32(num(1)), int32(num(2)))
		}
	case "commit": // text
		if s.ti != nil {
			s.event(s.ti, "commit_string", arg)
		}
	case "done":
		if s.ti != nil {
			s.tiSerial++
			s.event(s.ti, "done", s.tiSerial)
		}
	case "scale": // preferred_buffer_scale on the main surface
		if m := s.mainSurface(); m != nil {
			s.event(m, "preferred_buffer_scale", int32(num(0)))
		}
	case "pinch_begin":
		if s.pinch != nil {
			s.event(s.pinch, "begin", s.nextSerial(), uint32(0), s.mainSurface(), uint32(2))
		}
	case "pinch_update": // scale rotation
		if s.pinch != nil {
			s.event(s.pinch, "update", uint32(0), fx(0), fx(0), fx(num(0)), fx(num(1)))
		}
	case "pinch_end": // cancelled
		if s.pinch != nil {
			s.event(s.pinch, "end", s.nextSerial(), uint32(0), int32(num(0)))
		}
	case "leave":
		if s.ptr != nil {
			s.event(s.ptr, "leave", s.nextSerial(), s.mainSurface())
		}
	case "motion":
		if s.ptr != nil {
			s.event(s.ptr, "motion", uint32(0), fx(num(0)), fx(num(1)))
		}
	case "button": // button code, 1 = down, time in ms
		if s.ptr != nil {
			ser := s.nextSerial()
			s.say("SERIAL %d", ser)
			s.event(s.ptr, "button", ser, uint32(num(2)), uint32(num(0)), uint32(num(1)))
		}
	case "ext-copy": // another application copies arg
		s.extText = []byte(arg)
		s.extStall = false
		s.offerExt()
	case "ext-copy-big": // n bytes of text
		n := int(num(0))
		s.extText = nil
		for len(s.extText) < n {
			s.extText = append(s.extText, "héllo wörld 日本語 ✓\n"...)
		}
		s.extStall = false
		s.offerExt()
	case "ext-stall": // another application that never finishes writing
		s.extText = []byte(strings.Repeat("x", 1000))
		s.extStall = true
		s.offerExt()
	case "ext-clear":
		if s.dd != nil {
			s.event(s.dd, "selection", nid(0))
		}
	case "paste": // another application pastes from the client's selection
		s.pasteFromClient(arg)
	case "deco": // mode arg: client or server; then a configure
		s.decoMode = map[string]uint32{"client": 1, "server": 2}[arg]
		if s.deco != nil {
			s.event(s.deco, "configure", s.decoMode)
		}
		s.sendConfigure(s.cfgW, s.cfgH)
	case "configure": // w h
		s.cfgW, s.cfgH = int32(num(0)), int32(num(1))
		s.sendConfigure(s.cfgW, s.cfgH)
	case "close":
		if s.toplevel != nil {
			s.event(s.toplevel, "close")
		}
	}
}

func (s *fakeComp) mainSurface() *fobj {
	if s.xdgSurf == nil {
		return nil
	}
	return s.xdgSurf.surface
}

// offerExt makes s.extText the selection, as another application's copy.
func (s *fakeComp) offerExt() {
	if s.dd == nil {
		return
	}
	if s.clientSrc != nil { // the client's selection is replaced
		s.event(s.clientSrc, "cancelled")
		s.clientSrc = nil
	}
	off := s.newObj(s.byName["wl_data_offer"], 3)
	off.ext = true
	s.extOffer = off
	s.event(s.dd, "data_offer", off)
	for _, m := range []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/uri-list"} {
		s.event(off, "offer", m)
	}
	s.event(s.dd, "selection", off)
}

// pasteFromClient asks the client's source for mime (default
// text/plain;charset=utf-8) and reports what it wrote.
func (s *fakeComp) pasteFromClient(mime string) {
	if s.clientSrc == nil {
		s.say("PASTED none")
		return
	}
	if mime == "" {
		mime = "text/plain;charset=utf-8"
	}
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_CLOEXEC); err != nil {
		s.say("WARN pipe: %v", err)
		return
	}
	s.event(s.clientSrc, "send", mime, fdv(p[1]))
	syscall.Close(p[1])
	go func() {
		f := os.NewFile(uintptr(p[0]), "r")
		data, _ := io.ReadAll(f)
		f.Close()
		h := fnv.New32a()
		h.Write(data)
		s.say("PASTED %d %08x", len(data), h.Sum32())
	}()
}
