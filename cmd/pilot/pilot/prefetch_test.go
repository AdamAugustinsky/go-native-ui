package pilot

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/fs"
	"gonativeui/text"
)

func nameBench(t testing.TB, n int, workers bool) (*Pilot, text.FontKey) {
	t.Helper()
	runtime.LockOSThread() // the same ownership as Pilot's UI thread
	t.Cleanup(runtime.UnlockOSThread)
	s, err := text.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.ReserveRuns(text.WorkingSetRuns, text.WorkingSetGlyphs, text.WorkingSetTextBytes)
	f := s.Font(text.FontDesc{SizePt: 13, Scale: 2, Weight: 400})
	pl := New(Options{RootTitle: "Root"})
	pl.a = &app.App{Text: s}
	if workers {
		pl.shapes, err = text.NewPrefetcher(2, nameSlots, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(pl.shapes.Close)
	}
	l := new(fs.Listing)
	FillSynth(l, n, 0)
	fs.Sort(l, fs.SortName, false)
	p := pl.panes[0]
	p.hideHidden = false
	p.install("/new-listing", l, 0, "")
	return pl, f
}

func awaitNames(t testing.TB, pl *Pilot, f text.FontKey, first, last int) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for {
		pl.shapes.Poll(pl.a.Text, nameSlots)
		all := true
		for i := first; i < last; i++ {
			if _, ok := pl.a.Text.CachedRun(f, pl.panes[0].v.l.Name(int(pl.panes[0].v.entryAt(i)))); !ok {
				all = false
				break
			}
		}
		if all {
			return
		}
		if time.Now().After(end) {
			t.Fatal("names did not arrive")
		}
		runtime.Gosched()
	}
}

func TestNamePrefetchFollowsScrollRelistAndFont(t *testing.T) {
	pl, f := nameBench(t, 10000, true)
	s, p := pl.a.Text, pl.panes[0]
	pl.queueNames(0, f, 0, 32)
	awaitNames(t, pl, f, 0, nameVisible+nameMargin)
	if _, ok := s.CachedRun(f, p.v.l.Name(int(p.v.entryAt(1000)))); ok {
		t.Fatal("prefetch shaped beyond the bounded neighborhood")
	}
	pl.queueNames(0, f, 1000, 1032)
	// A relist can return the same pooled Listing pointer with another lease.
	// The preceding range must be cancelled even though the address is equal.
	p.v.lease++
	p.fver++
	f2 := s.Font(text.FontDesc{Mono: true, SizePt: 12, Scale: 1.5})
	pl.queueNames(0, f2, 4000, 4032)
	awaitNames(t, pl, f2, 4000, 4032)
	if _, ok := s.CachedRun(f, p.v.l.Name(int(p.v.entryAt(1000)))); ok {
		t.Fatal("stale scroll range was imported after the relist")
	}
	if _, ok := s.CachedRun(f2, p.v.l.Name(int(p.v.entryAt(0)))); ok {
		t.Fatal("a new font caused an unbounded listing prefetch")
	}
}

// These compare the name-shaping portion of a first frame, using Pilot's real
// request path and 32 visible rows of a sorted 10k/100k listing. Main is the
// pre-worker path; Immediate gives workers no lead time; Ready measures a
// listing whose results arrived before the frame. Worker wait time is excluded
// only in Ready, and all three include main-thread queue/import overhead.
// No GPU rendering, disk I/O, filtering or whole-frame latency is measured.
func BenchmarkListingNameFirstFrame(b *testing.B) {
	for _, n := range []int{10000, 100000} {
		for _, mode := range []string{"Main", "Immediate", "Ready"} {
			b.Run(fmt.Sprintf("%d/%s", n, mode), func(b *testing.B) {
				pl, f := nameBench(b, n, mode != "Main")
				p, s := pl.panes[0], pl.a.Text
				var ns int64
				var misses int
				var prefix [7]byte
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					b.StopTimer()
					if pl.shapes != nil {
						pl.shapes.Cancel(p.id)
					}
					// Change only the bounded visible neighborhood, so every
					// sample is a new listing's names even after cache warmup.
					pref := appendPadded(prefix[:0], i, 7)
					for row := 0; row < nameVisible+nameMargin; row++ {
						copy(p.v.l.Name(int(p.v.entryAt(row)))[:7], pref)
					}
					p.fver++
					b.StartTimer()
					pl.queueNames(0, f, 0, 32)
					if mode == "Ready" {
						b.StopTimer()
						// Await only completion, leaving import in the timed path.
						// Poll uses bounded work; checking the public cache here
						// would import it outside the measurement.
						waitNameWorkers(b, pl.shapes, nameVisible+nameMargin)
						b.StartTimer()
					}
					if pl.shapes != nil {
						pl.shapes.Poll(s, nameSlots)
					}
					start := time.Now()
					for row := 0; row < 32; row++ {
						name := p.v.l.Name(int(p.v.entryAt(row)))
						if _, ok := s.CachedRun(f, name); !ok {
							misses++
						}
						s.Shape(f, name)
					}
					ns += time.Since(start).Nanoseconds()
				}
				b.ReportMetric(float64(ns)/float64(b.N), "shape-ns/frame")
				b.ReportMetric(float64(misses)/float64(b.N), "misses/frame")
			})
		}
	}
}

func waitNameWorkers(t testing.TB, p *text.Prefetcher, n int) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for p.Ready() < n {
		if time.Now().After(end) {
			t.Fatalf("%d names ready, want %d", p.Ready(), n)
		}
		runtime.Gosched()
	}
}

// A steady scroll advances one row per 60 Hz frame. The pacing is outside
// the measured window: ns/op is timed UI work, excluding the inter-frame
// delay. Only the first screen
// is warmed; later names are cache misses without prefetch.
func BenchmarkListingNameScroll(b *testing.B) {
	for _, mode := range []string{"Main", "Workers"} {
		b.Run(mode, func(b *testing.B) {
			pl, f := nameBench(b, max(10000, b.N+128), mode == "Workers")
			s, p := pl.a.Text, pl.panes[0]
			pl.queueNames(0, f, 0, 32)
			if pl.shapes != nil {
				awaitNames(b, pl, f, 0, 96)
			}
			for row := 0; row < 32; row++ {
				s.Shape(f, p.v.l.Name(int(p.v.entryAt(row))))
			}
			var ns int64
			var misses int
			b.ReportAllocs()
			b.ResetTimer()
			for i := 1; i <= b.N; i++ {
				b.StopTimer()
				time.Sleep(time.Second / 60)
				b.StartTimer()
				pl.queueNames(0, f, i, i+32)
				if pl.shapes != nil {
					pl.shapes.Poll(s, nameSlots)
				}
				start := time.Now()
				for row := i; row < i+32; row++ {
					name := p.v.l.Name(int(p.v.entryAt(row)))
					if _, ok := s.CachedRun(f, name); !ok {
						misses++
					}
					s.Shape(f, name)
				}
				ns += time.Since(start).Nanoseconds()
			}
			b.ReportMetric(float64(ns)/float64(b.N), "shape-ns/frame")
			b.ReportMetric(float64(misses)/float64(b.N), "misses/frame")
		})
	}
}
