//go:build linux

package platform

import "slices"

// Wayland window decorations. The compositor decides who draws them:
// zxdg_toplevel_decoration_v1.configure says server_side or client_side, at
// the start and whenever it changes, and a compositor without the protocol
// (GNOME, Weston) leaves the drawing to the client. ClientDecorations reports
// the result; the app draws the titlebar, buttons and borders when it is true
// (the theme's Chrome does, later) and the platform supplies the protocol half:
//
//	window geometry   SetShadowInsets: the part of the surface outside the
//	                  visible window (shadows); xdg_surface.set_window_geometry
//	move              a press in a drag region (SetDragRegions): xdg_toplevel.move
//	resize            a press in a resize region (SetResizeRegions):
//	                  xdg_toplevel.resize with the region's edges
//	maximize          a double click in a drag region, or ToggleMaximize
//	minimize          Minimize
//	close             RequestClose queues EvClose, as the compositor's close does
//
// A change of the mode or of the window state (maximized, fullscreen, tiled) is
// applied at the xdg_surface.configure that carries it and reported as EvWake,
// after which ClientDecorations and Maximized have the new values. (A dedicated
// event kind would need platform.go.)
//
// Opcodes: xdg_surface.set_window_geometry 3; xdg_toplevel: move 5, resize 6,
// set_maximized 9, unset_maximized 10, set_minimized 13.

// ResizeEdges are the window edges a press in a ResizeRegion drags; the values
// are xdg_toplevel's resize_edge, so a corner is the sum of two edges.
type ResizeEdges uint8

const (
	// EdgeTop is the top edge.
	EdgeTop ResizeEdges = 1 << iota
	// EdgeBottom is the bottom edge.
	EdgeBottom
	// EdgeLeft is the left edge.
	EdgeLeft
	// EdgeRight is the right edge.
	EdgeRight
)

// ResizeRegion is a rectangle of the window (pixels, origin top left) where a
// left press starts an interactive resize by Edges: the border band of a
// client-side decoration, including the corners. The last region containing
// the point wins; Edges 0 makes it a rectangle that keeps its presses (content
// laid over a border).
type ResizeRegion struct {
	// Rect is x0, y0, x1, y1 in pixels, origin top left.
	Rect [4]float32
	// Edges are the window edges a press drags; a corner is two edges.
	Edges ResizeEdges
}

const (
	// defaultResizeBand is the logical width of the invisible resize border
	// used while the app has set no resize regions: a window that the
	// compositor leaves undecorated could not be resized at all otherwise.
	defaultResizeBand = 6
	doubleClickMs     = 400 // two presses this close (wl_pointer time) maximize
	doubleClickPx     = 6   // ... and this close, logical pixels
)

// SetResizeRegions sets the window's resize regions (see ResizeRegion). Only
// used while the decorations are client-side. The list is copied.
func (w *Window) SetResizeRegions(r []ResizeRegion) {
	if !slices.Equal(w.resizes, r) {
		w.resizes = append(w.resizes[:0], r...)
		w.cursorSync(false)
	}
}

// ClientDecorations reports whether the app has to draw the window's titlebar,
// buttons and borders: the compositor configured client_side, or has no
// decoration protocol. It is false on X11, where the window manager decorates.
func (w *Window) ClientDecorations() bool {
	return w.wl && w.wlClientDeco()
}

func (w *Window) wlClientDeco() bool {
	y := &w.wy
	if y.deco == 0 {
		return true
	}
	return y.decoMode == 1
}

// Maximized reports whether the compositor has the window maximized.
func (w *Window) Maximized() bool { return w.wl && w.wy.maximized }

// Minimize asks the compositor to minimize the window. (No-op on X11, whose
// window manager draws the buttons.)
func (w *Window) Minimize() {
	if y := &w.wy; w.wl && y.toplevel != 0 {
		w.req(y.toplevel, 13, 0, y.toplevelV, 0)
		w.wlFlush()
	}
}

// ToggleMaximize maximizes the window, or restores it when it is maximized.
func (w *Window) ToggleMaximize() {
	y := &w.wy
	if !w.wl || y.toplevel == 0 {
		return
	}
	if y.maximized {
		w.req(y.toplevel, 10, 0, y.toplevelV, 0)
	} else {
		w.req(y.toplevel, 9, 0, y.toplevelV, 0)
	}
	w.wlFlush()
}

// RequestClose queues an EvClose for the next Wait, as if the user had asked
// the compositor or window manager to close the window: the app's close button
// calls it and the app answers it like any other close.
func (w *Window) RequestClose() { w.closeReq = true }

