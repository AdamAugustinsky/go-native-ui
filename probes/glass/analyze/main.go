// Command analyze reads GlassProbe's captures (issue #25): colour differences
// over the toolbar band, a fit of the drawn scroll edge to AppKit's, and the
// sequence checks (flash, sync markers). A probe, not framework code.
//
//	go run ./probes/glass/analyze mask FILE.png
//	go run ./probes/glass/analyze cmp A.png B.png [-band 52] [-x0 90]
//	go run ./probes/glass/analyze fit -content content-light.png -ref ref.png [-scroll 100] [-dark]
//	go run ./probes/glass/analyze flash DIR [-band 52]
//	go run ./probes/glass/analyze marker DIR [-band 52]
package main

import (
	"flag"
	"fmt"
	"image"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"

	"gonativeui/internal/cielab"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: analyze mask|cmp|fit|flash|marker ...")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "mask":
		mask(os.Args[2])
	case "cmp":
		cmp(os.Args[2:])
	case "fit":
		fit(os.Args[2:])
	case "flash":
		flash(os.Args[2:])
	case "marker":
		marker(os.Args[2:])
	case "row":
		row(os.Args[2:])
	default:
		fmt.Fprintln(os.Stderr, "unknown command", os.Args[1])
		os.Exit(2)
	}
}

// img is an image as float sRGB-encoded channels, 0..1, opaque.
type img struct {
	w, h int
	p    []float64 // r, g, b per pixel
}

func load(path string) *img {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	m, err := png.Decode(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, path, err)
		os.Exit(1)
	}
	b := m.Bounds()
	o := &img{w: b.Dx(), h: b.Dy(), p: make([]float64, b.Dx()*b.Dy()*3)}
	for y := 0; y < o.h; y++ {
		for x := 0; x < o.w; x++ {
			r, g, bl, a := m.At(b.Min.X+x, b.Min.Y+y).RGBA()
			i := (y*o.w + x) * 3
			if a == 0 {
				continue
			}
			// un-premultiply (captures are opaque in the band; this is defensive)
			o.p[i] = float64(r) / float64(a)
			o.p[i+1] = float64(g) / float64(a)
			o.p[i+2] = float64(bl) / float64(a)
		}
	}
	return o
}

func (m *img) at(x, y int) (r, g, b float64) {
	i := (y*m.w + x) * 3
	return m.p[i], m.p[i+1], m.p[i+2]
}

func to8(v float64) uint8 { return uint8(math.Round(math.Max(0, math.Min(1, v)) * 255)) }

func lab(r, g, b float64) cielab.Lab { return cielab.SRGBToLab(to8(r), to8(g), to8(b)) }

func hex(r, g, b float64) string { return fmt.Sprintf("#%02X%02X%02X", to8(r), to8(g), to8(b)) }

func mask(path string) {
	m := load(path)
	for y := 0; y < m.h; y += 8 {
		r, _, _ := m.at(0, y)
		fmt.Printf("%3d %.3f\n", y, r)
	}
	r, _, _ := m.at(0, m.h-1)
	fmt.Printf("%3d %.3f\n", m.h-1, r)
}

type stats struct {
	n                   int
	mean, p50, p95, p99 float64
	max                 float64
	maxAt               [2]int
	inkMean, inkMax     float64
	inkN                int
	worstA, worstB      string
}

func summarize(d []float64) (mean, p50, p95, p99, mx float64) {
	if len(d) == 0 {
		return
	}
	s := append([]float64(nil), d...)
	sort.Float64s(s)
	for _, v := range s {
		mean += v
	}
	mean /= float64(len(s))
	return mean, s[len(s)/2], s[len(s)*95/100], s[len(s)*99/100], s[len(s)-1]
}

// region is the compared part of the band: rows y0..y1, columns x0..x1 (pixels).
type region struct{ x0, y0, x1, y1 int }

func compare(a, b *img, rg region, ink func(x, y int) bool) stats {
	var d, di []float64
	var st stats
	for y := rg.y0; y < rg.y1; y++ {
		for x := rg.x0; x < rg.x1; x++ {
			ar, ag, ab := a.at(x, y)
			br, bg, bb := b.at(x, y)
			e := cielab.DeltaE2000(lab(ar, ag, ab), lab(br, bg, bb))
			d = append(d, e)
			if ink != nil && ink(x, y) {
				di = append(di, e)
			}
			if e > st.max {
				st.max, st.maxAt = e, [2]int{x, y}
				st.worstA, st.worstB = hex(ar, ag, ab), hex(br, bg, bb)
			}
		}
	}
	st.n = len(d)
	st.mean, st.p50, st.p95, st.p99, _ = summarize(d)
	if len(di) > 0 {
		st.inkN = len(di)
		st.inkMean, _, _, _, st.inkMax = summarize(di)
	}
	return st
}

