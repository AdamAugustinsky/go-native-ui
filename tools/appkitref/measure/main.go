// Command measure reads the edges, corner radii, fills, label colours and
// focus rings of AppKitRef's sample renders and of rexui's counterparts
// (tools/appkitref/ours), as package rexui/catalog says to measure each
// sample, and prints them side by side with the differences and whether each
// is within tolerance:
//
//   - lengths (width and height along the probe's row and column, focus ring
//     included) within ±0.5 pt;
//   - corner radii within ±0.5 pt;
//   - fills and focus rings within ΔE2000 2 of AppKit's;
//   - label ink within ΔE2000 3.
//
// With -write it records AppKit's numbers, the focus rings AppKitRef measured
// and the system colours of the run as JSON: rexui's recipe test checks the
// recipes against that file (rexui/testdata/appkit-27.2.json).
//
//	go run ./tools/appkitref/measure -ref DIR [-ours DIR] [-write FILE]
//	go run ./tools/appkitref/measure -names 0 > names.txt   # for AppKitRef -live
//	go run ./tools/appkitref/measure -cut capture.png -page 0 -o DIR/light
//	go run ./tools/appkitref/measure -live -ref APPKIT_CELLS -ours OUR_CELLS
//
// Both renders are at 2× with AppKit's frame at 14 pt from the top left, as
// AppKitRef and ours write them.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	_ "image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"gonativeui/internal/cielab"
	"gonativeui/platform"
	"gonativeui/rexui/catalog"
)

const (
	scale  = 2  // pixels per point
	origin = 28 // the frame's top left in pixels (14 pt)
	thresh = 3  // a pixel differs from the background by more than this in a channel: ink
)

// Tolerances.
const (
	tolLength = 0.5 // pt
	tolRadius = 0.5 // pt
	tolFill   = 2.0 // ΔE2000
	tolLabel  = 3.0 // ΔE2000
)

// Result is one sample's measurements in one mode. Lengths are points, colours
// opaque #RRGGBB.
type Result struct {
	W      float64 `json:"w,omitempty"`
	H      float64 `json:"h,omitempty"`
	Radius float64 `json:"radius,omitempty"`
	Fill   string  `json:"fill,omitempty"`
	Label  string  `json:"label,omitempty"`
	Ring   string  `json:"ring,omitempty"`
	// Fill2 is the colour at the second probe (a table's stripe).
	Fill2 string `json:"fill2,omitempty"`
	// RingOutset and RingWidth are AppKit's focus ring as AppKitRef measured
	// it from the ring's own mask (metrics.json, focusRing).
	RingOutset float64 `json:"ringOutset,omitempty"`
	RingWidth  float64 `json:"ringWidth,omitempty"`
}

// File is what -write records.
type File struct {
	Source  string                       `json:"source"`
	Scale   int                          `json:"scale"`
	System  map[string]map[string]string `json:"system"`
	Samples map[string]map[string]Result `json:"samples"`
	// Layouts are the frames of the form samples' subviews (AppKitRef's
	// "layouts"), and Sheets what it read of sheets ("sheets"); rexui's form
	// and dialog tests compare ours with them.
	Layouts json.RawMessage `json:"layouts,omitempty"`
	Sheets  json.RawMessage `json:"sheets,omitempty"`
}

var modes = [...]string{"light", "dark"}

