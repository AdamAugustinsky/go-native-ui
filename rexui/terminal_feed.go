package rexui

import (
	"errors"
	"io"
	"sync"
	"time"

	"gonativeui/ui"
)

// Output pacing for terminal views: the producer side (TerminalInbox), the
// parse loop the views share (TerminalScheduler), and the diagnostics
// (TerminalStats). See docs/design/terminal-output-pacing.md.
//
// Nothing here drops output. A TerminalInbox bounds the bytes queued for a
// terminal by blocking its writer; TerminalState.Feed, which runs on the UI
// thread and so cannot block, queues without a bound as it always has.

// Inbox defaults.
const (
	// DefaultInboxHighWater is the queued-but-unparsed bytes at which a
	// TerminalInbox's writer blocks: about two frames of parsing at the
	// default budget, so Ctrl+C in a flood shows within a few frames.
	DefaultInboxHighWater = 1 << 20
	// DefaultInboxLowWater is where a blocked writer resumes.
	DefaultInboxLowWater = 256 << 10
)

// ErrTerminalInboxClosed is what a TerminalInbox's Write and ReadFrom
// return after Close.
var ErrTerminalInboxClosed = errors.New("rexui: terminal inbox closed")

// TerminalInboxOpts configure a TerminalInbox.
type TerminalInboxOpts struct {
	// Wake is called from the writer's goroutine when bytes arrive for a
	// UI that may be idle (app.Win.Wake, platform.Wake). Calls are
	// coalesced: one per batch the UI takes. Without it the UI sees new
	// output only when something else draws a frame.
	Wake func()
	// HighWater is the queued-but-unparsed bytes at which Write blocks
	// (0: DefaultInboxHighWater); LowWater is where it resumes (0:
	// DefaultInboxLowWater, at most HighWater/2).
	HighWater, LowWater int
}

// TerminalInbox is a terminal's thread-safe producer side: a reader
// goroutine writes the program's output into it, and the view parses it
// on the UI thread. It is bounded: while HighWater bytes are queued but not
// parsed, Write blocks (and ReadFrom stops reading) until the view has
// parsed down to LowWater, so a local pty's kernel buffer fills and the
// program waits, as it would on a real terminal. Make it with
// TerminalState.OpenInbox.
//
//	in := s.OpenInbox(rexui.TerminalInboxOpts{Wake: win.Wake})
//	go func() { in.ReadFrom(ptyFile); in.Close() }()
type TerminalInbox struct {
	mu     sync.Mutex
	cond   sync.Cond
	buf    []byte // written, not yet taken by the UI
	queued int    // written, not yet parsed (buf and the UI's share of s.pending)
	high   int
	low    int
	closed bool
	wait   int // writers blocked
	wake   func()

	written int64
	hits    uint64
	blocked time.Duration
}

func newTerminalInbox(o TerminalInboxOpts) *TerminalInbox {
	in := &TerminalInbox{wake: o.Wake, high: o.HighWater, low: o.LowWater}
	if in.high <= 0 {
		in.high = DefaultInboxHighWater
	}
	if in.low <= 0 {
		in.low = DefaultInboxLowWater
	}
	in.low = min(in.low, in.high/2)
	in.cond.L = &in.mu
	return in
}

// Write queues p for the view, blocking while HighWater bytes are waiting
// to be parsed. It returns len(p) and nil, or after Close the bytes queued
// so far and ErrTerminalInboxClosed. Bytes queued before Close are still
// parsed. Safe for concurrent use; concurrent writers' bytes interleave at
// write boundaries.
func (in *TerminalInbox) Write(p []byte) (int, error) {
	in.mu.Lock()
	defer in.mu.Unlock()
	n := 0
	for len(p) > 0 {
		if in.closed {
			return n, ErrTerminalInboxClosed
		}
		if in.queued >= in.high {
			in.block()
			continue
		}
		k := min(in.high-in.queued, len(p))
		wake := len(in.buf) == 0
		in.buf = append(in.buf, p[:k]...)
		in.queued += k
		in.written += int64(k)
		n += k
		p = p[k:]
		if wake && in.wake != nil {
			in.mu.Unlock()
			in.wake()
			in.mu.Lock()
		}
	}
	return n, nil
}

// block waits, with in.mu held, until the view has parsed down to LowWater
// or the inbox closes.
func (in *TerminalInbox) block() {
	in.hits++
	t0 := time.Now()
	in.wait++
	for !in.closed && in.queued > in.low {
		in.cond.Wait()
	}
	in.wait--
	in.blocked += time.Since(t0)
}

