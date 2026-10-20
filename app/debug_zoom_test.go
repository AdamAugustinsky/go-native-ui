package app

import (
	"image"
	"testing"
)

// zoomRig is an overlay rig at backing scale and zoom, the pointer at the
// middle of the scene's button.
func zoomRig(t *testing.T, b Builder, w, h int, scale, zoom float32) *Offscreen {
	t.Helper()
	o := newOverlayRig(t, b, w, h, scale)
	o.SetZoom(zoom)
	frames(o, 3)
	return o
}

var zoomCases = []struct {
	name        string
	scale, zoom float32
}{{"1x at 150 %", 1, 1.5}, {"2x at 150 %", 2, 1.5}, {"1x at 300 %", 1, 3}, {"2x at 50 %", 2, 0.5}}

func buttonCentre(o *Offscreen, b *scene) (x, y float32, r [4]float32) {
	r = o.UI.RectOf(b.target)
	return (r[0] + r[2]) / 2, (r[1] + r[3]) / 2, r
}

// Zoomed, the UI is built at the backing scale times the zoom, and so is the
// overlay: the outline of the box under the pointer lies on the box's
// rectangle, the same rectangle the snapshot reports, and its thickness is the
// effective scale's.
func TestOverlayOutlineMatchesTheBoxAtZoom(t *testing.T) {
	for _, tc := range zoomCases {
		t.Run(tc.name, func(t *testing.T) {
			b := &scene{}
			o := zoomRig(t, b, 900, 700, tc.scale, tc.zoom)
			o.EnableDebug(true)
			x, y, r := buttonCentre(o, b)
			if r[2]-r[0] <= 0 {
				t.Fatalf("the button has no rectangle: %v", r)
			}
			pointAt(o, x, y)
			img := frames(o, 4)
			dumpPNG(t, "overlay-zoom-"+tc.name, img)
			s := &o.dbg.snap
			if es := tc.scale * tc.zoom; s.Scale != es {
				t.Errorf("snapshot scale %v, want the effective scale %v", s.Scale, es)
			}
			if s.NUnder < 2 {
				t.Fatalf("%d boxes under the pointer", s.NUnder)
			}
			if got := s.Under[s.NUnder-1].Rect; got != r {
				t.Errorf("innermost box %v, want the button %v", got, r)
			}
			x0, y0, x1, y1 := int(r[0]), int(r[1]), int(r[2]), int(r[3])
			cx, cy := int(x), int(y)
			for _, p := range [][2]int{{cx, y0 + 1}, {cx, y1 - 2}, {x0 + 1, cy}, {x1 - 2, cy}} {
				if c := pix(img, p[0], p[1]); !near(c, 0xFF8A3DFF, 24) {
					t.Errorf("pixel %v is %08x, want the inner outline colour on the box's edge %v", p, c, r)
				}
			}
			// not misplaced: just outside the rectangle is not outlined, and the
			// outline is as thick as the effective scale says (2 × its rounding)
			for _, p := range [][2]int{{cx, y0 - 3}, {cx, y1 + 2}, {x0 - 3, cy}, {x1 + 2, cy}} {
				if c := pix(img, p[0], p[1]); near(c, 0xFF8A3DFF, 24) {
					t.Errorf("pixel %v outside the box is the outline colour %08x", p, c)
				}
			}
			bw := int(tc.scale*tc.zoom + 0.5)
			if bw < 1 {
				bw = 1
			}
			if c := pix(img, cx, y0+2*bw+2); near(c, 0xFF8A3DFF, 24) {
				t.Errorf("the outline is thicker than %d px: %08x at %d px in", 2*bw, c, 2*bw+2)
			}
		})
	}
}

