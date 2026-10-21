package img

import (
	"sync"
	"sync/atomic"
	"time"

	"gonativeui/core"
	"gonativeui/gpu"
	"gonativeui/platform"
	"gonativeui/task"
)

// Stages of an entry.
const (
	stFree     uint8 = iota // the record is unused
	stDecoding              // a decode job is out
	stCPU                   // decoded: pixels in memory, waiting for (or between) uploads
	stGPU                   // on the GPU (pixels maybe still in memory too)
	stFailed                // could not be decoded
)

// entry is one image (pointer-free: the table holds thousands).
type entry struct {
	key   Key
	gen   uint32
	stage uint8
	err   uint8
	queue bool // in the upload queue

	w, h, pad    int32 // image size; replicated border in its buffer and atlas region
	frames, dlay int32

	buf int32 // decoded pixels in memory, -1 if none

	tex    gpu.TextureID // its texture: an atlas page's or its own
	page   int32         // atlas page index, -1 for its own texture
	shelf  int32
	ax, ay int32 // origin of the (padded) region in the page

	lastUse    uint64 // frame of the last Request or Use
	prev, next int32  // LRU list, most recent first
}

func (e *entry) cpuBytes() int64 { return int64(e.w+2*e.pad) * int64(e.h+2*e.pad) * 4 }

// Cache decodes, uploads and evicts images; see the package comment.
type Cache struct {
	r                              Renderer
	gpuBudget, cpuBudget, upBudget int64
	pool                           *task.Pool[job, result]
	results                        []task.Result[result]
	apool                          *task.Pool[job, result] // the frames of animations: it wakes nobody (a frame is looked for at its deadline)
	aresults                       []task.Result[result]
	anims                          []*anim // by entry: an animated image's state
	animOut                        []*anim // by entry: the animation a finished first decode built, until Poll takes it
	animList                       []int32 // the entries that are animations
	ringCap                        int     // decoded frames an animation holds waiting, besides the one showing
	autoFn                         func() bool
	autoVal, autoKnown             bool
	wake                           func() // wakes the UI loop (Options.Wake)
	watching                       bool   // registered for the autoplay setting's change notification
	wakeMu                         sync.Mutex
	wakeShut                       bool // Close ran: wakeApp does nothing
	bufs                           *bufPool
	fr                             *cframe // the owner's C call frame (C memory on macOS)
	wmu                            core.Mutex
	workers                        []*worker
	leases                         []Lease         // by entry: a lease held for its decode
	jobs                           []atomic.Uint32 // by entry: who owns its decode's resources (jobIdle...)
	jobCopy, jobOut                []int32         // by entry: its decode's inline copy and output buffer, -1 if none
	refs                           atomic.Int32    // after Close: Close and the decodes it left running; the last drains the buffers
	ents                           []entry
	slots                          []int32 // open-addressing key table: entry index + 1, 0 = empty
	free                           []int32
	head, tail                     int32 // LRU list
	upq                            []int32
	upHead, upN                    int
	pages                          []*page
	frame                          uint64
	gpuUsed, cpuUsed               int64
	st                             Stats
	closed                         bool
}

