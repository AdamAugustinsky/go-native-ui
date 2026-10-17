// Command rexcompare checks a rendering of the Rex look against the reference
// screenshot using the method of docs/design/rex-benchmark.md ("Benchmark app
// and acceptance"):
//
//   - per-region median and 95th percentile CIEDE2000 over the rectangles of
//     the regions file (background bands, pane interiors, tab capsule, active
//     pill; text, icons, edge bands and shadow zones excluded);
//   - probe scanlines: the 50 % crossing between two plateau colours must sit
//     within ±1 px of the reference;
//   - text metrics: cell advance (≥ 40 cells on a row) and row pitch (≥ 8
//     rows), measured on both images, plus the candidate's layout geometry.
//
// It prints a table with PASS/FAIL per check and writes a side-by-side image
// and an absolute-difference heatmap next to the candidate. Both images are
// treated as sRGB. The exit status is 1 if any check fails.
//
//	go run ./tools/rexcompare -ref docs/design/ref/rex-screenshot@2x.png \
//	    -regions docs/design/rex-regions.json -cand out.png
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
)

func main() {
	refPath := flag.String("ref", "docs/design/ref/rex-screenshot@2x.png", "reference screenshot (PNG)")
	regPath := flag.String("regions", "docs/design/rex-regions.json", "regions file")
	candPath := flag.String("cand", "", "candidate rendering (PNG), same size as the reference")
	outDir := flag.String("out-dir", "", "directory for the side-by-side and heatmap images (default: next to the candidate)")
	flag.Parse()
	if *candPath == "" {
		fmt.Fprintln(os.Stderr, "rexcompare: -cand is required")
		flag.Usage()
		os.Exit(2)
	}
	fail := func(err error) {
		fmt.Fprintln(os.Stderr, "rexcompare:", err)
		os.Exit(2)
	}
	spec, err := LoadSpec(*regPath)
	if err != nil {
		fail(err)
	}
	ref, err := loadNRGBA(*refPath)
	if err != nil {
		fail(err)
	}
	cand, err := loadNRGBA(*candPath)
	if err != nil {
		fail(err)
	}
	if ref.Bounds().Size() != cand.Bounds().Size() {
		fail(fmt.Errorf("size mismatch: reference %v, candidate %v", ref.Bounds().Size(), cand.Bounds().Size()))
	}
	if ref.Bounds().Dx() != spec.Ref.W || ref.Bounds().Dy() != spec.Ref.H {
		fail(fmt.Errorf("regions file is for %dx%d, reference is %v", spec.Ref.W, spec.Ref.H, ref.Bounds().Size()))
	}

	rep := &report{}
	checkRegions(rep, spec, ref, cand)
	checkProbes(rep, spec, ref, cand)
	checkText(rep, spec, ref, cand, *candPath)
	rep.print(os.Stdout)

	dir := *outDir
	if dir == "" {
		dir = filepath.Dir(*candPath)
	}
	base := strings.TrimSuffix(filepath.Base(*candPath), filepath.Ext(*candPath))
	sbs := filepath.Join(dir, base+".sbs.png")
	heat := filepath.Join(dir, base+".heat.png")
	if err := writePNG(sbs, sideBySide(ref, cand)); err != nil {
		fail(err)
	}
	if err := writePNG(heat, heatmap(ref, cand, spec)); err != nil {
		fail(err)
	}
	fmt.Printf("\nside-by-side: %s\nheatmap:      %s\n", sbs, heat)
	if rep.fails > 0 {
		os.Exit(1)
	}
}

// report collects the table rows.
type report struct {
	rows          []row
	passes, fails int
}

type row struct{ check, value, limit, result string }

func (r *report) add(check, value, limit string, ok bool) {
	res := "PASS"
	if !ok {
		res = "FAIL"
		r.fails++
	} else {
		r.passes++
	}
	r.rows = append(r.rows, row{check, value, limit, res})
}

func (r *report) print(w *os.File) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CHECK\tVALUE\tLIMIT\tRESULT")
	for _, x := range r.rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", x.check, x.value, x.limit, x.result)
	}
	tw.Flush()
	fmt.Fprintf(w, "\n%d checks passed, %d failed\n", r.passes, r.fails)
}

