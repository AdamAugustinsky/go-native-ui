package mdview

import (
	"bytes"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gonativeui/highlight"
	"gonativeui/ui"
)

// A paused pool gives admission and cancellation tests a deterministic queue.
func pausedHighlights() *highlightPool {
	p := &highlightPool{}
	p.once.Do(func() { p.cond = sync.NewCond(&p.mu) })
	return p
}

func completeHighlight(p *highlightPool, e *hlEntry) {
	p.mu.Lock()
	for i, j := range p.jobs {
		if j == e.job {
			p.jobs[i] = nil
			break
		}
	}
	p.mu.Unlock()
	p.complete(e.job, highlight.Tokens(e.job.lang, e.job.src, nil))
}

func TestHighlightAdmissionAndCoalescing(t *testing.T) {
	p := pausedHighlights()
	h := highlighter{pool: p}
	c := ui.New(nil)
	src := []byte("var a = 1")
	h.tokens(c, 1, highlight.Go, src)
	first := h.byBlock[1].want
	h.tokens(c, 2, highlight.Go, src)
	h.tokens(c, 1, highlight.Go, []byte("var a = 2")) // same-length revision
	if first.job.cancelled.Load() {
		t.Fatal("superseding one block cancelled another block's shared job")
	}
	h.tokens(c, 2, highlight.Plain, nil)
	if !first.job.cancelled.Load() {
		t.Fatal("the superseded job was not cancelled")
	}
	if got := h.mail.pending.Load(); got != 1 {
		t.Fatalf("pending=%d, want one current source", got)
	}
	if p.bytes != len(src) {
		t.Fatalf("queued bytes=%d, want %d", p.bytes, len(src))
	}
	h.close()
	h.close()
	if h.mail.pending.Load() != 0 || p.bytes != 0 || h.byBlock != nil || h.byHash != nil {
		t.Fatal("Close retained jobs or cache state")
	}
	h.tokens(c, 1, highlight.Go, src)
	if p.bytes != 0 {
		t.Fatal("closed highlighter accepted a job")
	}
}

func TestFullHighlightQueueDoesNotCopy(t *testing.T) {
	p := pausedHighlights()
	m := &hlMailbox{}
	src := bytes.Repeat([]byte("x"), 1<<20)
	for range maxQueuedBytes / len(src) {
		if p.submit(m, highlight.Go, src) == nil {
			t.Fatal("early rejection")
		}
	}
	h := highlighter{pool: p}
	c := ui.New(nil)
	h.tokens(c, 1, highlight.Go, src)
	if n := testing.AllocsPerRun(100, func() { h.tokens(c, 1, highlight.Go, src) }); n != 0 {
		t.Fatalf("a rejected request allocated %v times", n)
	}
	if len(h.byHash) != 0 || p.bytes != maxQueuedBytes {
		t.Fatal("rejection changed retained inputs")
	}
	h.close()
}

func TestHighlightCompletionNotifiesAndCloseDiscards(t *testing.T) {
	p := pausedHighlights()
	h := highlighter{pool: p}
	h.init()
	var wakes atomic.Int32
	h.mail.wake = func() { wakes.Add(1) }
	c := ui.New(nil)
	h.tokens(c, 1, highlight.Go, []byte("var a = 1"))
	h.tokens(c, 2, highlight.Go, []byte("var b = 2"))
	completeHighlight(p, h.byBlock[1].want)
	completeHighlight(p, h.byBlock[2].want)
	if wakes.Load() != 1 {
		t.Fatalf("completion woke %d times", wakes.Load())
	}
	if c.WakeAfter() != 0 {
		t.Fatal("pending highlighting armed a poll timer")
	}
	if len(h.tokens(c, 1, highlight.Go, []byte("var a = 1"))) == 0 {
		t.Fatal("no completed tokens")
	}
	h.tokens(c, 3, highlight.Go, []byte("var c = 3"))
	late := h.byBlock[3].want
	// Take the last job as a worker would, then close before it publishes.
	for i, j := range p.jobs {
		if j == late.job {
			p.jobs[i] = nil
		}
	}
	h.close()
	p.complete(late.job, []highlight.Token{{Off: 0, Len: 3, Role: highlight.Keyword}})
	if wakes.Load() != 1 || h.mail.bytes != 0 || h.mail.pending.Load() != 0 {
		t.Fatal("late completion retained results or woke a closed view")
	}
}

