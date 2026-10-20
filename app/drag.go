package app

import (
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
// and started. The picture is the spec's, or else the system's icon for the
// first item: a snapshot of the box would drain the GPU work of the frame
// synchronously (an asynchronous one is a follow-up). The system takes the
// mouse then, so the press is cancelled at once (no release will come), and
// EvDragEnd tells ui when and how the drag ended.
//
// A drop reaches the next frame's input; the drops that arrive while a frame
// still holds one wait in a bounded queue (maxQueuedDrops) and each gets a frame
// of its own, in order.

// dragTarget is the part of platform.Window that drags use.
type dragTarget interface {
	SetDropKinds(platform.DragKinds)
	StartDrag(*platform.DragSpec) (platform.DragTicket, error)
}

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
	op, accept, target := a.UI.DropAt(x, y, ui.DropKinds(offered), ui.DropOp(allowed))
	return platform.DragAnswer{Op: platform.DragOp(op), Accept: platform.DragKinds(accept), Target: uint64(target)}
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
		a.tr.dropIn(ui.DropKinds(e.Code), txt, e.X, e.Y, int(e.SelStart), e.Flags&platform.FlagFailed == 0, ui.Key(e.Tag))
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
	if len(s.Preview) > 0 {
		spec.Preview = &platform.DragPreview{PNG: s.Preview, Rect: req.Box}
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

// maxQueuedDrops bounds the drops waiting for a frame; one more is lost.
const maxQueuedDrops = 8

// queuedDrop is a drop that waits for its frame; its data is at buf[off:off+n].
type queuedDrop struct {
	kind   ui.DropKinds
	x, y   float32
	items  int
	ok     bool
	target ui.Key
	off, n int
}

// dropQueue holds the drops that arrived while the input of the next frame
// had one already (Input.Drop holds one drop a frame).
type dropQueue struct {
	q    []queuedDrop
	buf  []byte // their data, kept until the queue is empty
	lost int    // drops that did not fit
}

// dropIn takes a drop for the next frame's input, or queues it behind the one
// that is there. The data is bounded in all by ui.MaxKeptPaste: a drop that
// would pass it is queued failed.
func (t *translator) dropIn(kind ui.DropKinds, data []byte, x, y float32, items int, ok bool, target ui.Key) {
	d := &t.in.Drop
	q := &t.drops
	if !d.Has && len(q.q) == 0 {
		d.SetDrop(kind, data, x, y, items, ok)
		d.Target = target
		return
	}
	if len(q.q) >= maxQueuedDrops {
		q.lost++
		return
	}
	if !ok || len(q.buf)+len(data) > ui.MaxKeptPaste {
		data, ok = nil, false
	}
	q.q = append(q.q, queuedDrop{kind: kind, x: x, y: y, items: items, ok: ok, target: target, off: len(q.buf), n: len(data)})
	q.buf = append(q.buf, data...)
}

// nextDrop puts the oldest queued drop into the input, after a frame took the
// one before it, and owes a frame for it.
func (t *translator) nextDrop() {
	q := &t.drops
	if len(q.q) == 0 {
		return
	}
	r := q.q[0]
	t.in.Drop.SetDrop(r.kind, q.buf[r.off:r.off+r.n], r.x, r.y, r.items, r.ok)
	t.in.Drop.Target = r.target
	q.q = q.q[:copy(q.q, q.q[1:])]
	if len(q.q) == 0 {
		q.buf = q.buf[:0]
	}
	t.dirty, t.refed = true, true
}