// NewCache starts a cache and its decoding workers. Setup allocates: the
// tables are sized by Options.Entries and never grow.
func NewCache(o Options) *Cache {
	if o.Renderer == nil {
		panic("img: Options.Renderer is nil")
	}
	if o.GPUBytes <= 0 {
		o.GPUBytes = 128 << 20
	}
	if o.CPUBytes <= 0 {
		o.CPUBytes = 32 << 20
	}
	if o.UploadBytes <= 0 {
		o.UploadBytes = 4 << 20
	}
	if o.Entries <= 0 {
		o.Entries = 4096
	}
	if o.Wake == nil {
		o.Wake = platform.Wake
	}
	if o.AnimRing <= 0 {
		o.AnimRing = defaultAnimRing
	}
	o.AnimRing = max(2, min(o.AnimRing, maxRing))
	n := o.Entries
	queue := 64
	c := &Cache{
		r: o.Renderer, gpuBudget: o.GPUBytes, cpuBudget: o.CPUBytes, upBudget: o.UploadBytes,
		fr:      new(cframe),
		leases:  make([]Lease, n),
		jobs:    make([]atomic.Uint32, n),
		jobCopy: make([]int32, n),
		jobOut:  make([]int32, n),
		ents:    make([]entry, n),
		anims:   make([]*anim, n),
		animOut: make([]*anim, n),
		free:    make([]int32, n),
		upq:     make([]int32, n),
		head:    -1, tail: -1,
	}
	slots := 1
	for slots < 2*n {
		slots <<= 1
	}
	c.slots = make([]int32, slots)
	for i := range c.free {
		c.free[i] = int32(n - 1 - i)
		c.ents[i] = entry{gen: 1, buf: -1, page: -1, prev: -1, next: -1}
	}
	c.pool = task.NewPool(task.Options{Workers: o.Workers, Queue: queue, Wake: o.Wake}, c.decodeJob)
	c.apool = task.NewPool(task.Options{Workers: animWorkers, Queue: queue}, c.decodeJob)
	c.wake = o.Wake
	c.ringCap, c.autoFn = o.AnimRing-1, o.Autoplay
	if c.autoFn == nil {
		c.autoFn = func() bool { return systemAutoplay(c.fr) }
	}
	c.animList = make([]int32, 0, n)
	c.aresults = make([]task.Result[result], 0, queue)
	workers := o.Workers
	if workers <= 0 {
		workers = 4 // at least as many as task's default
	}
	c.workers = make([]*worker, 0, max(workers+animWorkers, 64)) // a worker's scratch for every goroutine of both pools
	for i := 0; i < cap(c.workers); i++ {
		c.workers = append(c.workers, new(worker))
	}
	c.results = make([]task.Result[result], 0, queue)
	// Buffer records: every entry's pixels, and per job a source copy and
	// its output, plus the free list.
	c.bufs = newBufPool(n+2*(queue+cap(c.workers))+maxFreeBufs+256*o.AnimRing, 16<<20)
	c.bufs.wake = c.wakeApp
	return c
}

// wakeApp calls Options.Wake from any goroutine, and never once Close has
// returned (a notification that was on its way when the cache closed finds
// nothing to wake).
func (c *Cache) wakeApp() {
	c.wakeMu.Lock()
	defer c.wakeMu.Unlock()
	if !c.wakeShut && c.wake != nil {
		c.wake()
	}
}

// Request returns the handle and state of the image key, and starts decoding
// it from src if the cache does not hold it. The same key returns the same
// handle while the image stays cached. It marks the image as in use this
// frame: the cache does not evict it before two Polls have passed without a
// Request or Use of it.
//
// A request the cache cannot take now (the decode queue full, every entry in
// use) returns the zero Handle and Pending; the next Request tries again.
// Request allocates nothing for an image already known; a new image's inline
// bytes are copied into a pooled buffer.
func (c *Cache) Request(key Key, src Source) (Handle, State) {
	if i := c.find(key); i >= 0 {
		e := &c.ents[i]
		c.touch(i)
		if src.kind == srcLease {
			src.lease.Release() // not needed: decoded or decoding
		}
		if e.stage == stCPU && !e.queue {
			c.enqueue(i) // evicted from the GPU: upload again
		}
		return makeHandle(i, e.gen), stateOf(e.stage)
	}
	if c.closed || src.kind == srcNone {
		if src.kind == srcLease {
			src.lease.Release()
		}
		return 0, Failed
	}
	i := c.alloc()
	if i < 0 {
		return c.refuse(src)
	}
	j := job{ent: i, src: src, copy: -1, w: key.W, h: key.H, flags: key.Flags}
	if src.kind == srcBytes {
		n := len(src.bytes)
		if n > 0 {
			if j.copy = c.bufs.get(n, c.fr); j.copy < 0 {
				c.release(i)
				return c.refuse(src)
			}
			copy(c.bufs.bytes(j.copy, n), src.bytes)
			j.copyN = int32(n)
		}
		j.src.bytes = nil // the worker reads the copy
	}
	e := &c.ents[i]
	c.jobCopy[i] = j.copy
	c.jobs[i].Store(jobQueued)
	if _, err := c.pool.Submit(j, taskKey(i, e.gen)); err != nil {
		c.jobs[i].Store(jobIdle)
		if j.copy >= 0 {
			c.bufs.put(j.copy, c.fr)
		}
		c.release(i)
		return c.refuse(src)
	}
	e.key, e.stage = key, stDecoding
	c.insert(i)
	c.pushFront(i)
	e.lastUse = c.frame
	if src.kind == srcLease {
		c.leases[i] = src.lease
	}
	return makeHandle(i, e.gen), Pending
}

