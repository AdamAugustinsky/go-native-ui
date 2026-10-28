//go:build darwin

package gpu

import (
	"encoding/json"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestContinuousCornerIsAppKits renders a black rect with rounded corners at
// 2x and measures how far the corner cuts into each row (the width of the
// white to the left of the black, with the edge's coverage), as
// tools/appkitref/corner.sh measures Core Animation's corner at the same
// rows: circular arcs agree to a few hundredths of a point, and ShapeContinuous
// (the squircle) to 0.3 pt at most, for the radii of the controls (about 1 % of
// the radius; the fit is for radii up to 30 pt, the table's largest is 24).
func TestContinuousCornerIsAppKits(t *testing.T) {
	raw, err := os.ReadFile("testdata/appkit-corner-27.2.json")
	if err != nil {
		t.Fatal(err)
	}
	var ref struct {
		Corners []struct {
			Radius               float64
			Continuous, Circular string // x of each 1/16 pt row, top down
		}
	}
	if err := json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	r := newTestRenderer(t)
	for _, c := range ref.Corners {
		for _, curve := range []struct {
			name string
			tex  TextureID
			want string
			tol  float64 // pt
		}{{"circular", 0, c.Circular, 0.05}, {"continuous", ShapeContinuous, c.Continuous, 0.3}} {
			const w, h, off = 300, 300, 20
			q := solid(off, off, off+200, off+200, 0x000000FF)
			q.Radii = [4]float32{float32(2 * c.Radius), float32(2 * c.Radius), float32(2 * c.Radius), float32(2 * c.Radius)}
			q.Tex = curve.tex
			m := render(r, w, h, [4]float32{1, 1, 1, 1}, q)
			xs := strings.Fields(curve.want)
			// AppKit's cut per pixel row (a half point, 8 of its rows), ours
			// from the coverage, and the distance between the curves at right
			// angles to them: at the top of the corner the curve runs nearly
			// along the rows, and a small error across it is a large one along
			rows := len(xs) / 8
			want, got := make([]float64, rows), make([]float64, rows)
			for j := range want {
				for _, f := range xs[8*j : 8*j+8] {
					v, _ := strconv.ParseFloat(f, 64)
					want[j] += v / 8
				}
				for x := off; x < off+100; x++ { // px of the row left of the black, in the rect
					got[j] += float64(m.at(x, off+j)[0]) / 255 / 2
				}
			}
			worst := 0.0
			for j := 1; j < rows-1; j++ {
				slope := want[j+1] - want[j-1] // across a point of rows
				if d := math.Abs(got[j]-want[j]) / math.Hypot(1, slope); d > worst {
					worst = d
				}
			}
			if worst > curve.tol {
				t.Errorf("radius %v pt, %s: the corner is off AppKit's by up to %.2f pt (tolerance %v)", c.Radius, curve.name, worst, curve.tol)
			}
		}
	}
}
