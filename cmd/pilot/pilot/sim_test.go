package pilot

import (
	"bytes"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/core"
	"gonativeui/fs"
	"gonativeui/platform"
	"gonativeui/text"
)

// The environment changes independently of the two panes. Listings still go
// through the real service, sorting, pool, result ring and lease protocol.
type simFolder struct {
	n, variant              int
	removed, renamed, added string
	err                     error
}

func (f simFolder) fill(l *fs.Listing) error {
	if f.err != nil {
		return f.err
	}
	FillSynth(l, f.n, f.variant)
	n := 0
	for _, e := range l.Entries {
		name := l.Names[e.NameOff : uint32(e.NameOff)+uint32(e.NameLen)]
		if string(name) == f.removed {
			continue
		}
		if string(name) == f.renamed {
			e.NameOff = uint32(len(l.Names))
			l.Names = append(l.Names, "renamed_"...)
			l.Names = append(l.Names, name...)
			e.NameLen += uint16(len("renamed_"))
		}
		l.Entries[n] = e
		n++
	}
	l.Entries = l.Entries[:n]
	if f.added != "" {
		l.Entries = append(l.Entries, fs.Entry{NameOff: uint32(len(l.Names)), NameLen: uint16(len(f.added)), Size: 123})
		l.Names = append(l.Names, f.added...)
	}
	return nil
}

type simFiles struct {
	*fs.Service
	mu         sync.Mutex
	folders    map[string]simFolder
	calls      int
	leases     map[*fs.Listing]uint32
	badRelease bool
}

func (f *simFiles) list(path string, l *fs.Listing) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	d, ok := f.folders[path]
	if !ok {
		return os.ErrNotExist
	}
	return d.fill(l)
}
func (f *simFiles) set(path string, d simFolder) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.folders[path] = d
}
func (f *simFiles) snapshot(path string) simFolder {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.folders[path]
}
func (f *simFiles) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}
func (f *simFiles) Poll(dst []fs.Result) []fs.Result {
	dst = f.Service.Poll(dst)
	for _, r := range dst {
		if r.Listing != nil {
			f.leases[r.Listing] = r.Lease
		}
	}
	return dst
}
func (f *simFiles) Release(l *fs.Listing, lease uint32) {
	if lease != 0 {
		if f.leases[l] != lease {
			f.badRelease = true
		}
		delete(f.leases, l)
	}
	f.Service.Release(l, lease)
}

type simPilot struct {
	*Pilot
	files *simFiles
	n     int
}

func (b *simPilot) Init(a *app.App) error {
	b.files = &simFiles{folders: map[string]simFolder{
		nativePath("/synth"): {n: 12}, nativePath("/synth/current"): {n: b.n}, nativePath("/synth/small"): {n: 24},
	}, leases: make(map[*fs.Listing]uint32)}
	b.files.Service = fs.NewServiceWith(platform.Wake, b.files.list)
	b.Pilot = New(Options{Src: b.files, Home: nativePath("/synth"), RootTitle: "Simulation", Places: []Place{}, Still: true, Legacy: true, Loc: time.UTC})
	if err := b.Pilot.Init(a); err != nil {
		return err
	}
	for _, p := range b.panes {
		p.request(nativePath("/synth/current"), navUser, "")
	}
	return nil
}
func (b *simPilot) SimClose() {
	if b.Pilot != nil {
		b.Close()
	}
}
func (b *simPilot) SimRetained() map[string]int {
	m := map[string]int{"listing-pool": b.files.RetainedPoolBytes()}
	for i, p := range b.panes {
		m[fmt.Sprintf("pane-%d", i)] = 4*(cap(p.v.rows)+cap(p.rc.rows)+cap(p.rc.m)) + 8*(cap(p.rowFilter.bits)+cap(p.rc.filter.bits)+cap(p.rc.tab.h)) + 4*cap(p.rc.tab.v)
	}
	return m
}

