package mdview

import (
	"bytes"
	"hash/maphash"
	"sync"
	"sync/atomic"

	"gonativeui/highlight"
	"gonativeui/platform"
	"gonativeui/ui"
)

const (
	maxEntries       = 512
	maxTokenBytes    = 16 << 20
	maxQueuedBytes   = 16 << 20
	maxHighlightJobs = 64
)

// Workers own only jobs and a small mailbox, never the view containing the
// highlighter. Two workers serve all windows; admission bounds copied input.
type highlightPool struct {
	mu      sync.Mutex
	cond    *sync.Cond
	once    sync.Once
	jobs    [maxHighlightJobs]*hlJob
	bytes   int
	refused bool
	next    int
}

var codeWorkers highlightPool

type hlMailbox struct {
	mu       sync.Mutex
	pending  atomic.Int32
	closed   bool
	bytes    int
	notified bool
	wake     func()
}

type hlJob struct {
	mail      *hlMailbox
	e         *hlEntry
	src       []byte
	lang      highlight.Lang
	cancelled atomic.Bool
	counted   atomic.Bool
}

type hlEntry struct {
	toks  []highlight.Token
	ready atomic.Bool
	seen  uint32
	size  int
	refs  int
	job   *hlJob
}

type hlAlias struct {
	last, want *hlEntry
	seen       uint32
}

type highlighter struct {
	byHash  map[uint64]*hlEntry
	byBlock map[ui.Key]hlAlias
	seed    maphash.Seed
	mail    *hlMailbox
	pool    *highlightPool
	closed  bool
	frame   uint32
	ack     bool
}

func (p *highlightPool) start() {
	p.once.Do(func() {
		p.cond = sync.NewCond(&p.mu)
		for range 2 {
			go p.worker()
		}
	})
}

// submit reserves both a slot and bytes before copying the source.
func (p *highlightPool) submit(m *hlMailbox, lang highlight.Lang, src []byte) *hlEntry {
	p.start()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bytes+len(src) > maxQueuedBytes {
		p.refused = true
		return nil
	}
	for i, j := range p.jobs {
		if j != nil {
			continue
		}
		e := &hlEntry{}
		j = &hlJob{mail: m, e: e, lang: lang, src: bytes.Clone(src)}
		j.counted.Store(true)
		m.pending.Add(1)
		p.bytes += len(src)
		e.job = j
		p.jobs[i] = j
		p.cond.Signal()
		return e
	}
	p.refused = true
	return nil
}

func (j *hlJob) finish() {
	if j.counted.Swap(false) {
		j.mail.pending.Add(-1)
	}
}

func (p *highlightPool) cancel(j *hlJob) {
	if j == nil {
		return
	}
	j.mail.mu.Lock()
	j.cancelled.Store(true)
	j.finish()
	j.mail.mu.Unlock()
	p.mu.Lock()
	for i, queued := range p.jobs {
		if queued == j {
			p.jobs[i] = nil
			p.bytes -= len(j.src)
			j.src = nil
			break
		}
	}
	wake := p.refused
	p.refused = false
	p.mu.Unlock()
	if wake {
		platform.Wake()
	}
}

func (p *highlightPool) worker() {
	for {
		p.mu.Lock()
		var j *hlJob
		for j == nil {
			for n := range len(p.jobs) {
				i := (p.next + n) % len(p.jobs)
				if queued := p.jobs[i]; queued != nil {
					j, p.jobs[i] = queued, nil
					p.next = (i + 1) % len(p.jobs)
					break
				}
			}
			if j == nil {
				p.cond.Wait()
			}
		}
		p.mu.Unlock()
		var toks []highlight.Token
		if !j.cancelled.Load() {
			toks = highlight.Tokens(j.lang, j.src, nil)
		}
		p.complete(j, toks)
	}
}

func (p *highlightPool) complete(j *hlJob, toks []highlight.Token) {
	p.mu.Lock()
	p.bytes -= len(j.src)
	j.src = nil
	wake := p.refused
	p.refused = false
	p.mu.Unlock()
	if wake {
		platform.Wake()
	}
	m := j.mail
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.closed && !j.cancelled.Load() {
		n := cap(toks) * 12
		if m.bytes+n <= maxTokenBytes {
			j.e.toks, j.e.size = toks, n
			m.bytes += n
		}
		j.e.ready.Store(true)
		j.finish()
		if !m.notified {
			m.notified = true
			m.wake()
		}
	} else {
		j.finish()
	}
}

