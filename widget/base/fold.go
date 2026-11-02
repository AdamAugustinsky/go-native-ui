package base

import (
	"unicode"
	"unicode/utf8"
)

// Matching that ignores case and accents, for searches and type-to-find:
// "precos" finds "Página de preços", "ze" finds "Zé". Letters are compared
// lower-cased with their accents taken off (the Latin-1 and Latin Extended-A
// letters, by a table, and combining marks, U+0300–U+036F, skipped, so
// decomposed text matches too); other scripts compare lower-cased. Nothing
// allocates: golang.org/x/text's transform chain (norm.NFD, runes.Remove)
// would fold more scripts, but it allocates a buffer per call, and a filter
// runs these per row as the query changes.

// foldLatin is the base letter of U+00C0–U+017F, '_' where there is none
// (Æ, ß, Œ, ĸ, ŉ …: compared lower-cased).
const foldLatin = "aaaaaa_ceeeeiiiidnooooo_ouuuuy__" + // U+00C0–U+00DF
	"aaaaaa_ceeeeiiiidnooooo_ouuuuy_y" + // U+00E0–U+00FF
	"aaaaaaccccccccdd" + "ddeeeeeeeeeegggg" + "gggghhhhiiiiiiii" + "ii__jjkk_lllllll" + // U+0100–U+013F
	"lllnnnnnn_nnoooo" + "oo__rrrrrrssssss" + "ssttttttuuuuuuuu" + "uuuuwwyyyzzzzzzs" // U+0140–U+017F

// FoldRune is r as the matching compares it: lower case, without its
// accent; -1 for a combining mark, which the matching skips.
func FoldRune(r rune) rune {
	switch {
	case r < 0x80:
		if r >= 'A' && r <= 'Z' {
			return r + 'a' - 'A'
		}
		return r
	case r >= 0xC0 && r <= 0x17F:
		if b := foldLatin[r-0xC0]; b != '_' {
			return rune(b)
		}
	case r >= 0x300 && r <= 0x36F:
		return -1
	}
	return unicode.ToLower(r)
}

// foldNext is the next folded rune of s from byte i and the index after it,
// skipping combining marks; -1 at the end.
func foldNext(s string, i int) (rune, int) {
	for i < len(s) {
		r, n := utf8.DecodeRuneInString(s[i:])
		i += n
		if f := FoldRune(r); f >= 0 {
			return f, i
		}
	}
	return -1, i
}

// foldAt reports whether sub matches s from byte i, folded.
func foldAt(s string, i int, sub string) bool {
	j := 0
	for {
		b, nj := foldNext(sub, j)
		if b < 0 {
			return true
		}
		a, ni := foldNext(s, i)
		if a != b {
			return false
		}
		i, j = ni, nj
	}
}

// FoldHasPrefix reports whether s starts with prefix, ignoring case and
// accents.
func FoldHasPrefix(s, prefix string) bool { return foldAt(s, 0, prefix) }

// FoldContains reports whether sub is in s, ignoring case and accents. An
// empty sub is in every s.
func FoldContains(s, sub string) bool {
	if sub == "" {
		return true
	}
	for i := 0; i < len(s); {
		if foldAt(s, i, sub) {
			return true
		}
		_, n := utf8.DecodeRuneInString(s[i:])
		i += n
	}
	return false
}

// FoldWordPrefix reports whether a word of s starts with prefix (a word
// starts after a space, a punctuation mark or a symbol, and at the start),
// ignoring case and accents: "nc" does not, "nova" does, in "Nova conversa".
func FoldWordPrefix(s, prefix string) bool {
	start := true
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if start && foldAt(s, i, prefix) {
			return true
		}
		start = !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.Is(unicode.Mn, r)
		i += n
	}
	return false
}