// The HUD is legible and inside the window at any zoom: its text is the size of
// the UI's (10 pt at the effective scale), and the panel it is drawn in lies
// where the layout puts it, a margin of 8 points from the corner, wholly
// inside the window; the card beside the pointer is inside too.
func TestOverlayHUDIsLegibleAndInsideTheWindowAtZoom(t *testing.T) {
	for _, tc := range zoomCases {
		t.Run(tc.name, func(t *testing.T) {
			const w, h = 900, 700
			b := &scene{}
			base := zoomRig(t, &scene{}, w, h, tc.scale, tc.zoom)
			baseImg := clone(frames(base, 2))
			o := zoomRig(t, b, w, h, tc.scale, tc.zoom)
			o.EnableDebug(true)
			x, y, _ := buttonCentre(o, b)
			pointAt(o, x, y)
			img := frames(o, 4)
			dumpPNG(t, "hud-zoom-"+tc.name, img)

			es := tc.scale * tc.zoom
			ds := &o.dbg.draw
			if ds.fontScale != es {
				t.Fatalf("the overlay's font is at scale %v, want the effective scale %v", ds.fontScale, es)
			}
			// 10 pt mono: a line is at least 10 effective pixels per point of
			// size, a character about 0.6 of that
			if ds.lh < 10*es || ds.adv < 4.5*es {
				t.Errorf("text too small: line %.1f px, character %.1f px at scale %v", ds.lh, ds.adv, es)
			}
			// the card fits the window (it sits beside the pointer, and the
			// other side of it when there is no room)
			cw, ch := ds.size(o.App, &ds.card)
			if ds.card.n == 0 || cw > w-16*es || ch > h-16*es {
				t.Errorf("the card is %.0f×%.0f px in a %d×%d window (%d lines)", cw, ch, w, h, ds.card.n)
			}
			// the HUD alone (no pointer, so no card and no outlines): the pixels
			// that change are the panel, where the layout puts it
			o.tr.in.Inside = false
			img = frames(o, 3)
			pw, ph := ds.size(o.App, &ds.hud)
			want := image.Rect(int(8*es), h-int(ph)-int(8*es), int(8*es)+int(pw), h-int(8*es))
			if want.Min.X < 0 || want.Min.Y < 0 || want.Max.X > w || want.Max.Y > h {
				t.Fatalf("the HUD panel %v is not inside the %d×%d window", want, w, h)
			}
			got := changedBounds(img, baseImg, image.Rect(0, h/2, w, h))
			if got.Empty() {
				t.Fatal("no HUD pixels")
			}
			// the panel (its border and background) is what changes; allow a few
			// pixels for rounding and the corner radius
			if d := 4; got.Min.X < want.Min.X-d || got.Min.Y < want.Min.Y-d || got.Max.X > want.Max.X+d || got.Max.Y > want.Max.Y+d ||
				got.Dx() < want.Dx()-2*d || got.Dy() < want.Dy()-2*d {
				t.Errorf("the HUD changes pixels in %v, the layout puts the panel at %v", got, want)
			}
		})
	}
}

// changedBounds is the bounding box of the pixels of r that differ.
func changedBounds(a, b *image.RGBA, r image.Rectangle) image.Rectangle {
	var out image.Rectangle
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if pix(a, x, y) != pix(b, x, y) {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// The layout audit's findings are outlined where the audit says, at zoom too.
func TestOverlayAuditFindingIsOutlinedAtZoom(t *testing.T) {
	o := zoomRig(t, &overflowing{}, 900, 700, 1, 1.5)
	o.EnableDebug(true)
	o.DebugRunAudit()
	if !o.Step(0) {
		t.Fatal("no frame")
	}
	img := o.img
	s := &o.dbg.snap
	if s.NFindings != 1 {
		t.Fatalf("%d findings", s.NFindings)
	}
	r := s.Findings[0].Rect
	if w := r[2] - r[0]; w != 160 { // the child is 160 px wide whatever the zoom
		t.Fatalf("finding %v: %v wide, want the child's 160 px", r, w)
	}
	cy := int((r[1] + r[3]) / 2)
	for _, p := range [][2]int{{int(r[0]) + 1, cy}, {int(r[2]) - 2, cy}, {int(r[0]+r[2]) / 2, int(r[1]) + 1}} {
		if c := pix(img, p[0], p[1]); !near(c, 0xFF2D2DFF, 40) {
			t.Errorf("pixel %v is %08x, want the finding's red on the rectangle %v", p, c, r)
		}
	}
}

// With the overlay on at 150 % zoom a steady-state frame still allocates
// nothing: the zoom changes the font's scale once, not per frame.
func TestOverlayFramesAllocateNothingAtZoom(t *testing.T) {
	b := &scene{dup: true}
	o := newOverlayRig(t, b, 900, 700, 2)
	o.SetZoom(1.5)
	o.EnableDebug(true)
	o.SetInspectPanel(true)
	o.SetDebugAudit(func(dst []DebugFinding) []DebugFinding {
		return append(dst, DebugFinding{Key: 7, Rect: [4]float32{300, 20, 390, 90}, Reason: "alignment set with no text"})
	})
	o.DebugRunAudit()
	frames(o, 3)
	x, y, _ := buttonCentre(o, b)
	pointAt(o, x, y)
	frames(o, 400) // every character of the overlay's text is shaped and in the atlas by now
	if allocs := testing.AllocsPerRun(200, func() { o.Frame() }); allocs != 0 {
		t.Errorf("%.2f allocations per frame with the overlay on at 150 %%, want 0", allocs)
	}
	if r, ok := o.DebugFrame(0); !ok || r.Allocs != 0 {
		t.Errorf("the HUD's own count of allocations is %d (ok %v), want 0", r.Allocs, ok)
	}
}