func pilotSimMust(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func newPilotSim(t testing.TB, seed uint64, n int, render ...bool) (*app.Sim, *simPilot) {
	t.Helper()
	// ReadMemStats is process-wide. Workers already run serially at the
	// barrier; one P also isolates exact frame gates from per-P runtime timer
	// heap/sudog growth, as the filesystem allocation tests do.
	procs := runtime.GOMAXPROCS(1)
	t.Cleanup(func() { runtime.GOMAXPROCS(procs) })
	// Native icon preferences and sidebar discovery are external inputs. Pin
	// the former; explicit Places above avoids the latter. Icon decoding and
	// prefetch still use their real, barrier-gated worker pools.
	old := iconStyle
	iconStyle = func() uint64 { return 0 }
	baseline := runtime.NumGoroutine()
	b := &simPilot{n: n}
	s, err := app.NewSim(Config(), tw, th, 1, b, app.SimOptions{Seed: seed, Render: len(render) > 0 && render[0]})
	pilotSimMust(t, err)
	t.Cleanup(func() {
		s.Close()
		pilotSimMust(t, s.CheckGoroutines(baseline))
		iconStyle = old
	})
	pilotSimSettle(t, s, b)
	return s, b
}

func pilotSimSettle(t testing.TB, s *app.Sim, b *simPilot) {
	t.Helper()
	pilotSimMust(t, s.Advance(10*time.Second))
	for i, p := range b.panes {
		if p.loading() || p.relPending || p.jobBusy || p.rc.active {
			t.Fatalf("pane %d did not settle: nav=%v relist=%v rows=%v reconcile=%v", i, p.nav.pending, p.relPending, p.jobBusy, p.rc.active)
		}
	}
	if b.files.badRelease || b.files.IgnoredReleases() != 0 || len(b.files.leases) != 2 {
		t.Fatalf("leases: active=%d bad=%v ignored=%d", len(b.files.leases), b.files.badRelease, b.files.IgnoredReleases())
	}
}

func pilotSimContents(t testing.TB, b *simPilot) {
	t.Helper()
	for i, p := range b.panes {
		want := new(fs.Listing)
		pilotSimMust(t, b.files.snapshot(p.dir).fill(want))
		fs.Sort(want, p.sortKey, p.desc)
		if !slices.Equal(p.v.l.Entries, want.Entries) || !bytes.Equal(p.v.l.Names, want.Names) || !slices.Equal(p.v.l.Order, want.Order) {
			t.Fatalf("pane %d stale contents: dir=%s got=%d want=%d", i, p.dir, len(p.v.l.Entries), len(want.Entries))
		}
		var rows []uint32
		for _, e := range want.Order {
			if p.hideHidden && want.Entries[e].Flags&fs.FlagHidden != 0 {
				continue
			}
			if len(p.q) != 0 && !bytes.Contains(bytes.ToLower(want.Name(int(e))), bytes.ToLower(p.q)) {
				continue
			}
			rows = append(rows, e)
		}
		if !slices.Equal(p.v.order(), rows) {
			t.Fatalf("pane %d stale filtered rows: got=%d want=%d", i, p.v.nRows(), len(rows))
		}
		if p.topRow < 0 || p.topRow > max(p.v.nRows()-1, 0) {
			t.Fatalf("pane %d scroll outside rows: %d/%d", i, p.topRow, p.v.nRows())
		}
		for e, ok := p.v.ts.Sel.NextSet(0); ok; e, ok = p.v.ts.Sel.NextSet(e + 1) {
			if int(e) >= len(want.Entries) {
				t.Fatalf("pane %d selected vanished index %d", i, e)
			}
		}
	}
}

func pilotSimHide(t testing.TB, s *app.Sim) {
	t.Helper()
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus}))
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible}))
}
func pilotSimReturn(t testing.TB, s *app.Sim, b *simPilot) {
	t.Helper()
	before := b.files.count()
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown}))
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
	pilotSimSettle(t, s, b)
	if n := b.files.count() - before; n < 2 || n > 4 {
		t.Fatalf("return relists=%d, want 2..4", n)
	}
}

func TestPilotSimDeletedFolder(t *testing.T) {
	s, b := newPilotSim(t, 1, 128)
	pilotSimHide(t, s)
	b.files.set(nativePath("/synth/current"), simFolder{err: os.ErrNotExist})
	_, err := s.Idle(4*time.Hour, 0)
	pilotSimMust(t, err)
	pilotSimReturn(t, s, b)
	for i, p := range b.panes {
		if p.dir != nativePath("/synth") {
			t.Fatalf("pane %d still shows deleted directory %s (%d stale rows)", i, p.dir, p.v.nRows())
		}
	}
	pilotSimContents(t, b)
}

