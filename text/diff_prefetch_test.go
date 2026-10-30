//go:build darwin || windows || linux

package text

import (
	"bytes"
	"runtime"
	"slices"
	"testing"
	"time"
)

type diffPending struct {
	group uint32
	key   diffRunKey
}

// Wait at explicit scheduling boundaries: a worker may run concurrently with
// Cancel, but no subsequent model action depends on which instruction won.
func diffSettlePrefetch(t *testing.T, p *Prefetcher) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for {
		p.mu.Lock()
		busy := p.queued > 0
		for _, j := range p.jobs {
			busy = busy || j.state == shapeRunning || j.state == shapeImporting
		}
		p.mu.Unlock()
		if !busy {
			return
		}
		if time.Now().After(end) {
			t.Fatal("prefetch failed to settle in 5s")
		}
		runtime.Gosched()
	}
}

// Up to 64 actions, 1 worker, 1..8 slots, <=PrefetchTextLimit copied bytes
// per slot. A map models accepted requests, deduplication, slot-order Poll,
// cancellation and closure. All imported runs must match fresh main shaping.
func FuzzPrefetchDifferential(f *testing.F) {
	for _, s := range diffTextSeeds {
		f.Add([]byte(s), []byte{4, 0, 0, 0, 0, 0, 0, 2, 1, 0, 0, 2, 1, 1, 0, 0, 3, 0, 0, 5, 0, 0})
	}
	f.Add([]byte("copied 日本語"), []byte{1,
		0, 0, 0, 0, 1, 1, 0, 2, 2, 3, 1, 0, 2, 1, 0,
		0, 0, 4, 0, 0, 4, 3, 8, 0, 0, 0, 5, 4, 0, 0, 5, 0, 0, 6, 0, 0, 0, 0, 0})
	s, direct := workerSystem(f), workerSystem(f)
	desc := FontDesc{SizePt: 13, Scale: 1}
	font, df := s.Font(desc), direct.Font(desc)
	f.Fuzz(func(t *testing.T, b, ops []byte) {
		if len(b) > 128 || len(ops) > 193 || len(ops) == 0 {
			return
		}
		defer diffDeadline(t)()
		s.runs.drop()
		capacity := 1 + int(ops[0]%8)
		p, err := NewPrefetcher(1, capacity, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer p.Close()
		pending := make(map[int]diffPending)
		cached := make(map[diffRunKey]bool)
		free := make([]int, capacity)
		for i := range free {
			free[i] = capacity - 1 - i
		}
		closed := false
		names := [][]byte{b, []byte("office fi"), []byte("日本語 😀"), nil, bytes.Repeat([]byte("x"), PrefetchTextLimit), bytes.Repeat([]byte("x"), PrefetchTextLimit+1)}
		for i := 1; i+2 < len(ops); i += 3 {
			group := uint32(ops[i+1] % 4)
			name := names[int(ops[i+2])%len(names)]
			key := diffRunKey{font, string(name)}
			switch ops[i] % 7 {
			case 0, 1:
				want := !closed && len(name) <= PrefetchTextLimit
				duplicate := cached[key]
				for _, j := range pending {
					duplicate = duplicate || j.group == group && j.key == key
				}
				want = want && (duplicate || len(free) > 0)
				if got := p.Submit(group, s, font, name); got != want {
					t.Fatalf("step=%d submit=%v model=%v", i/3, got, want)
				}
				if want && !duplicate {
					slot := free[len(free)-1]
					free = free[:len(free)-1]
					pending[slot] = diffPending{group, key}
				}
				diffSettlePrefetch(t, p)
			case 2:
				p.Cancel(group)
				for slot := 0; slot < capacity; slot++ {
					if j, ok := pending[slot]; ok && j.group == group {
						delete(pending, slot)
						free = append(free, slot)
					}
				}
			case 3:
				limit := int(int8(ops[i+1])) % 10
				want := 0
				for slot := 0; slot < capacity && want < limit; slot++ {
					if j, ok := pending[slot]; ok {
						cached[j.key] = true
						delete(pending, slot)
						free = append(free, slot)
						want++
					}
				}
				if n := p.Poll(s, limit); n != want {
					t.Fatalf("step=%d poll=%d model=%d", i/3, n, want)
				}
			case 4:
				if p.Submit(group, s, ^FontKey(0), name) {
					t.Fatal("invalid font accepted")
				}
			case 5:
				// An uncached private group is cancelled immediately, before or
				// during the native call. No cancelled result may be imported.
				private := append([]byte("cancel:"), b...)
				want := !closed && len(free) > 0
				if got := p.Submit(0xffffffff, s, font, private); got != want {
					t.Fatal("immediate-cancel submit mismatch")
				}
				p.Cancel(0xffffffff)
				diffSettlePrefetch(t, p)
			case 6:
				p.Close()
				p.Close()
				closed = true
				clear(pending)
			}
			if p.Ready() != len(pending) {
				t.Fatal("ready count differs from model")
			}
			if !closed {
				p.mu.Lock()
				if !slices.Equal(p.free, func() []int32 {
					out := make([]int32, len(free))
					for j, v := range free {
						out[j] = int32(v)
					}
					return out
				}()) {
					p.mu.Unlock()
					t.Fatal("free slots differ from model")
				}
				for slot, j := range p.jobs {
					want, ok := pending[slot]
					if ok && (j.state != shapeReady || j.group != want.group || j.font != want.key.font || string(j.name) != want.key.text) || !ok && j.state != shapeFree || len(j.name) > PrefetchTextLimit {
						p.mu.Unlock()
						t.Fatal("slot state differs from model")
					}
				}
				p.mu.Unlock()
			}
			for _, name := range names {
				k := diffRunKey{font, string(name)}
				ref, hit := s.CachedRun(font, name)
				if hit != cached[k] {
					t.Fatalf("unexpected cache state for %x", name)
				}
				if hit {
					direct.runs.drop()
					want := direct.Run(direct.Shape(df, name))
					got := s.Run(ref)
					diffEqualRun(t, got, want)
					for j := range got.Faces {
						if s.faceName(got.Faces[j]) != direct.faceName(want.Faces[j]) {
							t.Fatal("import changed concrete font")
						}
					}
				}
			}
		}
	})
}