func main() {
	ref := flag.String("ref", "", "AppKitRef output directory (light/, dark/, metrics.json)")
	ours := flag.String("ours", "", "tools/appkitref/ours output directory to compare")
	write := flag.String("write", "", "write AppKit's numbers as JSON to this file")
	names := flag.Int("names", -1, "print the sample names of grid page N (catalog.Pages), one per line, for AppKitRef -live, and exit")
	cutFrom := flag.String("cut", "", "cut this 2× capture of a grid page into one PNG per sample in -o, and exit")
	page := flag.Int("page", 0, "with -cut: the grid page the capture shows")
	cutTo := flag.String("o", "", "with -cut: the output directory")
	live := flag.Bool("live", false, "the -ref cells are cut from AppKitRef -live captures: leave out the samples one key window cannot show (focused, inactive)")
	flag.Parse()
	if *cutFrom != "" {
		if *cutTo == "" || *page < 0 || *page >= len(catalog.Pages) {
			fmt.Fprintln(os.Stderr, "usage: measure -cut CAPTURE.png -page N -o DIR")
			os.Exit(2)
		}
		if err := cut(*cutFrom, *page, *cutTo); err != nil {
			fail(err)
		}
		return
	}
	if *names >= 0 && *names < len(catalog.Pages) {
		for _, i := range catalog.Page(*names) {
			fmt.Println(catalog.Samples[i].Name)
		}
		return
	}
	if *ref == "" {
		fmt.Fprintln(os.Stderr, "usage: measure -ref DIR [-ours DIR] [-write FILE]")
		os.Exit(2)
	}
	meta, err := loadMetrics(filepath.Join(*ref, "metrics.json"))
	if err != nil && (*write != "" || !os.IsNotExist(err)) {
		fail(err)
	}
	if meta == nil { // cells cut from live captures: no metrics, no focus rings
		meta = &metrics{}
	}
	out := File{
		Source:  "tools/appkitref/measure -ref " + filepath.Base(*ref) + " (AppKitRef, " + meta.System.OS + ")",
		Scale:   scale,
		System:  map[string]map[string]string{},
		Samples: map[string]map[string]Result{},
		Layouts: meta.Layouts,
		Sheets:  meta.Sheets,
	}
	for mi, ap := range [...]string{"NSAppearanceNameAqua", "NSAppearanceNameDarkAqua"} {
		cs := map[string]string{}
		for _, name := range platform.SystemColorNames {
			if v, ok := meta.Colors[ap][name]; ok {
				var hex string
				if json.Unmarshal(v, &hex) == nil {
					cs[name] = hex
				}
			}
		}
		out.System[modes[mi]] = cs
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight|tabwriter.Debug)
	if *ours != "" {
		fmt.Fprintln(tw, "sample\tmode\tmetric\tAppKit\tours\tΔ\ttolerance\t")
	}
	checked, failed := 0, 0
	for _, s := range catalog.Samples {
		sp := s.Spec
		if *live && (strings.HasSuffix(s.Name, "-focused") || strings.HasSuffix(s.Name, "-inactive")) {
			if *ours != "" {
				fmt.Fprintf(tw, "%s\t\t—\t\t\t\t%s\t\n", s.Name, "not shown live: one key window, no first responder")
			}
			continue
		}
		if sp.What == 0 {
			if *ours != "" {
				fmt.Fprintf(tw, "%s\t\t—\t\t\t\t%s\t\n", s.Name, sp.Note)
			}
			continue
		}
		per := map[string]Result{}
		for mi, mode := range modes {
			a, err := measureFile(filepath.Join(*ref, mode, s.Name+".png"), sp)
			if os.IsNotExist(err) && *write == "" { // not on the page that was cut
				continue
			}
			if err != nil {
				fail(err)
			}
			if sp.What&catalog.MeasureRing != 0 {
				if fr, ok := meta.Controls[mode][s.Name]; ok && fr.FocusRing != nil {
					a.RingOutset = round2(fr.FocusRing.OutsetFromBody)
					// a width across the whole control is a mask AppKitRef
					// could not read the band of (a checkbox's fills its box)
					if fr.FocusRing.Width < 6 {
						a.RingWidth = round2(fr.FocusRing.Width)
					}
				}
			}
			per[mode] = a
			if *ours == "" {
				continue
			}
			o, err := measureFile(filepath.Join(*ours, mode, s.Name+".png"), sp)
			if err != nil {
				fail(err)
			}
			for _, row := range compare(sp.What, a, o) {
				checked++
				mark := ""
				if !row.ok {
					failed++
					mark = " ✗"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s%s\t\n", s.Name, modes[mi], row.metric, row.a, row.o, row.delta, row.tol, mark)
			}
		}
		out.Samples[s.Name] = per
	}
	tw.Flush()
	if *ours != "" {
		fmt.Printf("\n%d of %d measurements within tolerance\n", checked-failed, checked)
	}
	if *write != "" {
		b, err := json.MarshalIndent(out, "", "\t")
		if err != nil {
			fail(err)
		}
		if err := os.WriteFile(*write, append(b, '\n'), 0o644); err != nil {
			fail(err)
		}
		fmt.Println("wrote", *write)
	}
}

type row struct {
	metric, a, o, delta, tol string
	ok                       bool
}

// compare lists the measurements what names, AppKit's a against ours o.
func compare(what catalog.Measure, a, o Result) []row {
	var rows []row
	length := func(name string, x, y, tol float64) {
		d := y - x
		rows = append(rows, row{name, f2(x), f2(y), fmt.Sprintf("%+.2f", d), fmt.Sprintf("±%.1f pt", tol), math.Abs(d) <= tol+1e-9})
	}
	colour := func(name, x, y string, tol float64) {
		d := deltaE(x, y)
		rows = append(rows, row{name, x, y, fmt.Sprintf("ΔE %.2f", d), fmt.Sprintf("ΔE ≤ %.0f", tol), d <= tol})
	}
	if what&catalog.MeasureW != 0 {
		length("width", a.W, o.W, tolLength)
	}
	if what&catalog.MeasureH != 0 {
		length("height", a.H, o.H, tolLength)
	}
	if what&catalog.MeasureRadius != 0 {
		length("radius", a.Radius, o.Radius, tolRadius)
	}
	if what&catalog.MeasureFill != 0 {
		colour("fill", a.Fill, o.Fill, tolFill)
	}
	if what&catalog.MeasureLabel != 0 {
		colour("label", a.Label, o.Label, tolLabel)
	}
	if what&catalog.MeasureRing != 0 {
		colour("ring", a.Ring, o.Ring, tolFill)
	}
	if what&catalog.MeasureFill2 != 0 {
		colour("fill2", a.Fill2, o.Fill2, tolFill)
	}
	return rows
}

func measureFile(path string, sp catalog.Spec) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return Result{}, fmt.Errorf("%s: %w", path, err)
	}
	return Measure(img, sp), nil
}

