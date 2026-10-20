package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"math"
	"time"

	"gonativeui/app"
	"gonativeui/img"
	"gonativeui/internal/animgen"
	"gonativeui/internal/sysinfo"
	"gonativeui/platform"
	"gonativeui/theme/rex"
	"gonativeui/ui"
	"gonativeui/widget/base"
)

// animHash is the first content hash of the animated samples.
const animHash = 1 << 40

// Geometry in points.
const (
	cellPt   = 56
	gapPt    = 4
	pitchPt  = cellPt + gapPt
	marginPt = 8
	radiusPt = 8
	sources  = 48 // distinct encoded photos; every item is its own image all the same
	srcW     = 640
	srcH     = 480
)

// grid is a scrolling grid of thumbnails, each item its own cache entry (its
// key's hash is the item number, as a chat's media hash would be), decoded
// from one of the encoded photos.
type grid struct {
	a     *app.App
	k     *base.Kit
	cache *img.Cache  // made by the first Build, when the scale is known
	opt   img.Options // its options; a GPU budget is for 1× and grows with the scale's square
	items int
	srcs  [][]byte

	scroll float32 // points from the top
	frame  int
	script func(g *grid, c *ui.Context)

	// What the last frame showed.
	visible, ready int

	// Measurement of the frames the script marks (poll and build wall time).
	counting               bool
	pollNs, pollCPU, build []float64
	coldFrames             int
	coldStart              int64
	coldEnd                int64
	blocking               bool // offscreen PNG: wait in Build until the view has loaded

	// anim puts the animated samples in the first cells (animSamples); off
	// in -bench and -idle, which measure the still grid.
	anim    bool
	samples []animSample
}

// An animated sample is an item of the grid whose image animates.
type animSample struct {
	name string
	data []byte
}

// animSamples are the animated cells, items 0 to 3: a GIF and an APNG of a
// disc travelling a transparent canvas (frames disposed to the background),
// and a GIF and an APNG that sweep a colour ramp slowly. All are made here.
func animSamples() []animSample {
	return []animSample{
		{"GIF disc", animgen.GIF(96, 96, animgen.Ball(96, 96, 24, 50, color.NRGBA{236, 72, 153, 255}), 0)},
		{"APNG disc", animgen.APNG(96, 96, animgen.Ball(96, 96, 24, 70, color.NRGBA{37, 150, 255, 255}), 0)},
		{"GIF ramp", animgen.GIF(96, 96, animgen.Sheet(96, 96, 12, 400), 0)},
		{"APNG ramp", animgen.APNG(96, 96, animgen.Sheet(96, 96, 12, 400), 0)},
	}
}

func newGrid(items int, o img.Options) *grid {
	g := &grid{items: items, opt: o,
		pollNs: make([]float64, 0, 1<<14), pollCPU: make([]float64, 0, 1<<14), build: make([]float64, 0, 1<<14)}
	g.samples = animSamples()
	g.srcs = make([][]byte, sources)
	for i := range g.srcs {
		g.srcs[i] = photo(i)
	}
	return g
}

