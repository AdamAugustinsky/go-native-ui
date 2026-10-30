package fs

import (
	"bytes"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

func diffSortOrder(l *Listing, key SortKey, desc bool) []uint32 {
	c := diffOldSort{l: l, key: key, desc: desc, keys: make([][]byte, len(l.Entries))}
	order := make([]uint32, len(l.Entries))
	for i := range order {
		order[i] = uint32(i)
		c.keys[i] = diffOldKey(nil, l.Name(i))
	}
	sort.SliceStable(order, func(i, j int) bool { return c.cmp(order[i], order[j]) < 0 })
	return order
}

// At most 64 entries/4096 source bytes; folded keys <=3*source bytes and
// O(n log n) comparisons. Resort runs three bounded requests through the real
// service with the existing fake filesystem (zero directory reads).
func FuzzNameSortResortDifferential(f *testing.F) {
	for _, s := range []string{"", "file10\x00File2\x00file02\x00file2", "É\x00é\x00E\x00e\x00Ж\x00ж", "same\x00same\x00same", "\xff\x00\xed\xa0\x80\x00\x80", "prefixAA\x00prefixAB\x00prefixA", "x" + strings.Repeat("1", 256) + "\x00x" + strings.Repeat("9", 255)} {
		f.Add([]byte(s), []byte{0, 1, 2, 3, 4, 5, 6, 7})
	}
	f.Fuzz(func(t *testing.T, b, meta []byte) {
		if len(b) > 4096 || len(meta) > 128 {
			return
		}
		start := time.Now()
		var l Listing
		for i, name := range bytes.Split(b, []byte{0}) {
			if i >= 64 {
				break
			}
			if len(name) > 512 {
				return
			}
			v := byte(i)
			if len(meta) > 0 {
				v = meta[i%len(meta)]
			}
			e := Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(name)), Size: int64(int8(v)), MTime: int64(int8(v >> 2))}
			if v&7 == 1 {
				e.Kind = KindDir
			}
			if v&7 == 2 {
				e.Kind = KindSymlink
				e.Flags = FlagSymlinkDir
			}
			l.Entries = append(l.Entries, e)
			l.Names = append(l.Names, name...)
		}
		for _, key := range []SortKey{SortName, SortSize, SortModified, SortKind} {
			for _, desc := range []bool{false, true} {
				want := diffSortOrder(&l, key, desc)
				Sort(&l, key, desc)
				if l.Err != nil || !slices.Equal(l.Order, want) {
					t.Fatalf("key=%v desc=%v names=%x got=%v old=%v err=%v", key, desc, b, l.Order, want, l.Err)
				}
			}
		}
		key := SortName
		if len(meta) > 0 {
			key = []SortKey{SortName, SortSize, SortModified, SortKind}[meta[0]%4]
		}
		Sort(&l, key, false)
		h := newHarness(t)
		source := &l
		lease := uint32(0)
		for step := 0; step < 3; step++ {
			desc := step != 1
			if step == 2 {
				key = SortName
			}
			before := slices.Clone(source.Order)
			want := diffSortOrder(source, key, desc)
			req := h.s.Resort(1, source, lease, key, desc)
			if req == 0 {
				t.Fatal("valid immutable snapshot rejected")
			}
			r := h.await(2*time.Second, isListing(1))
			if r.Req != req || !slices.Equal(r.Listing.Order, want) || !slices.Equal(source.Order, before) || !bytes.Equal(r.Listing.Names, source.Names) || !slices.Equal(r.Listing.Entries, source.Entries) {
				t.Fatal("resort differs from stable reference or mutated source")
			}
			if lease != 0 {
				h.s.Release(source, lease)
			}
			source, lease = r.Listing, r.Lease
		}
		h.s.Release(source, lease)
		if h.fs.calls.Load() != 0 {
			t.Fatal("resort read the filesystem")
		}
		if time.Since(start) > 8*time.Second {
			t.Fatal("bounded sort/resort exceeded 8s")
		}
	})
}
