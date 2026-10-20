package main

import (
	"encoding/json"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gonativeui/internal/appkitref"
)

// diffSets implements -diff A B: it compares two reference sets (appkitref.Sets)
// file by file and prints a short Markdown report, the biggest differences
// first. The files are, per set:
//
//   - rexui/testdata/appkit-27.2.json: the recipes' measurements;
//   - theme/testdata/appkit-scroller-27.2.json: NSScroller's geometry;
//   - metrics.json of the default-accent run (research dir): sizes, fonts,
//     colours, behaviour;
//   - scenes/captures/measured.json, when both sets have it;
//   - the sample renders of that run, pixel by pixel.
//
// Run it from the repository root.
func diffSets(a, b string) error {
	var w strings.Builder
	fmt.Fprintf(&w, "# AppKit reference: %s vs %s\n\n", a, b)
	ma, mb := readMachine(a), readMachine(b)
	fmt.Fprintf(&w, "| | %s | %s |\n|---|---|---|\n", a, b)
	for _, k := range []string{"model", "macOS", "display", "appearance", "accent", "accentRendered", "scrollerStyle", "date"} {
		fmt.Fprintf(&w, "| %s | %s | %s |\n", k, machineField(ma, k), machineField(mb, k))
	}
	w.WriteString("\n")

	files := []struct{ title, a, b string }{
		{"Recipe measurements (rexui/testdata/appkit-27.2.json)", appkitref.File(a, "rexui/testdata/appkit-27.2.json"), appkitref.File(b, "rexui/testdata/appkit-27.2.json")},
		{"NSScroller geometry (theme/testdata/appkit-scroller-27.2.json)", appkitref.File(a, "theme/testdata/appkit-scroller-27.2.json"), appkitref.File(b, "theme/testdata/appkit-scroller-27.2.json")},
		{"AppKitRef metrics.json (default accent)", appkitref.Metrics(a), appkitref.Metrics(b)},
		{"appkitscenes measured.json", filepath.Join(appkitref.Scenes(a), "measured.json"), filepath.Join(appkitref.Scenes(b), "measured.json")},
	}
	for _, f := range files {
		ja, err := readTree(f.a)
		if err != nil {
			fmt.Fprintf(&w, "## %s\n\nskipped: %v\n\n", f.title, err)
			continue
		}
		jb, err := readTree(f.b)
		if err != nil {
			fmt.Fprintf(&w, "## %s\n\nskipped: %v\n\n", f.title, err)
			continue
		}
		var d treeDiff
		d.walk("", ja, jb)
		d.report(&w, f.title)
	}
	pixelReport(&w, a, b)
	fmt.Print(w.String())
	return nil
}

func readMachine(set string) map[string]any {
	t, err := readTree(appkitref.Metrics(set))
	if err != nil {
		return nil
	}
	m, _ := t.(map[string]any)["machine"].(map[string]any)
	return m
}

func machineField(m map[string]any, k string) string {
	v, ok := m[k]
	if !ok {
		return "—"
	}
	switch v := v.(type) {
	case map[string]any:
		if k == "macOS" {
			return fmt.Sprintf("%v (%v)", v["version"], v["build"])
		}
		return fmt.Sprintf("%v, %v×%v pt @%vx, %v Hz", v["name"], v["widthPt"], v["heightPt"], v["backingScale"], v["refreshHz"])
	}
	return fmt.Sprint(v)
}

func readTree(path string) (any, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return v, nil
}