func TestPilotSimMemoryFloor(t *testing.T) {
	s, b := newPilotSim(t, 1, 1_000_000, true)
	for _, p := range b.panes {
		p.v.ts.Sel.SelectAll(len(p.v.l.Entries))
		p.relist()
	}
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	pilotSimSettle(t, s, b)
	for i, p := range b.panes {
		if p.v.nSel() != 1_000_000 {
			t.Fatalf("pane %d lost select-all on unchanged relist: %d", i, p.v.nSel())
		}
		if cap(p.rc.tab.h) < 1_000_000 {
			t.Fatal("unchanged select-all relist discarded warm name map")
		}
	}
	peak, err := s.Memory()
	pilotSimMust(t, err)
	for _, p := range b.panes {
		p.request(nativePath("/synth/small"), navUser, "")
	}
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	pilotSimSettle(t, s, b)
	// UI slabs and renderer rings trim after calm uses, rather than elapsed
	// time. Worker/listing/sorter idle expiry needs no such frame activity.
	for range 210 {
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		pilotSimMust(t, s.Advance(20*time.Millisecond))
	}
	pilotSimHide(t, s)
	_, err = s.Idle(4*time.Hour, 0)
	pilotSimMust(t, err)
	// Pilot explicitly reserves 4096 boxes plus the sentinel; Reserve is a
	// retained floor, including when fewer rows are shown.
	floor, err := s.CheckMemory(app.SimMemory{Boxes: 4097, Instances: 8192, RunGlyphs: text.MaxCachedGlyphs, RunText: text.MaxCachedTextBytes, Subsystems: map[string]int{"0/listing-pool": 8 << 20, "0/pane-0": 1 << 20, "0/pane-1": 1 << 20}})
	t.Logf("memory peak=%+v floor=%+v", peak, floor)
	pilotSimMust(t, err)
	if floor.HeapLive >= peak.HeapLive {
		t.Fatal("heap did not fall after leaving million-entry folder")
	}
	pilotSimMust(t, s.DeviceLost())
	pilotSimReturn(t, s, b)
	pilotSimContents(t, b)
}

func TestPilotSimCloseReturnsLeases(t *testing.T) {
	s, b := newPilotSim(t, 1, 32)
	s.Close()
	if len(b.files.leases) != 0 {
		t.Fatalf("Close retained %d listing leases", len(b.files.leases))
	}
}

