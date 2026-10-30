//go:build darwin || windows || linux

package text

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func workerSystem(t testing.TB) *System {
	t.Helper()
	s, err := New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func waitShapes(t testing.TB, p *Prefetcher, n int) {
	t.Helper()
	end := time.Now().Add(10 * time.Second)
	for {
		p.mu.Lock()
		ready := p.ready
		p.mu.Unlock()
		if ready == n {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("%d results ready, want %d", ready, n)
		}
		runtime.Gosched()
	}
}

// The UI goroutine is pinned while workers create CTFonts and CTLines (or
// DirectWrite/go-text objects) on other pinned threads. The output must match
// synchronous shaping, including concrete fallback faces and raster metrics.
func TestWorkerShapeMatchesMain(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	s, direct := workerSystem(t), workerSystem(t)
	var wakes atomic.Int32
	p, err := NewPrefetcher(2, 32, func() { wakes.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	texts := []string{"office fi fl ffi", "日本語のメモ 文件", "Hello 👨‍👩‍👧 👍🏽 🇫🇷", "abc مرحبا שלום 123", "e\u0301x\u0308", "a\xffb\xe6\x97c", ""}
	for _, d := range []FontDesc{
		{SizePt: 13, Scale: 2, Weight: 400},
		{SizePt: 15, Scale: 1.25, Weight: 700, Italic: true},
		{SizePt: 13, Scale: 2, Mono: true, TabularDigits: true},
	} {
		f, df := s.Font(d), direct.Font(d)
		for _, str := range texts {
			if !p.Submit(1, s, f, []byte(str)) {
				t.Fatal("request rejected")
			}
			// Shape on the UI thread concurrently with both workers.
			direct.Shape(df, []byte(str))
		}
		waitShapes(t, p, len(texts))
		if n := p.Poll(s, len(texts)); n != len(texts) {
			t.Fatalf("imported %d, want %d", n, len(texts))
		}
		for _, str := range texts {
			ref, hit := s.CachedRun(f, []byte(str))
			if !hit {
				t.Fatalf("missing import %q", str)
			}
			got, want := s.Run(ref), direct.Run(direct.Shape(df, []byte(str)))
			if !bytes.Equal(got.Text, want.Text) || !slices.Equal(got.Glyphs, want.Glyphs) ||
				!slices.Equal(got.Clusters, want.Clusters) || !slices.Equal(got.X, want.X) || got.Advance != want.Advance {
				t.Fatalf("worker/main mismatch for %+v %q:\n%+v\n%+v", d, str, got, want)
			}
			for i, g := range got.Glyphs {
				if s.faceName(got.Faces[i]) != direct.faceName(want.Faces[i]) {
					t.Fatalf("different concrete face for %q glyph %d", str, i)
				}
				a, b := s.Glyph(got.Faces[i], g, 0), direct.Glyph(want.Faces[i], g, 0)
				if a.W != b.W || a.H != b.H || a.BearingX != b.BearingX || a.BearingY != b.BearingY || a.Color != b.Color {
					t.Fatalf("different raster metrics for %q glyph %d: %+v / %+v", str, i, a, b)
				}
			}
		}
	}
	if wakes.Load() == 0 {
		t.Fatal("no completion wake")
	}
}

func TestImportOwnsOutputAcrossEvictionAndWorkerClose(t *testing.T) {
	s := workerSystem(t)
	d := FontDesc{SizePt: 13, Scale: 2, Weight: 400}
	f := s.Font(d)
	w, err := NewShaper()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var r ShapedRun
	defer r.Close()
	b := []byte("fallback 日本語 😀")
	if !w.Shape(d, b, &r) {
		t.Fatal("shape failed")
	}
	advance := r.advance
	clear(b) // output owns the input and all glyph data
	w.s.runs.drop()
	w.Close() // output retains native faces independently
	s.runs.maxRuns, s.runs.maxGlyphs, s.runs.maxText = 2, 8, 16
	gen := s.RunGeneration()
	ref := s.ImportRun(f, &r)
	if ref != overRunRef || s.RunGeneration() == gen || s.Run(ref).Advance != advance {
		t.Fatal("oversized import did not obey the cache's generation contract")
	}
	if hit, ok := s.CachedRun(f, []byte("fallback 日本語 😀")); !ok || hit != ref {
		t.Fatal("oversized run not cached")
	}
	if rr, width, generation := s.ShapeInfo(f, []byte("fallback 日本語 😀")); rr != ref || width != advance || generation != s.RunGeneration() {
		t.Fatal("UI shape info lost the imported oversized run's width")
	}
	gen = s.RunGeneration()
	if s.ImportRun(f, &r) != ref || s.RunGeneration() != gen {
		t.Fatal("duplicate import evicted or duplicated a run")
	}
	r.Close()
	v := s.Run(ref)
	if v.Advance != advance || string(v.Text) != "fallback 日本語 😀" {
		t.Fatal("import aliases the result's buffers")
	}
	for i, g := range v.Glyphs {
		s.Glyph(v.Faces[i], g, 0) // every native face must still be alive
	}
}

func TestImportRejectsReusedFontAndObeysEpochEviction(t *testing.T) {
	s := workerSystem(t)
	d := FontDesc{SizePt: 13, Scale: 2}
	f := s.Font(d)
	w, err := NewShaper()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	var r ShapedRun
	defer r.Close()
	w.Shape(d, []byte("old"), &r)
	s.ReleaseScale(2)
	if s.ImportRun(f, &r) != 0 {
		t.Fatal("released font accepted")
	}
	other := s.Font(FontDesc{SizePt: 16, Scale: 1})
	if other != f || s.ImportRun(other, &r) != 0 {
		t.Fatal("reused key accepted the old font")
	}
	f = s.Font(d)
	s.runs.maxRuns, s.runs.maxGlyphs, s.runs.maxText = 16, 128, 128
	s.runs.drop()
	for i := 0; i < 80; i++ {
		s.Shape(f, []byte("hot"))
		w.Shape(d, []byte(fmt.Sprintf("row%03d", i)), &r)
		s.ImportRun(f, &r)
		if _, ok := s.CachedRun(f, []byte("hot")); !ok {
			t.Fatal("imports evicted the hot run")
		}
		if len(s.runs.ents) > 16 || len(s.runs.glyph) > 128 || len(s.runs.text) > 128 {
			t.Fatal("import exceeded a cache budget")
		}
	}
	if s.RunGeneration() < 3 {
		t.Fatal("imports did not exercise eviction")
	}
}

func TestPrefetchBoundCancelAndCopiedInput(t *testing.T) {
	s := workerSystem(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 1})
	p, err := NewPrefetcher(1, 4, nil) // serial fallback
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	buf := []byte("name0")
	for i := 0; i < 4; i++ {
		buf[4] = byte('0' + i)
		if !p.Submit(1, s, f, buf) {
			t.Fatal("request rejected before capacity")
		}
	}
	clear(buf)
	if p.Submit(2, s, f, []byte("full")) || p.Submit(2, s, f, []byte(strings.Repeat("x", PrefetchTextLimit+1))) {
		t.Fatal("pool exceeded its bounds")
	}
	waitShapes(t, p, 4)
	if p.Poll(s, 1) != 1 {
		t.Fatal("bounded import failed")
	}
	p.Cancel(1)
	if p.Poll(s, 4) != 0 {
		t.Fatal("cancelled results imported")
	}
	hits := 0
	for i := 0; i < 4; i++ {
		if _, ok := s.CachedRun(f, []byte(fmt.Sprintf("name%d", i))); ok {
			hits++
		}
	}
	if hits != 1 {
		t.Fatalf("%d copied names cached, want only the one imported", hits)
	}
	for i := 0; i < 100; i++ {
		p.Submit(1, s, f, []byte("cancel while running 日本語 😀"))
		p.Cancel(1)
	}
	if p.Poll(s, 4) != 0 {
		t.Fatal("stale work imported after cancellation")
	}
	p.Close() // also joins in-flight native calls
	p.Close()
	if p.Submit(2, s, f, []byte("closed")) {
		t.Fatal("closed pool accepted work")
	}
}

// Alternating fonts of one scale keeps both: after the first pass, shaping
// in either allocates nothing (a release per switch would remake the faces).
func TestShaperKeepsFontsOfOneScale(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	w, err := NewShaper()
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	regular, semi := FontDesc{SizePt: 13, Scale: 2, Weight: 400}, FontDesc{SizePt: 13, Scale: 2, Weight: 600}
	name := []byte("report_2026-10.pdf")
	var out ShapedRun
	defer out.Close()
	round := func() {
		if !w.Shape(regular, name, &out) || !w.Shape(semi, name, &out) {
			t.Fatal("shape failed")
		}
	}
	round()
	if n := testing.AllocsPerRun(20, round); n != 0 {
		t.Fatalf("alternating fonts of one scale: %v allocations per round", n)
	}
}

func TestWorkerBulkDataIsPointerFree(t *testing.T) {
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Array:
			check(typ.Elem())
		case reflect.Struct:
			for i := 0; i < typ.NumField(); i++ {
				check(typ.Field(i).Type)
			}
		case reflect.Pointer, reflect.UnsafePointer, reflect.String, reflect.Slice, reflect.Map, reflect.Interface, reflect.Func, reflect.Chan:
			t.Fatalf("pointer-bearing bulk record: %v", typ)
		}
	}
	check(reflect.TypeFor[shapedFace]())
	check(reflect.TypeFor[runEnt]())
}