// px is an image read as opaque 8-bit RGB.
type px struct {
	img image.Image
	b   image.Rectangle
}

func (p px) at(x, y int) [3]int {
	x = min(max(x, p.b.Min.X), p.b.Max.X-1)
	y = min(max(y, p.b.Min.Y), p.b.Max.Y-1)
	r, g, b, _ := p.img.At(x, y).RGBA()
	return [3]int{int(r >> 8), int(g >> 8), int(b >> 8)}
}

// coverage is how much of the way from bg to full c lies, in the channel
// where full differs most from bg, clamped to 0…1.
func coverage(c, full, bg [3]int) float64 {
	k, best := 0, 0
	for i := range 3 {
		if d := abs(full[i] - bg[i]); d > best {
			k, best = i, d
		}
	}
	if best == 0 {
		return 1
	}
	return clamp01(float64(c[k]-bg[k]) / float64(full[k]-bg[k]))
}

// ink reports whether c differs from bg by more than the threshold in a
// channel.
func ink(c, bg [3]int) bool { return dist(c, bg) > thresh }

// Measure measures img (a render at 2× with the frame at 14 pt) as sp says.
func Measure(img image.Image, sp catalog.Spec) Result {
	p := px{img, img.Bounds()}
	if sp.Inside {
		return measureInside(p, sp)
	}
	bg := p.at(p.b.Min.X, p.b.Min.Y)
	cx := origin + int(math.Floor(float64(sp.Probe[0])*scale))
	cy := origin + int(math.Floor(float64(sp.Probe[1])*scale))
	var r Result
	if sp.What&catalog.MeasureFill != 0 {
		r.Fill = hex(p.at(cx, cy))
	}
	var l, rt, t, b float64
	if sp.What&(catalog.MeasureW|catalog.MeasureH|catalog.MeasureRadius|catalog.MeasureRing) != 0 || sp.Label == ([4]float32{}) {
		right := p.b.Max.X - 1
		if sp.Label != ([4]float32{}) {
			right = origin + int(sp.Label[0]*scale) - 1
		}
		l, rt, _, _ = edges(p, bg, cx, cy, right)
		// the height along the column through the row's middle, clear of
		// the corners
		_, _, t, b = edges(p, bg, int(math.Floor((l+rt)/2)), cy, right)
		r.W, r.H = round2((rt-l)/scale), round2((b-t)/scale)
		if sp.What&catalog.MeasureRadius != 0 {
			r.Radius = round2(radius(p, bg, l, t, rt-l, b-t) / scale)
		}
		if sp.What&catalog.MeasureRing != 0 {
			r.Ring = hex(p.at(int(math.Floor(l))+3, cy))
		}
		if sp.What&catalog.MeasureW == 0 {
			r.W = 0
		}
		if sp.What&catalog.MeasureH == 0 {
			r.H = 0
		}
	}
	if sp.What&catalog.MeasureLabel != 0 {
		under := bg
		var x0, y0, x1, y1 int
		if sp.Label == ([4]float32{}) {
			under = p.at(cx, cy)
			x0, y0, x1, y1 = int(l)+4, int(t)+2, int(rt)-4, int(b)-2
		} else {
			x0, y0 = origin+int(sp.Label[0]*scale), origin+int(sp.Label[1]*scale)
			x1, y1 = origin+int(sp.Label[2]*scale), origin+int(sp.Label[3]*scale)
		}
		r.Label = hex(labelInk(p, under, x0, y0, x1, y1))
	}
	return r
}

