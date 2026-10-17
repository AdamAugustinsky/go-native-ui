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

// nameSet is an open-addressing set of name hashes in a reused slice.
type nameSet struct {
	t    []uint64
	mask uint64
	n    int
}

// reset empties the set and sizes it for up to n names.
func (s *nameSet) reset(n int) {
	sz := 16
	for sz < 2*n {
		sz <<= 1
	}
	if cap(s.t) < sz {
		s.t = make([]uint64, sz)
	}
	s.t = s.t[:sz]
	clear(s.t)
	s.mask, s.n = uint64(sz-1), 0
}

func (s *nameSet) add(h uint64) {
	for i := (h >> 7) & s.mask; ; i = (i + 1) & s.mask {
		switch s.t[i] {
		case 0:
			s.t[i] = h
			s.n++
			return
		case h:
			return
		}
	}
}

func (s *nameSet) has(h uint64) bool {
	if s.n == 0 {
		return false
	}
	for i := (h >> 7) & s.mask; ; i = (i + 1) & s.mask {
		switch s.t[i] {
		case 0:
			return false
		case h:
			return true
		}
	}
}

// entryName is entry e's name in l.
func entryName(l *fs.Listing, e int32) []byte { return l.Name(int(e)) }