func (c *Cache) refuse(src Source) (Handle, State) {
	if src.kind == srcLease {
		src.lease.Release()
	}
	c.st.Refused++
	return 0, Pending
}

// Get returns the handle and state of key without starting anything or
// marking it in use; ok is false when the cache does not hold it.
func (c *Cache) Get(key Key) (h Handle, st State, ok bool) {
	i := c.find(key)
	if i < 0 {
		return 0, Pending, false
	}
	e := &c.ents[i]
	return makeHandle(i, e.gen), stateOf(e.stage), true
}

// Use returns what draws image h: its texture and the texel rectangle (u0,
// v0, u1, v1) of the image in it, when it is Ready. It marks the image in
// use this frame, like Request. A handle that no longer resolves reports
// Pending (the image was evicted and its key must be requested again).
func (c *Cache) Use(h Handle) (tex gpu.TextureID, uv [4]float32, st State) {
	e := c.entry(h)
	if e == nil {
		return 0, uv, Pending
	}
	i := h.index()
	c.touch(i)
	if e.stage != stGPU {
		if e.stage == stCPU && !e.queue {
			c.enqueue(i)
		}
		return 0, uv, stateOf(e.stage)
	}
	x, y := float32(e.ax+e.pad), float32(e.ay+e.pad)
	return e.tex, [4]float32{x, y, x + float32(e.w), y + float32(e.h)}, Ready
}

// State reports the state of image h (Pending for a handle that no longer
// resolves), without marking it in use.
func (c *Cache) State(h Handle) State {
	if e := c.entry(h); e != nil {
		return stateOf(e.stage)
	}
	return Pending
}

// Err reports why image h failed (nil unless its state is Failed).
func (c *Cache) Err(h Handle) error {
	if e := c.entry(h); e != nil && e.stage == stFailed {
		return errOf(e.err)
	}
	return nil
}

// Info describes image h once it has been decoded; ok is false before.
func (c *Cache) Info(h Handle) (Info, bool) {
	e := c.entry(h)
	if e == nil || (e.stage != stCPU && e.stage != stGPU) {
		return Info{}, false
	}
	plays := 1
	if a := c.anims[h.index()]; a != nil {
		plays = int(a.plays)
	}
	return Info{W: int(e.w), H: int(e.h), Frames: int(e.frames), DelayMs: int(e.dlay), Plays: plays}, true
}

// Stats returns the cache's counters.
func (c *Cache) Stats() Stats {
	s := c.st
	s.GPUBytes, s.CPUBytes, s.Pages = c.gpuUsed, c.cpuUsed, 0
	for _, p := range c.pages {
		if p != nil {
			s.Pages++
		}
	}
	for i := range c.ents {
		switch c.ents[i].stage {
		case stFree:
			continue
		case stGPU:
			s.Ready++
		case stFailed:
			s.Failed++
		default:
			s.Pending++
		}
		s.Entries++
	}
	return s
}

func stateOf(stage uint8) State {
	switch stage {
	case stGPU:
		return Ready
	case stFailed:
		return Failed
	}
	return Pending
}

func (c *Cache) entry(h Handle) *entry {
	i := h.index()
	if h == 0 || i < 0 || int(i) >= len(c.ents) {
		return nil
	}
	e := &c.ents[i]
	if e.stage == stFree || e.gen != h.gen() {
		return nil
	}
	return e
}

func taskKey(i int32, gen uint32) uint64 { return uint64(gen)<<32 | uint64(uint32(i)) + 1 }