func (s stats) String() string {
	r := fmt.Sprintf("ΔE2000 mean %.2f p50 %.2f p95 %.2f p99 %.2f max %.2f (at %d,%d: %s vs %s; n %d)", s.mean, s.p50, s.p95, s.p99, s.max, s.maxAt[0], s.maxAt[1], s.worstA, s.worstB, s.n)
	if s.inkN > 0 {
		r += fmt.Sprintf("; over ink: mean %.2f max %.2f (n %d)", s.inkMean, s.inkMax, s.inkN)
	}
	return r
}

func cmp(args []string) {
	fs := flag.NewFlagSet("cmp", flag.ExitOnError)
	band := fs.Float64("band", 52, "band height, points")
	x0 := fs.Float64("x0", 90, "first column compared, points (clear of the traffic lights)")
	x1 := fs.Float64("x1", 0, "last column, points (0: the width less 10)")
	a, b := load(args[0]), load(args[1])
	fs.Parse(args[2:])
	w := min(a.w, b.w)
	xe := int(*x1 * 2)
	if xe <= 0 {
		xe = w - 20
	}
	bp := int(*band * 2)
	fmt.Printf("band   (rows 4..%d): %v\n", bp-1, compare(a, b, region{int(*x0 * 2), 4, xe, bp}, nil))
	fmt.Printf("below  (rows %d..%d): %v\n", bp+2, bp+200, compare(a, b, region{int(*x0 * 2), bp + 2, xe, min(bp+200, min(a.h, b.h))}, nil))
	fmt.Printf("hairline row %d: a %s b %s\n", bp, hex(a.at(w/2, bp)), hex(b.at(w/2, bp)))
}

// row prints a column of pixels of a capture: row FILE X Y0 Y1
func row(args []string) {
	m := load(args[0])
	var x, y0, y1 int
	fmt.Sscan(args[1], &x)
	fmt.Sscan(args[2], &y0)
	fmt.Sscan(args[3], &y1)
	for y := y0; y <= y1 && y < m.h; y++ {
		fmt.Printf("%4d %s\n", y, hex(m.at(x, y)))
	}
}

// --- the fit ---

func lin(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

func enc(v float64) float64 {
	v = math.Max(0, v)
	if v <= 0.0031308 {
		return v * 12.92
	}
	return 1.055*math.Pow(v, 1/2.4) - 0.055
}

// blur is a separable Gaussian of sigma pixels over w x h planes with
// renormalised (clamped-away) edges, as CA's inputNormalizeEdges.
func blur(src []float64, w, h int, sigma float64) []float64 {
	if sigma <= 0 {
		return append([]float64(nil), src...)
	}
	r := int(math.Ceil(sigma * 3))
	k := make([]float64, 2*r+1)
	for i := -r; i <= r; i++ {
		k[i+r] = math.Exp(-0.5 * float64(i*i) / (sigma * sigma))
	}
	tmp := make([]float64, len(src))
	out := make([]float64, len(src))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var s [3]float64
			ws := 0.0
			for i := -r; i <= r; i++ {
				xx := x + i
				if xx < 0 || xx >= w {
					continue
				}
				kw := k[i+r]
				j := (y*w + xx) * 3
				s[0] += kw * src[j]
				s[1] += kw * src[j+1]
				s[2] += kw * src[j+2]
				ws += kw
			}
			j := (y*w + x) * 3
			tmp[j], tmp[j+1], tmp[j+2] = s[0]/ws, s[1]/ws, s[2]/ws
		}
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var s [3]float64
			ws := 0.0
			for i := -r; i <= r; i++ {
				yy := y + i
				if yy < 0 || yy >= h {
					continue
				}
				kw := k[i+r]
				j := (yy*w + x) * 3
				s[0] += kw * tmp[j]
				s[1] += kw * tmp[j+1]
				s[2] += kw * tmp[j+2]
				ws += kw
			}
			j := (y*w + x) * 3
			out[j], out[j+1], out[j+2] = s[0]/ws, s[1]/ws, s[2]/ws
		}
	}
	return out
}

