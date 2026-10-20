package app

import (
	"bytes"
	"image"
	"image/png"
	"math"

	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/ui"
)

// Drag and drop (phase 6, N5; ui/drag.go and platform/drag.go have the
// contracts).
//
// In. The platform asks the application, synchronously and in the middle of
// the system's drag call, which operation a drag over the window gets
// (dragAnswer, which asks ui.Context.DropAt: the topmost target of the last
// finished frame), and queues EvDragEnter, EvDragMove, EvDragLeave and EvDrop,
// which onDrag puts in the ui.Input (Input.Drop) for the next frame, where
// ui.Context.DropSignal and Drop report them. The window registers for drags
// only while a target is declared (syncDrag).
//
// Out. A box asks for a drag with ui.Context.StartDrag during Build; after the
// frame (flushDrag, a safe point) the request is turned into a platform.DragSpec
// and started. The picture is the spec's, or a snapshot of the box: the quads
// of the finished frame that touch the box's rectangle, drawn again offscreen
// and encoded as PNG. The system takes the mouse then, so the press is
// cancelled at once (no release will come), and EvDragEnd tells ui when and
// how the drag ended.

// dragTarget is the part of platform.Window that drags use.
type dragTarget interface {
	SetDropKinds(platform.DragKinds)
	StartDrag(*platform.DragSpec) (platform.DragTicket, error)
}

// maxSnapshotPixels bounds a drag picture: a box larger than this is dragged
// without one (the system draws none, or a file's icon).
const maxSnapshotPixels = 2048 * 2048

func (a *App) dragWin() dragTarget {
	if a.dragT != nil {
		return a.dragT
	}
	if a.Win == nil || !platform.NativeDrag {
		return nil
	}
	return a.Win
}

// initDrag connects the window to the UI's drops: the handler that answers the
// system, and the system's drag threshold.
func (a *App) initDrag() {
	a.UI.SetDragThreshold(platform.DragThreshold())
	if a.Win != nil {
		a.Win.SetDragHandler(a.dragAnswer)
	}
}

// dragAnswer is the platform's drag handler: the operation and the kinds taken
// at a position, from the last finished frame.
func (a *App) dragAnswer(x, y float32, offered platform.DragKinds, allowed platform.DragOp) platform.DragAnswer {
	op, accept := a.UI.DropAt(x, y, ui.DropKinds(offered), ui.DropOp(allowed))
	return platform.DragAnswer{Op: platform.DragOp(op), Accept: platform.DragKinds(accept)}
}

// onDrag handles the drag events: they go into the next frame's input. txt is
// the data of an EvDrop.
func (a *App) onDrag(e *platform.Event, txt []byte) {
	d := &a.tr.in.Drop
	switch e.Kind {
	case platform.EvDragEnter, platform.EvDragMove:
		d.SetDragOver(e.X, e.Y, ui.DropKinds(e.Code), ui.DropOp(e.SelStart))
	case platform.EvDragLeave:
		d.ClearDragOver()
	case platform.EvDrop:
		d.SetDrop(ui.DropKinds(e.Code), txt, e.X, e.Y, int(e.SelStart), e.Flags&platform.FlagFailed == 0)
	case platform.EvDragEnd:
		d.EndDrag(ui.DropOp(e.Code))
	}
	a.tr.touch(e)
	a.p.invalidate()
}

// flushDrag runs after a frame is presented: the window registers for the kinds
// the frame's targets take, and a drag out the frame asked for is started.
func (a *App) flushDrag() {
	win := a.dragWin()
	if win == nil {
		return
	}
	win.SetDropKinds(platform.DragKinds(a.UI.DropKinds()))
	if req, ok := a.UI.TakeDragStart(); ok {
		a.UI.DragStarted(a.startDrag(win, req))
	}
}

// startDrag starts the system drag for req and cancels the press it began
// with. It reports whether the drag started.
func (a *App) startDrag(win dragTarget, req ui.DragStart) bool {
	s := &req.Spec
	spec := platform.DragSpec{Ops: platform.DragOp(s.Ops)}
	for _, p := range s.Files {
		if p != "" {
			spec.Items = append(spec.Items, platform.DragItem{Path: p})
		}
	}
	if s.Text != "" {
		spec.Items = append(spec.Items, platform.DragItem{Text: s.Text})
	}
	if len(s.Image) > 0 {
		spec.Items = append(spec.Items, platform.DragItem{PNG: s.Image})
	}
	for i := range s.Promises {
		spec.Items = append(spec.Items, platform.DragItem{Promise: &s.Promises[i]})
	}
	pic := s.Preview
	if len(pic) == 0 {
		pic = a.snapshot(req.Box)
	}
	if len(pic) > 0 {
		spec.Preview = &platform.DragPreview{PNG: pic, Rect: req.Box}
	}
	if _, err := win.StartDrag(&spec); err != nil {
		return false
	}
	// The system has the mouse: no release arrives. The press ends here, so
	// that nobody is clicked, and the next frame sees it.
	a.tr.in.Cancel(ui.ButtonLeft)
	a.tr.dirty = true
	a.p.invalidate()
	return true
}

// snapshot is a PNG of the window's pixels in rect (x0, y0, x1, y1): the quads
// of the last drawn frame that touch it, moved to the origin and drawn into an
// offscreen target. Nil when there is no renderer, the rectangle is empty or
// too large, or the encoding fails. The renderer is the window's: it is used
// between frames, on the main thread.
func (a *App) snapshot(rect [4]float32) []byte {
	if a.R == nil {
		return nil
	}
	x0, y0 := int(math.Floor(float64(max(rect[0], 0)))), int(math.Floor(float64(max(rect[1], 0))))
	x1, y1 := int(math.Ceil(float64(min(rect[2], float32(a.w))))), int(math.Ceil(float64(min(rect[3], float32(a.h)))))
	w, h := x1-x0, y1-y0
	if w <= 0 || h <= 0 || w*h > maxSnapshotPixels {
		return nil
	}
	dx, dy := float32(x0), float32(y0)
	var sub gpu.DrawList
	for i := range a.list.Quads {
		q := a.list.Quads[i]
		if q.Rect[2] <= rect[0] || q.Rect[0] >= rect[2] || q.Rect[3] <= rect[1] || q.Rect[1] >= rect[3] {
			continue
		}
		q.Rect = [4]float32{q.Rect[0] - dx, q.Rect[1] - dy, q.Rect[2] - dx, q.Rect[3] - dy}
		if q.Clip != ([4]float32{}) {
			q.Clip = [4]float32{q.Clip[0] - dx, q.Clip[1] - dy, q.Clip[2] - dx, q.Clip[3] - dy}
		}
		sub.Add(q)
	}
	if len(sub.Quads) == 0 {
		return nil
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	a.R.RenderToRGBA(w, h, [4]float32{}, &sub, img.Pix)
	var b bytes.Buffer
	if png.Encode(&b, img) != nil {
		return nil
	}
	return b.Bytes()
}