// measureInside measures a shape inside the sample, as Spec.Inside says: the
// run of the probe's colour (within Spec.Tol) along the probe's column gives
// the top and bottom; along its row, where to start scanning for the left and
// right edges, which are then found from just outside the shape, inward, at
// most 12 pt below its top (clear of the corners, and of text inside it).
func measureInside(p px, sp catalog.Spec) Result {
	tol := sp.Tol
	if tol == 0 {
		tol = thresh
	}
	cx := origin + int(math.Floor(float64(sp.Probe[0])*scale))
	cy := origin + int(math.Floor(float64(sp.Probe[1])*scale))
	pc := p.at(cx, cy)
	row := func(y int) func(int) [3]int { return func(x int) [3]int { return p.at(x, y) } }
	col := func(x int) [3]int { return p.at(cx, x) }
	t := runEdge(col, pc, tol, cy, -1)
	b := runEdge(col, pc, tol, cy, 1)
	l0 := runEdge(row(cy), pc, tol, cx, -1)
	r0 := runEdge(row(cy), pc, tol, cx, 1)
	y := int(math.Floor(t + math.Min((b-t)/2, 12*scale)))
	xs := max(int(math.Floor(l0))-8, origin)
	xe := min(int(math.Ceil(r0))+8, p.b.Max.X-origin-1)
	out := p.at(xs, y)
	l := edge(row(y), out, xs, cx, 1)
	r := edge(row(y), p.at(xe, y), xe, cx, -1)
	var res Result
	res.Fill = hex(pc)
	if sp.What&catalog.MeasureW != 0 {
		res.W = round2((r - l) / scale)
	}
	if sp.What&catalog.MeasureH != 0 {
		res.H = round2((b - t) / scale)
	}
	if sp.What&catalog.MeasureRadius != 0 {
		// within the top 12 pt: AppKit's offscreen inset rows are not
		// merged, and the first row's bottom corners would count
		res.Radius = round2(radius(p, out, l, t, r-l, math.Min(b-t, 24*scale)) / scale)
	}
	if sp.What&catalog.MeasureFill == 0 {
		res.Fill = ""
	}
	if sp.What&catalog.MeasureLabel != 0 {
		res.Label = hex(labelInk(p, pc, origin+int(sp.Label[0]*scale), origin+int(sp.Label[1]*scale),
			origin+int(sp.Label[2]*scale), origin+int(sp.Label[3]*scale)))
	}
	if sp.What&catalog.MeasureFill2 != 0 {
		res.Fill2 = hex(p.at(origin+int(math.Floor(float64(sp.Probe2[0])*scale)), origin+int(math.Floor(float64(sp.Probe2[1])*scale))))
	}
	return res
}

// runEdge is where the run of colour c (within tol in every channel) through
// i = from ends, going in steps of dir: the first pixel beyond it, less the
// share of c it still covers, in pixel coordinates.
func runEdge(get func(int) [3]int, c [3]int, tol, from, dir int) float64 {
	j := from
	for n := 0; n < 4000 && dist(get(j), c) <= tol; n++ {
		j += dir
	}
	cov := coverage(get(j), c, get(j+dir))
	if dir > 0 {
		return float64(j) + cov
	}
	return float64(j+1) - cov
}

// edges finds the shape's left, right, top and bottom edges in pixels with
// subpixel precision, scanning inward from the render's edges (the right one
// at right) along the row and the column through (cx, cy).
func edges(p px, bg [3]int, cx, cy, right int) (l, r, t, b float64) {
	row := func(i int) [3]int { return p.at(i, cy) }
	col := func(i int) [3]int { return p.at(cx, i) }
	return edge(row, bg, p.b.Min.X, cx, 1), edge(row, bg, right, cx, -1),
		edge(col, bg, p.b.Min.Y, cy, 1), edge(col, bg, p.b.Max.Y-1, cy, -1)
}

// edge is where a shape begins along a line of pixels get(i), scanning from
// i = from toward stop in steps of dir. A shape's edge is a sharp rise in the
// difference from bg, antialiased over a pixel or two; a halo or soft shadow
// before it rises gently and is passed over. The edge lies short of the
// pixel where the rise ends, whose colour it blends toward, by the coverage
// of the pixels from the one before the rise. In pixel coordinates.
func edge(get func(int) [3]int, bg [3]int, from, stop, dir int) float64 {
	sharp := func(lo, hi int) bool { return hi-lo >= thresh && float64(hi-lo) >= 0.4*float64(hi) }
	i := from
	for i != stop && !ink(get(i), bg) {
		i += dir
	}
	k, prev, found := i, dist(get(i-dir), bg), false
	for n := 0; n < 60 && k != stop; n++ {
		d := dist(get(k), bg)
		if found = sharp(prev, d); found {
			break
		}
		prev, k = d, k+dir
	}
	if !found { // no sharp rise (a corner's row, a soft edge): from the first ink
		k = i
	}
	// on while the rise lasts: sharp, or any rise where there was no sharp one
	j := k
	for m := 0; m < 6 && j != stop; m++ {
		a, b := dist(get(j), bg), dist(get(j+dir), bg)
		if b <= a || found && !sharp(a, b) {
			break
		}
		j += dir
	}
	f := get(j)
	partial := 0.0
	for m := k - dir; m != j; m += dir {
		partial += coverage(get(m), f, bg)
	}
	if dir > 0 {
		return float64(j) - partial
	}
	return float64(j+1) + partial
}

