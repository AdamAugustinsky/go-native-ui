package base

import "testing"

// The phases that reach into the id table at random look at the clock every
// vlRandomChunk items, whatever the chunk asked for: a page faulted back in
// per item made a 4096-item chunk of indexing take 5-60 ms on CI.
func TestVListRandomPhasesLookAtTheClockOften(t *testing.T) {
	defer func(d int) { vlDirect = d }(vlDirect)
	vlDirect = 40
	const n = 20_000
	var st VListState
	st.Reserve(n)
	st.Append(seq(0, n))
	st.Move(100, 1, 15_000)
	if !st.Pending() {
		t.Fatal("the move was not rebuilt")
	}
	for i := 0; st.job.phase != phIndex; i++ {
		if i > 1000 || !st.job.on {
			t.Fatalf("the rebuild did not reach indexing (phase %d)", st.job.phase)
		}
		st.work(vlChunk, 1<<30, true)
	}
	for st.job.phase == phIndex {
		i0 := st.job.i
		if did := st.work(vlChunk, 1<<30, true); did > vlRandomChunk || st.job.i-i0 > vlRandomChunk {
			t.Fatalf("indexing did %d items between two looks at the clock, want at most %d", did, vlRandomChunk)
		}
	}
	st.finishNow()
	if st.Pending() || st.Len() != n {
		t.Fatalf("the rebuild did not finish: pending %v, %d items", st.Pending(), st.Len())
	}
}