// Poll ends the previous frame for the cache and does this frame's work:
// it takes the finished decodes, uploads them within the per-frame byte
// budget (evicting what has not been used for two frames when the GPU budget
// is short), and trims decoded pixels to the CPU budget. Call it once per
// frame, before the frame's Requests; it reports whether uploads are left
// for the next frame (then ask for one: ui.Context.RequestFrame). It
// allocates nothing.
func (c *Cache) Poll() (more bool) {
	if c.closed {
		return false
	}
	c.frame++
	c.autoKnown = false
	c.results = c.pool.Poll(c.results[:0])
	for k := range c.results {
		c.finish(&c.results[k])
	}
	clear(c.results) // no stale copies of anything
	c.pollFrames()
	budget := c.upBudget
	for c.upN > 0 {
		i := c.upq[c.upHead]
		e := &c.ents[i]
		if !e.queue || e.stage != stCPU || !c.inUse(e) { // dropped, or out of view: it waits in memory
			c.dequeue()
			continue
		}
		n := e.cpuBytes()
		if n > budget && budget < c.upBudget {
			break // the first upload of a frame goes whatever its size
		}
		c.dequeue()
		if !c.upload(i) {
			c.enqueueBack(i) // no room: try again next frame
			break
		}
		budget -= n
	}
	c.trimCPU()
	return c.upN > 0
}

// finish takes one finished decode.
func (c *Cache) finish(r *task.Result[result]) {
	i := int32(uint32(r.Key - 1))
	gen := uint32((r.Key - 1) >> 32)
	e := &c.ents[i]
	out := &r.Out
	c.jobs[i].Store(jobIdle)
	c.st.Decodes++
	c.st.DecodeNs += out.ns
	a := c.animOut[i]
	c.animOut[i] = nil
	if l := c.leases[i]; l != nil {
		c.leases[i] = nil
		l.Release()
	}
	if e.stage != stDecoding || e.gen != gen { // dropped (Close)
		if out.buf >= 0 {
			c.bufs.put(out.buf, c.fr)
		}
		if a != nil {
			a.dec.close(c.fr)
		}
		return
	}
	if out.buf < 0 {
		e.stage, e.err = stFailed, out.err
		if a != nil {
			a.dec.close(c.fr)
		}
		return
	}
	e.stage, e.buf = stCPU, out.buf
	e.w, e.h, e.pad, e.frames, e.dlay = out.w, out.h, out.pad, out.frames, out.dlay
	c.cpuUsed += e.cpuBytes()
	if a != nil {
		c.startAnim(i, a)
	}
	c.enqueue(i)
}

// upload puts entry i on the GPU; false if there is no room yet.
func (c *Cache) upload(i int32) bool {
	e := &c.ents[i]
	bw, bh := e.w+2*e.pad, e.h+2*e.pad
	pix := c.bufs.bytes(e.buf, int(bw*bh*4))
	if e.w <= AtlasMax && e.h <= AtlasMax {
		p, x, y, sh, ok := c.atlasAlloc(i, bw, bh)
		if !ok {
			return false
		}
		e.page, e.shelf, e.ax, e.ay, e.tex = int32(p), int32(sh), x, y, c.pages[p].tex
		c.r.Stage(e.tex, int(x), int(y), int(bw), int(bh), pix, int(bw*4))
	} else {
		need := int64(bw) * int64(bh) * 4
		c.makeRoom(i, need)
		e.page, e.ax, e.ay = -1, 0, 0
		e.tex = c.r.NewTexture(gpu.TexRGBA8, int(bw), int(bh))
		c.r.Upload(e.tex, 0, 0, int(bw), int(bh), pix, int(bw*4))
		c.gpuUsed += need
	}
	e.stage = stGPU
	c.st.Uploads++
	c.st.UploadedBytes += int64(bw) * int64(bh) * 4
	return true
}

