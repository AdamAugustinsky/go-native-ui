# Images

Decoding an image is slow and an image is big, so a frame never decodes one.
Package `img` decodes on worker goroutines, at the size the image is shown, into
a cache with a GPU and a CPU byte budget (D44). The frame asks for what is in
view and draws what is ready; what is not ready draws as a skeleton, and a frame
that finds everything cached allocates nothing.

The blocks below are excerpts of [`examples/images`](../../examples/images),
which shows six pictures made in memory, and of two programs that use the
system's icons and animations: `cmd/chromedemo` and `cmd/imgbench`. The test
that checks the guide checks them.

```sh
go run ./examples/images                  # the window
go run ./examples/images -o images.png    # render it offscreen, once every picture has decoded
go test ./examples/images                 # decode size, a bad picture, layout, 0 allocations
```

## 1. The cache

A cache makes textures, so it needs the app's renderer (`a.R`), and it is used
from the thread that draws. Make it once, in `Init`, and give it to the kit,
whose `Image` draws from it:

```go examples/images/main.go
func (g *gallery) Init(a *app.App) error {
	g.k = widget.NewKit(a.UI, a.Text, rex.Theme())
	g.cache = img.NewCache(img.Options{Renderer: a.R, GPUBytes: 32 << 20, CPUBytes: 16 << 20, Entries: 64})
	g.k.SetImages(g.cache)
	a.UI.Reserve(256)
	return nil
}
```

`GPUBytes` is the budget of texture memory; when it is short, the least
recently drawn images are evicted, never one that was drawn or requested in this
frame or the last. `CPUBytes` is the budget of decoded pixels kept so that an
image evicted from the GPU is uploaded again without decoding again.
`Entries` sizes the tables, which never grow. Close the cache after the window,
which stops the workers and frees the textures:

```go examples/images/main.go
		g := newGallery()
		err = app.Run(cfg, g)
		g.cache.Close(time.Second) // after the window: stops the workers and frees the textures
```

## 2. Keys, sources and handles

An image is named by its `Key`: a **hash of its content** that you choose, the
decode size in pixels and flags. The same key is the same image, so two views of
one file at one size share a decode. A `Source` says where the encoded bytes
are: `img.File(path)`, `img.Bytes(b)` (copied at the first request that needs
them, for small images), or `img.Leased(l)`, bytes you own and lend. It is
only read when the image is not in the cache yet.

`Request` returns a `Handle` and a `State`. Call it every frame for every image
in view; it is cheap, and allocates nothing for an image it knows:

```go examples/images/main.go
func (g *gallery) Build(c *ui.Context) {
	if g.cache.Poll() { // uploads finished decodes, within a budget per frame
		c.RequestFrame() // more are waiting: they are left for the next frame
	}
	k := g.k
	k.Begin(c)
	k.BeginWindow()
	k.BeginCard("pictures")
	k.LabelStyle("Images", widget.TextSemi)

	px := int32(c.Pt(tilePt)) // the decode size is the size shown, in pixels
	g.ready = 0
	c.BeginFlow("tiles", ui.FlowOpts{GapX: c.Pt(12), GapY: c.Pt(12)}) // the tiles wrap onto lines
	for i := range g.pics {
		key := img.Key{Hash: g.hashes[i], W: px, H: px, Flags: img.Cover}
		h, st := g.cache.Request(key, img.Bytes(g.pics[i]))
		if st == img.Ready {
			g.ready++
		}
		k.NextSize(k.Fixed(tilePt), k.Fixed(tilePt))
		k.Image(uint64(i), h, ui.FitFill, c.Pt(12), "Picture")
	}
	c.EndFlow()
```

- **`Pending`** while the image decodes or waits for its upload; **`Ready`** once
  it is on the GPU; **`Failed`** when it cannot be decoded, and `cache.Err(h)` says
  why. `Image` draws the theme's track fill, a skeleton, for the first and the
  last.
- **The decode box is the size shown, in pixels.** The image is scaled to fit
  inside `Key.W × Key.H`, or with the `Cover` flag to cover it (draw it with
  `ui.FitFill`), and is never enlarged. A picture of 400 px shown at 96 pt is decoded at
  96 px at 1× and at 192 px at 2×; `TestPicturesDecodeAtDisplaySize` checks
  the first. Changing the size is a different key, a different image.
