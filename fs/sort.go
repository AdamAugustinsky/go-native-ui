package fs

import (
	"bytes"
	"encoding/binary"
	"math"
	"slices"
	"time"
	"unicode"
	"unicode/utf8"

	"gonativeui/core"
)

// Name order. Names compare as natural, case-insensitive UTF-8:
//
//   - runs of ASCII digits compare by numeric value ("file2" < "file10");
//     equal values (leading zeros) fall through to the raw bytes;
//   - letters fold with unicode.ToLower, rune by rune (É = é, Ж = ж); bytes
//     that are not valid UTF-8 compare as themselves;
//   - everything else, and all remaining ties, compares by byte value,
//     which for UTF-8 is code point order.
//
// It is not locale-aware collation (no accent folding: "e" < "é" < "f" but
// "é" does not sort with "e"; no Unicode normalization). Ties after all of
// that are broken by entry index, so the order is total and stable.
//
// Sorting builds one folded key per name (digit runs become '0', a length
// byte, then the digits without leading zeros) in a recycled scratch
// buffer. Name sorting keeps a prefix beside each index; only prefix ties
// compare the full keys.

// arenaLimit is the largest arena (names, keys, entry count) that 32-bit
// offsets can address. A variable only so tests can exercise the overflow
// paths without gigabytes.
var arenaLimit uint64 = math.MaxUint32

// arenaFits32 reports whether an arena holding used bytes can take add more
// and stay addressable: every offset and every end offset (offset + length)
// must fit the limit, with no wraparound in the sum.
func arenaFits32(used, add uint64) bool {
	return used <= arenaLimit && add <= arenaLimit-used
}

// sorter is the recycled scratch of one Sort call.
type sorter struct {
	keys  []byte   // folded keys of all names, back to back
	off   []uint32 // key of entry i is keys[off[i]:off[i+1]]
	pairs []namePair
	bits  []uint64
}

var (
	sorterMu      core.Mutex
	sorterFree    []*sorter
	sorterTimer   *time.Timer
	sorterLastPut time.Time
)

const sorterIdle = 30 * time.Second

func getSorter() *sorter {
	sorterMu.Lock()
	defer sorterMu.Unlock()
	if n := len(sorterFree); n > 0 {
		s := sorterFree[n-1]
		sorterFree[n-1] = nil
		sorterFree = sorterFree[:n-1]
		return s
	}
	return new(sorter)
}

func putSorter(s *sorter) {
	sorterMu.Lock()
	sorterFree = append(sorterFree, s)
	sorterLastPut = time.Now()
	if sorterTimer == nil {
		sorterTimer = time.AfterFunc(sorterIdle, trimSorters)
	} else {
		sorterTimer.Reset(sorterIdle)
	}
	sorterMu.Unlock()
}

func trimSorters() {
	sorterMu.Lock()
	defer sorterMu.Unlock()
	if idle := time.Since(sorterLastPut); idle < sorterIdle {
		// A callback already running when putSorter reset the timer must not
		// discard newly returned, hot scratch.
		sorterTimer.Reset(sorterIdle - idle)
		return
	}
	sorterFree = nil
}

// Sort fills l.Order with a permutation of the entry indices in the
// requested order. Directories (and symlinks to directories) always come
// first; within each group, desc reverses the order exactly.
//
//	SortName:     natural name order (see above)
//	SortSize:     files by size, then name; directories by name
//	SortModified: modification time, then name
//	SortKind:     files by extension (ASCII case-insensitive), then name;
//	              directories by name
//
// It allocates nothing once l.Order and the shared scratch have grown.
// Unused scratch is released after 30 seconds idle; a later call warms it again.
// It may be called from any goroutine on a Listing it owns.
func Sort(l *Listing, key SortKey, desc bool) {
	s := getSorter()
	s.sort(l, key, desc)
	putSorter(s)
}

func (s *sorter) sort(l *Listing, key SortKey, desc bool) {
	l.sorted = false
	n := len(l.Entries)
	if !arenaFits32(0, uint64(n)) { // Order holds uint32 indices
		l.Order = l.Order[:0]
		if l.Err == nil {
			l.Err = ErrTooLarge
		}
		return
	}
	if cap(l.Order) < n {
		l.Order = make([]uint32, n)
	}
	l.Order = l.Order[:n]
	for i := range l.Order {
		l.Order[i] = uint32(i)
	}
	if n < 2 {
		l.sorted, l.sortKey, l.desc = true, key, desc
		return
	}
	if !s.buildKeys(l) { // the keys do not fit 32-bit offsets: leave the order as listed
		if l.Err == nil {
			l.Err = ErrTooLarge
		}
		return
	}
	c := cmpCtx{l: l, s: s, key: key, desc: desc}
	if key == SortName {
		s.sortNames(&c)
	} else {
		slices.SortFunc(l.Order, c.cmp)
	}
	l.sorted, l.sortKey, l.desc = true, key, desc
}