// atlasAlloc finds a bw×bh region in a page: an existing page first, then a
// new page within the budget, evicting images not in use to make room.
func (c *Cache) atlasAlloc(i, bw, bh int32) (p int, x, y int32, sh int, ok bool) {
	var dim int32
	for {
		dim = 512
		if bw > dim || bh > dim {
			dim = pageSize
		}
		for k, pg := range c.pages {
			if pg == nil {
				continue
			}
			dim = pageSize // add full-size pages once the small first page fills
			if x, y, sh, ok := pg.alloc(bw, bh); ok {
				return k, x, y, sh, true
			}
		}
		// Use the budget's tail rather than evicting a working set that
		// would fit: a small first page leaves a non-page-sized remainder.
		if c.gpuUsed+pageBytes > c.gpuBudget && bw <= 512 && heightClass(bh) <= 512 {
			dim = 512
		}
		if c.gpuUsed+int64(dim)*int64(dim)*4 <= c.gpuBudget {
			break
		}
		if !c.evictOne(i) {
			c.st.OverBudget++ // everything resident is in use: a page over budget
			break
		}
	}
	k := -1
	for j, pg := range c.pages {
		if pg == nil {
			k = j
			break
		}
	}
	pg := &page{dim: dim}
	pg.tex = c.r.NewTexture(gpu.TexRGBA8, int(dim), int(dim))
	if k < 0 {
		k = len(c.pages)
		c.pages = append(c.pages, pg)
	} else {
		c.pages[k] = pg
	}
	c.gpuUsed += int64(dim) * int64(dim) * 4
	x, y, sh, ok = pg.alloc(bw, bh)
	return k, x, y, sh, ok
}

// makeRoom evicts images not in use until need more bytes fit the GPU
// budget, or nothing more can go.
func (c *Cache) makeRoom(i int32, need int64) {
	for c.gpuUsed+need > c.gpuBudget {
		if !c.evictOne(i) {
			c.st.OverBudget++
			return
		}
	}
}

// inUse reports whether entry e was requested or drawn in this frame or the
// last: it is in view.
func (c *Cache) inUse(e *entry) bool { return e.lastUse+1 >= c.frame }

// evictOne frees the GPU memory of the least recently used image that is on
// the GPU and not in use (other than except); false if there is none.
func (c *Cache) evictOne(except int32) bool {
	for i := c.tail; i >= 0; i = c.ents[i].prev {
		e := &c.ents[i]
		if c.inUse(e) {
			return false // the list is in order of use: the rest is newer
		}
		if e.stage == stGPU && i != except {
			c.evictGPU(i)
			return true
		}
	}
	return false
}

// evictGPU takes entry i off the GPU. Its pixels stay if it has them in
// memory; otherwise the entry goes.
func (c *Cache) evictGPU(i int32) {
	e := &c.ents[i]
	if e.page >= 0 {
		pg := c.pages[e.page]
		pg.free(e.ax, e.w+2*e.pad, int(e.shelf))
		if pg.images == 0 {
			c.r.FreeTexture(pg.tex)
			c.pages[e.page] = nil
			c.gpuUsed -= int64(pg.dim) * int64(pg.dim) * 4
		}
	} else {
		c.r.FreeTexture(e.tex)
		c.gpuUsed -= int64(e.w+2*e.pad) * int64(e.h+2*e.pad) * 4
	}
	e.tex, e.page = 0, -1
	c.st.EvictedGPU++
	if e.buf >= 0 {
		e.stage = stCPU
		return
	}
	c.remove(i)
}

// trimCPU drops the pixels kept in memory beyond the CPU budget, least
// recently used first: those of images already on the GPU, and images not
// in use that only have their pixels (they go).
func (c *Cache) trimCPU() {
	for i := c.tail; i >= 0 && c.cpuUsed > c.cpuBudget; {
		prev := c.ents[i].prev
		e := &c.ents[i]
		switch {
		case c.anims[i] != nil && !c.inUse(e) && !c.animBusy(i): // an animation that is not shown goes whole: its decoder, canvases and frames
			if e.stage == stGPU {
				c.evictGPU(i)
			}
			if c.ents[i].stage != stFree {
				c.remove(i)
			}
			c.st.EvictedCPU++
		case e.stage == stGPU && e.buf >= 0 && c.anims[i] == nil: // an animation in view keeps the frame it shows
			c.dropPixels(i)
			c.st.EvictedCPU++
		case e.stage == stCPU && !c.inUse(e) && !c.animBusy(i):
			c.remove(i)
			c.st.EvictedCPU++
		}
		i = prev
	}
}

func (c *Cache) dropPixels(i int32) {
	e := &c.ents[i]
	c.cpuUsed -= e.cpuBytes()
	c.bufs.put(e.buf, c.fr)
	e.buf = -1
}

