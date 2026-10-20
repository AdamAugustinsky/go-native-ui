package ui

import (
	"math"
	"slices"

	"gonativeui/platform"
)

// Drag and drop (phase 6, N5; platform/drag.go has the system contract).
//
// Drop targets. A box declares that it takes drags with AcceptDrop, every
// frame it should: the kinds of content (files, an image, text) and the
// operations it can do. While a drag from another application is over the
// window, the topmost declared box under it that takes the kinds the drag
// offers is the target: DropSignal(id) says so (Over) with the position, and
// the component draws its highlight from that, as it draws a hover. The
// system asks for the operation at every move, before any frame can run, so
// the answer comes from the last finished frame's layout (DropAt), the way
// hit testing uses the previous frame's. A target that is under a modal, a
// popup, a hidden box or outside a clip is not found. Declared boxes need no
// hit flags, and a disabled one takes nothing.
//
// A drop is delivered to the box that was the target at the pointer:
// DropSignal(id).Dropped is true for one frame, and Context.Drop returns the
// data (valid for that frame). The data is bounded like a paste
// (MaxKeptPaste) and kept in the Input between frames without allocating once
// its storage has grown.
//
// Drag out. StartDrag is called from a box's build once DragExceeded says the
// press became a drag (the system's drag threshold). The app takes the
// request after the frame (TakeDragStart) and starts the system drag, with a
// snapshot of the box as its picture unless the spec has one. The system
// takes the mouse then, so the press is cancelled: no Released, Clicked or
// DoubleClicked follows. DragEnd tells when the drag is over and what was done.
//
// A source inside a WindowDrag band must be Clickable (or Hoverable or
// Focusable): such boxes are Block regions of the window-drag map already, so
// a press on them is the app's and not the window's.

// DropKinds is a set of kinds of dragged content; DropOp an operation, or a
// set of them (what a target can do, what a source allows). They are the
// platform's types.
type (
	DropKinds = platform.DragKinds
	DropOp    = platform.DragOp
	// FilePromise is a file that does not exist yet, written when the
	// destination asks for it (see platform.FilePromise).
	FilePromise = platform.FilePromise
)

const (
	// DropFiles are file URLs, DropImage image data, DropText plain text.
	DropFiles = platform.DragFiles
	DropImage = platform.DragImage
	DropText  = platform.DragText
	// DropAny is every kind.
	DropAny = platform.DragAny

	// DropNone is no operation (the drop is refused), DropCopy copies,
	// DropLink links, DropMove moves, DropAllOps is the set of them.
	DropNone   = platform.DragOpNone
	DropCopy   = platform.DragOpCopy
	DropLink   = platform.DragOpLink
	DropMove   = platform.DragOpMove
	DropAllOps = platform.DragOpAll
)

// DropSpec is what a drop target takes.
type DropSpec struct {
	// Kinds are the kinds of content the box takes. A drag is taken by the
	// box when it offers one of them; the data of the drop is the first of
	// files, image and text that the drag offers and Kinds has.
	Kinds DropKinds
	// Ops are the operations the box can do, in order of preference copy,
	// link, move; the answer is the first the source allows. Zero is copy.
	Ops DropOp
}

// DropSignal is what a drop target reports for the frame.
type DropSignal struct {
	// Over: a drag from another application that this box takes is over it,
	// and nothing above it takes the drag instead. Draw the highlight from it.
	Over bool
	// Dropped: the drag was dropped on this box this frame; Context.Drop has
	// the data.
	Dropped bool
	// Op is, with Over, the operation answered to the system (copy, link).
	Op DropOp
	// Offered is, with Over, the kinds the drag offers.
	Offered DropKinds
	// Pos is, with Over or Dropped, the pointer relative to the box's top-left
	// corner, in pixels.
	Pos [2]float32
}

// DragState is the drag from another application over the window, as the
// platform last reported it (Input.Drop).
type DragState struct {
	// Active: a drag is over the window; X, Y is where (pixels from the top
	// left), Offered the kinds it offers and Allowed the operations its
	// source allows.
	Active  bool
	X, Y    float32
	Offered DropKinds
	Allowed DropOp
}

