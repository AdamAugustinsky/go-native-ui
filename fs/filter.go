package fs

import (
	"encoding/binary"
	"unicode"
	"unicode/utf8"
)

// Filter appends to dst the indices (in l.Order order) of the entries whose
// name contains query, ignoring case, and returns it. An empty query
// matches every entry. Pass dst[:0] to reuse a buffer; with enough capacity
// it allocates nothing once the shared match scratch is warm. A complete
// listing is scanned in storage order into match bits, then in Order order;
// partial Orders inspect only their own entries. FilterBits supports a caller's
// incremental scan without rescanning the arena for each chunk.
//
// Case folding is the one Sort uses: unicode.ToLower per rune, with an
// ASCII fast path for names without non-ASCII bytes. A query longer than 128 bytes takes a slower path that
// still does not allocate.
func Filter(l *Listing, query []byte, dst []uint32) []uint32 {
	if len(query) == 0 {
		return append(dst, l.Order...)
	}
	// A partial Order can be a tiny chunk of a large arena. Inspect only it;
	// complete listings use a sequential pass followed by the permutation.
	if len(l.Order) != len(l.Entries) || len(l.Entries) < 256 {
		m := makeMatcher(query)
		for _, i := range l.Order {
			if m.matches(l.Name(int(i))) {
				dst = append(dst, i)
			}
		}
		return dst
	}
	s := getSorter()
	s.bits = FilterBits(l, query, s.bits, 0, len(l.Entries))
	for _, i := range l.Order {
		if s.bits[i/64]&(uint64(1)<<(i%64)) != 0 {
			dst = append(dst, i)
		}
	}
	putSorter(s)
	return dst
}

// FilterBits sets the match bits for Entries[start:end] in storage order,
// using Filter's case folding. Bits outside the range are unchanged. Pass the
// returned slice again to reuse it; it grows to one bit per entry as needed.
// Callers can split the scan across frames, then visit Order once to emit rows.
func FilterBits(l *Listing, query []byte, dst []uint64, start, end int) []uint64 {
	n := (len(l.Entries) + 63) / 64
	if cap(dst) < n {
		dst = make([]uint64, n)
	}
	dst = dst[:n]
	m := makeMatcher(query)
	for i := start; i < end; i++ {
		bit := uint64(1) << uint(i%64)
		if len(query) == 0 || m.matches(l.Name(i)) {
			dst[i/64] |= bit
		} else {
			dst[i/64] &^= bit
		}
	}
	return dst
}

type matcher struct {
	query  []byte
	folded asciiQuery
	ascii  bool
}

func makeMatcher(query []byte) matcher {
	m := matcher{query: query, ascii: len(query) <= len(asciiQuery{}) && isASCII(query)}
	if m.ascii {
		for i, c := range query {
			m.folded[i] = foldTable[c]
		}
	}
	return m
}

func (m *matcher) matches(name []byte) bool {
	if m.ascii {
		q := m.folded[:len(m.query)]
		if isASCII(name) {
			return containsFoldASCII(name, q)
		}
		return containsFoldRunes(name, q)
	}
	return containsFoldRunes(name, m.query)
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
