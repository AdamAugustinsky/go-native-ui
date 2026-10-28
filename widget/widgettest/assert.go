package widgettest

import (
	"bytes"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gonativeui/ui"
)

// Golden assertions. AssertTree compares the semantic tree of a window with a
// text file, AssertImage a rendering with a PNG, both in the testdata
// directory of the test's package. A run with -update writes the files; a
// failing run writes what it got to testdata/failed/ (with a picture of the
// difference for an image), so that a change that is meant is promoted by
// copying the file over, and one that is not is looked at.
//
// The -update flag belongs to the test package, because two packages cannot
// define it both. A package that defines none calls DefineUpdateFlag once, in
// a package-level variable; one that already has its own flag named update
// (a bool) needs nothing: the assertions read it by name. The environment
// variable GNUI_UPDATE=1 does the same as -update for packages that have
// neither.

// DefineUpdateFlag defines the bool flag -update in the command line, unless
// the test package defined it already, and returns it. Call it from a
// package-level variable of the test package:
//
//	var _ = widgettest.DefineUpdateFlag()
//
// The returned pointer is the flag's value when this call defined it, and a
// placeholder when the package had defined it: the assertions read the flag by
// name, so the value need not be used.
func DefineUpdateFlag() *bool {
	if flag.Lookup("update") != nil {
		return new(bool)
	}
	return flag.Bool("update", false, "rewrite the golden files of testdata (widgettest.AssertTree, AssertImage)")
}

// updating reports whether the run was started with -update (or
// GNUI_UPDATE=1).
func updating() bool {
	if f := flag.Lookup("update"); f != nil && f.Value.String() == "true" {
		return true
	}
	return os.Getenv("GNUI_UPDATE") == "1"
}

const (
	goldenDir = "testdata"
	failedDir = "testdata/failed"
)

// AssertTree fails the test unless the semantic tree dumps (Dump) to the
// text in testdata/<name>.txt. With -update the file is written; on a
// mismatch what the tree dumped to is written to testdata/failed/<name>.txt.
func AssertTree(t testing.TB, name string, tree *ui.SemTree) {
	t.Helper()
	AssertText(t, name, Dump(tree))
}

// AssertText is AssertTree for any text: the golden file is
// testdata/<name>.txt.
func AssertText(t testing.TB, name, got string) {
	t.Helper()
	if err := assertText(goldenDir, name, got, updating()); err != nil {
		t.Fatal(err)
	}
}

// AssertTree is the package function on the semantic tree of the rig's last
// frame.
func (r *Rig) AssertTree(name string) { r.t.Helper(); AssertTree(r.t, name, r.Tree()) }

func assertText(dir, name, got string, update bool) error {
	path := filepath.Join(dir, name+".txt")
	failed := filepath.Join(dir, "failed", name+".txt")
	if update {
		os.Remove(failed)
		return writeFile(path, []byte(got))
	}
	want, err := os.ReadFile(path)
	if err != nil {
		writeFile(failed, []byte(got))
		return fmt.Errorf("widgettest: %v (run the test with -update to write it; what the test got is in %s)", err, failed)
	}
	want = bytes.ReplaceAll(want, []byte("\r\n"), []byte("\n")) // a Windows checkout may have converted the line endings
	if bytes.Equal(want, []byte(got)) {
		os.Remove(failed)
		return nil
	}
	writeFile(failed, []byte(got))
	wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return fmt.Errorf("widgettest: %s differs at line %d:\n  want %s\n  got  %s\n(what the test got is in %s; run the test with -update if the change is intended)", path, i+1, w, g, failed)
		}
	}
	return fmt.Errorf("widgettest: %s differs", path)
}

