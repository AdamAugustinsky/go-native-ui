package app

import (
	"gonativeui/ui"
)

// The inspector of the debug overlay (phase 6, T3).
//
// The plan puts it in a second window (OpenWindow, p6-f4); for now it is a
// panel inside the window, drawn by the overlay. What the panel shows is an
// InspectSnapshot, plain fixed-size data filled once per frame, and an
// InspectHost is what receives it: the day a second window exists, a host
// that owns that window's ui.Context draws the same snapshot there, and
// SetInspectHost replaces the panel. Nothing else of the overlay changes.

const (
	maxUnder     = 16 // boxes under the pointer in a snapshot
	maxFindings  = 32
	histShown    = 12
	maxDupsShown = 8
)

// InspectBox is a box in a snapshot.
type InspectBox struct {
	// Key is the box's key and Parent its parent's.
	Key, Parent ui.Key
	// Flags are the box's flags.
	Flags ui.Flags
	// Rect is the box's rectangle (x0, y0, x1, y1, window pixels) after the
	// last layout; Clip the rectangle its ancestors clip it to.
	Rect, Clip [4]float32
	// Role is the box's semantic role (RoleNone without one).
	Role ui.Role
	// Name[:NameLen] is the semantic name, or the display text without one,
	// cut to 40 bytes.
	Name    [40]byte
	NameLen uint8
	// Overlay is 0 for the main tree, else 1 + the overlay's index.
	Overlay uint8
}

func (ib *InspectBox) set(b *ui.DebugBox) {
	*ib = InspectBox{Key: b.Key, Parent: b.Parent, Flags: b.Flags, Rect: b.Rect, Clip: b.Clip, Role: b.Role, Overlay: b.Overlay}
	ib.NameLen = uint8(copy(ib.Name[:], b.Name))
}

// DebugFinding is a layout problem to show: the box outlined in red, with the
// reason. SetDebugAudit supplies them.
type DebugFinding struct {
	// Key is the box and Rect its rectangle (x0, y0, x1, y1, window pixels).
	Key  ui.Key
	Rect [4]float32
	// Reason says what is wrong, in a line.
	Reason string
}

// InspectSnapshot is what the debug overlay knows about the frame being built,
// for the HUD, the pointer card and the inspector.
type InspectSnapshot struct {
	// Frame is the number of the frame being built, counted since the
	// overlay turned on. W and H are the window in pixels and Scale the pixels
	// per ui point (App.EffectiveScale: the backing scale times the zoom).
	Frame uint64
	W, H  int
	Scale float32
	// Pointer is the position of the pointer in pixels; Inside whether it is
	// over the window.
	Pointer [2]float32
	Inside  bool

	// Under[:NUnder] are the boxes whose rectangle contains the pointer,
	// outermost first (ui.Context.DebugUnder).
	Under  [maxUnder]InspectBox
	NUnder int

	// Boxes is the number of boxes the app built this frame, without the
	// overlay's own.
	Boxes int
	// This[:NThis] are the causes of the frame being built.
	This  [maxCauses]RepaintCause
	NThis int
	// Last is the last frame that ended (HasLast false before the first), and
	// History[:NHistory] the frames before it, newest first.
	Last        FrameRecord
	HasLast     bool
	History     [histShown]FrameRecord
	NHistory    int
	CauseCount  [numCauses]uint64
	Unexplained uint64

	// AtlasPages and AtlasBytes are the text atlas; TexLive and TexRetiring
	// the renderer's textures (TexBytes when the renderer reports them, else
	// -1). Imports and Leases are the pixel buffers imported for the frame
	// being built and those the frames in flight hold (gpu.Renderer.ExternalCounts).
	AtlasPages, AtlasBytes int
	TexLive, TexRetiring   int
	TexBytes               int64
	Imports, Leases        int

	// Dups[:NDups] are the duplicate keys seen (ui.Context.DebugDups), DupsMore
	// the number that did not fit.
	Dups     [maxDupsShown]ui.DebugDup
	NDups    int
	DupsMore int
	// DupBoxes[i] is the first box built with Dups[i].Key in this frame, when
	// there is one (HasDupBox[i]).
	DupBoxes  [maxDupsShown]InspectBox
	HasDupBox [maxDupsShown]bool

	// Findings[:NFindings] are the layout audit's findings (SetDebugAudit),
	// shown when the audit is on.
	Findings  [maxFindings]DebugFinding
	NFindings int
}

// InspectHost receives the inspector's snapshot, once per frame, while the
// overlay is on. The snapshot is valid during the call; copy what is kept. A
// host that shows it somewhere else than the window is set with
// SetInspectHost; without one the overlay draws the panel itself (in
// development builds: Debug > Inspector, or SetInspectPanel).
//
// The plan puts the inspector in a second window. That needs more than one
// window, which is F4's: until it lands the panel is the inspector, and the
// second window is a host that owns its own ui.Context and draws the snapshot
// there. Deferred to F4.
type InspectHost interface {
	Inspect(s *InspectSnapshot)
}

