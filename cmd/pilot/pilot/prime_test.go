package pilot

import (
	"testing"

	"gonativeui/internal/allocsite"
	"gonativeui/text"
	"gonativeui/theme"
)

// After primeNames, the synthetic names shape and rasterize without an allocation of ours
// in the fonts the rows are drawn in (the theme's fonts, the body style's, the
// list's mono one). On the Go text engine a script's first shape in a face
// builds a plan, and on macOS the first name that needs a fallback font makes
// its face; a measured window must not be where either happens (D27: CI saw a
// Greek name's plan in Pilot's first window, and a face in a later one).
// Without the priming the same shapes allocate, which shows the test sees a
// missing plan or face.
func TestPrimeNamesBuildsEveryPlan(t *testing.T) {
	fresh := func() (*text.System, *theme.Resolved) {
		ts, err := text.New()
		if err != nil {
			t.Skip(err)
		}
		t.Cleanup(ts.Close)
		ts.ReserveRuns(text.MaxCachedRuns, text.MaxCachedGlyphs, text.MaxCachedTextBytes)
		r := look(false).Resolve(ts, 2, theme.Light)
		return ts, &r
	}
	buf := make([]byte, 0, 64)
	shapeNames := func(ts *text.System, r *theme.Resolved) {
		f := r.Font
		var fonts [theme.NumTextStyles + 5]text.FontKey
		n := copy(fonts[:], []text.FontKey{f.UI, f.UISemi, f.Small, f.Mono, f.MonoBold})
		for i := range r.Styles { // any text style may be a row's, a cell's or a header's
			fonts[n] = r.Styles[i].Font
			n++
		}
		for _, k := range fonts[:n] {
			for _, n := range synthNames {
				// other strings than primeNames' (new to the run cache), of
				// the same characters
				buf = append(append(buf[:0], n...), "_9876543210.txt"...)
				v := ts.Run(ts.Shape(k, buf))
				for i, gid := range v.Glyphs {
					for sub := uint8(0); sub < 4; sub++ {
						ts.Glyph(v.Faces[i], gid, sub)
					}
				}
			}
		}
	}

	ts, r := fresh()
	if n, _ := oursDuring(func() { shapeNames(ts, r) }); n == 0 {
		t.Fatal("the names shaped afresh allocated nothing: the test cannot see a missing plan")
	}
	ts, r = fresh()
	primeNames(ts, r)
	if n, sites := oursDuring(func() { shapeNames(ts, r) }); n != 0 {
		t.Fatalf("%d allocations of ours shaping/rasterizing the names after primeNames:\n%s", n, sites)
	}
}

// oursDuring returns the objects our code allocated during fn, and their sites.
func oursDuring(fn func()) (int64, string) { return allocsite.During(fn) }
