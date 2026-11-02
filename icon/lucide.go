package icon

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"os"
	"sync"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/math/fixed"

	"gonativeui/third_party/lucide"
)

// Lucide returns the spec of the Lucide icon name ("search", "chevron-down",
// "git-branch"; https://lucide.dev/icons) at 16 pt, the size of an icon in a
// row or a button. Size, Small and In change its size; the names are those of
// lucide-static (third_party/lucide/names.txt), Lucide's older aliases
// included.
//
//	k.AddIcon(icon.Lucide("folder-open"))
//	k.AddIcon(icon.Lucide("x").Small())
func Lucide(name string) Spec { return Spec{Lucide: name, Pt: LucidePt} }

// LucidePt and LucideSmallPt are the sides of a Lucide icon in a row or a
// button and of a small one (a badge, a dense row), in points: T3 Code's
// size-4 and size-3.5.
const (
	LucidePt      = 16
	LucideSmallPt = 14
)

// Size returns s at pt points: a Lucide icon's side, or an SF Symbol's point
// size.
func (s Spec) Size(pt float32) Spec {
	s.Pt = pt
	return s
}

// Small returns s at LucideSmallPt.
func (s Spec) Small() Spec { return s.Size(LucideSmallPt) }

// lucideSide is the side in points of a Lucide icon sized for text style st
// at scale sc: a body line (13 pt) takes a 16 pt icon, as T3 Code's 14 px
// text takes size-4, and the other styles in proportion, in whole points.
func lucideSide(st TextStyle, sc Scale) float32 {
	return float32(math.Round(float64(st.Pt() * LucidePt / 13 * scaleFactor(sc))))
}

// lucideStroke is a Lucide icon's stroke width in grid units for weight (as
// NSFontWeight): Lucide's 2 at regular, heavier with the weight (medium 2.3,
// semibold 2.4, bold 2.5) and lighter below it, from 1 to 3.
func lucideStroke(weight float32) float64 {
	return min(3, max(1, 2+1.25*float64(weight)))
}

// lucideSidePx is the side in pixels of a Lucide spec drawn at scale (pixels
// per point): whole pixels, so that the 24-unit grid maps the same way onto
// every icon of a size and the image needs no resampling.
func lucideSidePx(sp Spec, scale float32) int {
	return max(1, int(math.Round(float64(sp.Pt*scaleFactor(sp.Scale)*scale))))
}

var lucideWarned sync.Map

// lucideIcon returns the Lucide icon named name, warning once (on stderr)
// about a name the set does not have.
func lucideIcon(name string) (lucide.Icon, bool) {
	ic, ok := lucide.Lookup(name)
	if !ok {
		msg := fmt.Sprintf("lucide icon %q not found", name)
		if err := lucide.Err(); err != nil {
			msg = err.Error()
		}
		if _, dup := lucideWarned.LoadOrStore(msg, true); !dup {
			fmt.Fprintln(os.Stderr, "icon:", msg)
		}
	}
	return ic, ok
}

// lucideRaster draws Lucide icons into coverage: one per Atlas, reused.
type lucideRaster struct {
	img     image.Alpha
	scan    *rasterx.ScannerGV
	stroker *rasterx.Stroker
	filler  *rasterx.Filler
	path    rasterx.Path
	side    int
	k       float64 // 26.6 grid units to 26.6 pixels
	fill    bool
}

// draw rasterises ic at side×side pixels with a stroke of sw grid units into
// dst's memory (grown if too small): the coverage, side×side bytes. Lucide
// keeps its drawing inside the grid with its caps, so there is no margin.
func (r *lucideRaster) draw(dst []byte, ic lucide.Icon, side int, sw float64) []byte {
	if cap(dst) < side*side {
		dst = make([]byte, side*side)
	}
	pix := dst[:side*side]
	clear(pix)
	r.img = image.Alpha{Pix: pix, Stride: side, Rect: image.Rect(0, 0, side, side)}
	if r.scan == nil {
		r.scan = rasterx.NewScannerGV(side, side, &r.img, r.img.Bounds())
		r.scan.SetColor(color.Alpha{A: 0xFF})
		r.stroker = rasterx.NewStroker(side, side, r.scan)
		r.filler = rasterx.NewFiller(side, side, r.scan)
		r.side = side
	} else if r.side != side {
		// the three share the scanner, whose buffers are kept when a side is
		// no larger than one drawn before: a new size allocates nothing then
		r.filler.SetBounds(side, side)
		r.side = side
	}
	r.scan.Dest = &r.img
	r.k = float64(side) / lucide.Grid
	w := fixed.Int26_6(math.Round(sw * r.k * 64))
	r.stroker.SetStroke(w, 4<<6, rasterx.RoundCap, nil, rasterx.RoundGap, rasterx.Round)
	ic.Decode(r)
	return pix
}

func (r *lucideRaster) Begin(flags byte) {
	r.path.Clear()
	r.fill = flags&lucide.FlagFill != 0
}

func (r *lucideRaster) Op(op byte, pts [6]int32) {
	p := func(i int) fixed.Point26_6 {
		return fixed.Point26_6{X: fixed.Int26_6(math.Round(float64(pts[i]) * r.k)), Y: fixed.Int26_6(math.Round(float64(pts[i+1]) * r.k))}
	}
	switch op {
	case lucide.OpMove:
		r.path.Start(p(0))
	case lucide.OpLine:
		r.path.Line(p(0))
	case lucide.OpQuad:
		r.path.QuadBezier(p(0), p(2))
	case lucide.OpCubic:
		r.path.CubeBezier(p(0), p(2), p(4))
	case lucide.OpClose:
		r.path.Stop(true)
	}
}

func (r *lucideRaster) End() {
	if r.fill {
		r.filler.Clear()
		r.path.AddTo(r.filler)
		r.filler.Draw()
	}
	r.stroker.Clear()
	r.path.AddTo(r.stroker)
	r.stroker.Draw()
}
