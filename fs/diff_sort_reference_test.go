package fs

import (
	"bytes"
	"unicode"
	"unicode/utf8"
)

// Frozen key encoder and comparator from main at 2836ce5a. No prefix sorting.
type diffOldSort struct {
	l    *Listing
	keys [][]byte
	key  SortKey
	desc bool
}

func diffOldKey(dst, name []byte) []byte {
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
func (c *diffOldSort) cmp(a, b uint32) int {
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

		r = bytes.Compare(c.keys[a], c.keys[b])
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
