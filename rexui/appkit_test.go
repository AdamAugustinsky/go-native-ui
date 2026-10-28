package rexui_test

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/appkitref"
	"gonativeui/internal/cielab"
	"gonativeui/platform"
	"gonativeui/rexui"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
)

// appkitRef is rexui/testdata/appkit-27.2.json: macOS 27.2's controls as
// tools/appkitref/measure read them from AppKitRef's renders (MacBook,
// default blue accent), and the system colours of that run. CI reads the
// MacBook's set; GNUI_APPKIT_REF=macmini reads appkit-27.2-macmini.json, the
// Mac mini's (internal/appkitref).
type appkitRef struct {
	System  map[string]map[string]string            `json:"system"`
	Samples map[string]map[string]appkitMeasurement `json:"samples"`
	// Layouts are the frames of the form samples' subviews, in points from
	// the sample's top left (AppKitRef's "layouts"); Sheets what it read of
	// sheets.
	Layouts map[string]map[string]json.RawMessage `json:"layouts"`
	Sheets  map[string]any                        `json:"sheets"`
}

type appkitMeasurement struct {
	W, H, Radius          float64
	Fill, Label, Ring     string
	RingOutset, RingWidth float64
	Fill2                 string
}

func loadAppKitRef(t *testing.T) *appkitRef {
	t.Helper()
	set, ok, err := appkitref.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		set = appkitref.Default
	}
	b, err := os.ReadFile(appkitref.File(set, "testdata/appkit-27.2.json"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("AppKit reference: %s", set)
	var f appkitRef
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

// systemValues are the reference run's system colours, as the platform
// would report them.
func (f *appkitRef) systemValues(t *testing.T) *theme.SystemValues {
	t.Helper()
	s := &theme.SystemValues{Known: true}
	for m, mode := range [...]string{"light", "dark"} {
		for i, name := range platform.SystemColorNames {
			h, ok := f.System[mode][name]
			if !ok {
				continue
			}
			v, err := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
			if err != nil {
				t.Fatalf("%s %s = %q", mode, name, h)
			}
			s.Colors[m][i] = uint32(v)
		}
	}
	return s
}

// appKitControls switches the controls to AppKit's look (Rex's capsules and
// quiet tints are the default) for the test, and back after it.
func appKitControls(t *testing.T) {
	buttons, icons, on, seg := rexui.ButtonRecipes, rexui.IconButtonRecipes, rexui.ToggleOnLook, rexui.SegmentedLook
	rexui.UseAppKitControls()
	t.Cleanup(func() {
		rexui.ButtonRecipes, rexui.IconButtonRecipes, rexui.ToggleOnLook, rexui.SegmentedLook = buttons, icons, on, seg
	})
}

// Tolerances (docs/PHASE6-PLAN.md, the native-feel gate).
const (
	tolLength = 0.5 // pt: widths, heights, radii, the ring's outset and width
	tolFill   = 2.0 // ΔE2000: fills and the focus ring
	tolLabel  = 3.0 // ΔE2000: label ink
)

// Each sample's recipe, resolved and drawn, has AppKit's measured geometry
// and colours within tolerance, in light and dark: the measurements
// tools/appkitref/measure takes from pixels, taken here from the draw list
// (the shapes' extents along the probe's row and column, the outermost
// corner's radius, the colours composited at the probe, the label's colour
// over what lies under it, the ring's colour, outset and width). AppKit's
// numbers are committed; its pixels are not.
func TestRecipesMatchAppKit(t *testing.T) {
	appKitControls(t)
	ref := loadAppKitRef(t)
	sys := ref.systemValues(t)
	for i, s := range catalog.Samples {
		if s.Spec.What == 0 {
			continue
		}
		for m, mode := range [...]string{"light", "dark"} {
			want, ok := ref.Samples[s.Name][mode]
			if !ok {
				t.Fatalf("%s: no reference in %s", s.Name, mode)
			}
			var root ui.ID
			g := newRig(t, theme.Mode(m), sys, sample(i, &root))
			g.settle()
			if s.Spec.Inside {
				// a list's overlay scroll bars fade once it rests (0.9 s),
				// as AppKit's are hidden at rest
				g.frames(60)
				g.settle()
			}
			got := measureQuads(g, root, s.Spec)
			check := func(what string, d, tol float64, a, b any) {
				if d > tol {
					t.Errorf("%s %s %s: ours %v, AppKit %v (off by %.2f, tolerance %.1f)", s.Name, mode, what, b, a, d, tol)
				}
			}
			sp := s.Spec
			if sp.What&catalog.MeasureW != 0 {
				check("width", math.Abs(got.W-want.W), tolLength, want.W, got.W)
			}
			if sp.What&catalog.MeasureH != 0 {
				check("height", math.Abs(got.H-want.H), tolLength, want.H, got.H)
			}
			if sp.What&catalog.MeasureRadius != 0 {
				check("radius", math.Abs(got.Radius-want.Radius), tolLength, want.Radius, got.Radius)
			}
			if sp.What&catalog.MeasureFill != 0 {
				check("fill ΔE", deltaE(got.Fill, want.Fill), tolFill, want.Fill, got.Fill)
			}
			if sp.What&catalog.MeasureFill2 != 0 {
				check("fill2 ΔE", deltaE(got.Fill2, want.Fill2), tolFill, want.Fill2, got.Fill2)
			}
			if sp.What&catalog.MeasureLabel != 0 {
				check("label ΔE", deltaE(got.Label, want.Label), tolLabel, want.Label, got.Label)
			}
			if sp.What&catalog.MeasureRing != 0 {
				check("ring ΔE", deltaE(got.Ring, want.Ring), tolFill, want.Ring, got.Ring)
				check("ring outset", math.Abs(got.RingOutset-want.RingOutset), tolLength, want.RingOutset, got.RingOutset)
				if want.RingWidth > 0 {
					check("ring width", math.Abs(got.RingWidth-want.RingWidth), tolLength, want.RingWidth, got.RingWidth)
				}
			}
		}
	}
}

// measureQuads measures the sample whose root is root in the rig's last draw
// list as sp says, in points and opaque #RRGGBB colours, as
// tools/appkitref/measure measures a render.
func measureQuads(g *rig, root ui.ID, sp catalog.Spec) appkitMeasurement {
	r := g.k.Theme()
	sc := float64(g.scale)
	rr := g.c.Rect(root)
	ox, oy := float64(rr[0]), float64(rr[1])
	bg := r.Colors[theme.Window] | 0xFF
	var shapes []gpu.Quad
	ringRGB := r.Colors[theme.FocusRing] >> 8
	var ring *gpu.Quad
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode != gpu.ModeShape || q.Soft > 0 {
			continue
		}
		if q.Color[0]&0xFF == 0 && (q.Border == 0 || q.BorderColor&0xFF == 0) {
			continue
		}
		shapes = append(shapes, *q)
		if q.Border > 0 && q.BorderColor>>8 == ringRGB && q.Color[0]&0xFF == 0 {
			ring = q
		}
	}
	px := ox + float64(sp.Probe[0])*sc + 0.5
	py := oy + float64(sp.Probe[1])*sc + 0.5
	px, py = math.Floor(px)+0.5, math.Floor(py)+0.5

	if sp.Inside {
		return measureInside(g, shapes, root, sp, px, py)
	}

	var out appkitMeasurement
	out.Fill = hex(composite(shapes, bg, px, py))
	right := math.Inf(1)
	if sp.Label != ([4]float32{}) {
		right = ox + float64(sp.Label[0])*sc
	}
	l, rt, lq := span(shapes, bg, px, py, right, true)
	mid := math.Floor((l+rt)/2) + 0.5
	t, b, tq := span(shapes, bg, mid, py, math.Inf(1), false)
	out.W, out.H = (rt-l)/sc, (b-t)/sc
	// the outermost shape at the top left: the one whose left edge is the
	// row's, or the column's top
	corner := lq
	if corner == nil || (tq != nil && tq.Rect[1] < corner.Rect[1] && tq.Rect[0] <= corner.Rect[0]) {
		corner = tq
	}
	if corner != nil {
		out.Radius = float64(corner.Radii[0]) / sc
	}
	if ring != nil {
		out.Ring = hex(composite(shapes, bg, math.Floor(l)+3.5, py))
		out.RingOutset = (oy - float64(ring.Rect[1])) / sc
		out.RingWidth = float64(ring.Border) / sc
	}
	if sp.What&catalog.MeasureLabel != 0 {
		var x0, y0, x1, y1 float64
		under := bg
		if sp.Label == ([4]float32{}) {
			under = composite(shapes, bg, px, py)
			x0, y0, x1, y1 = l+4, t+2, rt-4, b-2
		} else {
			x0, y0 = ox+float64(sp.Label[0])*sc, oy+float64(sp.Label[1])*sc
			x1, y1 = ox+float64(sp.Label[2])*sc, oy+float64(sp.Label[3])*sc
		}
		out.Label = labelOver(g, under, x0, y0, x1, y1)
	}
	return out
}

// labelOver is the colour of the first glyph in the region (x0, y0, x1, y1),
// in pixels, over under; empty if none is there.
func labelOver(g *rig, under uint32, x0, y0, x1, y1 float64) string {
	for i := range g.list.Quads {
		q := &g.list.Quads[i]
		if q.Mode != gpu.ModeMaskR8 {
			continue
		}
		if float64(q.Rect[0]) < x1 && float64(q.Rect[2]) > x0 && float64(q.Rect[1]) < y1 && float64(q.Rect[3]) > y0 {
			return hex(over(q.Color[0], under))
		}
	}
	return ""
}

// measureInside measures a shape inside the sample (catalog.Spec.Inside) as
// tools/appkitref/measure does, at pixel centres: the run of the probe's
// colour (px, py) along its column gives the top and bottom; the left and
// right edges are where the colour changes scanning inward from 8 px outside
// that run along its row, at most 12 pt below the top; the radius is the top
// left corner's of the shape there.
func measureInside(g *rig, shapes []gpu.Quad, root ui.ID, sp catalog.Spec, px, py float64) appkitMeasurement {
	r := g.k.Theme()
	sc := float64(g.scale)
	rr := g.c.Rect(root)
	ox, oy := float64(rr[0]), float64(rr[1])
	bg := r.Colors[theme.Window] | 0xFF
	tol := sp.Tol
	if tol == 0 {
		tol = 3
	}
	at := func(x, y float64) uint32 { return composite(shapes, bg, x, y) }
	pc := at(px, py)
	run := func(x, y, dx, dy float64) float64 { // the last pixel centre in the run
		for n := 0; n < 4000; n++ {
			if dist(at(x+dx, y+dy), pc) > tol {
				break
			}
			x, y = x+dx, y+dy
		}
		if dx != 0 {
			return x
		}
		return y
	}
	t, b := run(px, py, 0, -1)-0.5, run(px, py, 0, 1)+0.5
	l0, r0 := run(px, py, -1, 0)-0.5, run(px, py, 1, 0)+0.5
	y := math.Floor(t+min((b-t)/2, 12*sc)) + 0.5
	xs := max(math.Floor(l0)-8, float64(rr[0])) + 0.5
	xe := min(math.Ceil(r0)+8, float64(rr[2])-1) + 0.5
	scan := func(x, dx float64) float64 { // the first pixel centre unlike x's
		out := at(x, y)
		for n := 0; n < 4000 && dist(at(x, y), out) <= 3; n++ {
			x += dx
		}
		return x
	}
	l, rt := scan(xs, 1)-0.5, scan(xe, -1)+0.5

	var out appkitMeasurement
	if sp.What&catalog.MeasureFill != 0 {
		out.Fill = hex(pc)
	}
	if sp.What&catalog.MeasureW != 0 {
		out.W = (rt - l) / sc
	}
	if sp.What&catalog.MeasureH != 0 {
		out.H = (b - t) / sc
	}
	if sp.What&catalog.MeasureRadius != 0 {
		// the topmost shape with its top left corner at (l, t)
		for i := len(shapes) - 1; i >= 0; i-- {
			q := &shapes[i]
			if math.Abs(float64(q.Rect[0])-l) < 1 && math.Abs(float64(q.Rect[1])-t) < 1 {
				out.Radius = float64(q.Radii[0]) / sc
				break
			}
		}
	}
	if sp.What&catalog.MeasureLabel != 0 {
		out.Label = labelOver(g, pc, ox+float64(sp.Label[0])*sc, oy+float64(sp.Label[1])*sc,
			ox+float64(sp.Label[2])*sc, oy+float64(sp.Label[3])*sc)
	}
	if sp.What&catalog.MeasureFill2 != 0 {
		out.Fill2 = hex(at(math.Floor(ox+float64(sp.Probe2[0])*sc)+0.5, math.Floor(oy+float64(sp.Probe2[1])*sc)+0.5))
	}
	return out
}

// dist is the largest channel difference between two colours.
func dist(a, b uint32) int {
	d := 0
	for s := uint(8); s <= 24; s += 8 {
		x, y := int(a>>s&0xFF), int(b>>s&0xFF)
		d = max(d, x-y, y-x)
	}
	return d
}

// visible reports whether q draws at (x, y): inside its rounded rectangle and
// its clip, and on its border band when it is only an outline. It returns the
// colour it draws there.
func visible(q *gpu.Quad, x, y float64) (uint32, bool) {
	x0, y0, x1, y1 := float64(q.Rect[0]), float64(q.Rect[1]), float64(q.Rect[2]), float64(q.Rect[3])
	if x < x0 || x >= x1 || y < y0 || y >= y1 {
		return 0, false
	}
	if q.Clip != ([4]float32{}) && (x < float64(q.Clip[0]) || x >= float64(q.Clip[2]) || y < float64(q.Clip[1]) || y >= float64(q.Clip[3])) {
		return 0, false
	}
	if !inRounded(x, y, x0, y0, x1, y1, q.Radii) {
		return 0, false
	}
	if q.Border > 0 {
		bw := float64(q.Border)
		var inner [4]float32
		for i, rd := range q.Radii {
			inner[i] = max(rd-q.Border, 0)
		}
		if inRounded(x, y, x0+bw, y0+bw, x1-bw, y1-bw, inner) {
			if q.Color[0]&0xFF == 0 {
				return 0, false
			}
			return q.Color[0], true
		}
		return q.BorderColor, q.BorderColor&0xFF != 0
	}
	return q.Color[0], true
}

// inRounded reports whether (x, y) lies inside the rectangle with corner radii
// (top left, top right, bottom right, bottom left).
func inRounded(x, y, x0, y0, x1, y1 float64, radii [4]float32) bool {
	if x < x0 || x >= x1 || y < y0 || y >= y1 {
		return false
	}
	corner := func(cx, cy, rd float64) bool {
		dx, dy := x-cx, y-cy
		return dx*dx+dy*dy <= rd*rd
	}
	tl, tr, br, bl := float64(radii[0]), float64(radii[1]), float64(radii[2]), float64(radii[3])
	switch {
	case x < x0+tl && y < y0+tl:
		return corner(x0+tl, y0+tl, tl)
	case x > x1-tr && y < y0+tr:
		return corner(x1-tr, y0+tr, tr)
	case x > x1-br && y > y1-br:
		return corner(x1-br, y1-br, br)
	case x < x0+bl && y > y1-bl:
		return corner(x0+bl, y1-bl, bl)
	}
	return true
}

// composite is the colour the shapes paint at (x, y) over bg.
func composite(shapes []gpu.Quad, bg uint32, x, y float64) uint32 {
	c := bg
	for i := range shapes {
		if col, ok := visible(&shapes[i], x, y); ok {
			c = over(col, c)
		}
	}
	return c
}

// span is the extent along the row through y (horiz) or the column through x
// of the shapes that paint something other than the background there, seen
// from outside (on the right, from right). It returns the edges in pixels and
// the shape that gives the first.
func span(shapes []gpu.Quad, bg uint32, x, y, right float64, horiz bool) (lo, hi float64, first *gpu.Quad) {
	lo, hi = math.Inf(1), math.Inf(-1)
	for i := range shapes {
		q := &shapes[i]
		var a, b float64
		if horiz {
			if y < float64(q.Rect[1]) || y >= float64(q.Rect[3]) {
				continue
			}
			a, b = float64(q.Rect[0]), float64(q.Rect[2])
		} else {
			if x < float64(q.Rect[0]) || x >= float64(q.Rect[2]) {
				continue
			}
			a, b = float64(q.Rect[1]), float64(q.Rect[3])
		}
		if horiz && a >= right {
			continue
		}
		// it must paint something other than the background at its edge
		mx, my := x, y
		if horiz {
			mx = a + float64(max(q.Border, 1))/2
		} else {
			my = a + float64(max(q.Border, 1))/2
		}
		col, ok := visible(q, mx, my)
		if !ok || same(over(col, bg), bg) {
			continue
		}
		if a < lo {
			lo, first = a, q
		}
		hi = max(hi, b)
	}
	return lo, hi, first
}

// over composites the straight-alpha top over the opaque bottom.
func over(top, bottom uint32) uint32 {
	a := float64(top&0xFF) / 255
	ch := func(s uint) uint32 {
		t, b := float64(top>>s&0xFF), float64(bottom>>s&0xFF)
		return uint32(math.Round(t*a+b*(1-a))) << s
	}
	return ch(24) | ch(16) | ch(8) | 0xFF
}

func same(a, b uint32) bool { return a>>8 == b>>8 }

func hex(c uint32) string {
	return "#" + strings.ToUpper(strconv.FormatUint(uint64(c>>8|1<<24), 16)[1:])
}

func deltaE(a, b string) float64 {
	return cielab.DeltaE2000(lab(a), lab(b))
}

func lab(h string) cielab.Lab {
	v, _ := strconv.ParseUint(strings.TrimPrefix(h, "#"), 16, 32)
	return cielab.SRGBToLab(uint8(v>>16), uint8(v>>8), uint8(v))
}

// The layout audit of p6-align compared our boxes with AppKitRef's sample
// frames. Two of those frames are not the control's. AppKitRef gives every
// NSSwitch a 54 × 24 frame, while NSSwitch's own size (its fittingSize, and the
// ink in the reference) is 36 × 16, 44 × 20, 54 × 24 and 64 × 28 by control
// size. A text field's frame is 200 × 24 and its bezel's 1 pt outline is drawn
// outside it (the reference's field is 202 × 26 over a 200 × 24 frame, its
// fill 200 × 24). Our boxes are AppKit's.
func TestBoxesAreAppKitsNotTheSampleFrames(t *testing.T) {
	ref := loadAppKitRef(t)
	sys := ref.systemValues(t)
	for name, size := range map[string]theme.Size{"switch-on-mini": theme.SizeMini, "switch-on-small": theme.SizeSmall, "switch-on": theme.SizeRegular, "switch-on-large": theme.SizeLarge} {
		var on = true
		var id ui.ID
		g := newRig(t, theme.Light, sys, column(func(g *rig) {
			id = rexui.Switch(g.k, "", &on, rexui.SwitchOpts{Size: size}).ID
		}))
		g.settle()
		want := ref.Samples[name]["light"]
		got := ourRect(g, id, g.c.Rect(id)[0], g.c.Rect(id)[1])
		near(t, name+" width", got.W, want.W)
		near(t, name+" height", got.H, want.H)
	}

	i := catalog.Index("field-text")
	var root ui.ID
	g := newRig(t, theme.Light, sys, sample(i, &root))
	g.settle()
	frame := g.c.Rect(root)
	s := catalog.Samples[i]
	got := ourRect(g, root, frame[0], frame[1])
	near(t, "field frame width", got.W, float64(s.W))
	near(t, "field frame height", got.H, float64(s.H))
	sc := g.scale
	found := false
	for _, q := range g.list.Quads {
		if q.Mode == gpu.ModeShape && q.Border > 0 && q.Rect == [4]float32{frame[0] - sc, frame[1] - sc, frame[2] + sc, frame[3] + sc} {
			found = true
		}
	}
	if !found {
		t.Error("the field's outline is not the 1 pt ring just outside its frame, as AppKit's bezel is")
	}
	if w := ref.Samples["field-text"]["light"]; w.W != float64(s.W)+2 || w.H != float64(s.H)+2 {
		t.Errorf("the reference's field is %v × %v: not the frame plus the outline", w.W, w.H)
	}
}

// TestLabelStylesAreAsTallAsAppKits: AppKitRef's label-styles stack (11 text
// styles and 5 colours of one-line labels, 4 pt apart) fits 336.5 pt, because
// its label cells are 31, 20.5 and 19 pt for Large Title, Title 2 and Title 3
// (theme.TextSpec.Cell), where the lines are 32, 22 and 20. The sample's 337 pt
// frame is the same stack rounded up.
func TestLabelStylesAreAsTallAsAppKits(t *testing.T) {
	var root ui.ID
	g := newRig(t, theme.Light, nil, sample(catalog.Index("label-styles"), &root))
	g.settle()
	if r := g.c.Rect(root); math.Abs(float64(r[3]-r[1])-2*336.5) > 0.01 {
		t.Errorf("label-styles is %v pt tall, AppKit's fitting height 336.5", (r[3]-r[1])/2)
	}
}
