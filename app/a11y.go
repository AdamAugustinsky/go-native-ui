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
	a          *App
	generation uint64
	building   bool // between Begin and End: the context has no finished frame to read
}

// AXExport publishes the last finished semantic tree when its generation changes.
func (b *axBridge) AXExport(dst *platform.AXTree) bool {
	a := b.a
	if a == nil || a.UI == nil || b.building {
		return false
	}
	if !a.UI.SemanticsActive() {
		b.AXSetActive(true)
		return false // activation needs a complete frame with semantic records
	}
	tree, generation := a.UI.SemanticSnapshot()
	if tree == nil || generation == b.generation {
		return false
	}
	ExportAX(tree, dst)
	b.generation = generation
	return true
}

// AXSetActive is the optional platform activation hook. Activation turns
// recording on for an app that switched it off (ui.Context.SetSemanticsActive),
// which then needs one complete build before a tree can be published; expiry
// leaves recording alone, because scripts, the agent bridge and the shot
// tools read the same records (Context.Semantics).
func (b *axBridge) AXSetActive(active bool) {
	if b.a == nil || b.a.UI == nil {
		return
	}
	b.generation = 0
	if !active || b.a.UI.SemanticsActive() {
		return
	}
	b.a.UI.SetSemanticsActive(true)
	if b.a.Win != nil {
		b.a.Win.Wake()
	}
}

// ExportAX borrows src's node and text slices until src is changed. A bridge
// keeps two semantic buffers so its current and previous trees stay distinct.
func ExportAX(src *ui.SemTree, dst *platform.AXTree) {
	dst.Bytes = src.Bytes
	dst.Nodes = src.Nodes
	dst.Announce, dst.AnnounceSeq, dst.AnnouncePolite = src.Announce, src.AnnounceSeq, src.AnnouncePolite
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