func TestPrefetchScrollAllocatesNothing(t *testing.T) {
	s := workerSystem(t)
	f := s.Font(FontDesc{SizePt: 13, Scale: 1})
	s.runs.maxRuns, s.runs.maxGlyphs, s.runs.maxText = 128, 4096, 4096
	s.runs.reserve(128, 4096, 4096)
	p, err := NewPrefetcher(2, 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	names := make([][]byte, 2048)
	for i := range names {
		names[i] = []byte(fmt.Sprintf("file_%07d.txt", i))
	}
	step := func(start int) {
		for _, b := range names[start : start+32] {
			if !p.Submit(1, s, f, b) {
				t.Fatal("request rejected")
			}
		}
		waitShapes(t, p, 32)
		if p.Poll(s, 32) != 32 {
			t.Fatal("lost result")
		}
	}
	for i := 0; i < 256; i += 32 {
		step(i)
	}
	allocFree(t, "threaded scroll and imports", func() func() {
		s.runs.drop()
		step(0)
		return func() {
			for i := 256; i < len(names); i += 32 {
				step(i)
			}
		}
	})
}

// Warm runs its function once on each worker and returns when all have; a
// second Warm runs the new function; after Close it returns at once.
func TestPrefetchWarmRunsOnEveryWorker(t *testing.T) {
	p, err := NewPrefetcher(3, 8, nil)
	if err != nil {
		t.Fatal(err)
	}
	for round := 1; round <= 2; round++ {
		var n atomic.Int32
		p.Warm(func(w *Shaper) {
			var out ShapedRun
			defer out.Close()
			if !w.Shape(FontDesc{SizePt: 13, Scale: 2, Weight: 400}, []byte("Ωmega warm"), &out) {
				t.Error("warm shape failed")
			}
			n.Add(1)
		})
		if n.Load() != 3 {
			t.Fatalf("round %d: Warm ran on %d workers, want 3", round, n.Load())
		}
	}
	p.Close()
	done := make(chan struct{})
	go func() { p.Warm(func(*Shaper) { t.Error("warm after Close ran") }); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Warm after Close blocked")
	}
}
