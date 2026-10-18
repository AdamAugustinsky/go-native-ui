// SPDX-License-Identifier: Unlicense OR BSD-3-Clause

package text

import (
	"unicode/utf8"

	"github.com/go-text/typesetting/segmenter"
)

// BreakMandatory is set in a break of LineBreaker.Breaks where the text must
// end the line (a newline, or the end of the text).
const BreakMandatory = 1 << 31

// LineBreaker finds the places where text may be broken into lines, by the
// Unicode line breaking algorithm (UAX #14) of go-text's segmenter. The zero
// value is ready to use; it keeps its buffers, so a LineBreaker that has seen
// text of a size allocates nothing for text of that size again. Not safe for
// concurrent use.
type LineBreaker struct {
	seg   segmenter.Segmenter
	runes []rune
}

// Breaks appends to dst the line break opportunities of the UTF-8 text s: the
// byte offset of the end of each segment that a line may end after, ascending,
// with BreakMandatory set where the line must end there. The segments hold
// their trailing spaces, so a line that ends at one of the offsets usually has
// white space at its end, which hangs outside the line's width. The last
// offset is len(s) (mandatory); empty text has no break.
//
// Text with invalid UTF-8 is broken after each space and at newlines only.
func (b *LineBreaker) Breaks(dst []uint32, s []byte) []uint32 {
	if len(s) == 0 {
		return dst
	}
	if !utf8.Valid(s) {
		return fallbackBreaks(dst, s)
	}
	// (Segmenter.InitWithBytes allocates a closure on every call; Init does not.)
	b.runes = b.runes[:0]
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRune(s[i:])
		b.runes = append(b.runes, r)
		i += n
	}
	b.seg.Init(b.runes)
	it := b.seg.LineIterator()
	for it.Next() {
		l := it.Line()
		end := uint32(l.OffsetInBytes + l.LengthInBytes)
		if l.IsMandatoryBreak || int(end) == len(s) {
			end |= BreakMandatory
		}
		dst = append(dst, end)
	}
	return dst
}

func fallbackBreaks(dst []uint32, s []byte) []uint32 {
	for i, c := range s {
		switch {
		case c == '\n':
			dst = append(dst, uint32(i+1)|BreakMandatory)
		case c == ' ' && i+1 < len(s) && s[i+1] != ' ':
			dst = append(dst, uint32(i+1))
		}
	}
	if n := len(dst); n == 0 || dst[n-1]&^BreakMandatory != uint32(len(s)) {
		dst = append(dst, uint32(len(s))|BreakMandatory)
	}
	return dst
}