func writeFile(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// Tolerance says how far an image may be from its golden file and still
// match. Two renderings of the same scene differ in the last bits (GPU
// rounding, text antialiasing, a different OS release), so a byte comparison
// fails for nothing; this one counts only what a person could see, and counts
// it strictly, so that a control that moved by one pixel is not lost in a big
// picture.
//
// Each pixel is compared in the CIELAB colour space (sRGB, D65), where the
// distance between two colours (CIE76 ΔE) follows how different they look:
// about 1 is the least a trained eye can tell apart on a flat patch, 2.3 is
// one just-noticeable difference. Pixels are first composited over white (so
// an alpha difference counts only as far as it shows over white; Exact
// compares the raw pixels, alpha included). A pixel counts as different when
// its ΔE exceeds Delta. The images match when they have the same size, no
// pixel is off by more than Hard, and the number of different pixels is at
// most Fraction of all of them and at most MaxPixels.
//
// The zero Tolerance is the default: Delta 1.5 (a difference a person can
// just see; one level of one channel is below it), Fraction 0.001 (antialiased
// edges of text move by this much between renderers), MaxPixels 16 and Hard
// 25 (nothing may be a different colour altogether). MaxPixels is what keeps
// the default strict as the picture grows: a thousandth of a window is
// thousands of pixels, enough to hide a checkbox that moved by a pixel (about
// 60 pixels of its outline change), so the allowance never exceeds sixteen.
// For a control, compare the control (AssertImageRect), not the window around
// it. A Tolerance that sets any field is taken as written: an unset
// MaxPixels there means no cap, and Exact accepts only identical pixels.
type Tolerance struct {
	// Delta is the ΔE up to which a pixel is the same colour.
	Delta float64
	// Fraction is the share of pixels (0..1) that may exceed Delta.
	Fraction float64
	// Hard is the ΔE no pixel may exceed.
	Hard float64
	// MaxPixels is the most pixels that may exceed Delta, whatever Fraction
	// allows for a big picture (0: no cap in an explicit Tolerance).
	MaxPixels int
}

// Exact is the Tolerance that accepts only identical pixels, alpha included
// (a negative Delta means any difference counts, a negative Hard no limit).
var Exact = Tolerance{Delta: -1, Fraction: 0, Hard: -1}

func (tol Tolerance) resolved() Tolerance {
	if tol == (Tolerance{}) {
		return Tolerance{Delta: 1.5, Fraction: 0.001, Hard: 25, MaxPixels: 16}
	}
	return tol
}

// AssertImage fails the test unless img matches testdata/<name>.png within
// tol (the zero Tolerance is the default; see Tolerance). With -update the
// file is written. On a mismatch the image is written to
// testdata/failed/<name>.png, and a picture of where it differs, the pixels
// beyond the tolerance in red over a faded copy of the golden image, to
// testdata/failed/<name>.diff.png.
//
// A golden image is one machine's pixels: its GPU, its fonts and its OS
// version. In CI (CI set) the comparison is skipped and logged, since a
// runner draws with other fonts and another rasteriser (WARP, llvmpipe); the
// tree goldens and the layout audit are what CI checks. GNUI_CI_IMAGES=1
// compares there too.
func AssertImage(t testing.TB, name string, img image.Image, tol Tolerance) {
	t.Helper()
	if skipImageInCI(t, name) {
		return
	}
	if err := assertImage(goldenDir, name, img, tol, updating()); err != nil {
		t.Fatal(err)
	}
}

// skipImageInCI reports, and logs, that golden image name is not compared:
// in CI unless GNUI_CI_IMAGES=1 (see AssertImage).
func skipImageInCI(t testing.TB, name string) bool {
	t.Helper()
	if os.Getenv("CI") == "" || os.Getenv("GNUI_CI_IMAGES") == "1" || updating() {
		return false
	}
	t.Logf("widgettest: %s.png not compared in CI (a golden image is the machine it was made on; GNUI_CI_IMAGES=1 compares)", name)
	return true
}

// AssertImageRect is AssertImage for the part of img inside r (in img's
// coordinates): the golden file is the crop. Compare a control, not the
// window around it; the tolerance then scales with the control (see
// Tolerance).
func AssertImageRect(t testing.TB, name string, img image.Image, r image.Rectangle, tol Tolerance) {
	t.Helper()
	if skipImageInCI(t, name) {
		return // before the crop: a rectangle measured on the golden's machine may not fit another's image
	}
	crop, err := Crop(img, r)
	if err != nil {
		t.Fatal(err)
	}
	AssertImage(t, name, crop, tol)
}

// Crop copies the part of img inside r (in img's coordinates) into an image
// of its own whose origin is (0, 0). It is an error if r is empty or reaches
// outside img: a crop that silently lost its edge would compare less than the
// test asked for.
func Crop(img image.Image, r image.Rectangle) (*image.RGBA, error) {
	b := img.Bounds()
	if r.Empty() || !r.In(b) {
		return nil, fmt.Errorf("widgettest: crop %v is empty or outside the %v image", r, b)
	}
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			out.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return out, nil
}

func assertImage(dir, name string, img image.Image, tol Tolerance, update bool) error {
	path := filepath.Join(dir, name+".png")
	failed := filepath.Join(dir, "failed", name+".png")
	diffPath := filepath.Join(dir, "failed", name+".diff.png")
	if update {
		os.Remove(failed)
		os.Remove(diffPath)
		return writePNG(path, img)
	}
	f, err := os.Open(path)
	if err != nil {
		writePNG(failed, img)
		return fmt.Errorf("widgettest: %v (run the test with -update to write it; the image the test got is in %s)", err, failed)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		return fmt.Errorf("widgettest: %s: %v", path, err)
	}
	res := CompareImages(want, img, tol)
	if res.OK {
		os.Remove(failed)
		os.Remove(diffPath)
		return nil
	}
	writePNG(failed, img)
	if res.Diff != nil {
		writePNG(diffPath, res.Diff)
	}
	return fmt.Errorf("widgettest: %s: %s (the image the test got is in %s, the difference in %s; run the test with -update if the change is intended)", path, res.Why, failed, diffPath)
}

func writePNG(path string, img image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o644)
}

// Comparison is the result of CompareImages.
type Comparison struct {
	// OK is true when the images match within the tolerance.
	OK bool
	// Why says what differs when OK is false.
	Why string
	// Different is the number of pixels beyond Tolerance.Delta and Pixels the
	// number compared; Worst is the largest ΔE and WorstAt where it is.
	Different, Pixels int
	Worst             float64
	WorstAt           image.Point
	// FirstAt is where the first pixel beyond Delta is.
	FirstAt image.Point
	// Diff is a picture of the difference (nil when the sizes differ).
	Diff *image.RGBA
}

