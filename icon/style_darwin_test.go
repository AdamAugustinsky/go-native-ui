//go:build darwin

package icon

import (
	"testing"
	"unsafe"

	"github.com/ebitengine/purego"

	"gonativeui/internal/objc"
)

// textStyleName is the AppKit constant of a text style (NSFontTextStyleBody …).
var textStyleConst = [NumStyles]string{
	StyleBody: "NSFontTextStyleBody", StyleCallout: "NSFontTextStyleCallout", StyleSubheadline: "NSFontTextStyleSubheadline",
	StyleFootnote: "NSFontTextStyleFootnote", StyleCaption1: "NSFontTextStyleCaption1", StyleCaption2: "NSFontTextStyleCaption2",
	StyleHeadline: "NSFontTextStyleHeadline", StyleTitle3: "NSFontTextStyleTitle3", StyleTitle2: "NSFontTextStyleTitle2",
	StyleTitle1: "NSFontTextStyleTitle1", StyleLargeTitle: "NSFontTextStyleLargeTitle",
}

// rasterTextStyle rasterises name through AppKit's own text-style
// configuration (NSImageSymbolConfiguration configurationWithTextStyle:scale:)
// at 1×: the reference for what Spec.In asks for.
func rasterTextStyle(t *testing.T, name string, st TextStyle, sc Scale) (pix []byte, w, h int) {
	t.Helper()
	if err := loadSyms(); err != nil {
		t.Skip(err)
	}
	lib, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit", purego.RTLD_NOW|purego.RTLD_GLOBAL)
	if err != nil {
		t.Skip(err)
	}
	sym, err := purego.Dlsym(lib, textStyleConst[st])
	if err != nil {
		t.Skipf("%s: %v", textStyleConst[st], err)
	}
	// sym is the address of the constant, which holds the NSString
	style := *(*uintptr)(*(*unsafe.Pointer)(unsafe.Pointer(&sym)))
	sel := objc.Sel("configurationWithTextStyle:scale:")
	pix, w, h, ok := rasterConfigured(name, func() objc.ID {
		return objc.ID(objc.F.Reset().Arg(uintptr(clsSymbolConfig)).Arg(uintptr(sel)).Arg(style).Arg(uintptr(symbolScale(sc))).Call(objc.MsgSend).R0())
	})
	if !ok {
		t.Fatalf("%s: not rasterised", name)
	}
	return pix, w, h
}

func TestStylesAreAppKits(t *testing.T) {
	for _, name := range []string{"plus", "gearshape", "star.fill", "folder"} {
		for st := range NumStyles {
			for _, sc := range []Scale{ScaleSmall, ScaleMedium, ScaleLarge} {
				want, ww, wh := rasterTextStyle(t, name, st, sc)
				got, gw, gh, ok := rasterSymbol(name, float64(st.Pt()), float64(st.Weight()), sc)
				if !ok {
					t.Fatalf("%s: not rasterised", name)
				}
				if gw != ww || gh != wh {
					t.Errorf("%s style %d scale %d: %d×%d, AppKit's text style %d×%d", name, st, sc, gw, gh, ww, wh)
					continue
				}
				if d := coverageDiff(got, want); d > 1 {
					t.Errorf("%s style %d scale %d: coverage differs from AppKit's text style by %d", name, st, sc, d)
				}
			}
		}
	}
}

func coverageDiff(a, b []byte) (worst int) {
	for i := range a {
		d := int(a[i]) - int(b[i])
		if d < 0 {
			d = -d
		}
		worst = max(worst, d)
	}
	return worst
}

// The drawn glyphs of Windows and Linux follow AppKit's scales: the factors
// are the mean ratio of the symbols' heights, within a few percent.
func TestScaleFactors(t *testing.T) {
	var sum [3]float64
	n := 0
	for _, name := range []string{"plus", "gearshape", "star.fill", "folder", "checkmark", "chevron.right"} {
		for _, pt := range []float64{13, 22} {
			var h [3]float64
			for i, sc := range []Scale{ScaleMedium, ScaleSmall, ScaleLarge} {
				_, _, hh, ok := rasterSymbol(name, pt, 0, sc)
				if !ok {
					t.Skipf("%s is not here", name)
				}
				h[i] = float64(hh - 2) // without the margin
			}
			sum[1] += h[1] / h[0]
			sum[2] += h[2] / h[0]
			n++
		}
	}
	for _, c := range []struct {
		sc   Scale
		mean float64
	}{{ScaleSmall, sum[1] / float64(n)}, {ScaleLarge, sum[2] / float64(n)}} {
		if got := float64(scaleFactor(c.sc)); got < c.mean-0.03 || got > c.mean+0.03 {
			t.Errorf("scale %d: factor %.3f, AppKit's symbols average %.3f", c.sc, got, c.mean)
		}
	}
}