// photo is encoded photo i: smooth colour fields and a few discs, like a
// photograph at thumbnail size, as a JPEG.
func photo(i int) []byte {
	m := image.NewRGBA(image.Rect(0, 0, srcW, srcH))
	h := float64(i) * 0.61803398875
	for y := 0; y < srcH; y++ {
		for x := 0; x < srcW; x++ {
			fx, fy := float64(x)/srcW, float64(y)/srcH
			r := 0.5 + 0.5*math.Sin(6.28*(h+fx*0.7))
			gg := 0.5 + 0.5*math.Sin(6.28*(h*1.7+fy*0.9))
			b := 0.5 + 0.5*math.Sin(6.28*(h*2.3+fx*fy))
			for d := 0; d < 3; d++ {
				cx, cy := 0.2+0.3*float64(d)+0.1*math.Sin(h+float64(d)), 0.3+0.4*math.Cos(h*3+float64(d))
				if (fx-cx)*(fx-cx)+(fy-cy)*(fy-cy)*0.56 < 0.01 {
					r, gg, b = 1-r, 1-gg, 1-b
				}
			}
			m.SetRGBA(x, y, color.RGBA{uint8(r * 255), uint8(gg * 255), uint8(b * 255), 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, m, &jpeg.Options{Quality: 85}); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// Init implements app.Initer.
func (g *grid) Init(a *app.App) error {
	g.a = a
	a.UI.Reserve(8192)
	g.k = base.NewKit(a.UI, a.Text, rex.Theme())
	return nil
}

// newCache makes the cache at scale s: the thumbnails' bytes, and so the
// budget that makes the cold pass evict, grow with its square.
func (g *grid) newCache(s float32) {
	o := g.opt
	o.Renderer = g.a.R
	o.GPUBytes = int64(float32(o.GPUBytes) * s * s)
	g.opt.GPUBytes = o.GPUBytes
	g.cache = img.NewCache(o)
	g.k.SetImages(g.cache)
}

// Script implements app.Scripter: the bench drives the grid from Build
// instead (g.script); input is only read in the interactive mode.
func (g *grid) Script(int, *ui.Input) bool { return true }

func (g *grid) cols(c *ui.Context) int {
	w, _ := c.Size()
	return max(1, int((float32(w)/c.Scale()-2*marginPt+gapPt)/pitchPt))
}

func (g *grid) rows(c *ui.Context) int { return (g.items + g.cols(c) - 1) / g.cols(c) }

// maxScroll is the scroll offset of the end of the grid, in points.
func (g *grid) maxScroll(c *ui.Context) float32 {
	_, h := c.Size()
	return max(0, float32(g.rows(c))*pitchPt+2*marginPt-float32(h)/c.Scale())
}

// key is item i's image at the cell's size in pixels.
func (g *grid) key(c *ui.Context, i int) img.Key {
	px := int32(math.Round(float64(cellPt * c.Scale())))
	return img.Key{Hash: uint64(i) + 1, W: px, H: px, Flags: img.Cover}
}

// Build implements app.Builder.
func (g *grid) Build(c *ui.Context) {
	g.frame++
	if g.cache == nil {
		g.newCache(c.Scale())
	}
	t0, c0 := platform.Now(), sysinfo.ThreadCPUNs()
	if g.cache.Poll() {
		c.RequestFrame()
	}
	t1, c1 := platform.Now(), sysinfo.ThreadCPUNs()
	if g.script != nil {
		g.script(g, c)
	} else {
		g.scroll -= c.Input().ScrollY / c.Scale()
	}
	g.scroll = min(max(g.scroll, 0), g.maxScroll(c))

	k := g.k
	k.Begin(c)
	c.NextSize(ui.Pct(1), ui.Pct(1))
	c.NextBG(k.Theme().Pal.Background)
	c.Open(ui.DrawBackground|ui.Clip, "grid")
	cols := g.cols(c)
	_, wh := c.Size()
	first := max(0, int((g.scroll-marginPt)/pitchPt))
	last := min(g.rows(c)-1, int((g.scroll+float32(wh)/c.Scale())/pitchPt))
	s := c.Scale()
	g.visible, g.ready = 0, 0
	for {
		g.visible, g.ready = 0, 0
		for r := first; r <= last; r++ {
			for col := 0; col < cols; col++ {
				i := r*cols + col
				if i >= g.items {
					break
				}
				key, src := g.key(c, i), img.Bytes(g.srcs[i%sources])
				sample := g.anim && i < len(g.samples)
				if sample {
					key.Hash, key.Flags = animHash+uint64(i), img.Animate
					src = img.Bytes(g.samples[i].data)
				}
				h, st := g.cache.Request(key, src)
				g.visible++
				if st == img.Ready {
					g.ready++
				}
				if g.blocking {
					continue
				}
				c.NextPos((marginPt+float32(col)*pitchPt)*s, (marginPt+float32(r)*pitchPt-g.scroll)*s)
				c.NextSize(ui.Px(cellPt*s), ui.Px(cellPt*s))
				flags := base.Floating
				if sample {
					flags |= ui.Clickable // a click pauses and plays
				}
				c.NextFlags(flags)
				id := k.Image(uint64(i), h, ui.FitFill, radiusPt*s, "")
				if sample && c.Signal(id).Clicked {
					if an := g.cache.Animation(h); an.Playing() {
						an.Pause()
					} else {
						an.Play()
					}
				}

			}
		}
		if !g.blocking {
			break
		}
		if g.ready == g.visible { // the PNG: everything in view has loaded
			g.blocking = false
			continue
		}
		time.Sleep(time.Millisecond)
		g.cache.Poll()
	}
	c.Close()
	k.End()
	if g.counting {
		t2 := platform.Now()
		if len(g.pollNs) < cap(g.pollNs) {
			g.pollNs = append(g.pollNs, float64(t1-t0)/1e6)
			g.pollCPU = append(g.pollCPU, float64(c1-c0)/1e6)
			g.build = append(g.build, float64(t2-t0)/1e6)
		}
	}
}