// DropInput is the drag and drop part of one frame of Input, filled by the app
// from the platform's drag events.
type DropInput struct {
	// Drag is the drag over the window; it stays until the platform says it
	// left or was dropped (state, not an edge).
	Drag DragState
	// Has: a drop arrived this frame, of Kind, at X, Y (pixels), with Items
	// dragged items. Data is its data (Failed: it could not be read or was
	// over MaxKeptPaste, and Data is empty); set it with SetDrop, which reuses
	// Data's storage.
	Has    bool
	Failed bool
	Kind   DropKinds
	X, Y   float32
	Items  int
	Data   []byte
	// Ended: the drag this app started ended this frame, with operation
	// EndOp (DropNone: cancelled); set it with EndDrag.
	Ended bool
	EndOp DropOp
}

// SetDragOver records the drag over the window (EvDragEnter, EvDragMove).
func (d *DropInput) SetDragOver(x, y float32, offered DropKinds, allowed DropOp) {
	d.Drag = DragState{Active: true, X: x, Y: y, Offered: offered, Allowed: allowed}
}

// ClearDragOver records that the drag left the window (EvDragLeave).
func (d *DropInput) ClearDragOver() { d.Drag = DragState{} }

// SetDrop records a drop, which ends the drag over the window. The data is
// copied into Data, reusing its capacity; ok false is a drop whose data could
// not be read.
func (d *DropInput) SetDrop(kind DropKinds, b []byte, x, y float32, items int, ok bool) {
	d.Drag = DragState{}
	d.Has, d.Failed, d.Kind, d.X, d.Y, d.Items = true, !ok, kind, x, y, items
	if !ok {
		d.Data = d.Data[:0]
		return
	}
	d.Data = append(d.Data[:0], b...)
}

// EndDrag records that the app's drag out ended with operation op.
func (d *DropInput) EndDrag(op DropOp) { d.Ended, d.EndOp = true, op }

// clearFrame clears the per-frame parts (ClearFrame).
func (d *DropInput) clearFrame() {
	d.Has, d.Failed, d.Ended = false, false, false
	if cap(d.Data) > MaxKeptPaste { // do not hold on to more than a supported drop
		d.Data = nil
	}
	d.Data = d.Data[:0]
}

// ReleaseDrop drops the storage of dropped data; the next drop grows it again.
func (d *DropInput) ReleaseDrop() { d.Data = nil }

// DropData is a drop, as Context.Drop returns it.
type DropData struct {
	// Kind is the one kind delivered: DropFiles, DropImage or DropText.
	Kind DropKinds
	// Data is the content, valid for the frame it is returned in: for
	// DropFiles the paths each ended by a NUL byte (see Paths), for DropImage
	// a PNG, for DropText UTF-8.
	Data []byte
	// Failed: the data could not be read, or was over MaxKeptPaste; Data is
	// empty.
	Failed bool
	// Items is the number of items dragged.
	Items int
	// Pos is where it was dropped, in pixels from the window's top left.
	Pos [2]float32
}

// Paths appends the dropped paths to dst and returns it (DropFiles). A name
// that is not valid UTF-8 comes through unchanged.
func (d DropData) Paths(dst []string) []string {
	if d.Kind != DropFiles {
		return dst
	}
	b := d.Data
	for len(b) > 0 {
		i := slices.Index(b, 0)
		if i < 0 {
			i = len(b)
		}
		dst = append(dst, string(b[:i]))
		if i == len(b) {
			break
		}
		b = b[i+1:]
	}
	return dst
}

// Text is the dropped text (DropText), or "".
func (d DropData) Text() string {
	if d.Kind != DropText {
		return ""
	}
	return string(d.Data)
}

type dropRec struct {
	id    ID
	key   Key
	kinds DropKinds
	ops   DropOp
}

// DragSpec describes a drag out.
type DragSpec struct {
	// Files are paths of existing files and folders; each is one dragged item.
	Files []string
	// Text is plain text, one item.
	Text string
	// Image is a PNG image, one item.
	Image []byte
	// Promises are files that do not exist yet: each is written on a worker
	// when the destination takes the drop.
	Promises []FilePromise
	// Ops are the operations the source allows; zero is copy.
	Ops DropOp
	// Preview is a PNG picture dragged along with the pointer; nil makes the
	// app use a snapshot of the box. Its pixels are at the window's scale.
	Preview []byte
}

// DragStart is a request to start a drag out: the source box's rectangle in
// window pixels, and the spec.
type DragStart struct {
	// Box is the source box's rectangle in window pixels (x0, y0, x1, y1).
	Box [4]float32
	// Spec is what to drag.
	Spec DragSpec
}

