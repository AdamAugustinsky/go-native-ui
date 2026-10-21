package app

import (
	"gonativeui/platform"
	"gonativeui/ui"
)

// The app's side of the accessibility bridge (platform/a11y.go): the window
// asks for the semantic tree of the last finished frame only once an
// assistive technology has activated the bridge, and then once a frame
// (syncAX). An assistive technology's actions come back as
// platform.EvAccessibility events, which become ui's action requests
// (ui.Input.Actions) of the next frame.

// axBridge implements platform.AXSource over the App's ui context.
type axBridge struct {
	a        *App
	sem      ui.SemTree
	building bool // between Begin and End: the context has no finished frame to read
}

// AXExport converts the semantic tree of the last finished frame.
func (b *axBridge) AXExport(dst *platform.AXTree) bool {
	a := b.a
	if a == nil || a.UI == nil || b.building {
		return false
	}
	a.UI.Semantics(&b.sem)
	ExportAX(&b.sem, dst)
	return true
}

// ExportAX converts the semantic tree src into dst, reusing dst's storage (it
// allocates nothing once dst has grown to the tree's size). The roles, state
// bits and actions have the same values on both sides.
func ExportAX(src *ui.SemTree, dst *platform.AXTree) {
	dst.Bytes = append(dst.Bytes[:0], src.Bytes...)
	dst.Announce, dst.AnnounceSeq, dst.AnnouncePolite = src.Announce, src.AnnounceSeq, src.AnnouncePolite
	dst.Nodes = dst.Nodes[:0]
	for i := range src.Nodes {
		n := &src.Nodes[i]
		dst.Nodes = append(dst.Nodes, platform.AXNode{
			Key: uint64(n.Key), Role: platform.AXRole(n.Role), State: platform.AXState(n.State),
			Actions: platform.AXAction(n.Actions), Bounds: n.Bounds,
			Parent: n.Parent, FirstChild: n.FirstChild, NextSibling: n.NextSibling,
			Name: n.Name, Value: n.Value, Range: n.Range, Pos: n.Pos, TextSel: n.TextSel,
			Level: n.Level, Host: n.Host, Text: platform.AXTextKind(n.Text),
		})
	}
}

// The text and help functions answer from the published frame only: while a
// frame is being built (an assistive technology asking from inside a nested
// run loop of the app's Build), the context holds half of the next frame, so
// they answer nothing and the bridge falls back to the published nodes.

func (b *axBridge) AXLine(key uint64, off int) (int, bool) {
	if b.building {
		return 0, false
	}
	return b.a.UI.SemTextLine(ui.Key(key), off)
}

func (b *axBridge) AXLineRange(key uint64, line int) (int, int, bool) {
	if b.building {
		return 0, 0, false
	}
	return b.a.UI.SemTextLineRange(ui.Key(key), line)
}

func (b *axBridge) AXBounds(key uint64, from, to int) ([4]float32, bool) {
	if b.building {
		return [4]float32{}, false
	}
	return b.a.UI.SemTextBounds(ui.Key(key), from, to)
}

func (b *axBridge) AXOffset(key uint64, x, y float32) (int, bool) {
	if b.building {
		return 0, false
	}
	return b.a.UI.SemTextOffset(ui.Key(key), x, y)
}

func (b *axBridge) AXVisible(key uint64) (int, int, bool) {
	if b.building {
		return 0, 0, false
	}
	return b.a.UI.SemTextVisible(ui.Key(key))
}

func (b *axBridge) AXHelp(key uint64, dst []byte) []byte {
	if b.building {
		return dst
	}
	return b.a.UI.TooltipText(key, dst)
}

// initAX gives the window the bridge's source (after initUI). Where the OS
// has no bridge yet the window says so, and nothing happens.
func (a *App) initAX() {
	a.ax.a = a
	if a.Win != nil {
		_ = a.Win.SetAXSource(&a.ax) // platform.ErrAXUnsupported off macOS
	}
}

// syncAX hands the frame just built to the bridge; it does nothing until an
// assistive technology activated it.
func (a *App) syncAX() {
	if a.Win != nil {
		a.Win.SyncAX()
	}
}

// axAction turns an EvAccessibility event into ui's action request.
func (a *App) axAction(e *platform.Event) bool {
	return a.tr.in.PushAction(ui.SemRequest{Key: ui.Key(e.Tag), Action: ui.SemAction(e.Code), Value: float64(e.Amount)})
}
