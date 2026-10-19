package rexui_test

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	"gonativeui/gpu"
	"gonativeui/internal/cielab"
	"gonativeui/platform"
	"gonativeui/rexui/catalog"
	"gonativeui/theme"
	"gonativeui/ui"
)

// appkitRef is rexui/testdata/appkit-27.2.json: macOS 27.2's controls as
// tools/appkitref/measure read them from AppKitRef's renders (MacBook,
// default blue accent), and the system colours of that run.
type appkitRef struct {
	System  map[string]map[string]string            `json:"system"`
	Samples map[string]map[string]appkitMeasurement `json:"samples"`
}

type appkitMeasurement struct {
	W, H, Radius          float64
	Fill, Label, Ring     string
	RingOutset, RingWidth float64
}

func loadAppKitRef(t *testing.T) *appkitRef {
	t.Helper()
	b, err := os.ReadFile("testdata/appkit-27.2.json")
	if err != nil {
		t.Fatal(err)
	}
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
		for i := range g.list.Quads {
			q := &g.list.Quads[i]
			if q.Mode != gpu.ModeMaskR8 {
				continue
			}
			if float64(q.Rect[0]) < x1 && float64(q.Rect[2]) > x0 && float64(q.Rect[1]) < y1 && float64(q.Rect[3]) > y0 {
				out.Label = hex(over(q.Color[0], under))
				break
			}
		}
	}
	return out
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
