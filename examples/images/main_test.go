package main

import (
	"runtime"
	"testing"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/ui"
	"gonativeui/widget/widgettest"
)

var cfg = app.Config{Title: "Images", Width: 360, Height: 300, Clear: [4]float32{1, 1, 1, 1}}

// settle plays frames until every picture is Ready, which takes the workers'
// decodes and the uploads of Poll.
func settle(t *testing.T, g *gallery, frame func()) {
	t.Helper()
	for i := 0; i < 1000 && (i < 4 || g.ready < tiles); i++ {
		frame()
		time.Sleep(time.Millisecond)
	}
	if g.ready != tiles {
		t.Fatalf("%d of %d pictures are ready", g.ready, tiles)
	}
}

// Every picture is decoded at the size it is shown, not at its own 400 px, and
// the window names each as an image.
func TestPicturesDecodeAtDisplaySize(t *testing.T) {
	g := newGallery()
	r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, g)
	t.Cleanup(func() { g.cache.Close(time.Second) })
	settle(t, g, r.Frame)

	h, st := g.cache.Request(img.Key{Hash: g.hashes[0], W: tilePt, H: tilePt, Flags: img.Cover}, img.Bytes(g.pics[0]))
	if st != img.Ready {
		t.Fatalf("the picture's state is %v", st)
	}
	if in, ok := g.cache.Info(h); !ok || in.W != tilePt || in.H != tilePt {
		t.Errorf("decoded %+v (ok %v), want %d×%d: the size shown, not the source's %d", in, ok, tilePt, tilePt, srcPx)
	}
	n := 0
	for _, node := range r.Tree().Nodes {
		if node.Role == ui.RoleImage {
			n++
		}
	}
	if n != tiles {
		t.Errorf("%d images in the semantic tree, want %d", n, tiles)
	}
}

// A picture the cache cannot decode is Failed, with its error, and draws as a
// skeleton; the other pictures are not affected.
func TestUndecodableBytesFail(t *testing.T) {
	g := newGallery()
	g.pics[3] = []byte("not an image")
	g.hashes[3] = img.HashBytes(g.pics[3])
	r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, g)
	t.Cleanup(func() { g.cache.Close(time.Second) })
	for i := 0; i < 1000 && g.ready < tiles-1; i++ {
		r.Frame()
		time.Sleep(time.Millisecond)
	}
	r.Frames(4)
	h, _ := g.cache.Request(img.Key{Hash: g.hashes[3], W: tilePt, H: tilePt, Flags: img.Cover}, img.Bytes(g.pics[3]))
	for i := 0; i < 500 && g.cache.State(h) == img.Pending; i++ {
		r.Frame()
		time.Sleep(time.Millisecond)
	}
	if g.cache.State(h) != img.Failed || g.cache.Err(h) == nil {
		t.Errorf("the bad picture is %v, error %v", g.cache.State(h), g.cache.Err(h))
	}
	if g.ready != tiles-1 {
		t.Errorf("%d pictures are ready, want %d", g.ready, tiles-1)
	}
}

func TestLayoutAndNames(t *testing.T) {
	g := newGallery()
	r := widgettest.Open(t, cfg, int(cfg.Width), int(cfg.Height), 1, g)
	t.Cleanup(func() { g.cache.Close(time.Second) })
	settle(t, g, r.Frame)
	r.Frames(2)
	r.AuditLayout("images")
}

// Once the pictures are cached a frame allocates nothing: Request is called
// for every picture in every frame.
func TestCachedFramesAllocateNothing(t *testing.T) {
	g := newGallery()
	o := widgettest.NewOffscreen(t, cfg, int(cfg.Width), int(cfg.Height), 1, g)
	t.Cleanup(func() { g.cache.Close(time.Second) })
	settle(t, g, func() { o.Frame() })
	for range 300 {
		o.Frame()
	}
	runtime.GC()
	for range 300 {
		o.Frame()
	}
	var best uint64
	for w := 0; w < 12; w++ {
		var m0, m1 runtime.MemStats
		runtime.ReadMemStats(&m0)
		for range 200 {
			o.Frame()
		}
		runtime.ReadMemStats(&m1)
		if m1.Mallocs == m0.Mallocs && m1.TotalAlloc == m0.TotalAlloc {
			return
		}
		if w == 0 || m1.Mallocs-m0.Mallocs < best {
			best = m1.Mallocs - m0.Mallocs
		}
	}
	t.Errorf("every one of 12 runs of 200 frames allocated (fewest: %d mallocs)", best)
}