- **`Poll` each frame, at the start of `Build`.** It uploads the finished decodes
  within `Options.UploadBytes`, so a burst of decodes never makes a slow frame, and
  tells you when more are waiting: ask for another frame then.
- **A handle is generation-tagged:** one kept past its image's eviction never
  names another image.
- Everything is on the cache's goroutine, the one that draws its window. Workers
  decode; nothing else does.

`k.Image(n, h, fit, radius, alt)` builds the box: `n` tells apart the images of
one parent, `fit` is `ui.FitFill` for thumbnails and avatars or `ui.FitContain`
for media, `radius` is in pixels, and `alt` is the accessible name (empty for
decoration). The box's size is yours: `NextSize` before it.

## 3. What decodes what

- **macOS** uses ImageIO: JPEG, PNG, GIF, HEIC, WebP and whatever else the
  system reads, scaled by the system at decode time. The EXIF orientation is
  applied.
- **Windows and Linux** use the standard library's PNG, JPEG and GIF decoders,
  scaled by area averaging, and JPEG's EXIF orientation is applied. WebP and other
  formats fail with `ErrUnsupported`. An app that must show WebP there decodes
  it itself.
- An image whose header says more than 100 megapixels fails with `ErrTooLarge`
  before a pixel is decoded.

The window's test hands the cache bytes that are not an image and checks that
the picture is `Failed` with an error while the others draw.

## 4. The system's icons

A `Source` can also be what the file manager shows: `FileIcon(path)` for a file,
folder, volume or application, `TypeIcon(uti)` for a file type (macOS),
`ExtensionIcon(ext)` and `FolderIcon()`. They are decoded at the size shown and
cached like any image. The key is `HashTypeIcon` (or `HashFileIcon`, which
includes the modification time) and not a hash of your own, so that icons and
pictures never collide. The Finder scene of `cmd/chromedemo` asks for the icon
of each file's type:

```go cmd/chromedemo/demo/finder.go
func (d *Demo) typeIcon(i int, side float32) (h img.Handle, st img.State, ok bool) {
	if d.imgs == nil {
		return 0, img.Failed, false
	}
	typ, ext := "public.presentation", "key"
	if i >= 0 {
		typ, ext = fileTypes[i], strings.TrimPrefix(path.Ext(fileNames[i]), ".")
	}
	var src img.Source
	switch {
	case runtime.GOOS == "darwin":
		src = img.TypeIcon(typ)
	case typ == "public.folder":
		src = img.FolderIcon()
	default:
		typ, src = ext, img.ExtensionIcon(ext)
	}
	px := int32(side + 0.5)
	h, st = d.imgs.Request(img.Key{Hash: img.HashTypeIcon(typ), W: px, H: px}, src)
	if st == img.Failed && runtime.GOOS == "darwin" && typ != "public.data" {
		// a type the system does not know: the scene's UTTypeData
		h, st = d.imgs.Request(img.Key{Hash: img.HashTypeIcon("public.data"), W: px, H: px}, img.TypeIcon("public.data"))
	}
	if st == img.Pending {
		d.iconsPending = true
	}
	return h, st, st != img.Failed
}
```

## 5. Animations

With the `Animate` flag a GIF or an APNG plays (on macOS animated HEIC, HEICS
and WebP too). `Kit.Image` draws the frame showing and wakes the app at the next
frame's deadline, never every frame. Frames are decoded only while the image is in
view, a few ahead; a hidden window, or a row scrolled out, decodes and wakes
nothing. A file's loop count is honoured. The system's "Auto-play animated
images" setting and Reduce Motion hold the first frame until you call `Play`.
`cmd/imgbench`'s grid makes the animated samples a click that pauses and plays
them:

```go cmd/imgbench/grid.go
					key.Hash, key.Flags = animHash+uint64(i), img.Animate
					src = img.Bytes(g.samples[i].data)
// …
				if sample && c.Signal(id).Clicked {
					if an := g.cache.Animation(h); an.Playing() {
						an.Pause()
					} else {
						an.Play()
					}
				}
```

## 6. Performance

`cmd/imgbench` scrolls a grid of three thousand thumbnails through the cache:
every item its own image, decoded at its cell's size, uploaded within the
per-frame budget, evicted when the budget is short. `imgbench -bench -gate` exits
with status 3 unless the measured sessions allocated nothing, every image in view
was drawn and no frame's `Poll` took longer than a budget. CI runs it on macOS.