// ReadFrom reads r into the inbox until EOF (nil) or an error, waiting for
// room before each read so a pty's or socket's own buffer holds the
// backlog. It does not close the inbox, and cannot interrupt a Read that
// is blocked in r: closing r is its owner's job.
func (in *TerminalInbox) ReadFrom(r io.Reader) (int64, error) {
	b := make([]byte, 32<<10)
	var total int64
	for {
		in.mu.Lock()
		if in.queued >= in.high {
			in.block()
		}
		closed := in.closed
		in.mu.Unlock()
		if closed {
			return total, ErrTerminalInboxClosed
		}
		n, err := r.Read(b)
		if n > 0 {
			w, werr := in.Write(b[:n])
			total += int64(w)
			if werr != nil {
				return total, werr
			}
		}
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}

// Close ends the inbox from either side: blocked and later writes return
// ErrTerminalInboxClosed. What was queued is still parsed. A reader
// goroutine closes it at EOF to say the program is done; TerminalState.Close
// closes it to cancel.
func (in *TerminalInbox) Close() error {
	in.mu.Lock()
	in.closed = true
	in.cond.Broadcast()
	wake := in.wake
	in.mu.Unlock()
	if wake != nil {
		wake()
	}
	return nil
}

// Closed reports whether Close was called. With TerminalState.Pending at 0
// it means the program's output has all been parsed.
func (in *TerminalInbox) Closed() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.closed
}

// take swaps the inbox's bytes into dst (emptied; its array becomes the
// inbox's next buffer) on the UI thread.
func (in *TerminalInbox) take(dst []byte) []byte {
	in.mu.Lock()
	dst, in.buf = in.buf, dst[:0]
	in.mu.Unlock()
	return dst
}

// parsed credits n parsed bytes and lets blocked writers go once the queue
// is down to LowWater.
func (in *TerminalInbox) parsed(n int) {
	in.mu.Lock()
	in.queued -= n
	if in.wait > 0 && in.queued <= in.low {
		in.cond.Broadcast()
	}
	in.mu.Unlock()
}

// TerminalStats are a terminal's output diagnostics (TerminalState.Stats).
type TerminalStats struct {
	// Queued is the bytes written or fed and not yet parsed; QueuedPeak its
	// highest value seen at the start of a frame's parse.
	Queued, QueuedPeak int
	// Fed is the bytes queued with Feed (unbounded) and Written those
	// through the inbox (bounded); Parsed is the bytes parsed.
	Fed, Written, Parsed int64
	// ParsedFrame and ParseTime are the bytes parsed, and the time it took,
	// in the last frame that parsed this terminal.
	ParsedFrame int
	ParseTime   time.Duration
	// HighWaterHits is how many times a writer found the inbox full, and
	// Blocked the total time writers waited for room.
	HighWaterHits uint64
	Blocked       time.Duration
	// FramesBehind is the number of frames in a row that ended with output
	// still queued (0: the last frame parsed everything).
	FramesBehind int
}

// Stats is the terminal's output diagnostics. Call it on the UI thread; it
// does not allocate.
func (s *TerminalState) Stats() TerminalStats {
	st := TerminalStats{
		Queued: s.Pending(), QueuedPeak: s.peak, Fed: s.fed, Parsed: s.parsedTotal,
		ParsedFrame: s.parsedFrame, ParseTime: s.parseTime, FramesBehind: s.behind,
	}
	if in := s.inbox; in != nil {
		in.mu.Lock()
		st.Written, st.HighWaterHits, st.Blocked = in.written, in.hits, in.blocked
		in.mu.Unlock()
	}
	return st
}

// OpenInbox makes the terminal's producer side (the first call; later calls
// return the same inbox and ignore o). Its bytes are parsed after those
// already fed; mixing Feed and the inbox is allowed, but their relative
// order is only that of what the UI had taken when Feed ran.
func (s *TerminalState) OpenInbox(o TerminalInboxOpts) *TerminalInbox {
	if s.inbox == nil {
		s.inbox = newTerminalInbox(o)
	}
	return s.inbox
}

// Inbox is the terminal's inbox (nil before OpenInbox).
func (s *TerminalState) Inbox() *TerminalInbox { return s.inbox }

// parseRate is a running estimate of the library's parse cost, used to size
// chunks so a slice of time is not overrun by more than a small chunk.
type parseRate struct{ nsPerByte float64 }

