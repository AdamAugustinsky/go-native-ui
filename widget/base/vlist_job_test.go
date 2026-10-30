package base

import (
	"testing"
	"time"
)

// The phases that reach into the id table at random look at the clock every
// vlRandomChunk items, whatever the chunk asked for: a page faulted back in
// per item made a 4096-item chunk of indexing take 5-60 ms on CI. A Move
// sweeps sequential slots instead and can use the normal sequential chunk.
func TestVListRandomPhasesLookAtTheClockOften(t *testing.T) {
	defer func(d int) { vlDirect = d }(vlDirect)
	vlDirect = 40
	const n = 20_000
	for _, sweep := range []bool{false, true} {
		var st VListState
		st.Reserve(n)
		st.Append(seq(0, n))
		if sweep {
			st.Move(100, 1, 15000)
		} else {
			st.Reset(seq(100, n))
		}
		if !st.Pending() {
			t.Fatal("the edit was not rebuilt")
		}
		for i := 0; st.job.phase != phIndex; i++ {
			if i > 1000 || !st.job.on {
				t.Fatalf("the rebuild did not reach indexing (phase %d)", st.job.phase)
			}
			st.work(vlChunk, 1<<30, true)
		}
		for st.job.phase == phIndex {
			i0 := st.job.i
			limit := vlRandomChunk
			if st.job.sweep {
				limit = vlChunk
			}
			if did := st.work(vlChunk, 1<<30, true); did > limit || st.job.i-i0 > limit {
				t.Fatalf("indexing did %d items between two looks at the clock, want at most %d", did, limit)
			}
		}
		st.finishNow()
		if st.Pending() || st.Len() != n {
			t.Fatalf("the rebuild did not finish: pending %v, %d items", st.Pending(), st.Len())
		}
	}
}

func TestVListReleasesIdleBackBuffer(t *testing.T) {
	var st VListState
	st.Reserve(100000)
	st.Reset(seq(0, 100000))
	if st.bk.slots() != 0 {
		t.Fatal("initial Reset allocated a second buffer")
	}
	st.Move(25000, 1, 75000)
	st.finishNow()
	if st.bk.slots() == 0 {
		t.Fatal("long edit did not keep a warm back buffer")
	}
	st.bkUsed = time.Now().UnixNano() - vlBackIdleNs - 1
	st.Sync(1)
	if st.bk.slots() != 0 {
		t.Fatal("idle back buffer is retained")
	}
	st.Move(75000, 1, 25000)
	st.finishNow()
	for i := range 100000 {
		if st.ID(i) != uint64(i) {
			t.Fatalf("regrown order differs at %d", i)
		}
	}
}
