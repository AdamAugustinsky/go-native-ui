//go:build linux

package platform

import (
	"os"
	"path/filepath"
	"sync"
)

// Drag and drop on Linux (the contract is drag.go). The state and the events
// are shared by the two back ends: XDND on X11 (dnd_x11_linux.go) and the
// wl_data_device protocol on Wayland (dnd_wl_linux.go). Both run inside Wait on
// the main thread and never block: the drop's data is read like a paste's, from
// a property (INCR chunks included) or a pipe, and the EvDrop is queued when it
// has arrived, in the Wait that completes it.
//
// The pointer position of a drag is in physical pixels from the content's
// top-left. As on macOS and Windows the handler (SetDragHandler) answers at
// every enter and move, synchronously, from the application's last finished
// frame, and the answer goes back to the source (XdndStatus, wl_data_offer
// accept and set_actions) so that it shows the right cursor.
//
// Of what a drag offers, files are text/uri-list, an image is image/png and text
// is UTF8_STRING, text/plain;charset=utf-8, text/plain or (X11) STRING. A drag
// out offers the same from its spec. File promises and the drag picture are
// macOS's only so far (ErrPromiseUnsupported; no picture is drawn).

// NativeDrag reports whether the OS has native drag and drop: true here (a
// Wayland compositor without wl_data_device_manager version 3 refuses
// StartDrag with ErrDragUnsupported and offers no drops).
const NativeDrag = true

// dndState is the Window's drag and drop state.
type dndState struct {
	handler DragHandler
	kinds   DragKinds // what the window takes

	// The drag in progress over the window.
	inside  bool
	nDrags  uint64 // drags seen, which numbers them
	seq     uint64 // the number of the current one: the Tag of its events
	offered DragKinds
	allowed DragOp
	items   int
	lastX   float32 // where it was last answered
	lastY   float32

	// Drag out.
	out dndOut
}

// dndOut is a drag out in progress.
type dndOut struct {
	active  bool
	nTicket uint64
	ticket  DragTicket
	items   []DragItem
	ops     DragOp
	uris    []byte // the text/uri-list of the file items
}

// SetDragHandler sets the function that answers drags over the window (see
// DragHandler). Without one the window accepts every kind with the first
// operation the source allows. Main thread.
func (w *Window) SetDragHandler(h DragHandler) { w.dnd.handler = h }

// SetDropKinds sets the kinds of drag the window takes. The window takes no
// drag until it is called; calling it every frame with the same set costs a
// comparison. Main thread.
func (w *Window) SetDropKinds(k DragKinds) {
	k &= DragAny
	if k == w.dnd.kinds || !w.open {
		return
	}
	w.dnd.kinds = k
	if k == 0 {
		w.dndLeft()
	}
	if w.wl {
		w.wlDndKinds()
	} else {
		w.x11DndKinds()
	}
}

// StartDrag starts a drag out from the press that is being held (see
// DragSpec). It returns at once; EvDragEnd reports the end.
func (w *Window) StartDrag(s *DragSpec) (DragTicket, error) {
	if !w.open {
		return 0, ErrDragSpec
	}
	if err := s.Validate(); err != nil {
		return 0, err
	}
	if specHasPromise(s) {
		return 0, ErrPromiseUnsupported
	}
	o := &w.dnd.out
	if o.active {
		return 0, ErrDragBusy
	}
	if !w.leftDown {
		return 0, ErrNoPress
	}
	ops := s.Ops
	if ops == DragOpNone {
		ops = DragOpCopy
	}
	o.nTicket++
	o.ticket, o.items, o.ops = DragTicket(o.nTicket), s.Items, ops
	o.uris = appendURIList(o.uris[:0], ownFilePaths(s.Items))
	var err error
	if w.wl {
		err = w.wlDndStart()
	} else {
		err = w.x11DndStart()
	}
	if err != nil {
		*o = dndOut{nTicket: o.nTicket, uris: o.uris}
		return 0, err
	}
	o.active = true
	w.leftDown = false // the drag has the pointer: no release will come
	return o.ticket, nil
}