// SetShadowInsets declares how much of the surface lies outside the visible
// window on each side (logical pixels): the app draws its shadow there. The
// compositor's sizes and the maximized and tiled states refer to the visible
// window, so Size includes the insets and set_window_geometry names the rest.
// Set them to zero while maximized or fullscreen.
func (w *Window) SetShadowInsets(left, top, right, bottom float32) {
	y := &w.wy
	if !w.wl {
		return
	}
	n := [4]int32{int32(left + 0.5), int32(top + 0.5), int32(right + 0.5), int32(bottom + 0.5)}
	if n == y.insets {
		return
	}
	// The surface keeps the visible window's size and gains the new insets.
	w.lw += n[0] + n[2] - y.insets[0] - y.insets[2]
	w.lh += n[1] + n[3] - y.insets[1] - y.insets[3]
	y.insets = n
	w.wlApplyGeometry(false)
	w.wlSendGeometry()
	w.wlFlush()
}

// wlSendGeometry tells the compositor which part of the surface is the window.
func (w *Window) wlSendGeometry() {
	y := &w.wy
	if y.insets == [4]int32{} && !y.geomSent {
		return
	}
	y.geomSent = true
	y.args[0], y.args[1] = uint64(uint32(y.insets[0])), uint64(uint32(y.insets[1]))
	y.args[2], y.args[3] = uint64(uint32(w.lw-y.insets[0]-y.insets[2])), uint64(uint32(w.lh-y.insets[1]-y.insets[3]))
	w.req(y.xdgSurface, 3, 0, y.xdgSurfaceV, 0)
}

func (w *Window) wlFlush() { fr.Call1(wlf.flush, w.wy.display) }

// applyDecoState applies the mode and window state of the configure being
// acknowledged and reports a change as EvWake.
func (w *Window) applyDecoState() {
	y := &w.wy
	changed := y.decoMode != y.pendDeco || y.maximized != y.pendMax || y.fullscreen != y.pendFull || y.tiled != y.pendTiled
	y.decoMode, y.maximized, y.fullscreen, y.tiled = y.pendDeco, y.pendMax, y.pendFull, y.pendTiled
	if changed && y.configured {
		w.q.push(Event{Kind: EvWake, Time: nanotime()})
		w.cursorSync(false)
	}
}

// resizeAt is the edges a left press at pixel (x, y) drags: the app's regions,
// else the default border band; none while maximized, fullscreen or tiled.
func (w *Window) resizeAt(x, y float32) ResizeEdges {
	s := &w.wy
	if s.maximized || s.fullscreen || s.tiled || !w.wlClientDeco() {
		return 0
	}
	if len(w.resizes) > 0 {
		for i := len(w.resizes) - 1; i >= 0; i-- {
			if inRect(w.resizes[i].Rect, x, y) {
				return w.resizes[i].Edges
			}
		}
		return 0
	}
	band := float32(defaultResizeBand) * float32(w.s120) / 120
	var e ResizeEdges
	switch {
	case y < band:
		e |= EdgeTop
	case y >= float32(w.ph)-band:
		e |= EdgeBottom
	}
	switch {
	case x < band:
		e |= EdgeLeft
	case x >= float32(w.pw)-band:
		e |= EdgeRight
	}
	return e
}

// wlEdgeShape is the resize cursor for a pointer over a client-side window
// edge, or the arrow.
func (w *Window) wlEdgeShape(x, y float32) shape {
	switch w.resizeAt(x, y) {
	case EdgeTop:
		return shEdgeN
	case EdgeBottom:
		return shEdgeS
	case EdgeLeft:
		return shEdgeW
	case EdgeRight:
		return shEdgeE
	case EdgeTop | EdgeLeft:
		return shEdgeNW
	case EdgeTop | EdgeRight:
		return shEdgeNE
	case EdgeBottom | EdgeLeft:
		return shEdgeSW
	case EdgeBottom | EdgeRight:
		return shEdgeSE
	}
	return shArrow
}

// wlPress handles a left press with client-side decorations; it reports
// whether the press was taken (resize, move, or maximize) and is not for the
// app.
func (w *Window) wlPress(serial, timeMs uint32) bool {
	y := &w.wy
	if !w.wlClientDeco() {
		return false
	}
	if e := w.resizeAt(w.ptrX, w.ptrY); e != 0 {
		y.args[0], y.args[1], y.args[2] = uint64(y.seat), uint64(serial), uint64(e)
		w.req(y.toplevel, 6, 0, y.toplevelV, 0) // resize
		return true
	}
	scale := float32(w.s120) / 120
	if !w.dragsAt(w.ptrX, w.ptrY, scale) {
		return false
	}
	near := func(a, b float32) bool { d := a - b; return d < doubleClickPx*scale && d > -doubleClickPx*scale }
	if y.lastDragOK && timeMs-y.lastDragMs <= doubleClickMs && near(w.ptrX, y.lastDragX) && near(w.ptrY, y.lastDragY) {
		y.lastDragOK = false
		w.ToggleMaximize()
		return true
	}
	y.lastDragOK, y.lastDragMs, y.lastDragX, y.lastDragY = true, timeMs, w.ptrX, w.ptrY
	y.args[0], y.args[1] = uint64(y.seat), uint64(serial)
	w.req(y.toplevel, 5, 0, y.toplevelV, 0) // move
	return true
}
