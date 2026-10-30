package workspace

import (
	"encoding/json"
	"errors"
	"math"

	"gonativeui/ui"
)

// Version is the version of the saved layout MarshalJSON writes.
const Version = 1

// ErrVersion is Restore's error for a layout saved by a later version.
var ErrVersion = errors.New("workspace: layout saved by a later version")

// ErrEmpty is Restore's error for a layout with no pane the app knows.
var ErrEmpty = errors.New("workspace: no known pane in the saved layout")

// saved is the layout as JSON: the version, the focus, the maximised pane
// and the tree.
type saved struct {
	Version int        `json:"version"`
	Focus   PaneID     `json:"focus,omitempty"`
	Zoom    PaneID     `json:"zoom,omitempty"`
	Root    *savedNode `json:"root"`
}

// savedNode is a pane (Pane, and whether it is hidden) or a split: its axis ("x" side by side, "y"
// stacked), the first child's share, or the size in points of the child that
// keeps one (Fixed "a" or "b"), and the two children.
type savedNode struct {
	Pane    PaneID     `json:"pane,omitempty"`
	Hidden  bool       `json:"hidden,omitempty"`
	Axis    string     `json:"axis,omitempty"`
	Share   float32    `json:"share,omitempty"`
	Fixed   string     `json:"fixed,omitempty"`
	FixedPt float32    `json:"fixedPt,omitempty"`
	A       *savedNode `json:"a,omitempty"`
	B       *savedNode `json:"b,omitempty"`
}

// MarshalJSON saves the layout: its tree of panes and splits (shares and
// fixed sizes), the hidden panes, the focus and the maximised pane. Limits,
// empty panes and the solo pane are the app's state of the moment and are
// not saved.
func (l *Layout) MarshalJSON() ([]byte, error) {
	s := saved{Version: Version, Focus: l.focus, Zoom: l.zoom}
	if l.hasRoot() {
		s.Root = l.save(l.root)
	}
	return json.Marshal(&s)
}

func (l *Layout) save(n int8) *savedNode {
	nd := &l.nodes[n]
	if nd.leaf {
		return &savedNode{Pane: l.slots[nd.slot].id, Hidden: l.slots[nd.slot].hidden}
	}
	sn := &savedNode{Axis: "x", Share: nd.ratio, A: l.save(nd.a), B: l.save(nd.b)}
	if nd.axis == ui.AxisY {
		sn.Axis = "y"
	}
	switch nd.fixed {
	case fixedFirst:
		sn.Fixed, sn.FixedPt = "a", nd.fixPt
	case fixedSecond:
		sn.Fixed, sn.FixedPt = "b", nd.fixPt
	}
	return sn
}

// UnmarshalJSON is Restore with every pane known.
func (l *Layout) UnmarshalJSON(data []byte) error { return l.Restore(data, nil) }

// Restore makes the layout the one saved in data, keeping only the panes
// known reports true for (all when known is nil), each once and at most
// MaxPanes: the room of a pane left out goes to its neighbour, and a share
// out of range is a half. Fields it does not know are ignored. The focus is
// the saved one when it is kept and can be focused, else the first pane that
// can; the maximised pane likewise, else none. The panes' limits and empty
// states are kept for those still in the layout. Panes the app has that the
// saved layout does not are the app's to add (Split). On an error (ErrVersion, ErrEmpty, malformed JSON) the layout
// is unchanged.
func (l *Layout) Restore(data []byte, known func(PaneID) bool) error {
	var s saved
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	if s.Version > Version {
		return ErrVersion
	}
	var nl Layout
	nl.Key, nl.root = l.Key, -1
	nl.root = nl.load(s.Root, known, 0)
	if nl.root < 0 {
		return ErrEmpty
	}
	nl.nodes[nl.root].parent = -1
	nl.reindex()
	for i := range nl.slots {
		if id := nl.slots[i].id; id != 0 {
			if old := l.Slot(id); old >= 0 {
				nl.slots[i].lim, nl.slots[i].empty = l.slots[old].lim, l.slots[old].empty
			}
		}
	}
	if !nl.SetFocus(s.Focus) {
		nl.refocus(-1)
	}
	if s.Zoom != 0 && s.Zoom == nl.focus {
		nl.zoom = s.Zoom
	}
	*l = nl
	return nil
}

// load builds the saved node sn and returns its node, −1 when it holds no
// pane that is kept.
func (l *Layout) load(sn *savedNode, known func(PaneID) bool, depth int) int8 {
	if sn == nil || depth > maxNodes {
		return -1
	}
	if sn.A == nil && sn.B == nil {
		id := sn.Pane
		if id == 0 || l.Has(id) || l.n >= MaxPanes || known != nil && !known(id) {
			return -1
		}
		n := l.newNode()
		l.nodes[n].leaf, l.nodes[n].slot = true, l.newSlot(id)
		l.slots[l.nodes[n].slot].hidden = sn.Hidden
		l.n++ // counts the panes while loading; reindex sets it again
		return n
	}
	a := l.load(sn.A, known, depth+1)
	b := l.load(sn.B, known, depth+1)
	switch {
	case a < 0:
		return b
	case b < 0:
		return a
	}
	n := l.newNode()
	nd := &l.nodes[n]
	l.serial++
	nd.uid, nd.a, nd.b = l.serial, a, b
	l.nodes[a].parent, l.nodes[b].parent = n, n
	if sn.Axis == "y" {
		nd.axis = ui.AxisY
	}
	nd.ratio = sn.Share
	if !(nd.ratio > 0 && nd.ratio < 1) || math.IsNaN(float64(nd.ratio)) {
		nd.ratio = 0.5
	}
	if sn.FixedPt > 0 && !math.IsInf(float64(sn.FixedPt), 0) {
		switch sn.Fixed {
		case "a":
			nd.fixed, nd.fixPt = fixedFirst, sn.FixedPt
		case "b":
			nd.fixed, nd.fixPt = fixedSecond, sn.FixedPt
		}
	}
	return n
}