// sat applies AppKit's matrix for saturation s (luma weights 0.2126,
// 0.7152, 0.0722) and adds off.
func sat(c [3]float64, s, off float64) [3]float64 {
	l := 0.2126*c[0] + 0.7152*c[1] + 0.0722*c[2]
	return [3]float64{l + s*(c[0]-l) + off, l + s*(c[1]-l) + off, l + s*(c[2]-l) + off}
}

type model struct {
	name      string
	linear    bool
	sigmaPt   float64
	replayA   float64 // the window colour replayed under the blur (0.5 light, 0.6 dark)
	satS, off float64
	flatA     float64 // > 0: D4b's flat bar instead (no blur)
	bar       [3]float64
	bandOnly  bool // the blur sees the band only (CA's backdrop); else the band and 16 pt below
	p3        bool // do the arithmetic on Display P3-encoded values (the display's space)
}

// toP3 and fromP3 convert sRGB-encoded colours to Display P3-encoded and back.
func toP3(c [3]float64) [3]float64 {
	r, g, b := lin(c[0]), lin(c[1]), lin(c[2])
	return [3]float64{enc(0.8225*r + 0.1774*g), enc(0.0332*r + 0.9669*g), enc(0.0171*r + 0.0724*g + 0.9108*b)}
}

func fromP3(c [3]float64) [3]float64 {
	r, g, b := lin(c[0]), lin(c[1]), lin(c[2])
	return [3]float64{enc(1.2249*r - 0.2247*g), enc(-0.0420*r + 1.0419*g), enc(-0.0197*r - 0.0786*g + 1.0979*b)}
}

// render renders model m over the band of content scrolled by scroll px
// into a w x bandPx image (only rows 0..bandPx).
func render(m model, content *img, scrollPx, bandPx, w int, bg [3]float64) *img {
	h := bandPx
	if !m.bandOnly {
		h += 32
	}
	pl := make([]float64, w*h*3)
	conv := func(v float64) float64 { return v }
	back := func(v float64) float64 { return v }
	if m.linear {
		conv, back = lin, enc
	}
	for y := 0; y < h; y++ {
		cy := y - bandPx + scrollPx
		for x := 0; x < w; x++ {
			var c [3]float64
			if cy >= 0 && cy < content.h && x < content.w {
				c[0], c[1], c[2] = content.at(x, cy)
			} else {
				c = bg
			}
			bgc, barc := bg, m.bar
			if m.p3 {
				c, bgc, barc = toP3(c), toP3(bg), toP3(m.bar)
			}
			j := (y*w + x) * 3
			for k := 0; k < 3; k++ {
				v := conv(c[k])
				if m.flatA > 0 {
					v = v*(1-m.flatA) + conv(barc[k])*m.flatA
				} else {
					v = v*(1-m.replayA) + conv(bgc[k])*m.replayA
				}
				pl[j+k] = v
			}
		}
	}
	if m.flatA == 0 {
		pl = blur(pl, w, h, m.sigmaPt*2)
	}
	o := &img{w: w, h: bandPx, p: make([]float64, w*bandPx*3)}
	for y := 0; y < bandPx; y++ {
		for x := 0; x < w; x++ {
			j := (y*w + x) * 3
			c := [3]float64{pl[j], pl[j+1], pl[j+2]}
			if m.flatA == 0 {
				c = sat(c, m.satS, m.off)
			}
			for k := 0; k < 3; k++ {
				c[k] = back(math.Max(0, math.Min(1, c[k])))
			}
			if m.p3 {
				c = fromP3(c)
			}
			for k := 0; k < 3; k++ {
				o.p[j+k] = math.Max(0, math.Min(1, c[k]))
			}
		}
	}
	return o
}

