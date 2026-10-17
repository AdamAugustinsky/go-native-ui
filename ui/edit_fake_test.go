package ui

import (
	"unicode"
	"unicode/utf8"

	"gonativeui/text"
)

// editShaper is the fakeShaper with the cluster information a text field
// needs and room for long text: every rune advances like the fakeShaper's,
// except that a combining mark has a glyph of its own with no advance (the
// CoreText way: one glyph, one string index per mark) and "fi" is a single
// ligature glyph two cells wide. Clusters are the byte offsets of the first
// character of each glyph.
type editShaper struct {
	*fakeShaper
	slots []editRun
	gen   uint32
}

const (
	editSlots = 128
	editGlyph = 512
	editText  = 1024
)

type editRun struct {
	used  bool
	font  text.FontKey
	tn    int
	txt   [editText]byte
	n     int
	g     [editGlyph]uint16
	x     [editGlyph]float32
	faces [editGlyph]text.FaceID
	cl    [editGlyph]uint32
	adv   float32
}

func newEditShaper() *editShaper {
	return &editShaper{fakeShaper: newFake(), slots: make([]editRun, editSlots)}
}

func (e *editShaper) Shape(k text.FontKey, b []byte) text.RunRef {
	if len(b) == 0 {
		return 0
	}
	h := uint64(14695981039346656037) ^ uint64(k)
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	slot := int(h>>20) % editSlots
	r := &e.slots[slot]
	if r.used && r.font == k && string(r.txt[:r.tn]) == string(b) {
		return text.RunRef(slot + 1)
	}
	if r.used {
		e.gen++
	}
	r.used, r.font, r.n = true, k, 0
	r.tn = copy(r.txt[:], b)
	adv := e.adv(k)
	var x float32
	add := func(g uint16, off int, w float32) {
		if r.n < editGlyph {
			r.g[r.n], r.x[r.n], r.cl[r.n] = g, x, uint32(off)
			r.n++
		}
		x += w
	}
	for i := 0; i < r.tn; {
		ru, sz := utf8.DecodeRune(r.txt[i:r.tn])
		switch {
		case ru == 'f' && i+1 < r.tn && r.txt[i+1] == 'i':
			add(0xFB01, i, 2*adv)
			i += 2
			continue
		case unicode.Is(unicode.Mn, ru):
			add(uint16(ru), i, 0)
		default:
			add(uint16(ru), i, adv)
		}
		i += sz
	}
	r.adv = x
	return text.RunRef(slot + 1)
}

func (e *editShaper) Run(r text.RunRef) text.RunView {
	if r == 0 {
		return text.RunView{}
	}
	s := &e.slots[r-1]
	return text.RunView{Glyphs: s.g[:s.n], X: s.x[:s.n], Faces: s.faces[:s.n], Clusters: s.cl[:s.n], Text: s.txt[:s.tn], Advance: s.adv}
}

func (e *editShaper) RunGeneration() uint32 { return e.gen }