func runPilotSim(t *testing.T, seed uint64, hours time.Duration) uint64 {
	t.Helper()
	s, b := newPilotSim(t, seed, 4096)
	defer s.Close()
	start := core.Now()
	finish := start + int64(hours)
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus, Flags: platform.FlagDown}))
	pilotSimSettle(t, s, b)
	baseline, err := s.Memory()
	pilotSimMust(t, err)
	var peak app.SimMemory
	huge := 100000 * (1 + rng.IntN(10))
	if seed == 1 {
		huge = 1_000_000
	}
	idleFrames := 0
	for round := range 6 {
		i := rng.IntN(2)
		p := b.panes[i]
		b.a.UI.SetFocus(listKey(i))
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		pilotSimMust(t, s.Advance(time.Second))
		// The same kinds of input as script.go: keyboard selection, header sort,
		// hover and trackpad scroll, plus type-to-filter and caret movement.
		for range 2 + rng.IntN(5) {
			pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvKey, Key: platform.KeyDown, Flags: platform.FlagDown}))
			pilotSimMust(t, s.Advance(50*time.Millisecond))
		}
		if p.v.nSel() == 0 {
			t.Fatalf("seed=%d round=%d keyboard did not select", seed, round)
		}
		sc := script{pl: b.Pilot}
		x, y := sc.listPoint(i, 4)
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerMove, X: x, Y: y}))
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvScroll, Flags: platform.FlagPrecise | platform.FlagPos, X: x, Y: y, DY: -float32(40 + rng.IntN(160))}))
		p.sortBy(fs.SortKey(rng.IntN(3)))
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		pilotSimSettle(t, s, b)
		if round == 1 {
			pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvText}, []byte("file_")))
			pilotSimMust(t, s.Advance(time.Second))
			pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvKey, Key: platform.KeyLeft, Flags: platform.FlagDown}))
			pilotSimMust(t, s.Advance(time.Second))
			if p.filter.String() != "file_" || p.filter.Caret() != 4 {
				t.Fatalf("filter input/caret: %q/%d", p.filter.String(), p.filter.Caret())
			}
			b.a.UI.SetFocus(listKey(i)) // leave the filter open, without an idle caret timer
			pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
			pilotSimSettle(t, s, b)
		}
		// Focus loss without occlusion is a separate background interval.
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvFocus}))
		pilotSimMust(t, s.Advance(time.Second))
		quiet, err := s.Idle(time.Duration(1+rng.IntN(60))*time.Second, app.SimDeadline|app.SimContinuation)
		pilotSimMust(t, err)
		idleFrames += quiet.Frames
		if quiet.Frames > 2 {
			t.Fatalf("background frame storm: %+v", quiet)
		}
		var sorts [2]fs.SortKey
		var descending [2]bool
		var filters [2]string
		var carets [2]int
		var selectedNames [2][]string
		var tops [2]string
		var topRows [2]int
		var fractions [2]float32
		for j, p := range b.panes {
			topRows[j] = p.topRow
			sorts[j], descending[j], filters[j], carets[j] = p.sortKey, p.desc, p.filter.String(), p.filter.Caret()
			for e, ok := p.v.ts.Sel.NextSet(0); ok; e, ok = p.v.ts.Sel.NextSet(e + 1) {
				selectedNames[j] = append(selectedNames[j], string(p.v.l.Name(int(e))))
			}
			if p.topEntry >= 0 {
				tops[j] = string(p.v.l.Name(int(p.topEntry)))
				fractions[j] = p.topFrac
			}
		}
		focus := b.a.UI.FocusKey()
		// Queue an uncached neighborhood, then hide before workers are released.
		f := b.nameRanges[i].font
		b.queueNames(i, f, 1500, 1532)
		b.flushNames()
		pilotSimHide(t, s)
		d := b.files.snapshot(nativePath("/synth/current"))
		switch round {
		case 0:
			d.removed = selectedNames[i][0]
			d.renamed = string(p.v.l.Name(int(p.v.entryAt(min(20, p.v.nRows()-1)))))
			d.added = fmt.Sprintf("added_%d.txt", seed)
		case 1:
			var synth SynthSource
			synth.variant.Store(int32(d.variant))
			synth.Bump()
			d.variant = int(synth.variant.Load())
		case 2:
			d = simFolder{n: huge}
			pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvResize, W: 1500, H: 950, Scale: 1.25}))
		case 3:
			d = simFolder{n: 37, variant: 2, added: "replacement.txt"}
		case 4:
			d = simFolder{err: os.ErrNotExist}
			pilotSimMust(t, s.DeviceLost())
		case 5:
			d = simFolder{n: 128, added: "recreated.txt"}
			for _, p := range b.panes {
				p.request(nativePath("/synth/current"), navUser, "")
			}
		}
		b.files.set(nativePath("/synth/current"), d)
		hidden := hours/10 + time.Duration(rng.IntN(120))*time.Second
		quiet, err = s.Idle(hidden, 0)
		pilotSimMust(t, err)
		idleFrames += quiet.Frames
		pilotSimReturn(t, s, b)
		pilotSimContents(t, b)
		for j, p := range b.panes {
			if p.sortKey != sorts[j] || p.desc != descending[j] {
				t.Fatalf("pane %d lost sort across return", j)
			}
			if round < 4 {
				if p.filter.String() != filters[j] || p.filter.Caret() != carets[j] {
					t.Fatalf("pane %d lost filter/caret", j)
				}
				var wantSelected []string
				for _, name := range selectedNames[j] {
					if findName(p.v.l, name) >= 0 {
						wantSelected = append(wantSelected, name)
					}
				}
				var gotSelected []string
				for e, ok := p.v.ts.Sel.NextSet(0); ok; e, ok = p.v.ts.Sel.NextSet(e + 1) {
					gotSelected = append(gotSelected, string(p.v.l.Name(int(e))))
				}
				slices.Sort(wantSelected)
				slices.Sort(gotSelected)
				if !slices.Equal(wantSelected, gotSelected) {
					t.Fatalf("pane %d selection got=%v want=%v", j, gotSelected, wantSelected)
				}
				e := findName(p.v.l, tops[j])
				row := p.v.rowOf(e)
				// At row zero Pilot keeps the beginning visible, including new
				// entries inserted before the old first row. A scrolled view
				// instead carries its surviving top name across reconciliation.
				if topRows[j] == 0 && p.topRow != 0 {
					t.Fatalf("pane %d left the top on return", j)
				}
				if topRows[j] > 0 && row > 0 && row < p.v.nRows()-b.geo[j].visRows && (p.topEntry != e || absSim(p.topFrac-fractions[j]) > 0.06) {
					t.Fatalf("seed=%d round=%d pane %d lost scroll anchor %q: before=%d row=%d top=%d frac=%g want=%g", seed, round, j, tops[j], topRows[j], row, p.topRow, p.topFrac, fractions[j])
				}
			} else if p.filterOpen || p.filter.Len() != 0 {
				t.Fatalf("pane %d navigation retained old filter", j)
			}
		}
		if round < 4 && b.a.UI.FocusKey() != focus {
			t.Fatalf("focus changed on return: %x -> %x", focus, b.a.UI.FocusKey())
		}
		if round == 2 {
			peak, err = s.Memory()
			pilotSimMust(t, err)
			for j, p := range b.panes {
				r := b.nameRanges[j]
				desc, ok := b.a.Text.FontDescription(r.font)
				if !ok || desc.Scale != 1.25 || r.font == f {
					t.Fatal("prefetch retained old scale font")
				}
				if p.v.nRows() > 0 {
					if _, ok := b.a.Text.CachedRun(r.font, p.v.l.Name(int(p.v.entryAt(p.topRow)))); !ok {
						t.Fatal("return names not shaped at new scale")
					}
				}
			}
		}
		if round == 3 {
			pilotSimHide(t, s)
			_, err = s.Idle(time.Minute, 0)
			pilotSimMust(t, err)
			floor, err := s.CheckMemory(app.SimMemory{HeapLive: baseline.HeapLive + 16<<20, RunGlyphs: text.MaxCachedGlyphs, RunText: text.MaxCachedTextBytes, Subsystems: map[string]int{"0/listing-pool": 8 << 20, "0/pane-0": 1 << 20, "0/pane-1": 1 << 20}})
			pilotSimMust(t, err)
			if floor.HeapLive >= peak.HeapLive {
				t.Fatalf("heap failed to fall: peak=%d floor=%d", peak.HeapLive, floor.HeapLive)
			}
			pilotSimReturn(t, s, b)
		}
	}
	b.a.UI.SetFocus(listKey(0))
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvPointerLeave}))
	pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
	pilotSimSettle(t, s, b)
	// Exact D11 accounting around warmed frame bodies, excluding trace/workers.
	s.CheckAllocations(true)
	for range 16 {
		pilotSimMust(t, s.Event(0, platform.Event{Kind: platform.EvWake}))
		pilotSimMust(t, s.Advance(20*time.Millisecond))
	}
	s.CheckAllocations(false)
	draw, _ := s.FrameHash(0)
	pilotSimHide(t, s)
	_, err = s.Idle(time.Duration(max(finish-core.Now()-int64(10*time.Second), 0)), 0)
	pilotSimMust(t, err)
	pilotSimReturn(t, s, b)
	pilotSimMust(t, app.SimReturnFrame(0, draw)(s))
	hash := s.TraceHash()
	frames := 0
	for _, r := range s.Trace() {
		if r.Kind == app.SimFrame {
			frames++
		}
	}
	t.Logf("seed=%d simulated=%s huge=%d trace=%016x frames=%d idle-frames=%d relists=%d", seed, time.Duration(core.Now()-start), huge, hash, frames, idleFrames, b.files.count())
	return hash
}

