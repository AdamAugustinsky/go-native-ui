package text

import (
	"math"
	"reflect"
	"slices"
	"testing"
)

// checkParaFit holds the basic layout to its certificate: wherever a layout
// says (ParaView.FitFrom, FitTo) that a fresh layout at another width would
// be the same, a fresh layout at that width is. The widths tried are the ends
// and the middle of the interval and the rendered width of the layout, the
// one that fitRich once took for the end of it (issue #35: an Arabic
// paragraph laid out at 61.5 px has a widest line of 59.17 px, and a fresh
// layout at 59.17 px has one more line).
func checkParaFit(t *testing.T, sh BasicShaper, font FontKey) {
	t.Helper()
	l := NewBasicLayouter(sh)
	bodies := []string{
		"hello world and more words to wrap across several lines",
		"office affinity AVATAR Toffee very difficult typography",
		"مرحبا بالعالم هذا نص طويل لاختبار التفاف السطور",
		"שלום עולם טקסט ארוך לבדיקת שבירת שורות",
		"hello مرحبا world שלום mixed scripts and words",
		"hello   world     and more    words   ",
		"hello world\r\nand more words\r\nlast line",
		"averylongwordwithnobreaksatallandmoreletters",
		"日本語の文章と漢字かなカナを混在させて改行を確認します",
	}
	certified, tried := 0, 0
	for _, body := range bodies {
		for _, o := range []Para{{}, {MaxLines: 2}, {MaxLines: 2, Ellipsis: true}, {MaxLines: 3, Ellipsis: true}} {
			p := o
			p.Text, p.Fonts = []byte(body), []FontKey{font}
			for w := float32(0); w <= 300; w += 0.5 {
				if w > 0 && w < 20 {
					continue
				}
				p.Width = w
				v := l.Paragraph(l.Layout(&p))
				lines, lo, hi, rendered := slices.Clone(v.Lines), v.FitFrom, v.FitTo, v.Width
				if !(lo <= hi) {
					continue
				}
				certified++
				for _, at := range []float32{lo, hi, (lo + hi) / 2, rendered, lo + 3} {
					if math.IsInf(float64(at), 0) || math.IsNaN(float64(at)) {
						at = 300
					}
					if at < lo || at > hi || at < 1 {
						continue
					}
					tried++
					q := p
					q.Width = at
					fresh := l.Paragraph(l.Layout(&q))
					if !reflect.DeepEqual(lines, fresh.Lines) {
						t.Fatalf("%q %+v laid out at %g is certified for [%g, %g], but at %g it has %d lines, not %d", body, o, w, lo, hi, at, len(fresh.Lines), len(lines))
					}
				}
			}
		}
	}
	if certified == 0 || tried == 0 {
		t.Fatalf("no layout was certified (%d, tried %d)", certified, tried)
	}
}

// A layout whose runs the layouter cannot measure certifies nothing.
func TestBasicFitUnmeasurable(t *testing.T) {
	l := NewBasicLayouter(fixedMeasureShaper{v: RunView{Glyphs: []uint16{1}, X: []float32{0}, Faces: []FaceID{0}, Advance: 10}})
	v := l.Paragraph(l.Layout(&Para{Text: []byte("some text"), Fonts: []FontKey{0}, Width: 50}))
	if v.FitFrom <= v.FitTo {
		t.Errorf("a layout without cluster information is certified for [%g, %g]", v.FitFrom, v.FitTo)
	}
}