// buildKeys folds every name into s.keys; false if the keys outgrow the
// 32-bit offsets.
func (s *sorter) buildKeys(l *Listing) bool {
	n := len(l.Entries)
	if cap(s.off) < n+1 {
		s.off = make([]uint32, n+1)
	}
	s.off = s.off[:n+1]
	if want := len(l.Names) + len(l.Names)/4 + 64; cap(s.keys) < want {
		s.keys = make([]byte, 0, want)
	}
	keys := s.keys[:0]
	for i := range l.Entries {
		s.off[i] = uint32(len(keys))
		keys = appendKey(keys, l.Name(i))
		if uint64(len(keys)) > arenaLimit {
			s.keys = keys[:0]
			return false
		}
	}
	s.off[n] = uint32(len(keys))
	s.keys = keys
	return true
}

// appendKey appends the folded natural-order key of name to dst.
func appendKey(dst, name []byte) []byte {
	for i := 0; i < len(name); {
		c := name[i]
		switch {
		case c >= '0' && c <= '9':
			j := i
			for j < len(name) && name[j] >= '0' && name[j] <= '9' {
				j++
			}
			k := i
			for k < j && name[k] == '0' {
				k++
			}
			dst = append(dst, '0')
			if n := j - k; n < 0xFF {
				dst = append(dst, byte(n))
			} else {
				dst = append(dst, 0xFF, byte(n>>8), byte(n))
			}
			dst = append(dst, name[k:j]...)
			i = j
		case c < utf8.RuneSelf:
			if c >= 'A' && c <= 'Z' {
				c += 'a' - 'A'
			}
			dst = append(dst, c)
			i++
		default:
			r, w := utf8.DecodeRune(name[i:])
			if r == utf8.RuneError && w == 1 {
				dst = append(dst, c)
			} else {
				dst = utf8.AppendRune(dst, unicode.ToLower(r))
			}
			i += w
		}
	}
	return dst
}

type cmpCtx struct {
	l    *Listing
	s    *sorter
	key  SortKey
	desc bool
}

func isDirEntry(e *Entry) bool { return e.Kind == KindDir || e.Flags&FlagSymlinkDir != 0 }

func cmp3[T int64 | uint32](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (c *cmpCtx) cmp(a, b uint32) int {
	ea, eb := &c.l.Entries[a], &c.l.Entries[b]
	da, db := isDirEntry(ea), isDirEntry(eb)
	if da != db {
		if da {
			return -1
		}
		return 1
	}
	r := 0
	switch c.key {
	case SortSize:
		if !da {
			r = cmp3(ea.Size, eb.Size)
		}
	case SortModified:
		r = cmp3(ea.MTime, eb.MTime)
	case SortKind:
		if !da {
			r = cmpExt(c.l.Name(int(a)), c.l.Name(int(b)))
		}
	}
	if r == 0 {
		s := c.s
		r = bytes.Compare(s.keys[s.off[a]:s.off[a+1]], s.keys[s.off[b]:s.off[b+1]])
		if r == 0 {
			r = bytes.Compare(c.l.Name(int(a)), c.l.Name(int(b)))
			if r == 0 {
				r = cmp3(a, b)
			}
		}
	}
	if c.desc {
		return -r
	}
	return r
}

// ext returns the extension of name without the dot ("" if none; a leading
// dot alone is not an extension).
func ext(name []byte) []byte {
	if i := bytes.LastIndexByte(name, '.'); i > 0 {
		return name[i+1:]
	}
	return nil
}

func cmpExt(a, b []byte) int {
	ea, eb := ext(a), ext(b)
	for i := 0; i < len(ea) && i < len(eb); i++ {
		ca, cb := ea[i], eb[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return cmp3(uint32(ca), uint32(cb))
		}
	}
	return cmp3(uint32(len(ea)), uint32(len(eb)))
}

// namePair keeps the directory bit and the first 63 key bits beside the index.
// Full keys resolve prefix ties, including short keys and the dropped low bit.
type namePair struct {
	prefix uint64
	index  uint32
}

func (s *sorter) sortNames(c *cmpCtx) {
	n := len(c.l.Entries)
	if cap(s.pairs) < n {
		s.pairs = make([]namePair, n)
	}
	s.pairs = s.pairs[:n]
	for i := range s.pairs {
		var b [8]byte
		copy(b[:], s.keys[s.off[i]:s.off[i+1]])
		p := binary.BigEndian.Uint64(b[:]) >> 1
		if c.desc {
			p ^= (1 << 63) - 1
		}
		if !isDirEntry(&c.l.Entries[i]) {
			p |= 1 << 63
		}
		s.pairs[i] = namePair{p, uint32(i)}
	}
	slices.SortFunc(s.pairs, func(a, b namePair) int {
		if a.prefix < b.prefix {
			return -1
		}
		if a.prefix > b.prefix {
			return 1
		}
		return c.cmp(a.index, b.index)
	})
	for i, p := range s.pairs {
		c.l.Order[i] = p.index
	}
}