func (h *highlighter) init() {
	if h.byHash != nil {
		return
	}
	if h.pool == nil {
		h.pool = &codeWorkers
	}
	h.byHash = make(map[uint64]*hlEntry)
	h.byBlock = make(map[ui.Key]hlAlias)
	h.seed = maphash.MakeSeed()
	h.mail = &hlMailbox{wake: platform.Wake}
}

func (h *highlighter) drop(e *hlEntry) {
	h.pool.cancel(e.job)
	h.mail.mu.Lock()
	h.mail.bytes -= e.size
	e.size, e.toks = 0, nil
	h.mail.mu.Unlock()
	for k, a := range h.byBlock {
		if a.last == e {
			a.last = nil
		}
		if a.want == e {
			a.want = nil
		}
		h.byBlock[k] = a
	}
}

func (h *highlighter) removeBlock(key ui.Key) {
	a := h.byBlock[key]
	delete(h.byBlock, key)
	if e := a.want; e != nil {
		e.refs--
		if e.refs == 0 && !e.ready.Load() {
			h.drop(e)
			for hash, entry := range h.byHash {
				if entry == e {
					delete(h.byHash, hash)
					break
				}
			}
		}
	}
}

// forget ages aliases by their own use, independently of shared content.
func (h *highlighter) forget(frame uint32) {
	for k, a := range h.byBlock {
		if frame-a.seen > 120 {
			h.removeBlock(k)
		}
	}
	for k, e := range h.byHash {
		if frame-e.seen > 120 {
			h.drop(e)
			delete(h.byHash, k)
		}
	}
}

func (h *highlighter) tokens(c *ui.Context, key ui.Key, lang highlight.Lang, src []byte) []highlight.Token {
	if h.closed {
		return nil
	}
	if lang == highlight.Plain || len(src) == 0 || len(src) > highlight.MaxSource {
		h.removeBlock(key)
		return nil
	}
	h.init()
	frame := c.Frame()
	if !h.ack || h.frame != frame {
		h.frame, h.ack = frame, true
		h.mail.mu.Lock()
		h.mail.notified = false
		h.mail.mu.Unlock()
	}
	a, exists := h.byBlock[key]
	if !exists && len(h.byBlock) >= maxEntries {
		h.forget(frame)
		if len(h.byBlock) >= maxEntries {
			var oldest ui.Key
			age := uint32(0)
			for k, b := range h.byBlock {
				if d := frame - b.seen; d >= age {
					oldest, age = k, d
				}
			}
			h.removeBlock(oldest)
		}
	}
	hash := maphash.Bytes(h.seed, src) ^ uint64(lang)*0x9E3779B97F4A7C15
	e := h.byHash[hash]
	if a.want != e || e == nil {
		if old := a.want; old != nil {
			old.refs--
			if old.refs == 0 && !old.ready.Load() {
				h.drop(old)
				for hk, entry := range h.byHash {
					if entry == old {
						delete(h.byHash, hk)
						break
					}
				}
			}
		}
		a.want = nil
		if e == nil {
			if len(h.byHash) >= maxEntries {
				h.forget(frame)
				if len(h.byHash) >= maxEntries {
					var oldest uint64
					age := uint32(0)
					for hk, entry := range h.byHash {
						if d := frame - entry.seen; d >= age {
							oldest, age = hk, d
						}
					}
					h.drop(h.byHash[oldest])
					delete(h.byHash, oldest)
				}
			}
			// Failed admission has no entry or source allocation.
			e = h.pool.submit(h.mail, lang, src)
			if e != nil {
				e.seen = frame
				h.byHash[hash] = e
			}
		}
		if e != nil {
			a.want = e
			e.refs++
		}
	}
	a.seen = frame
	if e != nil {
		e.seen = frame
		if e.ready.Load() {
			a.last = e
		}
	}
	h.byBlock[key] = a
	if a.last != nil {
		return clipTokens(a.last.toks, uint32(len(src)))
	}
	return nil
}

func (h *highlighter) close() {
	if h.closed {
		return
	}
	h.closed = true
	if h.mail != nil {
		h.mail.mu.Lock()
		h.mail.closed = true
		h.mail.mu.Unlock()
		for _, e := range h.byHash {
			h.drop(e)
		}
	}
	h.byHash, h.byBlock = nil, nil
}

// clipTokens is toks without what lies past n bytes.
func clipTokens(toks []highlight.Token, n uint32) []highlight.Token {
	for len(toks) > 0 && toks[len(toks)-1].Off+toks[len(toks)-1].Len > n {
		toks = toks[:len(toks)-1]
	}
	return toks
}