// dropState is the context's drag and drop state.
type dropState struct {
	recs  []dropRec
	stamp uint32 // the frame that declared recs

	// What Begin resolved from the input and the last finished layout.
	over        Key
	overOp      DropOp
	overOffered DropKinds
	overPos     [2]float32
	hit         Key
	hitPos      [2]float32
	data        DropData
	ended       bool
	endOp       DropOp

	// Drag out.
	req       DragStart
	pending   bool // StartDrag was called and the app has not taken it
	running   bool // taken: the system's drag is in progress
	threshold float32

	best, cur []uint32 // draw paths, for finding the topmost target
}

// SetDragThreshold sets how far, in points, the pointer moves with the button
// held before DragExceeded says a press became a drag. The app sets the
// system's (platform.DragThreshold); the default is 4.
func (c *Context) SetDragThreshold(points float32) { c.drop.threshold = points }

// DragThreshold is the drag threshold in pixels.
func (c *Context) DragThreshold() float32 {
	t := c.drop.threshold
	if t <= 0 {
		t = 4
	}
	return t * max(c.scale, 1)
}

// AcceptDrop declares box id a drop target for this frame. id must have been
// built in this frame. Call it every frame the box should take drags.
func (c *Context) AcceptDrop(id ID, s DropSpec) {
	if id == 0 || s.Kinds&DropAny == 0 {
		return
	}
	d := &c.drop
	if d.stamp != c.frame {
		d.stamp, d.recs = c.frame, d.recs[:0]
	}
	d.recs = append(d.recs, dropRec{id: id, key: c.boxes[id].key, kinds: s.Kinds & DropAny, ops: s.Ops})
}

// DropKinds is the union of the kinds the targets of the last finished frame
// take: what the window must register for. Between frames.
func (c *Context) DropKinds() DropKinds {
	d := &c.drop
	if d.stamp != c.frame {
		return 0
	}
	var k DropKinds
	for i := range d.recs {
		k |= d.recs[i].kinds
	}
	return k
}

// DropAt is the answer to a drag over the window at (x, y), pixels from the
// top left, that offers the kinds in offered and whose source allows the
// operations in allowed: the topmost target of the last finished frame that
// takes any of the kinds. Op is DropNone, with no Accept, when there is none.
// It is what the platform's drag handler calls; between frames, allocation
// free.
func (c *Context) DropAt(x, y float32, offered DropKinds, allowed DropOp) (op DropOp, accept DropKinds) {
	r := c.dropFind(x, y, offered, false)
	if r == nil {
		return DropNone, 0
	}
	return platform.Pick(r.ops, allowed), r.kinds
}

// dropFind finds the topmost target at (x, y) that takes one of the kinds
// offered. atBegin: called from Begin, when the overlays of the finished frame
// are ovPrev; otherwise they are ovs.
func (c *Context) dropFind(x, y float32, offered DropKinds, atBegin bool) *dropRec {
	d := &c.drop
	want := c.frame // between frames the finished frame is the current one
	if atBegin {
		want = c.frame - 1
	}
	if d.stamp != want || c.root == 0 {
		return nil
	}
	ovs, n := &c.ovs, c.nOvs
	if atBegin {
		ovs, n = &c.ovPrev, c.nOvPrev
	}
	var best *dropRec
	for i := range d.recs {
		r := &d.recs[i]
		if r.kinds&offered == 0 || int(r.id) >= len(c.boxes) {
			continue
		}
		b := &c.boxes[r.id]
		if b.key != r.key || b.flags&Disabled != 0 || c.hiddenByLayout(r.id) {
			continue
		}
		if cr := c.clippedRect(r.id); x < cr[0] || x >= cr[2] || y < cr[1] || y >= cr[3] {
			continue
		}
		if dropCovered(ovs, n, b.ovl, x, y, c) {
			continue
		}
		d.cur = c.appendDrawPath(d.cur[:0], b)
		if best == nil || slices.Compare(d.cur, d.best) >= 0 { // the later in draw order is on top
			best = r
			d.best = append(d.best[:0], d.cur...)
		}
	}
	return best
}

// dropCovered reports whether an overlay above layer ovl (0: the main tree,
// i+1: overlay i) covers the point: any modal, or a popup or tooltip whose
// rectangle contains it.
func dropCovered(ovs *[maxOverlays]overlay, n int, ovl uint8, x, y float32, c *Context) bool {
	for i := int(ovl); i < n; i++ {
		o := &ovs[i]
		if o.kind == OverlayModal {
			return true
		}
		if r := c.boxes[o.id].rect; x >= r[0] && x < r[2] && y >= r[1] && y < r[3] {
			return true
		}
	}
	return false
}

