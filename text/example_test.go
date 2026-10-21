package text_test

import (
	"fmt"

	"gonativeui/internal/fakeui"
	"gonativeui/text"
)

// Measuring a paragraph: break it into lines at a width and read the lines.
// Here the shaper is the deterministic test one (every rune advances half the
// font size); an app lays out with the window's text system, a.Text, which
// shapes through the operating system (CoreText, DirectWrite, HarfBuzz) and
// has the same Layout and Paragraph methods.
func Example() {
	ts := fakeui.New() // a.Text in an app
	font := ts.Font(text.FontDesc{SizePt: 10, Weight: 400, Scale: 1})
	l := text.NewBasicLayouter(ts)

	ref := l.Layout(&text.Para{
		Text:  []byte("the quick brown fox"),
		Fonts: []text.FontKey{font},
		Width: 60, // pixels
	})
	p := l.Paragraph(ref)
	fmt.Println("lines:", len(p.Lines), "widest:", p.Width <= 60)
	// Output: lines: 2 widest: true
}

// Moving the caret by what a person sees as one character, a grapheme
// cluster, not by bytes or runes.
func ExampleNextGrapheme() {
	s := []byte("e\u0301a👩🏽‍💻!") // e + combining acute, a, a family-style emoji sequence, !
	var sizes []int              // the bytes of each character
	for off := 0; off < len(s); {
		next := text.NextGrapheme(s, off)
		sizes = append(sizes, next-off)
		off = next
	}
	fmt.Println(len(sizes), "characters, bytes:", sizes)
	// Output: 4 characters, bytes: [3 1 15 1]
}

// Where a line may end, by the Unicode line breaking rules.
func ExampleLineBreaker() {
	var lb text.LineBreaker
	s := []byte("one two-three\nfour")
	var prev uint32
	for _, b := range lb.Breaks(nil, s) {
		end := b &^ text.BreakMandatory
		fmt.Printf("%q mandatory=%v\n", s[prev:end], b&text.BreakMandatory != 0)
		prev = end
	}
	// Output:
	// "one " mandatory=false
	// "two-" mandatory=false
	// "three\n" mandatory=true
	// "four" mandatory=true
}

// The real text system, one per app on the main thread. An app gets it as
// App.Text and passes it to widget.NewKit; creating one by hand is for tools.
func ExampleNew() {
	ts, err := text.New()
	if err != nil {
		return // no text backend on this system
	}
	defer ts.Close()
	font := ts.Font(text.FontDesc{SizePt: 13, Weight: 400, Scale: 2})
	run := ts.Shape(font, []byte("Hello"))
	v := ts.Run(run)
	_ = v // glyphs, pen positions and caret geometry (RunView.CaretX, HitByte)
}
