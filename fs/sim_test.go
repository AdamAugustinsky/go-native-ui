package fs

import (
	"fmt"
	"math/rand/v2"
	"os"
	"strconv"
	"testing"
	"time"

	"gonativeui/core"
)

// A sleep-like clock jump lets the deadline pass before the control worker
// runs again. Each pane must receive one current refresh, without timer wakes
// escaping to the UI or losing its listing lease.
func TestSimDebounceAcrossSleep(t *testing.T) {
	seeds := 1
	first := uint64(1)
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatal("invalid GNUI_SIM_SEEDS")
		}
		seeds = n
	}
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 0, 64)
		if err != nil {
			t.Fatal(err)
		}
		first, seeds = n, 1
	}
	for j := range seeds {
		seed := first + uint64(j)
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			c, err := core.InstallSimClockForTesting()
			if err != nil {
				t.Fatal(err)
			}
			defer c.Restore()
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			var f fakeFS
			w := new(fakeWatcher)
			s := newService(c.Wake, f.list, func(wake func()) (watcher, error) { w.wake = wake; return w, nil }, defaultDebounce)
			defer s.Close()
			for _, id := range []uint32{1, 2} {
				s.List(id, "n:11:current", SortName, false)
				s.Watch(id, "n:11:current")
			}
			must(c.Quiesce(time.Second))
			results := s.Poll(nil)
			if len(results) != 2 {
				t.Fatalf("initial results=%d", len(results))
			}
			for _, r := range results {
				s.Release(r.Listing, r.Lease)
			}
			c.TakeWake()
			rng := rand.New(rand.NewPCG(seed, seed))
			for round := range 8 {
				before := f.calls.Load()
				for range 1 + rng.IntN(32) {
					w.emit(Change{ID: 1, Kind: ChangeContent}, Change{ID: 2, Kind: ChangeContent})
				}
				if round%2 == 0 {
					w.emit(Change{Kind: ChangeOverflow})
				}
				must(c.Quiesce(time.Second))
				if c.TakeWake() || f.calls.Load() != before {
					t.Fatal("debounce woke UI or listed before its deadline")
				}
				mustAdvance := func(d time.Duration) {
					_, err := c.AdvanceTo(core.Now() + int64(d))
					must(err)
					must(c.Quiesce(time.Second))
				}
				mustAdvance(3 * time.Hour)
				results = s.Poll(results[:0])
				if len(results) != 2 || f.calls.Load()-before != 2 || !c.TakeWake() {
					t.Fatalf("round %d: results=%d relists=%d", round, len(results), f.calls.Load()-before)
				}
				for i, r := range results {
					if r.ID != uint32(i+1) || !r.Refresh {
						t.Fatalf("unstable/stale refresh: %+v", r)
					}
					checkFake(t, r.Listing, "n:11:current")
					s.Release(r.Listing, r.Lease)
				}
				mustAdvance(time.Minute)
				if c.TakeWake() || len(s.Poll(nil)) != 0 {
					t.Fatal("idle relist storm")
				}
			}
			if s.IgnoredReleases() != 0 {
				t.Fatal("lease mismatch")
			}
			s.mu.Lock()
			for i := range s.busy {
				if s.busy[i] || s.pins[i] != 0 {
					t.Errorf("slot %d retained: busy=%v pins=%d", i, s.busy[i], s.pins[i])
				}
			}
			s.mu.Unlock()
			sorterMu.Lock()
			retained := len(sorterFree)
			sorterMu.Unlock()
			if retained != 0 {
				t.Fatalf("idle sorter retained %d scratch buffers", retained)
			}
		})
	}
}

func TestSimListingPoolExpiresWithoutUIFrames(t *testing.T) {
	c, err := core.InstallSimClockForTesting()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Restore()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	s := NewServiceWith(c.Wake, func(_ string, l *Listing) error {
		const n = 330000
		if cap(l.Entries) < n {
			l.Entries = make([]Entry, n)
		} else {
			l.Entries = l.Entries[:n]
		}
		l.Names = append(l.Names[:0], 'x')
		for i := range l.Entries {
			l.Entries[i] = Entry{NameLen: 1, Size: int64(i)}
		}
		return nil
	})
	defer s.Close()
	for range 4 {
		s.List(1, "/synthetic", SortName, false)
		must(c.Quiesce(time.Second))
		r := s.Poll(nil)
		if len(r) != 1 || r[0].Listing == nil {
			t.Fatalf("listing: %+v", r)
		}
		s.Release(r[0].Listing, r[0].Lease)
		must(c.Quiesce(time.Second))
	}
	peak := s.RetainedPoolBytes()
	if peak <= listingPoolBytes {
		t.Fatalf("pool never grew: %d", peak)
	}
	c.TakeWake()
	_, err = c.AdvanceTo(core.Now() + int64(29*time.Second))
	must(err)
	must(c.Quiesce(time.Second))
	if s.RetainedPoolBytes() != peak {
		t.Fatal("pool expired early")
	}
	_, err = c.AdvanceTo(core.Now() + int64(time.Second))
	must(err)
	must(c.Quiesce(time.Second))
	floor := s.RetainedPoolBytes()
	if floor > listingPoolBytes || c.TakeWake() {
		t.Fatalf("idle pool=%d or maintenance woke UI", floor)
	}
	sorterMu.Lock()
	scratch := len(sorterFree)
	sorterMu.Unlock()
	if scratch != 0 {
		t.Fatalf("sorter retained %d buffers after 30s", scratch)
	}
	t.Logf("pool 29s=%d -> 30s=%d bytes; sorter buffers=%d; UI wakes=0", peak, floor, scratch)
}