// dist is the largest channel difference of c from bg.
func dist(c, bg [3]int) int {
	return max(abs(c[0]-bg[0]), abs(c[1]-bg[1]), abs(c[2]-bg[2]))
}

// radius is the top-left corner radius in pixels of the shape whose outer
// edges start at (l, t) and which is w × h: from the area the corner leaves
// uncovered, r²(1 − π/4), summed row by row from each row's edge.
func radius(p px, bg [3]int, l, t, w, h float64) float64 {
	s := int(math.Min(w, h) / 2)
	rowEdge := func(y int) float64 {
		x0 := int(math.Floor(l)) - 2
		return math.Min(edge(func(x int) [3]int { return p.at(x, y) }, bg, x0, x0+s+4, 1), l+float64(s))
	}
	area := 0.0
	y0 := int(math.Ceil(t))
	for y := y0; y < y0+s; y++ {
		area += clamp(rowEdge(y)-l, 0, float64(s))
	}
	if f := float64(y0) - t; f > 0 { // the partly covered top row
		area += f * clamp(rowEdge(y0)-l, 0, float64(s))
	}
	return math.Sqrt(area / (1 - math.Pi/4))
}

// labelInk is the colour in the region that differs most from under, among
// those covering at least 3 pixels (a glyph's solid stem, not an edge's blend).
func labelInk(p px, under [3]int, x0, y0, x1, y1 int) [3]int {
	count := map[[3]int]int{}
	for y := y0; y < y1; y++ {
		for x := x0; x < x1; x++ {
			count[p.at(x, y)]++
		}
	}
	keys := make([][3]int, 0, len(count))
	for c, n := range count {
		if n >= 3 {
			keys = append(keys, c)
		}
	}
	sort.Slice(keys, func(i, j int) bool { // deterministic among equals
		a, b := keys[i], keys[j]
		return a[0]<<16|a[1]<<8|a[2] < b[0]<<16|b[1]<<8|b[2]
	})
	lu := lab(under)
	best, bestD := under, -1.0
	for _, c := range keys {
		if d := cielab.DeltaE2000(lab(c), lu); d > bestD {
			best, bestD = c, d
		}
	}
	return best
}

func lab(c [3]int) cielab.Lab { return cielab.SRGBToLab(uint8(c[0]), uint8(c[1]), uint8(c[2])) }

func hex(c [3]int) string { return fmt.Sprintf("#%02X%02X%02X", c[0], c[1], c[2]) }

func deltaE(a, b string) float64 {
	pa, pb := parseHex(a), parseHex(b)
	return cielab.DeltaE2000(lab(pa), lab(pb))
}

func parseHex(s string) [3]int {
	v, _ := strconv.ParseUint(strings.TrimPrefix(s, "#")[:6], 16, 32)
	return [3]int{int(v >> 16 & 255), int(v >> 8 & 255), int(v & 255)}
}

// metrics is the part of AppKitRef's metrics.json the tool reads.
type metrics struct {
	System struct {
		OS string `json:"osBuild"`
	} `json:"system"`
	Colors   map[string]map[string]json.RawMessage `json:"colors"`
	Controls map[string]map[string]struct {
		FocusRing *struct {
			OutsetFromBody float64 `json:"outsetFromBody"`
			Width          float64 `json:"width"`
		} `json:"focusRing"`
	} `json:"controls"`
	Layouts json.RawMessage `json:"layouts"`
	Sheets  json.RawMessage `json:"sheets"`
}

func loadMetrics(path string) (*metrics, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m metrics
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &m, nil
}

func f2(v float64) string { return strconv.FormatFloat(v, 'f', 2, 64) }

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func clamp01(v float64) float64 { return clamp(v, 0, 1) }

func fail(err error) {
	fmt.Fprintln(os.Stderr, "measure:", err)
	os.Exit(1)
}