var colourRE = regexp.MustCompile(`^#[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)

// a leaf that differs
type leaf struct {
	path   string
	a, b   string
	amount float64 // ΔE for colours, absolute difference for numbers
}

type treeDiff struct {
	compared, same int
	colours, nums  []leaf
	other          []leaf // strings, bools, missing keys, list lengths
}

// ignored keys: where and when, not what
var skipKey = map[string]bool{"machine": true, "source": true, "note": true, "notes": true, "date": true}

func (d *treeDiff) walk(path string, a, b any) {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			d.other = append(d.other, leaf{path, "object", fmt.Sprintf("%T", b), 0})
			return
		}
		for k, av := range x {
			if skipKey[k] {
				continue
			}
			bv, ok := y[k]
			if !ok {
				d.other = append(d.other, leaf{path + "/" + k, "present", "missing", 0})
				continue
			}
			d.walk(path+"/"+k, av, bv)
		}
		for k := range y {
			if _, ok := x[k]; !ok && !skipKey[k] {
				d.other = append(d.other, leaf{path + "/" + k, "missing", "present", 0})
			}
		}
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			d.other = append(d.other, leaf{path, fmt.Sprintf("list of %d", len(x)), fmt.Sprintf("%T", b), 0})
			if ok {
				for i := range min(len(x), len(y)) {
					d.walk(fmt.Sprintf("%s[%d]", path, i), x[i], y[i])
				}
			}
			return
		}
		for i := range x {
			d.walk(fmt.Sprintf("%s[%d]", path, i), x[i], y[i])
		}
	case float64:
		y, ok := b.(float64)
		d.compared++
		if !ok {
			d.other = append(d.other, leaf{path, fmt.Sprint(x), fmt.Sprintf("%T", b), 0})
		} else if x == y {
			d.same++
		} else {
			d.nums = append(d.nums, leaf{path, fmt.Sprint(x), fmt.Sprint(y), math.Abs(x - y)})
		}
	case string:
		y, ok := b.(string)
		d.compared++
		switch {
		case ok && x == y:
			d.same++
		case ok && colourRE.MatchString(x) && colourRE.MatchString(y):
			de := deltaE(x[:7], y[:7])
			if len(x) != len(y) || x[7:] != y[7:] {
				de = math.Max(de, 0.001) // alpha differs
			}
			d.colours = append(d.colours, leaf{path, x, y, de})
		default:
			d.other = append(d.other, leaf{path, x, fmt.Sprint(b), 0})
		}
	default:
		d.compared++
		if fmt.Sprint(a) == fmt.Sprint(b) {
			d.same++
		} else {
			d.other = append(d.other, leaf{path, fmt.Sprint(a), fmt.Sprint(b), 0})
		}
	}
}

const topN = 8

func (d *treeDiff) report(w *strings.Builder, title string) {
	fmt.Fprintf(w, "## %s\n\n%d values compared, %d identical, %d colours, %d numbers and %d other differ.\n\n",
		title, d.compared, d.same, len(d.colours), len(d.nums), len(d.other))
	rank := func(ls []leaf) {
		sort.SliceStable(ls, func(i, j int) bool {
			if ls[i].amount != ls[j].amount {
				return ls[i].amount > ls[j].amount
			}
			return ls[i].path < ls[j].path
		})
	}
	rank(d.colours)
	rank(d.nums)
	table := func(head string, ls []leaf, unit string) {
		if len(ls) == 0 {
			return
		}
		fmt.Fprintf(w, "%s (largest %d of %d):\n\n| path | A | B | %s |\n|---|---|---|---|\n", head, min(topN, len(ls)), len(ls), unit)
		for _, l := range ls[:min(topN, len(ls))] {
			fmt.Fprintf(w, "| `%s` | %s | %s | %.2f |\n", l.path, l.a, l.b, l.amount)
		}
		w.WriteString("\n")
	}
	table("Colours", d.colours, "ΔE2000")
	table("Numbers", d.nums, "|Δ|")
	if len(d.other) > 0 {
		sort.Slice(d.other, func(i, j int) bool { return d.other[i].path < d.other[j].path })
		fmt.Fprintf(w, "Other (first %d of %d):\n\n", min(6, len(d.other)), len(d.other))
		for _, l := range d.other[:min(6, len(d.other))] {
			fmt.Fprintf(w, "- `%s`: %s vs %s\n", l.path, trunc(l.a), trunc(l.b))
		}
		w.WriteString("\n")
	}
}

func trunc(s string) string {
	if len(s) > 60 {
		return s[:57] + "…"
	}
	return s
}

// pixelReport compares the two runs' sample renders pixel by pixel.
func pixelReport(w *strings.Builder, a, b string) {
	type res struct {
		name           string
		diff, total    int
		maxd           int
		sizeA, sizeB   image.Point
		sameSize, ink0 bool
	}
	var rs []res
	for _, mode := range modes {
		da := filepath.Join(filepath.Dir(appkitref.Metrics(a)), mode)
		db := filepath.Join(filepath.Dir(appkitref.Metrics(b)), mode)
		ents, err := os.ReadDir(da)
		if err != nil {
			fmt.Fprintf(w, "## Sample renders\n\nskipped: %v\n\n", err)
			return
		}
		for _, e := range ents {
			if !strings.HasSuffix(e.Name(), ".png") {
				continue
			}
			ia, err1 := readPNG(filepath.Join(da, e.Name()))
			ib, err2 := readPNG(filepath.Join(db, e.Name()))
			if err1 != nil || err2 != nil {
				continue
			}
			r := res{name: mode + "/" + strings.TrimSuffix(e.Name(), ".png"), sizeA: ia.Bounds().Size(), sizeB: ib.Bounds().Size()}
			r.sameSize = r.sizeA == r.sizeB
			if r.sameSize {
				bnd := ia.Bounds()
				for y := bnd.Min.Y; y < bnd.Max.Y; y++ {
					for x := bnd.Min.X; x < bnd.Max.X; x++ {
						r.total++
						ar, ag, ab, _ := ia.At(x, y).RGBA()
						br, bg, bb, _ := ib.At(x, y).RGBA()
						m := max(abs(int(ar>>8)-int(br>>8)), abs(int(ag>>8)-int(bg>>8)), abs(int(ab>>8)-int(bb>>8)))
						if m > 0 {
							r.diff++
							r.maxd = max(r.maxd, m)
						}
					}
				}
			}
			rs = append(rs, r)
		}
	}
	identical, resized := 0, 0
	for _, r := range rs {
		if !r.sameSize {
			resized++
		} else if r.diff == 0 {
			identical++
		}
	}
	fmt.Fprintf(w, "## Sample renders (pixels)\n\n%d renders compared: %d pixel-identical, %d differ, %d have a different size.\n\n", len(rs), identical, len(rs)-identical-resized, resized)
	sort.Slice(rs, func(i, j int) bool {
		fi, fj := float64(rs[i].diff)/math.Max(1, float64(rs[i].total)), float64(rs[j].diff)/math.Max(1, float64(rs[j].total))
		return fi > fj
	})
	if len(rs) > 0 && (identical < len(rs)) {
		fmt.Fprintf(w, "Most different (share of pixels, largest channel step):\n\n| render | pixels differing | max step |\n|---|---|---|\n")
		n := 0
		for _, r := range rs {
			if !r.sameSize {
				fmt.Fprintf(w, "| %s | size %v vs %v | |\n", r.name, r.sizeA, r.sizeB)
			} else if r.diff > 0 {
				fmt.Fprintf(w, "| %s | %.1f%% (%d) | %d |\n", r.name, 100*float64(r.diff)/float64(r.total), r.diff, r.maxd)
			} else {
				continue
			}
			if n++; n == topN {
				break
			}
		}
		w.WriteString("\n")
	}
}

func readPNG(path string) (image.Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	return img, err
}