// dropBegin resolves, at Begin, what the input says about drags: which target
// the drag over the window is on, where a drop went, and whether the app's own
// drag ended. The layout is the previous frame's, as for the pointer.
func (c *Context) dropBegin() {
	d := &c.drop
	in := &c.in.Drop
	d.over, d.overOp, d.overOffered, d.hit, d.data, d.ended = 0, DropNone, 0, 0, DropData{}, false
	if in.Ended {
		d.running, d.ended, d.endOp = false, true, in.EndOp
	}
	switch {
	case in.Has:
		if r := c.dropFind(in.X, in.Y, in.Kind, true); r != nil {
			d.hit = r.key
			d.hitPos = c.dropRel(r, in.X, in.Y)
			d.data = DropData{Kind: in.Kind, Data: in.Data, Failed: in.Failed, Items: in.Items, Pos: [2]float32{in.X, in.Y}}
		}
	case in.Drag.Active:
		if r := c.dropFind(in.Drag.X, in.Drag.Y, in.Drag.Offered, true); r != nil {
			d.over, d.overOp, d.overOffered = r.key, platform.Pick(r.ops, in.Drag.Allowed), in.Drag.Offered
			d.overPos = c.dropRel(r, in.Drag.X, in.Drag.Y)
		}
	}
}

func (c *Context) dropRel(r *dropRec, x, y float32) [2]float32 {
	b := &c.boxes[r.id]
	return [2]float32{x - b.rect[0], y - b.rect[1]}
}

// DropSignal reports what drags did to the drop target id this frame. A box
// that is not a target, or not built, reports the zero DropSignal.
func (c *Context) DropSignal(id ID) DropSignal {
	var s DropSignal
	d := &c.drop
	if id == 0 || (d.over == 0 && d.hit == 0) {
		return s
	}
	k := c.boxes[id].key
	switch k {
	case d.over:
		s = DropSignal{Over: true, Op: d.overOp, Offered: d.overOffered, Pos: d.overPos}
	case d.hit:
		s = DropSignal{Dropped: true, Pos: d.hitPos}
	}
	return s
}

// Drop is the data of the drop of this frame (see DropSignal.Dropped). It is
// valid for the frame; the zero DropData when nothing was dropped on a target.
func (c *Context) Drop() DropData { return c.drop.data }

// DragActive reports whether a drag from another application is over the
// window (Input.Drop.Drag).
func (c *Context) DragActive() bool { return c.in.Drop.Drag.Active }

// DragExceeded reports whether the press on the box s describes (Dragging)
// has moved past the drag threshold: time to call StartDrag. It is false while
// a drag out is pending or running.
func (c *Context) DragExceeded(s Signal) bool {
	d := &c.drop
	if !s.Dragging || d.pending || d.running {
		return false
	}
	t := c.DragThreshold()
	dx, dy := float64(s.DragDelta[0]), float64(s.DragDelta[1])
	return math.Sqrt(dx*dx+dy*dy) >= float64(t)
}

// StartDrag asks to start a drag out from box id with the spec. It reports
// false, doing nothing, if the spec has nothing to drag or one is pending or
// running. The request is carried out after the frame (TakeDragStart); the
// spec's slices must stay as they are until then.
func (c *Context) StartDrag(id ID, s DragSpec) bool {
	d := &c.drop
	if id == 0 || d.pending || d.running {
		return false
	}
	if len(s.Files) == 0 && s.Text == "" && len(s.Image) == 0 && len(s.Promises) == 0 {
		return false
	}
	d.req = DragStart{Box: c.boxes[id].rect, Spec: s}
	d.pending = true
	return true
}

// TakeDragStart returns the drag out asked for in the last frame and marks it
// running; the app calls DragStarted with the outcome. It is the app's.
func (c *Context) TakeDragStart() (DragStart, bool) {
	d := &c.drop
	if !d.pending {
		return DragStart{}, false
	}
	r := d.req
	d.req, d.pending, d.running = DragStart{}, false, true
	return r, true
}

// DragStarted tells the context whether the system drag the app took with
// TakeDragStart started; when it did not, a new one can be asked for.
func (c *Context) DragStarted(ok bool) {
	if !ok {
		c.drop.running = false
	}
}

// DragEnd reports, for the frame in which the app's drag out ended, the
// operation the destination performed (DropNone: it was cancelled).
func (c *Context) DragEnd() (op DropOp, ended bool) { return c.drop.endOp, c.drop.ended }

// DragRunning reports whether the app's drag out is in progress (taken and
// not yet ended).
func (c *Context) DragRunning() bool { return c.drop.running }