func fit(args []string) {
	fs := flag.NewFlagSet("fit", flag.ExitOnError)
	contentPath := fs.String("content", "", "content bitmap (GlassProbe's content-*.png)")
	refPath := fs.String("ref", "", "the capture to fit (AppKit's)")
	scroll := fs.Float64("scroll", 100, "content scrolled under the band, points")
	band := fs.Float64("band", 52, "band height, points")
	dark := fs.Bool("dark", false, "dark appearance")
	x0 := fs.Float64("x0", 90, "first column compared, points")
	x1 := fs.Float64("x1", 0, "last column compared, points (0: width less 10)")
	sigmas := fs.String("sigmas", "0,1,2,3,4,5,6,8,10,12,16", "blur sigmas to try, points")
	out := fs.String("out", "", "write the best model's band here (PNG)")
	recipe := fs.String("recipe", "thin", "AppKit's recipe to start from: thin (an NSScrollView's ThinFilm pocket) or hard (the titlebar's)")
	quiet := fs.Bool("q", false, "print only the summary")
	eval := fs.String("eval", "", "evaluate one model only: sigmaPt,replay,sat,off")
	fs.Parse(args)
	content, ref := load(*contentPath), load(*refPath)
	bandPx, scrollPx := int(*band*2), int(*scroll*2)
	w := min(content.w, ref.w)
	xe := int(*x1 * 2)
	if xe <= 0 {
		xe = w - 20
	}
	rg := region{int(*x0 * 2), 4, xe, bandPx}
	bg := [3]float64{1, 1, 1}
	bar := [3]float64{1, 1, 1}
	replay, flatA := 0.5, 0.82
	if *dark {
		g := 0x1E / 255.0
		bg = [3]float64{g, g, g}
		b := 0x29 / 255.0
		bar = [3]float64{b, b, b}
		replay, flatA = 0.6, 0.78
	}
	// ink: content pixels whose luminance differs from the window colour by
	// more than a quarter (text and icons; not stripes)
	ink := func(x, y int) bool {
		cy := y - bandPx + scrollPx
		if cy < 0 || cy >= content.h {
			return false
		}
		r, g, b := content.at(x, cy)
		l := 0.2126*r + 0.7152*g + 0.0722*b
		lb := 0.2126*bg[0] + 0.7152*bg[1] + 0.0722*bg[2]
		return math.Abs(l-lb) > 0.25
	}
	cmpModel := func(m model) (stats, *img) {
		o := render(m, content, scrollPx, bandPx, w, bg)
		return compare(o, ref, rg, ink), o
	}
	s, _ := cmpModel(model{name: "D4b flat", flatA: flatA, bar: bar})
	fmt.Printf("D4b flat bar (%.0f%% of %s): %v\n", flatA*100, hex(bar[0], bar[1], bar[2]), s)
	if *eval != "" {
		v := splitFloats(*eval)
		m := model{sigmaPt: v[0], replayA: v[1], satS: v[2], off: v[3], bandOnly: true}
		s, o := cmpModel(m)
		fmt.Printf("model sigma %.1f pt replay %.4f sat %.2f off %.3f: %v\n", v[0], v[1], v[2], v[3], s)
		if *out != "" {
			writePNG(*out, o)
		}
		return
	}
	var sig []float64
	for _, f := range splitFloats(*sigmas) {
		sig = append(sig, f)
	}
	best, bestS := model{}, stats{mean: 1e9}
	var bestImg *img
	satS := 1.25
	if *recipe == "hard" {
		replay, satS = 0.8245, 1
	}
	for _, p3 := range []bool{false, true} {
		for _, linear := range []bool{false, true} {
			for _, bo := range []bool{true, false} {
				for _, sg := range sig {
					m := model{name: "recipe", linear: linear, sigmaPt: sg, replayA: replay, satS: satS, off: 0.03, bandOnly: bo, p3: p3}
					s, o := cmpModel(m)
					if !*quiet {
						fmt.Printf("recipe p3=%v linear=%v bandOnly=%v sigma %4.1f pt: %v\n", p3, linear, bo, sg, s)
					}
					if s.mean < bestS.mean {
						best, bestS, bestImg = m, s, o
					}
				}
			}
		}
	}
	fmt.Printf("\nbest: p3=%v", best.p3)
	fmt.Printf("  linear=%v bandOnly=%v sigma %.1f pt replay %.2f sat %.2f off %.2f: %v\n", best.linear, best.bandOnly, best.sigmaPt, best.replayA, best.satS, best.off, bestS)
	// refine the replay alpha, saturation and offset around the recipe
	for _, ra := range []float64{best.replayA - 0.06, best.replayA - 0.03, best.replayA, best.replayA + 0.03, best.replayA + 0.06} {
		for _, ss := range []float64{1.0, 1.25, 1.5} {
			for _, off := range []float64{0, 0.015, 0.03, 0.04, 0.05} {
				m := best
				m.replayA, m.satS, m.off = ra, ss, off
				s, o := cmpModel(m)
				if s.mean < bestS.mean-0.005 {
					best, bestS, bestImg = m, s, o
				}
			}
		}
	}
	fmt.Printf("refined: p3=%v ", best.p3)
	fmt.Printf("linear=%v bandOnly=%v sigma %.1f pt replay %.2f sat %.2f off %.3f: %v\n", best.linear, best.bandOnly, best.sigmaPt, best.replayA, best.satS, best.off, bestS)
	if *out != "" && bestImg != nil {
		writePNG(*out, bestImg)
	}
}