// CompareImages compares got with want within tol (see Tolerance).
func CompareImages(want, got image.Image, tol Tolerance) Comparison {
	tol = tol.resolved()
	wb, gb := want.Bounds(), got.Bounds()
	if wb.Dx() != gb.Dx() || wb.Dy() != gb.Dy() {
		return Comparison{Why: fmt.Sprintf("the image is %d×%d pixels, the golden image %d×%d", gb.Dx(), gb.Dy(), wb.Dx(), wb.Dy())}
	}
	c := Comparison{Pixels: wb.Dx() * wb.Dy()}
	c.Diff = image.NewRGBA(image.Rect(0, 0, wb.Dx(), wb.Dy()))
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			wc, gc := want.At(wb.Min.X+x, wb.Min.Y+y), got.At(gb.Min.X+x, gb.Min.Y+y)
			wr, wg, wbl := overWhite(wc)
			gr, gg, gbl := overWhite(gc)
			var d float64
			same := wr == gr && wg == gg && wbl == gbl
			if tol.Delta < 0 { // Exact: the raw pixels, alpha included, not what shows over white
				same = sameRGBA(wc, gc)
			}
			if wr != gr || wg != gg || wbl != gbl {
				d = deltaE(wr, wg, wbl, gr, gg, gbl)
			}
			if d > c.Worst {
				c.Worst, c.WorstAt = d, image.Pt(x, y)
			}
			if tol.Delta < 0 && !same || tol.Delta >= 0 && d > tol.Delta {
				if c.Different == 0 {
					c.FirstAt = image.Pt(x, y)
				}
				c.Different++
				c.Diff.SetRGBA(x, y, color.RGBA{255, 0, 0, 255})
			} else { // faded copy of the golden image
				c.Diff.SetRGBA(x, y, color.RGBA{uint8(128 + wr/2), uint8(128 + wg/2), uint8(128 + wbl/2), 255})
			}
		}
	}
	allowed := int(math.Floor(tol.Fraction * float64(c.Pixels)))
	if tol.MaxPixels > 0 && allowed > tol.MaxPixels {
		allowed = tol.MaxPixels
	}
	switch {
	case c.Worst > tol.Hard && tol.Hard >= 0:
		c.Why = fmt.Sprintf("the pixel at %v differs by ΔE %.1f, more than the %.1f any pixel may", c.WorstAt, c.Worst, tol.Hard)
	case c.Different > allowed && tol.Delta < 0:
		c.Why = fmt.Sprintf("%d of %d pixels differ in an exact comparison (first at %v)", c.Different, c.Pixels, c.FirstAt)
	case c.Different > allowed:
		c.Why = fmt.Sprintf("%d of %d pixels (%.3f %%) differ by more than ΔE %.1f, %d are allowed (largest ΔE %.1f at %v)",
			c.Different, c.Pixels, 100*float64(c.Different)/float64(c.Pixels), tol.Delta, allowed, c.Worst, c.WorstAt)
	default:
		c.OK = true
	}
	return c
}

// sameRGBA reports whether two colours are the same pixel: the same
// (premultiplied) red, green, blue and alpha, with no compositing.
func sameRGBA(a, b color.Color) bool {
	ar, ag, ab, aa := a.RGBA()
	br, bg, bb, ba := b.RGBA()
	return ar == br && ag == bg && ab == bb && aa == ba
}

// overWhite is the colour of c composited over white, as 8-bit sRGB.
func overWhite(c color.Color) (r, g, b int) {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	a := int(n.A)
	return (int(n.R)*a + 255*(255-a) + 127) / 255, (int(n.G)*a + 255*(255-a) + 127) / 255, (int(n.B)*a + 255*(255-a) + 127) / 255
}

// srgbLinear maps an 8-bit sRGB component to linear light.
var srgbLinear = func() (t [256]float64) {
	for i := range t {
		c := float64(i) / 255
		if c <= 0.04045 {
			t[i] = c / 12.92
		} else {
			t[i] = math.Pow((c+0.055)/1.055, 2.4)
		}
	}
	return
}()

// lab converts 8-bit sRGB to CIELAB (D65).
func lab(r, g, b int) (L, A, B float64) {
	lr, lg, lb := srgbLinear[r], srgbLinear[g], srgbLinear[b]
	x := (0.4124564*lr + 0.3575761*lg + 0.1804375*lb) / 0.95047
	y := 0.2126729*lr + 0.7151522*lg + 0.0721750*lb
	z := (0.0193339*lr + 0.1191920*lg + 0.9503041*lb) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(x), f(y), f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

// deltaE is the CIE76 colour difference between two 8-bit sRGB colours.
func deltaE(r1, g1, b1, r2, g2, b2 int) float64 {
	l1, a1, bb1 := lab(r1, g1, b1)
	l2, a2, bb2 := lab(r2, g2, b2)
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (bb1-bb2)*(bb1-bb2))
}
