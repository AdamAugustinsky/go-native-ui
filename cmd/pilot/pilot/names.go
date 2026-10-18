package pilot

import "gonativeui/fs"

// nameHash is FNV-1a over a name, never 0 (0 marks an empty slot of a nameSet).
func nameHash(b []byte) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range b {
		h = (h ^ uint64(c)) * 1099511628211
	}
	return h | 1
}

// nameMap is an open-addressing map from name hashes to a number (an entry
// index) in reused slices. A hash that is already in it keeps its first number.
type nameMap struct {
	h    []uint64 // the hashes; 0 marks an empty slot
	v    []uint32
	mask uint64
	n    int
}

// reset empties the map and sizes it for up to n names.
func (s *nameMap) reset(n int) {
	sz := 16
	for sz < 2*withSlack(n) {
		sz <<= 1
	}
	if cap(s.h) < sz {
		s.h = make([]uint64, sz)
		s.v = make([]uint32, sz)
	}
	s.h, s.v = s.h[:sz], s.v[:sz]
	clear(s.h)
	s.mask, s.n = uint64(sz-1), 0
}

func (s *nameMap) add(h uint64, v uint32) {
	for i := (h >> 7) & s.mask; ; i = (i + 1) & s.mask {
		switch s.h[i] {
		case 0:
			s.h[i], s.v[i] = h, v
			s.n++
			return
		case h:
			return
		}
	}
}

func (s *nameMap) get(h uint64) (uint32, bool) {
	if s.n == 0 {
		return 0, false
	}
	for i := (h >> 7) & s.mask; ; i = (i + 1) & s.mask {
		switch s.h[i] {
		case 0:
			return 0, false
		case h:
			return s.v[i], true
		}
	}
}

// entryName is entry e's name in l.
func entryName(l *fs.Listing, e int32) []byte { return l.Name(int(e)) }

// withSlack is the capacity to make for n elements that a directory may grow
// by: a refresh that brings a few more entries than the last listing must not
// reallocate (the buffers only ever grow, so a shrinking and growing directory
// settles at its largest).
func withSlack(n int) int { return n + n/8 + 64 }