func absSim(x float32) float32 {
	if x < 0 {
		return -x
	}
	return x
}

func TestPilotSimLifecycle(t *testing.T) {
	hours := 24 * time.Hour
	if v := os.Getenv("GNUI_SIM_HOURS"); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		pilotSimMust(t, err)
		if h < 1 {
			t.Fatal("GNUI_SIM_HOURS must be at least 1")
		}
		hours = time.Duration(h * float64(time.Hour))
	}
	seeds := 1
	if v := os.Getenv("GNUI_SIM_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		pilotSimMust(t, err)
		seeds = n
	}
	first := uint64(1)
	if v := os.Getenv("GNUI_SIM_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 0, 64)
		pilotSimMust(t, err)
		first, seeds = n, 1
	}
	if seeds < 1 {
		t.Fatal("GNUI_SIM_SEEDS must be positive")
	}
	for j := range seeds {
		seed := first + uint64(j)
		t.Run(fmt.Sprint(seed), func(t *testing.T) {
			var hashes [2]uint64
			for replay := range hashes {
				if !t.Run(fmt.Sprint(replay), func(t *testing.T) { hashes[replay] = runPilotSim(t, seed, hours) }) {
					return // an interrupted run has no replay hash to compare
				}
			}
			if hashes[0] != hashes[1] {
				t.Fatalf("seed=%d replay diverged: %016x != %016x", seed, hashes[0], hashes[1])
			}
		})
	}
}

var _ app.Builder = (*simPilot)(nil)
var _ app.SimRetainer = (*simPilot)(nil)