// SetInspectHost sets where the inspector is shown. With a host, the overlay no
// longer draws the panel in the window; the host gets every frame's snapshot.
// nil goes back to the panel.
func (a *App) SetInspectHost(h InspectHost) {
	if a.dbg == nil {
		if h == nil {
			return
		}
		a.EnableDebug(true)
	}
	a.dbg.host = h
	a.debugRepaint()
}

// SetInspectPanel shows or hides the inspector panel in the window (the
// overlay must be on; release builds have no panel).
func (a *App) SetInspectPanel(on bool) {
	if a.dbg != nil && a.dbg.panel != on {
		a.dbg.panel = on
		a.debugRepaint()
	}
}

// SetDebugAudit sets the layout audit the overlay runs. fn appends its
// findings to dst and returns it; the overlay calls it when the audit is turned
// on (Debug > Run Layout Audit, or DebugRunAudit) and shows each finding as a
// red outline with its Reason. nil restores the default, which is
// ui.Context.Audit of the last frame without the overlay's own boxes. It runs
// on demand, not every frame, because an audit walks every box and allocates
// for what it reports.
func (a *App) SetDebugAudit(fn func(dst []DebugFinding) []DebugFinding) {
	if a.dbg == nil {
		if fn == nil {
			return
		}
		a.EnableDebug(true)
	}
	if fn == nil {
		fn = a.uiAudit
	}
	a.dbg.audit = fn
}

// DebugRunAudit runs the audit now, on the last frame, and shows its findings
// (the first 32). Call it outside Build.
func (a *App) DebugRunAudit() {
	d := a.dbg
	if d == nil || d.audit == nil {
		return
	}
	d.auditOn = true
	var tmp [maxFindings]DebugFinding
	fs := d.audit(tmp[:0])
	d.nFind = copy(d.findings[:], fs)
	a.debugRepaint()
}

// fillSnapshot fills d.snap for the frame being built, before the overlay's
// own boxes are.
func (a *App) fillSnapshot() {
	d := a.dbg
	s := &d.snap
	c := a.UI
	s.Frame = d.nFrames
	s.W, s.H, s.Scale = a.w, a.h, a.EffectiveScale()
	in := c.Input()
	s.Pointer, s.Inside = [2]float32{in.MouseX, in.MouseY}, in.Inside
	s.Boxes = c.DebugBoxes()
	d.cur.Boxes = s.Boxes

	s.NUnder = 0
	if in.Inside {
		var ids [maxUnder]ui.ID
		n := c.DebugUnder(in.MouseX, in.MouseY, ids[:])
		var b ui.DebugBox
		for i := 0; i < n; i++ {
			c.DebugBoxOf(ids[i], &b)
			s.Under[i].set(&b)
		}
		s.NUnder = n
	}

	s.This, s.NThis = d.cur.Causes, d.cur.NCauses
	s.HasLast = d.nFrames >= 2
	if s.HasLast {
		s.Last = d.hist[(d.nFrames-2)%histFrame]
	}
	s.NHistory = 0
	for i := 1; i <= histShown && uint64(i+1) < d.nFrames && i < histFrame; i++ {
		s.History[s.NHistory] = d.hist[(d.nFrames-2-uint64(i))%histFrame]
		s.NHistory++
	}
	s.CauseCount, s.Unexplained = d.counts, d.unexpl

	s.AtlasPages, s.AtlasBytes = 0, 0
	if a.Text != nil {
		s.AtlasPages = a.Text.Pages()
		for p := 0; p < s.AtlasPages; p++ {
			w, h, color := a.Text.PageInfo(p)
			px := 1
			if color {
				px = 4
			}
			s.AtlasBytes += w * h * px
		}
	}
	s.TexLive, s.TexRetiring, s.TexBytes, s.Imports, s.Leases = 0, 0, -1, 0, 0
	if a.R != nil {
		s.TexLive, s.TexRetiring = a.R.TextureCounts()
		s.Imports, s.Leases = a.R.ExternalCounts()
		if d.texBytes != nil {
			s.TexBytes = d.texBytes()
		}
	}

	dups := c.DebugDups()
	s.NDups = copy(s.Dups[:], dups)
	var db ui.DebugBox
	for i := 0; i < s.NDups; i++ {
		s.HasDupBox[i] = c.DebugBoxByKey(s.Dups[i].Key, &db)
		if s.HasDupBox[i] {
			s.DupBoxes[i].set(&db)
		}
	}
	s.DupsMore = len(dups) - s.NDups + c.DebugDupsLost()
	s.NFindings = 0
	if d.auditOn {
		s.NFindings = copy(s.Findings[:], d.findings[:d.nFind])
	}
}