func splitFloats(s string) []float64 {
	var r []float64
	var cur string
	for _, c := range s + "," {
		if c == ',' {
			if cur != "" {
				var f float64
				fmt.Sscan(cur, &f)
				r = append(r, f)
			}
			cur = ""
			continue
		}
		cur += string(c)
	}
	return r
}

func writePNG(path string, m *img) {
	o := image.NewNRGBA(image.Rect(0, 0, m.w, m.h))
	for y := 0; y < m.h; y++ {
		for x := 0; x < m.w; x++ {
			r, g, b := m.at(x, y)
			i := o.PixOffset(x, y)
			o.Pix[i], o.Pix[i+1], o.Pix[i+2], o.Pix[i+3] = to8(r), to8(g), to8(b), 255
		}
	}
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	png.Encode(f, o)
	f.Close()
}

// --- sequences ---

func frames(dir string) []string {
	m, _ := filepath.Glob(filepath.Join(dir, "seq-*.png"))
	sort.Strings(m)
	return m
}

// hueClass says whether a colour is the flash's blue (b), orange (o) or
// neither (?).
func hueClass(r, g, b float64) byte {
	switch {
	case b > r+0.15:
		return 'b'
	case r > b+0.15:
		return 'o'
	}
	return '?'
}

// flash classifies, per captured frame, the content's colour (below the band)
// and the band's (middle of the band, clear of capsules) and counts
// disagreements: a band one frame behind the content disagrees at every
// colour change caught between the two.
func flash(args []string) {
	fs := flag.NewFlagSet("flash", flag.ExitOnError)
	band := fs.Float64("band", 52, "band height, points")
	bx := fs.Float64("x", 450, "band sample column, points")
	dir := args[0]
	fs.Parse(args[1:])
	agree, disagree, unknown := 0, 0, 0
	var line []byte
	for _, p := range frames(dir) {
		m := load(p)
		cy := int(*band*2) + 120
		cr, cg, cb := m.at(m.w/2, cy)
		br, bg, bb := m.at(int(*bx*2), int(*band))
		c, b := hueClass(cr, cg, cb), hueClass(br, bg, bb)
		switch {
		case c == '?' || b == '?':
			unknown++
			line = append(line, '?')
		case c == b:
			agree++
			line = append(line, '.')
		default:
			disagree++
			line = append(line, 'X')
		}
		fmt.Printf("%s content %s band %s\n", filepath.Base(p), hex(cr, cg, cb), hex(br, bg, bb))
	}
	fmt.Printf("frames %d: band agrees %d, disagrees %d, unclassified %d  %s\n", agree+disagree+unknown, agree, disagree, unknown, line)
}

// marker finds the content's green marker and the foreground's red marker
// just below the band and reports their x per frame.
func marker(args []string) {
	fs := flag.NewFlagSet("marker", flag.ExitOnError)
	band := fs.Float64("band", 52, "band height, points")
	dir := args[0]
	fs.Parse(args[1:])
	same, apart := 0, 0
	for _, p := range frames(dir) {
		m := load(p)
		find := func(y int, want func(r, g, b float64) bool) int {
			for x := 390; x < m.w; x++ { // the markers start at 200 pt, clear of the rows' icons
				if want(m.at(x, y)) {
					return x
				}
			}
			return -1
		}
		red := find(int((*band+5)*2), func(r, g, b float64) bool { return r > 0.8 && g < 0.3 && b < 0.3 })
		green := find(int((*band+11)*2), func(r, g, b float64) bool { return g > 0.5 && r < 0.3 && b < 0.3 })
		st := "same"
		if red != green {
			st = "APART"
			apart++
		} else {
			same++
		}
		fmt.Printf("%s fg %d content %d %s\n", filepath.Base(p), red, green, st)
	}
	fmt.Printf("frames %d: same %d, apart %d\n", same+apart, same, apart)
}