func (r *parseRate) bytesFor(d time.Duration) int {
	if r.nsPerByte <= 0 {
		r.nsPerByte = 4 // ~250 MB/s until measured
	}
	return int(float64(d) / r.nsPerByte)
}

func (r *parseRate) observe(n int, d time.Duration) {
	if n < 4<<10 {
		return // too short to time
	}
	v := float64(d) / float64(n)
	if r.nsPerByte <= 0 {
		r.nsPerByte = v
	}
	r.nsPerByte += (v - r.nsPerByte) / 4
}

const (
	feedChunk    = 64 << 10 // the largest write to the library at once
	feedMinChunk = 512      // the smallest, so a slice always progresses
)

// parse feeds queued output to the library until it is all parsed or d has
// passed, and returns the bytes parsed. Chunks are sized from rate so the
// last one ends near d rather than up to a 64 KiB chunk past it.
func (s *TerminalState) parse(d time.Duration, rate *parseRate) int {
	start := time.Now()
	total := 0
	for {
		if s.off == len(s.pending) {
			s.pending, s.off = s.pending[:0], 0
			if s.inbox == nil {
				break
			}
			s.pending = s.inbox.take(s.pending)
			s.fromInbox = len(s.pending)
			if len(s.pending) == 0 {
				break
			}
		}
		left := d - time.Since(start)
		if left <= 0 {
			break
		}
		n := min(len(s.pending)-s.off, feedChunk, max(feedMinChunk, rate.bytesFor(left)))
		t0 := time.Now()
		s.T.Write(s.pending[s.off : s.off+n])
		rate.observe(n, time.Since(t0))
		s.off += n
		total += n
		if k := min(n, s.fromInbox); k > 0 {
			s.fromInbox -= k
			s.inboxParsed += k
		}
	}
	if s.inboxParsed > 0 {
		s.inbox.parsed(s.inboxParsed)
		s.inboxParsed = 0
	}
	return total
}

// feedFrame is one frame's parse of this terminal for at most d, with the
// diagnostics kept; it reports whether output is still queued.
func (s *TerminalState) feedFrame(d time.Duration, rate *parseRate) bool {
	q := s.Pending()
	if q == 0 {
		s.behind = 0
		return false
	}
	s.peak = max(s.peak, q)
	t0 := time.Now()
	n := s.parse(d, rate)
	s.parseTime = time.Since(t0)
	s.parsedFrame = n
	s.parsedTotal += int64(n)
	if s.Pending() > 0 {
		s.behind++
		return true
	}
	s.behind = 0
	return false
}

// TerminalSchedulerOpts configure a TerminalScheduler.
type TerminalSchedulerOpts struct {
	// Budget is the time all the scheduler's terminals together may spend
	// parsing in one frame (0: 4 ms).
	Budget time.Duration
	// FocusShare is the part of Budget the focused terminal may take first
	// while others have output queued (0: 0.5); alone it takes it all.
	FocusShare float32
}

// TerminalScheduler shares one parse budget per frame among a window's
// terminals (TerminalOpts.Scheduler). Each frame the terminal with the
// keyboard focus (or TerminalOpts.Focus ≥ 0.5) parses first, for up to
// FocusShare of the budget; the others then share the rest in proportion,
// views built in the last frame twice as much as hidden ones, starting
// from a different terminal each frame so none waits for ever; time left
// goes round again in the same order. A busy pane in the background so
// cannot hold up the one being typed in, and a hidden one still drains.
//
// The parse runs once per frame, at the first Terminal view built with the
// scheduler, so the views drawn later in the frame show its result. In a
// frame that builds none of them (every pane hidden), call Run. Terminals
// join at their first view or with Add, and leave with Remove or their
// Close. Use one scheduler per window, on its UI thread.
type TerminalScheduler struct {
	budget time.Duration
	share  float32
	terms  []*TerminalState
	order  []*TerminalState // scratch: this frame's order
	next   int              // round-robin start
	rate   parseRate
	c      *ui.Context
	frame  uint32
	ran    bool

	last TerminalSchedulerStats
}

// TerminalSchedulerStats are a scheduler's last frame.
type TerminalSchedulerStats struct {
	// Budget is the frame's budget, Spent the time its terminals parsed,
	// Parsed the bytes.
	Budget, Spent time.Duration
	Parsed        int
	// Terminals is the number registered, Behind those left with output
	// queued.
	Terminals, Behind int
}

