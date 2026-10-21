package harfbuzz

import (
	"os"
	"testing"

	"github.com/go-text/typesetting/font"
)

// The apply context of a font with a kern (or kerx, morx, trak) table is the
// buffer's, reused, glyph set included: made afresh it allocated on every
// shape (CI: Pilot's measured window, a name in DejaVu's legacy kern table;
// zeroalloc.patch regression).
func TestAatApplyContextDoesNotAllocate(t *testing.T) {
	f, err := os.Open("../font/testdata/Roboto-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	face, err := font.ParseTTF(f)
	if err != nil {
		t.Fatal(err)
	}
	fnt := NewFont(face)
	b := NewBuffer()
	b.AddRunes([]rune("kerning"), 0, -1)
	use := func() {
		c := newAatApplyContext(nil, fnt, b)
		c.setupBufferGlyphSet()
		if !c.usingBufferGlyphSet || c.buffer != b || c.font != fnt {
			t.Fatal("the context is not set up for the buffer")
		}
	}
	use() // the glyph set's storage grows once
	if n := testing.AllocsPerRun(100, use); n != 0 {
		t.Fatalf("an apply context allocates %v per shape", n)
	}
}