// ---- colour regions ----

func checkRegions(rep *report, spec *Spec, ref, cand *image.NRGBA) {
	th := spec.Thresholds
	for _, reg := range spec.Regions {
		var des []float64
		for _, q := range reg.Rects {
			for y := q[1]; y < q[3]; y++ {
				for x := q[0]; x < q[2]; x++ {
					a, b := pixel(ref, x, y), pixel(cand, x, y)
					la := SRGBToLab(uint8(a[0]), uint8(a[1]), uint8(a[2]))
					lb := SRGBToLab(uint8(b[0]), uint8(b[1]), uint8(b[2]))
					des = append(des, DeltaE2000(la, lb))
				}
			}
		}
		sort.Float64s(des)
		med, p95 := percentile(des, 0.5), percentile(des, 0.95)
		ok := med <= th.MedianDE && p95 <= th.P95DE
		rep.add(fmt.Sprintf("color %s (%s, %d px)", reg.Name, reg.Group, len(des)),
			fmt.Sprintf("ΔE median %.2f, p95 %.2f", med, p95),
			fmt.Sprintf("≤ %.0f, ≤ %.0f", th.MedianDE, th.P95DE), ok)
	}
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return math.NaN()
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// ---- edge probes ----

func checkProbes(rep *report, spec *Spec, ref, cand *image.NRGBA) {
	tol := spec.Thresholds.EdgePx
	for _, p := range spec.Probes {
		name := fmt.Sprintf("edge %s (%s=%d)", p.Name, map[string]string{"x": "y", "y": "x"}[p.Axis], p.At)
		rp, err := EdgePosition(ref, p)
		if err != nil {
			rep.add(name, "reference: "+err.Error(), fmt.Sprintf("±%.0f px", tol), false)
			continue
		}
		cp, err := EdgePosition(cand, p)
		if err != nil {
			rep.add(name, fmt.Sprintf("ref %.2f, candidate: %s", rp, err), fmt.Sprintf("±%.0f px", tol), false)
			continue
		}
		d := cp - rp
		rep.add(name, fmt.Sprintf("ref %.2f, cand %.2f, Δ %+.2f px", rp, cp, d), fmt.Sprintf("±%.0f px", tol), math.Abs(d) <= tol)
	}
}

// ---- text metrics ----

// metrics is the layout geometry rexlook writes next to its PNG.
type metrics struct {
	CellAdvancePt float64 `json:"cellAdvancePt"`
	RowPitchPt    float64 `json:"rowPitchPt"`
}

func loadMetrics(path string) (metrics, error) {
	var m metrics
	b, err := os.ReadFile(path)
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if m.CellAdvancePt <= 0 || m.RowPitchPt <= 0 {
		return m, fmt.Errorf("%s: missing cellAdvancePt / rowPitchPt", path)
	}
	return m, nil
}

func checkText(rep *report, spec *Spec, ref, cand *image.NRGBA, candPath string) {
	t := spec.Text
	tol := spec.Thresholds.TextPct
	limit := fmt.Sprintf("±%.0f %%", tol)
	pct := func(got, want float64) float64 { return 100 * (got - want) / want }
	measure := func(bands []TextBand, axis string, nominalPt float64, kind string) {
		for _, b := range bands {
			nom := nominalPt * t.PxPerPt
			rp, err1 := MeasurePeriod(ref, b, axis, nom)
			cp, err2 := MeasurePeriod(cand, b, axis, nom)
			name := fmt.Sprintf("text %s %s: pixels, cand vs ref", kind, b.Name)
			if err1 != nil || err2 != nil {
				rep.add(name, fmt.Sprint(err1, err2), limit, false)
				continue
			}
			d := pct(cp, rp)
			rep.add(name, fmt.Sprintf("ref %.3f px, cand %.3f px (%.3f pt), Δ %+.2f %%", rp, cp, cp/t.PxPerPt, d), limit, math.Abs(d) <= tol)
		}
	}
	measure(t.Advance, "x", t.NominalCellAdvancePt, "advance")
	measure(t.Pitch, "y", t.NominalRowPitchPt, "pitch")

	m, err := loadMetrics(candPath + ".metrics.json")
	if err != nil {
		m2, err2 := loadMetrics(strings.TrimSuffix(candPath, filepath.Ext(candPath)) + ".metrics.json")
		if err2 != nil {
			rep.add("text layout geometry (cand metrics file)", err.Error(), limit, false)
			return
		}
		m = m2
	}
	d := pct(m.CellAdvancePt, t.NominalCellAdvancePt)
	rep.add("text cell advance: layout vs nominal",
		fmt.Sprintf("%.3f pt vs %.3f pt, Δ %+.2f %%", m.CellAdvancePt, t.NominalCellAdvancePt, d), limit, math.Abs(d) <= tol)
	d = pct(m.RowPitchPt, t.NominalRowPitchPt)
	rep.add("text row pitch: layout vs nominal",
		fmt.Sprintf("%.3f pt vs %.3f pt, Δ %+.2f %%", m.RowPitchPt, t.NominalRowPitchPt, d), limit, math.Abs(d) <= tol)
}

// ---- images ----

func loadNRGBA(path string) (*image.NRGBA, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if n, ok := img.(*image.NRGBA); ok && n.Rect.Min == (image.Point{}) {
		return n, nil
	}
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(out, out.Bounds(), img, b.Min, draw.Src)
	return out, nil
}

func writePNG(path string, img image.Image) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// onWhite composites an NRGBA pixel over white (the reference has transparent
// window corners).
func onWhite(c color.NRGBA) color.NRGBA {
	a := uint32(c.A)
	f := func(v uint8) uint8 { return uint8((uint32(v)*a + 255*(255-a) + 127) / 255) }
	return color.NRGBA{f(c.R), f(c.G), f(c.B), 255}
}

// sideBySide puts the reference and the candidate next to each other.
func sideBySide(ref, cand *image.NRGBA) *image.NRGBA {
	w, h := ref.Bounds().Dx(), ref.Bounds().Dy()
	const gap = 16
	out := image.NewNRGBA(image.Rect(0, 0, 2*w+gap, h))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.NRGBA{40, 40, 40, 255}), image.Point{}, draw.Src)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			out.SetNRGBA(x, y, onWhite(ref.NRGBAAt(x, y)))
			out.SetNRGBA(w+gap+x, y, onWhite(cand.NRGBAAt(x, y)))
		}
	}
	return out
}

