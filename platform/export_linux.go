//go:build linux

package platform

import (
	"strconv"
	"unsafe"

	"gonativeui/internal/wl"
)

// Window handles for portal dialog parents (D31).
//
// X11: "x11:<XID>", the window id in hexadecimal, known at once.
//
// Wayland: xdg-foreign exports the toplevel (zxdg_exporter_v2.export_toplevel)
// and the compositor answers with a handle string in zxdg_exported_v2.handle,
// asynchronously, so the export starts with the first ExternalHandle call and
// the handle is available after a later Wait has run. The export lasts until
// the window closes. Without the protocol there is never a handle.
//
// Opcodes: zxdg_exporter_v2: destroy 0, export_toplevel 1;
// zxdg_exported_v2: destroy 0, handle 0 (event).

// ExternalHandle returns the string a portal request takes as parent_window:
// "wayland:<handle>" once the export has completed, or "x11:<XID>". The
// Wayland export is made lazily by the first call, which returns false; ask
// again after the event loop has run. Main thread.
func (w *Window) ExternalHandle() (string, bool) {
	if !w.open {
		return "", false
	}
	if !w.wl {
		return "x11:" + strconv.FormatUint(uint64(w.x.win), 16), true
	}
	y := &w.wy
	if y.handle != "" {
		return y.handle, true
	}
	if y.exporter != 0 && y.exported == 0 {
		y.args[0], y.args[1] = 0, uint64(y.surface)
		y.exported = w.req(y.exporter, 1, wl.ZxdgExportedV2.Addr(), 1, 0)
		listen(y.exported)
		w.wlFlush()
	}
	return "", false
}

// onExported handles zxdg_exported_v2.handle(handle).
func (w *Window) onExported(op uint32, a unsafe.Pointer) {
	if op != 0 {
		return
	}
	p := argP(a, 0)
	var b []byte
	for ; *(*byte)(ptr(p)) != 0; p++ {
		b = append(b, *(*byte)(ptr(p)))
	}
	w.wy.handle = "wayland:" + string(b)
}

// wlExportClose releases the export.
func (w *Window) wlExportClose() {
	y := &w.wy
	w.release(y.exported, 0, 1)
	y.exported = 0
	w.release(y.exporter, 0, 1)
	y.exporter = 0
}