func TestMarkdownBlockStateIsBounded(t *testing.T) {
	p := pausedHighlights()
	v := MarkdownView{hl: highlighter{pool: p}}
	defer v.Close()
	c := ui.New(nil)
	src := []byte("var a = 1")
	v.Tokens(c, 1, highlight.Go, src)
	completeHighlight(p, v.hl.byBlock[1].want)
	for i := 1; i <= 20000; i++ {
		v.Tokens(c, ui.Key(i), highlight.Go, src)
		v.source(c, ui.Key(i), src)
	}
	if len(v.hl.byHash) != 1 || len(v.hl.byBlock) > maxEntries || len(v.scroll) > maxEntries {
		t.Fatalf("cache sizes: sources=%d aliases=%d scroll=%d", len(v.hl.byHash), len(v.hl.byBlock), len(v.scroll))
	}
	st := v.source(c, 1, []byte("a\nb"))
	st = v.source(c, 1, []byte("abc"))
	if st.lines != 1 || st.columns != 3 {
		t.Fatal("same-length source revision reused old layout")
	}
	for i := 1; i <= 20; i++ {
		v.source(c, ui.Key(i), bytes.Repeat([]byte("\n"), 200000))
	}
	if v.layoutBytes > maxLayoutBytes {
		t.Fatalf("layout retained %d bytes", v.layoutBytes)
	}
}

func TestHighlightWorkersDoNotRetainDiscardedViews(t *testing.T) {
	// Warm the shared pool, then discard views without Close to exercise the
	// ownership boundary as well as the explicit lifecycle path.
	c := ui.New(nil)
	var finalized atomic.Int32
	for i := range 12 {
		v := &MarkdownView{}
		v.Tokens(c, ui.Key(i+1), highlight.Go, []byte(fmt.Sprintf("var a = %d", i)))
		runtime.SetFinalizer(v, func(*MarkdownView) { finalized.Add(1) })
	}
	until := time.Now().Add(5 * time.Second)
	for finalized.Load() != 12 && time.Now().Before(until) {
		runtime.GC()
		time.Sleep(time.Millisecond)
	}
	if finalized.Load() != 12 {
		t.Fatalf("%d of 12 discarded views were collected", finalized.Load())
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	if workers := strings.Count(string(buf[:n]), "gonativeui/rexui/mdview.(*highlightPool).worker("); workers != 2 {
		t.Fatalf("%d highlight workers, want shared pair", workers)
	}
}

func TestHighlightAliasesAgeIndependentlyAndTokensHaveAByteLimit(t *testing.T) {
	p := pausedHighlights()
	h := highlighter{pool: p}
	defer h.close()
	c := ui.New(nil)
	src := []byte("var a = 1")
	h.tokens(c, 1, highlight.Go, src)
	e := h.byBlock[1].want
	completeHighlight(p, e)
	h.tokens(c, 1, highlight.Go, src)
	h.tokens(c, 2, highlight.Go, src)
	young := h.byBlock[2]
	young.seen = 120
	h.byBlock[2] = young
	e.seen = 120
	h.forget(121)
	if _, ok := h.byBlock[1]; ok {
		t.Fatal("a shared source kept an old alias young")
	}
	if _, ok := h.byBlock[2]; !ok {
		t.Fatal("recent alias was evicted")
	}
	h.tokens(c, 3, highlight.Go, []byte("var b = 2"))
	large := h.byBlock[3].want
	for i, j := range p.jobs {
		if j == large.job {
			p.jobs[i] = nil
		}
	}
	p.complete(large.job, make([]highlight.Token, maxTokenBytes/12+1))
	if len(large.toks) != 0 || h.mail.bytes > maxTokenBytes {
		t.Fatal("oversized token result exceeded retained byte budget")
	}
}
