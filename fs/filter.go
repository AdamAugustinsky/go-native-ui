package fs

import (
	"encoding/binary"
	"unicode"
	"unicode/utf8"
)

// Filter appends to dst the indices (in l.Order order) of the entries whose
// name contains query, ignoring case, and returns it. An empty query
// matches every entry. Pass dst[:0] to reuse a buffer; with enough capacity
// it allocates nothing.
//
// Case folding is the one Sort uses: unicode.ToLower per rune, with an
// ASCII fast path for names without non-ASCII bytes. A query longer than 128 bytes takes a slower path that
// still does not allocate.
func Filter(l *Listing, query []byte, dst []uint32) []uint32 {
	if len(query) == 0 {
		return append(dst, l.Order...)
	}
	if len(query) <= len(asciiQuery{}) && isASCII(query) {
		var q asciiQuery
		for i, c := range query {
			q[i] = foldTable[c]
		}
		qb := q[:len(query)]
		for _, idx := range l.Order {
			e := &l.Entries[idx]
			name := l.Names[e.NameOff : e.NameOff+uint32(e.NameLen)]
			// A name with any non-ASCII byte takes the rune path: some runes
			// fold to ASCII (U+212A KELVIN SIGN lowercases to 'k'), which a byte
			// comparison cannot see.
			var ok bool
			if isASCII(name) {
				ok = containsFoldASCII(name, qb)
			} else {
				ok = containsFoldRunes(name, qb)
			}
			if ok {
				dst = append(dst, idx)
			}
		}
		return dst
	}
	for _, idx := range l.Order {
		e := &l.Entries[idx]
		if containsFoldRunes(l.Names[e.NameOff:e.NameOff+uint32(e.NameLen)], query) {
			dst = append(dst, idx)
		}
	}
	return dst
}

type asciiQuery [128]byte

var foldTable = func() (t [256]byte) {
	for i := range t {
		t[i] = byte(i)
	}
	for c := 'A'; c <= 'Z'; c++ {
		t[c] = byte(c - 'A' + 'a')
	}
	return
}()

func isASCII(b []byte) bool {
	for len(b) >= 8 {
		if binary.LittleEndian.Uint64(b)&0x8080808080808080 != 0 {
			return false
		}
		b = b[8:]
	}
	for _, c := range b {
		if c >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

// containsFoldASCII reports whether name contains q (already folded).
// Bytes of multi-byte UTF-8 sequences are all >= 0x80 and never match an
// ASCII query byte, so a byte search is exact.
func containsFoldASCII(name, q []byte) bool {
	m := len(q)
	if len(name) < m {
		return false
	}
	q0 := q[0]
	last := len(name) - m
	for i := 0; i <= last; i++ {
		if foldTable[name[i]] != q0 {
			continue
		}
		j := 1
		for j < m && foldTable[name[i+j]] == q[j] {
			j++
		}
		if j == m {
			return true
		}
	}
	return false
}

// containsFoldRunes is the general case: rune-wise folding of both sides.
func containsFoldRunes(name, query []byte) bool {
	for i := 0; i < len(name); {
		if matchFoldAt(name[i:], query) {
			return true
		}
		_, w := utf8.DecodeRune(name[i:])
		i += w
	}
	return false
}

func matchFoldAt(name, query []byte) bool {
	for len(query) > 0 {
		if len(name) == 0 {
			return false
		}
		qr, qw := utf8.DecodeRune(query)
		nr, nw := utf8.DecodeRune(name)
		if qr == utf8.RuneError && qw == 1 || nr == utf8.RuneError && nw == 1 {
			if qw != nw || query[0] != name[0] {
				return false
			}
		} else if unicode.ToLower(qr) != unicode.ToLower(nr) {
			return false
		}
		query, name = query[qw:], name[nw:]
	}
	return true
}