// alloc takes a free entry, evicting the least recently used entry not in
// use (and not decoding) when the table is full; -1 if none can go.
func (c *Cache) alloc() int32 {
	if n := len(c.free); n > 0 {
		i := c.free[n-1]
		c.free = c.free[:n-1]
		return i
	}
	for i := c.tail; i >= 0; i = c.ents[i].prev {
		e := &c.ents[i]
		if c.inUse(e) {
			return -1
		}
		if e.stage == stDecoding || c.animBusy(i) {
			continue
		}
		if e.stage == stGPU {
			c.evictGPU(i)
		}
		if c.ents[i].stage != stFree {
			c.remove(i)
		}
		n := len(c.free)
		j := c.free[n-1]
		c.free = c.free[:n-1]
		return j
	}
	return -1
}

// release gives back an entry taken by alloc and never used.
func (c *Cache) release(i int32) { c.free = append(c.free, i) }

// remove drops entry i from the cache: its pixels, its key, its place in the
// LRU list. Its GPU memory must be gone already.
func (c *Cache) remove(i int32) {
	e := &c.ents[i]
	c.removeAnim(i)
	if e.buf >= 0 {
		c.dropPixels(i)
	}
	if e.queue {
		c.unqueue(i)
	}
	c.erase(i)
	c.unlink(i)
	gen := e.gen + 1
	if gen == 0 {
		gen = 1
	}
	*e = entry{gen: gen, buf: -1, page: -1}
	c.free = append(c.free, i)
}

// Close stops the workers (waiting up to wait for running decodes) and frees
// every texture, buffer and lease, on the caller's goroutine. A decode still
// running when the wait ends keeps its source (inline copy, lease) until its
// decoder returns, then frees it and its output on the worker; the last of
// them returns the pooled memory. The cache must not be used afterwards.
func (c *Cache) Close(wait time.Duration) {
	if c.closed {
		return
	}
	c.closed = true
	unwatchAutoplay(c)
	c.wakeMu.Lock()
	c.wakeShut = true
	c.wakeMu.Unlock()
	c.refs.Store(1) // Close's own, dropped at the end
	// Take every job that is not running; mark the running ones, to be
	// settled after the wait.
	for i := range c.jobs {
		st := &c.jobs[i]
		for {
			old := st.Load()
			if old == jobRunning {
				if st.CompareAndSwap(jobRunning, jobDraining) {
					break
				}
				continue
			}
			if st.CompareAndSwap(old, jobClosed) {
				c.closeJob(i, old)
				break
			}
		}
	}
	c.pool.Close(wait)
	c.apool.Close(wait)
	// A decode that finished within the wait is freed here; one still
	// running is its worker's to free.
	for i := range c.jobs {
		c.refs.Add(1) // before the swap: the worker may drop it at once
		old := c.jobs[i].Swap(jobClosed)
		if old == jobDraining {
			c.leases[i] = nil
			if a := c.anims[i]; a != nil {
				c.dropRing(a) // the frames waiting are the owner's: only the decoder goes to the running job
				c.anims[i] = nil
			}
			continue
		}
		c.refs.Add(-1)
		c.closeJob(i, old)
	}
	for i := range c.ents {
		e := &c.ents[i]
		if e.stage == stFree {
			continue
		}
		if a := c.anims[i]; a != nil {
			c.dropRing(a)
			a.dec.close(c.fr)
			c.anims[i] = nil
		}
		if e.stage == stGPU && e.page < 0 {
			c.r.FreeTexture(e.tex)
		}
		if e.buf >= 0 {
			c.bufs.put(e.buf, c.fr)
			e.buf = -1
		}
		e.stage = stFree
	}
	for k, pg := range c.pages {
		if pg != nil {
			c.r.FreeTexture(pg.tex)
			c.pages[k] = nil
		}
	}
	c.gpuUsed, c.cpuUsed = 0, 0
	c.animList = c.animList[:0]
	c.unref(c.fr)
}

// closeJob frees what Close took of entry i's job, found in state old: a
// queued job's copy, a finished one's output, and either's lease.
func (c *Cache) closeJob(i int, old uint32) {
	switch old {
	case jobQueued:
		if k := c.jobCopy[i]; k >= 0 {
			c.bufs.put(k, c.fr)
		}
	case jobDone:
		if k := c.jobOut[i]; k >= 0 {
			c.bufs.put(k, c.fr)
		}
		if a := c.animOut[i]; a != nil { // a first decode's animation nobody took
			c.animOut[i] = nil
			a.dec.close(c.fr)
		}
	case jobClosed:
		return
	}
	if l := c.leases[i]; l != nil {
		c.leases[i] = nil
		l.Release()
	}
}