// dndEnd queues the end of the drag out with the operation performed.
func (w *Window) dndEnd(op DragOp) {
	o := &w.dnd.out
	if !o.active {
		return
	}
	w.q.push(Event{Kind: EvDragEnd, Tag: uint64(o.ticket), Code: uint32(op), Time: nanotime()})
	*o = dndOut{nTicket: o.nTicket, uris: o.uris[:0]}
}

// dndMove answers the drag at (x, y) and queues EvDragEnter, for the first
// answer of a drag, or EvDragMove. offered is what the drag has, allowed the
// operations the source allows and items how many things it carries. It
// returns the answer; a drag that offers nothing the window takes is left.
func (w *Window) dndMove(x, y float32, offered DragKinds, allowed DragOp, items int) DragAnswer {
	d := &w.dnd
	if offered&d.kinds == 0 {
		w.dndLeft()
		return DragAnswer{}
	}
	kind := EvDragMove
	if !d.inside {
		d.nDrags++
		d.seq, d.inside = d.nDrags, true
		kind = EvDragEnter
	}
	d.offered, d.allowed, d.items, d.lastX, d.lastY = offered, allowed, items, x, y
	ans := answerDrag(d.handler, x, y, offered, allowed)
	w.q.push(Event{Kind: kind, X: x, Y: y, Code: uint32(offered), SelStart: int32(allowed), SelLen: int32(items),
		Mods: w.mods, Tag: d.seq, Time: nanotime()})
	return ans
}

// dndLeft ends the drag over the window, if one is in progress.
func (w *Window) dndLeft() {
	if d := &w.dnd; d.inside {
		d.inside = false
		w.q.push(Event{Kind: EvDragLeave, Tag: d.seq, Time: nanotime()})
	}
}

// dndDrop asks the handler once more, at the last position, whether it takes
// the drop. If it does it returns the EvDrop to fill in and send (Code, X, Y,
// Tag and Mods are set) and the answer, and the drag is no longer over the
// window (no EvDragLeave follows); if it does not, the drag is left.
func (w *Window) dndDrop() (Event, DragAnswer, bool) {
	d := &w.dnd
	if !d.inside {
		return Event{}, DragAnswer{}, false
	}
	ans := answerDrag(d.handler, d.lastX, d.lastY, d.offered, d.allowed)
	take := d.offered & ans.Accept
	if ans.Op == DragOpNone || take == 0 {
		w.dndLeft()
		return Event{}, DragAnswer{}, false
	}
	d.inside = false
	return Event{Kind: EvDrop, X: d.lastX, Y: d.lastY, Code: uint32(dropKind(take)), SelStart: int32(d.items),
		Tag: ans.Target, Mods: w.mods, Time: nanotime()}, ans, true
}

// ---- the drag threshold ----

var (
	thresholdOnce sync.Once
	threshold     float32
)

// DragThreshold is how far the pointer moves with the button held before a
// press becomes a drag, in logical units: GTK's gtk-dnd-drag-threshold from the
// settings.ini of gtk-3.0 and gtk-4.0 (the user's, then the system's), 8 where
// none sets it.
func DragThreshold() float32 {
	thresholdOnce.Do(func() {
		home, _ := os.UserHomeDir()
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(home, ".config")
		}
		dirs := []string{cfg}
		if xdg := os.Getenv("XDG_CONFIG_DIRS"); xdg != "" {
			dirs = append(dirs, filepath.SplitList(xdg)...)
		} else {
			dirs = append(dirs, "/etc/xdg")
		}
		threshold = readDragThreshold(append(dirs, "/etc"))
	})
	return threshold
}

// readDragThreshold looks for gtk-dnd-drag-threshold in the gtk-3.0 and
// gtk-4.0 settings.ini of each directory, in order; a value out of 1 to 128 is
// ignored.
func readDragThreshold(dirs []string) float32 {
	for _, d := range dirs {
		for _, v := range [...]string{"gtk-3.0", "gtk-4.0"} {
			b, err := os.ReadFile(filepath.Join(d, v, "settings.ini"))
			if err != nil || len(b) > 1<<16 {
				continue
			}
			if n, ok := gtkSettingInt(string(b), "gtk-dnd-drag-threshold"); ok && n >= 1 && n <= 128 {
				return float32(n)
			}
		}
	}
	return dragThresholdLinux
}