// heatmap shows the absolute difference of the two images: the largest
// channel difference, amplified 4× and mapped black → blue → cyan → yellow →
// red. The comparison regions are outlined in green.
func heatmap(ref, cand *image.NRGBA, spec *Spec) *image.NRGBA {
	w, h := ref.Bounds().Dx(), ref.Bounds().Dy()
	out := image.NewNRGBA(image.Rect(0, 0, w, h))
	ramp := [...]color.NRGBA{{0, 0, 0, 255}, {20, 40, 200, 255}, {0, 200, 220, 255}, {250, 230, 40, 255}, {230, 30, 30, 255}}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			a, b := pixel(ref, x, y), pixel(cand, x, y)
			d := math.Max(math.Abs(a[0]-b[0]), math.Max(math.Abs(a[1]-b[1]), math.Abs(a[2]-b[2])))
			v := math.Min(1, 4*d/255) * float64(len(ramp)-1)
			i := int(v)
			if i >= len(ramp)-1 {
				out.SetNRGBA(x, y, ramp[len(ramp)-1])
				continue
			}
			f := v - float64(i)
			lerp := func(p, q uint8) uint8 { return uint8(float64(p) + (float64(q)-float64(p))*f) }
			out.SetNRGBA(x, y, color.NRGBA{lerp(ramp[i].R, ramp[i+1].R), lerp(ramp[i].G, ramp[i+1].G), lerp(ramp[i].B, ramp[i+1].B), 255})
		}
	}
	green := color.NRGBA{60, 220, 90, 255}
	for _, reg := range spec.Regions {
		for _, q := range reg.Rects {
			for x := q[0]; x < q[2]; x++ {
				out.SetNRGBA(x, q[1], green)
				out.SetNRGBA(x, q[3]-1, green)
			}
			for y := q[1]; y < q[3]; y++ {
				out.SetNRGBA(q[0], y, green)
				out.SetNRGBA(q[2]-1, y, green)
			}
		}
	}
	return out
}