// unref drops a reference to the pooled buffers after Close; the last one
// releases them.
func (c *Cache) unref(fr *cframe) {
	if c.refs.Add(-1) == 0 {
		c.bufs.drain(fr)
	}
}

// ---- the key table, the LRU list and the upload queue -------------------

func hashKey(k Key) uint64 {
	h := k.Hash ^ uint64(uint32(k.W))<<32 ^ uint64(uint32(k.H))<<8 ^ uint64(k.Flags)
	h ^= h >> 33
	h *= 0xff51afd7ed558ccd
	h ^= h >> 33
	h *= 0xc4ceb9fe1a85ec53
	h ^= h >> 33
	return h
}

func (c *Cache) find(k Key) int32 {
	mask := uint64(len(c.slots) - 1)
	for s := hashKey(k) & mask; ; s = (s + 1) & mask {
		v := c.slots[s]
		if v == 0 {
			return -1
		}
		if c.ents[v-1].key == k {
			return v - 1
		}
	}
}

func (c *Cache) insert(i int32) {
	mask := uint64(len(c.slots) - 1)
	s := hashKey(c.ents[i].key) & mask
	for c.slots[s] != 0 {
		s = (s + 1) & mask
	}
	c.slots[s] = i + 1
}

// erase removes entry i's key, shifting the following run back (linear
// probing needs no tombstones).
func (c *Cache) erase(i int32) {
	mask := uint64(len(c.slots) - 1)
	s := hashKey(c.ents[i].key) & mask
	for c.slots[s] != i+1 {
		if c.slots[s] == 0 {
			return
		}
		s = (s + 1) & mask
	}
	for {
		c.slots[s] = 0
		n := s
		for {
			n = (n + 1) & mask
			v := c.slots[n]
			if v == 0 {
				return
			}
			home := hashKey(c.ents[v-1].key) & mask
			// Move v back to s unless its home lies cyclically in (s, n].
			if (n > s && (home <= s || home > n)) || (n < s && home <= s && home > n) {
				c.slots[s] = v
				s = n
				break
			}
		}
	}
}

func (c *Cache) touch(i int32) {
	c.ents[i].lastUse = c.frame
	if c.head == i {
		return
	}
	c.unlink(i)
	c.pushFront(i)
}

func (c *Cache) pushFront(i int32) {
	e := &c.ents[i]
	e.prev, e.next = -1, c.head
	if c.head >= 0 {
		c.ents[c.head].prev = i
	}
	c.head = i
	if c.tail < 0 {
		c.tail = i
	}
}

func (c *Cache) unlink(i int32) {
	e := &c.ents[i]
	if e.prev >= 0 {
		c.ents[e.prev].next = e.next
	} else if c.head == i {
		c.head = e.next
	}
	if e.next >= 0 {
		c.ents[e.next].prev = e.prev
	} else if c.tail == i {
		c.tail = e.prev
	}
	e.prev, e.next = -1, -1
}

func (c *Cache) enqueue(i int32) {
	c.ents[i].queue = true
	c.upq[(c.upHead+c.upN)%len(c.upq)] = i
	c.upN++
}

// enqueueBack puts i back at the head of the queue (it stays first).
func (c *Cache) enqueueBack(i int32) {
	c.ents[i].queue = true
	c.upHead = (c.upHead - 1 + len(c.upq)) % len(c.upq)
	c.upq[c.upHead] = i
	c.upN++
}

// unqueue takes i out of the middle of the upload queue.
func (c *Cache) unqueue(i int32) {
	n := 0
	for k := 0; k < c.upN; k++ {
		if v := c.upq[(c.upHead+k)%len(c.upq)]; v != i {
			c.upq[(c.upHead+n)%len(c.upq)] = v
			n++
		}
	}
	c.upN = n
	c.ents[i].queue = false
}

func (c *Cache) dequeue() {
	c.ents[c.upq[c.upHead]].queue = false
	c.upHead = (c.upHead + 1) % len(c.upq)
	c.upN--
}
