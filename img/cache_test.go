package img

import (
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

func TestAtlasSmallFirstPageAndStableRegions(t *testing.T) {
	c, g := newTestCache(t, Options{})
	keys, data := thumbs(t, 120, 48, 48)
	hs := make([]Handle, len(keys))
	view := func(n int) {
		for i := 0; i < n; i++ {
			hs[i], _ = c.Request(keys[i], Bytes(data[i]))
		}
	}
	settle(t, c, func() { view(10) })
	if n, used := g.live(); n != 1 || used != 512*512*4 || c.Stats().GPUBytes != used {
		t.Fatalf("ten icons: textures=%d bytes=%d stats=%+v", n, used, c.Stats())
	}
	tex, uv, _ := c.Use(hs[0])
	settle(t, c, func() { view(len(hs)) })
	if n, used := g.live(); n != 2 || used != (512*512+1024*1024)*4 || c.Stats().GPUBytes != used {
		t.Fatalf("growth: textures=%d bytes=%d stats=%+v", n, used, c.Stats())
	}
	if gotTex, gotUV, _ := c.Use(hs[0]); gotTex != tex || gotUV != uv {
		t.Fatalf("live region relocated: %v %v -> %v %v", tex, uv, gotTex, gotUV)
	}
	for _, h := range hs {
		c.evictGPU(h.index())
	}
	if n, used := g.live(); n != 0 || used != 0 || c.Stats().GPUBytes != 0 {
		t.Fatalf("evicted pages: textures=%d bytes=%d stats=%+v", n, used, c.Stats())
	}
}

func TestSmallAtlasPageBounds(t *testing.T) {
	p := page{dim: 512}
	for n := 0; n < 100; n++ {
		x, y, _, ok := p.alloc(250, 250)
		if !ok {
			if n != 4 {
				t.Fatalf("packed %d regions, want 4", n)
			}
			return
		}
		if x < 0 || y < 0 || x+250 > 512 || y+250 > 512 {
			t.Fatalf("region outside small texture: %d,%d", x, y)
		}
	}
	t.Fatal("page never filled")
}

func TestSmallFirstPagePreservesBudgetCapacity(t *testing.T) {
	const budget = 16 << 20
	c, g := newTestCache(t, Options{GPUBytes: budget})
	// Four full-size pages fit 672 of these padded thumbnails. Starting
	// small must not strand the budget's final 3 MiB and churn this set.
	for n := 0; n < 600; n++ {
		if _, _, _, _, ok := c.atlasAlloc(-1, 66, 66); !ok {
			t.Fatalf("thumbnail %d did not fit", n)
		}
	}
	if _, used := g.live(); used > budget || c.Stats().OverBudget != 0 {
		t.Fatalf("600 thumbnails: %d bytes, stats=%+v", used, c.Stats())
	}
}

// thumbs makes n distinct w×h PNGs (inline sources) and their keys at that
// size.
func thumbs(t testing.TB, n, w, h int) ([]Key, [][]byte) {
	keys := make([]Key, n)
	data := make([][]byte, n)
	base := gradient(w, h)
	for i := range keys {
		base.Pix[0] = byte(i) // distinct content
		base.Pix[1] = byte(i >> 8)
		data[i] = encPNG(t, base)
		keys[i] = Key{Hash: HashBytes(data[i]), W: int32(w), H: int32(h)}
	}
	return keys, data
}

// TestCacheHitsAndGenerations: the same key gives the same handle; another
// size is another image; an evicted image's handle stops resolving and the
// key gets a new handle.
func TestCacheHitsAndGenerations(t *testing.T) {
	c, _ := newTestCache(t, Options{CPUBytes: 1})
	keys, data := thumbs(t, 2, 32, 32)
	h0, _ := c.Request(keys[0], Bytes(data[0]))
	if again, _ := c.Request(keys[0], Bytes(data[0])); again != h0 {
		t.Fatalf("same key: %#x then %#x", h0, again)
	}
	small := keys[0]
	small.W, small.H = 16, 16
	h0s, _ := c.Request(small, Bytes(data[0]))
	if h0s == h0 {
		t.Fatal("another decode size shares the handle")
	}
	settle(t, c, func() { c.Request(keys[0], Bytes(data[0])); c.Request(small, Bytes(data[0])) })
	if c.State(h0) != Ready || c.State(h0s) != Ready {
		t.Fatalf("states %v %v", c.State(h0), c.State(h0s))
	}
	if info, _ := c.Info(h0s); info.W != 16 {
		t.Fatalf("16 px decode is %d wide", info.W)
	}

	// Two frames without use, then the image's GPU memory is wanted: it goes
	// (an uploaded still keeps no pixels, so the entry goes with it).
	c.Poll()
	c.Poll()
	c.evictGPU(h0.index())
	if c.State(h0) != Pending {
		t.Fatal("an evicted handle still resolves")
	}
	if _, _, st := c.Use(h0); st != Pending {
		t.Fatal("Use of an evicted handle")
	}
	h0b, _ := c.Request(keys[0], Bytes(data[0]))
	if h0b == h0 || h0b == 0 {
		t.Fatalf("re-request after eviction: %#x (was %#x)", h0b, h0)
	}
	if h0b.index() == h0.index() && h0b.gen() == h0.gen() {
		t.Fatal("same generation reused")
	}
}

// TestEvictionUnderBudget: images not in view are evicted, least recently
// used first, to keep the GPU within its budget.
func TestEvictionUnderBudget(t *testing.T) {
	// Own textures (300×300 > AtlasMax): 360,000 bytes each; room for 4.
	const each = 300 * 300 * 4
	c, g := newTestCache(t, Options{GPUBytes: 4*each + 1000, CPUBytes: 1})
	keys, data := thumbs(t, 10, 300, 300)
	var hs [10]Handle
	for i := range keys { // one image in view per frame, the earlier ones scrolled away
		for {
			c.Poll()
			hs[i], _ = c.Request(keys[i], Bytes(data[i]))
			if c.State(hs[i]) == Ready {
				break
			}
			time.Sleep(time.Millisecond)
		}
		if _, used := g.live(); used > 4*each+1000 {
			t.Fatalf("after image %d: %d bytes of textures, budget %d", i, used, 4*each+1000)
		}
	}
	for i := range hs {
		want := i >= 6 // the four most recent stay
		if got := c.State(hs[i]) == Ready; got != want {
			t.Errorf("image %d resident %v, want %v", i, got, want)
		}
	}
	if s := c.Stats(); s.EvictedGPU != 6 || s.OverBudget != 0 {
		t.Errorf("stats %+v: want 6 GPU evictions, none over budget", s)
	}
}

// TestInViewNeverEvicted: images requested every frame stay even when they
// overflow the budget (the budget gives, and says so).
func TestInViewNeverEvicted(t *testing.T) {
	const each = 300 * 300 * 4
	c, g := newTestCache(t, Options{GPUBytes: 2 * each})
	keys, data := thumbs(t, 5, 300, 300)
	var hs [5]Handle
	view := func() {
		for i := range keys {
			hs[i], _ = c.Request(keys[i], Bytes(data[i]))
		}
	}
	settle(t, c, view)
	for i, h := range hs {
		if c.State(h) != Ready {
			t.Errorf("image %d in view is %v", i, c.State(h))
		}
	}
	if n, _ := g.live(); n != 5 || g.frees != 0 {
		t.Errorf("%d live textures, %d freed; want 5, 0", n, g.frees)
	}
	if c.Stats().OverBudget == 0 {
		t.Error("over budget not reported")
	}
	// Out of view for two frames: the next upload reclaims the budget.
	c.Poll()
	c.Poll()
	more, _ := thumbs(t, 6, 300, 300)
	extra := more[5]
	extra.Hash ^= 1
	settle(t, c, func() { c.Request(extra, Bytes(data[0])) })
	if _, used := g.live(); used > 2*each {
		t.Errorf("%d bytes resident after the view moved on, budget %d", used, 2*each)
	}
}

// TestUploadBudgetPerFrame: a burst of finished decodes is uploaded a few
// per frame.
func TestUploadBudgetPerFrame(t *testing.T) {
	const each = 66 * 66 * 4 // 64×64 plus the atlas padding
	var decoded atomic.Int32
	c, g := newTestCache(t, Options{UploadBytes: 3 * each, Wake: func() { decoded.Add(1) }})
	keys, data := thumbs(t, 10, 64, 64)
	view := func() {
		for i := range keys {
			c.Request(keys[i], Bytes(data[i]))
		}
	}
	view()
	deadline := time.Now().Add(10 * time.Second)
	for decoded.Load() < 10 && time.Now().Before(deadline) { // let every decode finish first
		time.Sleep(time.Millisecond)
	}
	var perFrame []int
	for f := 0; f < 10 && g.uploads < 10; f++ {
		before := g.uploads
		more := c.Poll()
		view()
		perFrame = append(perFrame, g.uploads-before)
		if want := g.uploads < 10; more != want {
			t.Errorf("frame %d: Poll reported more=%v with %d of 10 uploaded", f, more, g.uploads)
		}
	}
	if len(perFrame) != 4 || perFrame[0] != 3 || perFrame[3] != 1 {
		t.Errorf("uploads per frame %v, want [3 3 3 1]", perFrame)
	}
	if g.news != 1 {
		t.Errorf("%d textures created, want 1 atlas page", g.news)
	}
}

// TestAtlasReuse: atlas regions freed by evictions are reused and an empty
// page is freed.
func TestAtlasReuse(t *testing.T) {
	c, g := newTestCache(t, Options{GPUBytes: 512 * 512 * 4, CPUBytes: 1})
	keys, data := thumbs(t, 300, 100, 100) // one small page: repeatedly reuse its regions
	for i := range keys {
		for {
			c.Poll()
			h, _ := c.Request(keys[i], Bytes(data[i]))
			if c.State(h) == Ready {
				break
			}
			time.Sleep(100 * time.Microsecond)
		}
	}
	if s := c.Stats(); s.Pages != 1 || s.EvictedGPU < 200 || s.OverBudget != 0 {
		t.Errorf("stats %+v: want one page reused, over 200 evictions", s)
	}
	if g.news != 1 {
		t.Errorf("%d textures created, want the one page", g.news)
	}
}

// TestRequestPollUseZeroAlloc: a frame of 200 cached images (Poll, then
// Request and Use of each) allocates nothing.
func TestRequestPollUseZeroAlloc(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	keys, data := thumbs(t, 200, 48, 48)
	srcs := make([]Source, len(keys))
	for i := range srcs {
		srcs[i] = Bytes(data[i])
	}
	var sink float32
	frame := func() {
		c.Poll()
		for i := range keys {
			h, _ := c.Request(keys[i], srcs[i])
			_, uv, _ := c.Use(h)
			sink += uv[2]
		}
	}
	settle(t, c, func() {
		for i := range keys {
			c.Request(keys[i], srcs[i])
		}
	})
	for i := 0; i < 10; i++ {
		frame()
	}
	if s := c.Stats(); s.Ready != 200 {
		t.Fatalf("%d ready", s.Ready)
	}
	var mallocs, bytes uint64
	for a := 0; a < 5; a++ {
		runtime.GC()
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for i := 0; i < 500; i++ {
			frame()
		}
		runtime.ReadMemStats(&m1)
		mallocs, bytes = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
		if mallocs == 0 && bytes == 0 {
			return
		}
	}
	t.Errorf("500 frames of 200 cached images: %d mallocs, %d bytes (sink %v)", mallocs, bytes, sink)
}

// The CPU budget is charged what a decode's buffer holds, which can be up to
// twice its pixels when a larger free buffer is reused.
func TestCPUBytesChargeTheBuffersCapacity(t *testing.T) {
	c, _ := newTestCache(t, Options{CPUBytes: 64 << 20})
	big := c.bufs.get(66*66*4, c.fr)
	held := c.bufs.size(big)
	c.bufs.put(big, c.fr)
	small, data := thumbs(t, 1, 56, 56)
	h, _ := c.Request(small[0], Bytes(data[0]))
	deadline := time.Now().Add(5 * time.Second)
	for c.State(h) == Pending && c.ents[h.index()].stage == stDecoding && time.Now().Before(deadline) {
		c.results = c.pool.Poll(c.results[:0])
		for k := range c.results {
			c.finish(&c.results[k])
		}
		runtime.Gosched()
	}
	e := &c.ents[h.index()]
	if e.stage != stCPU || e.buf < 0 {
		t.Fatalf("decode did not finish: stage %d", e.stage)
	}
	if got := c.bufs.size(e.buf); got != held || c.Stats().CPUBytes != held {
		t.Fatalf("buffer %d and CPUBytes %d, want reused capacity %d", got, c.Stats().CPUBytes, held)
	}
	c.Poll()
	if c.State(h) != Ready || e.buf != -1 || c.Stats().CPUBytes != 0 {
		t.Fatalf("uploaded still retained pixels: stage %d, buffer %d, CPUBytes %d", e.stage, e.buf, c.Stats().CPUBytes)
	}
}

// Free buffers kept for reuse are a share of the CPU budget and count
// against it.
func TestSpareBuffersCountAgainstTheCPUBudget(t *testing.T) {
	const budget = 1 << 20
	c, _ := newTestCache(t, Options{CPUBytes: budget})
	if c.bufs.maxFree != budget/4 {
		t.Fatalf("a %d-byte budget keeps %d spare bytes, want a quarter", budget, c.bufs.maxFree)
	}
	b := c.bufs.get(200<<10, c.fr)
	c.bufs.put(b, c.fr)
	c.cpuUsed = 900 << 10 // pixels held: the budget has 124 KiB left
	c.trimCPU()
	s := c.Stats()
	c.cpuUsed = 0
	if s.CPUBytes+s.SpareBytes > budget {
		t.Fatalf("pixels %d and spare buffers %d exceed the budget of %d", s.CPUBytes, s.SpareBytes, budget)
	}
}

// epochGPU is a fakeGPU that counts rebuilds, as gpu.Renderer does.
type epochGPU struct {
	*fakeGPU
	epoch uint32
}

func (g *epochGPU) Epoch() uint32 { return g.epoch }

// TestGPULostUploadsAgain: after the renderer was rebuilt (its Epoch moved)
// still-image handles no longer resolve and Request decodes them again.
func TestGPULostUploadsAgain(t *testing.T) {
	g := &epochGPU{fakeGPU: newFakeGPU()}
	c, _ := newTestCache(t, Options{Renderer: g, CPUBytes: 1 << 20})
	keys, data := thumbs(t, 3, 32, 32)
	var hs [3]Handle
	view := func() {
		for i := range keys {
			hs[i], _ = c.Request(keys[i], Bytes(data[i]))
		}
	}
	settle(t, c, view)
	for i, h := range hs {
		if c.State(h) != Ready {
			t.Fatalf("image %d is %v before the loss", i, c.State(h))
		}
	}
	old := hs
	uploads, decodes := g.uploads, c.Stats().Decodes
	for _, h := range hs {
		if c.ents[h.index()].buf >= 0 {
			t.Fatal("uploaded still retained its pixels")
		}
	}
	g.epoch++
	c.Poll()
	if g.uploads != uploads {
		t.Error("a still was re-uploaded without decoding")
	}
	for _, h := range old {
		if c.State(h) != Pending {
			t.Error("a lost still's handle still resolves")
		}
	}
	settle(t, c, view)
	for i, h := range hs {
		if h == old[i] || c.State(h) != Ready {
			t.Errorf("image %d: handle %#x (old %#x), state %v", i, h, old[i], c.State(h))
		}
	}
	if c.Stats().Decodes != decodes+3 || g.uploads != uploads+3 {
		t.Errorf("recovery decoded %d and uploaded %d, want 3 each", c.Stats().Decodes-decodes, g.uploads-uploads)
	}
	if n, _ := g.live(); n == 0 {
		t.Error("no texture live after the images were uploaded again")
	}
	// A cache that sees no further rebuild does not reset again.
	frees := g.frees
	c.Poll()
	if g.frees != frees {
		t.Error("a Poll without a new epoch took images off the GPU")
	}
}

// addResident builds a cache entry without involving file decoding.
func addResident(t testing.TB, c *Cache, w, h int32) Handle {
	t.Helper()
	i := c.alloc()
	if i < 0 {
		t.Fatal("no entry")
	}
	e := &c.ents[i]
	e.key = Key{Hash: uint64(i) + 1, W: w, H: h}
	e.w, e.h, e.pad = w, h, atlasPad(w, h)
	e.buf = c.bufs.get(int(e.cpuBytes()), c.fr)
	if e.buf < 0 {
		t.Fatal("no pixel buffer")
	}
	c.cpuUsed += c.bufs.size(e.buf)
	e.stage = stCPU
	c.insert(i)
	c.pushFront(i)
	if !c.upload(i) {
		t.Fatal("no upload space")
	}
	return makeHandle(i, e.gen)
}

func TestAtlasEvictionChoosesACompatiblePage(t *testing.T) {
	c, _ := newTestCache(t, Options{})
	c.frame = 10
	var short [8]Handle
	for i := range short {
		short[i] = addResident(t, c, 254, 126)
	}
	for i := 0; i < 16; i++ {
		addResident(t, c, 254, 254)
	}
	if len(c.pages) != 2 {
		t.Fatalf("pages %d, want 2", len(c.pages))
	}
	// Protect one image in each 128-pixel shelf. This page cannot supply
	// a 256-pixel-high region, even after every cold image is removed.
	for i := 1; i < len(short); i += 2 {
		c.touch(short[i].index())
	}
	c.gpuBudget = c.gpuUsed
	before := c.Stats().EvictedGPU
	addResident(t, c, 254, 254)
	for _, h := range short {
		if c.State(h) != Ready {
			t.Fatal("evicted an image from an incompatible page")
		}
	}
	if n := c.Stats().EvictedGPU - before; n != 1 {
		t.Fatalf("evicted %d images, want one compatible region", n)
	}
	// A byte-budget eviction cannot free the partly protected page either.
	c.makeRoom(-1, 360000)
	for _, h := range short {
		if c.State(h) != Ready {
			t.Fatal("evicted a cold image from a protected page for no GPU bytes")
		}
	}
}

func TestCacheScratchMatchesWorkerPools(t *testing.T) {
	old := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(old)
	for _, workers := range []int{0, 3, 64} {
		c, _ := newTestCache(t, Options{Workers: workers, Entries: 1})
		want := workers
		if want == 0 {
			want = 2
		}
		if got := cap(c.workers); got != want+animWorkers {
			t.Fatalf("Workers %d: scratches %d, want %d", workers, got, want+animWorkers)
		}
		c.Close(time.Second)
	}
}