// NewTerminalScheduler makes a scheduler.
func NewTerminalScheduler(o TerminalSchedulerOpts) *TerminalScheduler {
	if o.Budget <= 0 {
		o.Budget = 4 * time.Millisecond
	}
	if o.FocusShare <= 0 || o.FocusShare > 1 {
		o.FocusShare = 0.5
	}
	return &TerminalScheduler{budget: o.Budget, share: o.FocusShare}
}

// Add registers s, so it is parsed even before its first view.
func (q *TerminalScheduler) Add(s *TerminalState) {
	if s.sched == q {
		return
	}
	if s.sched != nil {
		s.sched.Remove(s)
	}
	s.sched = q
	q.terms = append(q.terms, s)
}

// Remove unregisters s; its view then parses on its own (FeedBudget).
func (q *TerminalScheduler) Remove(s *TerminalState) {
	if s.sched != q {
		return
	}
	s.sched = nil
	for i, t := range q.terms {
		if t == s {
			q.terms = append(q.terms[:i], q.terms[i+1:]...)
			break
		}
	}
}

// Stats is the last frame's parse.
func (q *TerminalScheduler) Stats() TerminalSchedulerStats { return q.last }

// Run parses the registered terminals' output for this frame of c, once:
// later calls in the same frame do nothing. It asks for another frame while
// output remains.
func (q *TerminalScheduler) Run(c *ui.Context) {
	f := c.Frame()
	if q.ran && q.c == c && q.frame == f {
		return
	}
	q.c, q.frame, q.ran = c, f, true

	// Order: the focused terminal, then views of the last frame, then the
	// hidden ones, each group from the round-robin start.
	q.order = q.order[:0]
	var focused *TerminalState
	n := len(q.terms)
	for _, s := range q.terms {
		if s.T == nil || s.Pending() == 0 {
			s.behind = 0
		} else if focused == nil && s.wantFocus && s.weight(f) == 2 {
			focused = s
			q.order = append(q.order, s)
		}
	}
	for _, w := range [2]int{2, 1} {
		for i := range n {
			s := q.terms[(q.next+i)%n]
			if s != focused && s.T != nil && s.Pending() > 0 && s.weight(f) == w {
				q.order = append(q.order, s)
			}
		}
	}
	if n > 0 {
		q.next = (q.next + 1) % n
	}

	start := time.Now()
	parsed := 0
	others := len(q.order)
	if focused != nil {
		others--
	}
	// First pass: each its share.
	weights := 0
	for _, s := range q.order {
		if s != focused {
			weights += s.weight(f)
		}
	}
	for _, s := range q.order {
		left := q.budget - time.Since(start)
		if left <= 0 {
			break
		}
		var d time.Duration
		if s == focused {
			d = left
			if others > 0 {
				d = time.Duration(float32(q.budget) * q.share)
			}
		} else {
			w := s.weight(f)
			d = left * time.Duration(w) / time.Duration(weights)
			weights -= w
		}
		parsed += q.feed(s, min(d, left), f)
	}
	// Second pass: what is left, in the same order.
	for _, s := range q.order {
		left := q.budget - time.Since(start)
		if left <= 0 {
			break
		}
		if s.Pending() > 0 {
			parsed += q.feed(s, left, f)
		}
	}
	behind := 0
	for _, s := range q.order {
		if s.Pending() > 0 {
			s.behind++
			behind++
		} else {
			s.behind = 0
		}
	}
	q.last = TerminalSchedulerStats{Budget: q.budget, Spent: time.Since(start), Parsed: parsed, Terminals: n, Behind: behind}
	if behind > 0 {
		c.RequestFrame()
	}
}

// weight is a terminal's share of the time after the focused one's: a view
// drawn last frame counts twice a hidden one.
func (s *TerminalState) weight(f uint32) int {
	if s.seenOK && s.seen == f-1 {
		return 2
	}
	return 1
}

// feed is one slice of s's parse within a frame f; a terminal's
// FeedBudget caps its total in the frame.
func (q *TerminalScheduler) feed(s *TerminalState, d time.Duration, f uint32) int {
	if s.feedCap > 0 {
		if s.capFrame != f {
			s.capFrame, s.capUsed = f, 0
		}
		d = min(d, s.feedCap-s.capUsed)
		if d <= 0 {
			return 0
		}
	}
	if s.peakFrame != f {
		s.peakFrame = f
		s.peak = max(s.peak, s.Pending())
		s.parsedFrame, s.parseTime = 0, 0
	}
	t0 := time.Now()
	n := s.parse(d, &q.rate)
	el := time.Since(t0)
	s.capUsed += el
	s.parseTime += el
	s.parsedFrame += n
	s.parsedTotal += int64(n)
	return n
}
