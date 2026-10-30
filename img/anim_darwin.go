//go:build darwin

package img

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"gonativeui/internal/cf"
	"gonativeui/internal/ffi"
	"gonativeui/internal/objc"
)

// Which formats beyond GIF and APNG the system can animate: ImageIO decodes
// animated WebP (macOS 11+) and HEICS (macOS 10.15+) the same way.
const (
	osAnimWebP = true
	osAnimHEIF = true
)

// The system's "Auto-play animated images" setting (System Settings,
// Accessibility, Motion; macOS 14+) is read with AXAnimatedImagesEnabled from
// Accessibility.framework, declared in AXSettings.h (API_AVAILABLE macos(14.0)),
// and never written. The framework is opened, and its change notification
// observed, by the first worker that opens an animation of any format (a GIF
// too), before the owner can hold an animated handle. Before macOS 14, or
// when the symbol is missing, animations play.
var ax struct {
	once sync.Once
	fn   uintptr // AXAnimatedImagesEnabled
	name uintptr // AXAnimatedImagesEnabledDidChangeNotification
	mu   sync.Mutex
	// The caches with animations: the change notification wakes their loops.
	caches []*Cache
}

// axLoads counts calls to axLoad (tests check a GIF-only app makes them).
var axLoads atomic.Int32

// axLoad opens Accessibility.framework and installs the observer of
// AXAnimatedImagesEnabledDidChangeNotification, once. It runs on a worker.
func axLoad() {
	axLoads.Add(1)
	ax.once.Do(func() {
		if cf.Init() != nil {
			return
		}
		lib, err := cf.Framework("Accessibility")
		if err != nil {
			return
		}
		ax.fn = cf.Sym(lib, "AXAnimatedImagesEnabled")
		name := cf.Const(lib, "AXAnimatedImagesEnabledDidChangeNotification")
		ax.name = name
		if ax.fn == 0 || name == 0 {
			return
		}
		fr := cf.Acquire()
		defer cf.Release(fr)
		defer cf.Pool(fr)()
		// The block is global and runs on the thread that posts: it wakes
		// the loop of every cache that has an animation, whose next frame
		// reads the setting again (autoplay is read once per frame).
		block := objc.NewGlobalBlock("v16@?0@8", func(*ffi.CB) { axChanged() })
		center := cf.Msg(fr, cf.Cls(fr, "NSNotificationCenter"), cf.Sel(fr, "defaultCenter"))
		if center != 0 {
			cf.Msg(fr, center, cf.Sel(fr, "addObserverForName:object:queue:usingBlock:"), name, 0, 0, uintptr(block))
		}
	})
}

// axSnapshotHook, if set, runs in axChanged between the list of caches and
// the wakes (tests close a cache there).
var axSnapshotHook func()

// axChanged wakes the caches' loops.
func axChanged() {
	ax.mu.Lock()
	cs := append([]*Cache(nil), ax.caches...)
	ax.mu.Unlock()
	if axSnapshotHook != nil {
		axSnapshotHook()
	}
	for _, c := range cs {
		c.wakeApp()
	}
}

// watchAutoplay registers c for the setting's change notification, once.
func watchAutoplay(c *Cache) {
	if c.watching {
		return
	}
	c.watching = true
	ax.mu.Lock()
	ax.caches = append(ax.caches, c)
	ax.mu.Unlock()
}

// unwatchAutoplay ends c's registration (Close).
func unwatchAutoplay(c *Cache) {
	if !c.watching {
		return
	}
	c.watching = false
	ax.mu.Lock()
	for k, o := range ax.caches {
		if o == c {
			last := len(ax.caches) - 1
			copy(ax.caches[k:], ax.caches[k+1:])
			ax.caches[last] = nil // (the backing array must not keep a closed cache alive)
			ax.caches = ax.caches[:last]
			break
		}
	}
	ax.mu.Unlock()
}

// systemAutoplay reads the setting on the owner's goroutine, with its frame.
func systemAutoplay(fr *ffi.Frame) bool {
	if ax.fn == 0 {
		return true
	}
	return fr.Call0(ax.fn)&0xFF != 0
}

// iioAnim is an animation decoded by ImageIO: the source object stays open
// between frames and is used by one worker at a time. ImageIO composites the
// frames of an APNG, a WebP or a HEICS sequence itself, so each frame is
// asked for alone, at the display size.
type iioAnim struct {
	data   []byte // ImageIO reads it until the source is released
	src    uintptr
	w, h   int
	plays  int
	delays []int32
}

func (d *iioAnim) size() (int, int) { return d.w, d.h }
func (d *iioAnim) count() int       { return len(d.delays) }
func (d *iioAnim) playCount() int   { return d.plays }
func (d *iioAnim) delay(i int) int  { return int(d.delays[i]) }

// retained: the file, and what ImageIO keeps of the composited canvas (taken
// as two canvases).
func (d *iioAnim) retained() int64 { return int64(len(d.data)) + 2*4*int64(d.w)*int64(d.h) }

// trim releases the source object, which holds ImageIO's decoded frames; render
// opens it again from the file.
func (d *iioAnim) trim(fr *cframe) int64 {
	d.close(fr)
	return int64(len(d.data))
}

func (d *iioAnim) close(fr *cframe) {
	if d.src != 0 {
		fr.Call1(iio.cfRelease, d.src)
		d.src = 0
	}
}

// openSystemDecoder opens a GIF in Go (ImageIO clears a disposed frame to the
// file's background colour where browsers clear it to transparent) and the
// other animated formats with ImageIO.
func (w *worker) openSystemDecoder(data []byte) (animDecoder, error) {
	axLoad() // the setting's reader, before the owner can hold an animation of any format
	if sniff(data) == "gif" {
		return openGoDecoder(data, false)
	}
	if err := loadImageIO(); err != nil || len(data) == 0 {
		return nil, errNotAnimated
	}
	fr := &w.fr
	src := openSource(fr, data)
	if src == 0 {
		return nil, errNotAnimated
	}
	d := &iioAnim{data: data, src: src}
	n := int(fr.Call1(iio.srcCount, src))
	if n <= 1 {
		d.close(fr)
		return nil, errNotAnimated
	}
	d.delays = make([]int32, n)
	for i := 0; i < n; i++ {
		props := fr.Call3(iio.srcProps, src, uintptr(i), 0)
		if props == 0 {
			d.close(fr)
			return nil, errDecodeAnim
		}
		if i == 0 {
			pw, okW := w.int32Prop(fr, props, iio.propW)
			ph, okH := w.int32Prop(fr, props, iio.propH)
			if !okW || !okH {
				fr.Call1(iio.cfRelease, props)
				d.close(fr)
				return nil, errDecodeAnim
			}
			d.w, d.h = int(pw), int(ph)
		}
		d.delays[i] = w.delayOf(fr, props, true)
		fr.Call1(iio.cfRelease, props)
	}
	if fp := fr.Call2(iio.srcFileProps, src, 0); fp != 0 {
		d.plays = w.playsOf(fr, fp)
		fr.Call1(iio.cfRelease, fp)
	}
	return d, nil
}

// openSource makes an ImageIO source over data (0 if it cannot).
func openSource(fr *ffi.Frame, data []byte) uintptr {
	cfd := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&data[0])).Arg(uintptr(len(data))).Arg(iio.allocNull).Call(iio.cfDataNoCopy).R0()
	if cfd == 0 {
		return 0
	}
	src := fr.Call2(iio.srcWithData, cfd, 0) // retains the data
	fr.Call1(iio.cfRelease, cfd)
	return src
}

// playsOf is the number of times an animation plays, from the container's
// loop count in the file's properties: 0 for ever. GIF stores the repeat count
// (a loop count of n plays n+1 times, 0 for ever); APNG, WebP and HEICS the
// play count (ImageIO's headers say "the number of times to play"). A file
// without one plays once.
func (w *worker) playsOf(fr *ffi.Frame, props uintptr) int {
	for _, d := range [...]struct {
		dict, key uintptr
		repeat    bool
	}{
		{iio.dictGIF, iio.loopGIF, true},
		{iio.dictPNG, iio.loopAPNG, false},
		{iio.dictWebP, iio.loopWebP, false},
		{iio.dictHEICS, iio.loopHEICS, false},
	} {
		if d.dict == 0 || d.key == 0 {
			continue
		}
		sub := fr.Call2(iio.cfDictGet, props, d.dict)
		if sub == 0 {
			continue
		}
		if n := fr.Call2(iio.cfDictGet, sub, d.key); n != 0 {
			w.i32 = 0
			if fr.Reset().Arg(n).Arg(cfNumberSInt32).ArgPtr(unsafe.Pointer(&w.i32)).Call(iio.cfNumGet).Bool() {
				switch {
				case w.i32 <= 0:
					return 0
				case d.repeat:
					return int(w.i32) + 1
				}
				return int(w.i32)
			}
		}
	}
	return 1
}

// render asks ImageIO for frame i at the display size.
func (d *iioAnim) render(w *worker, i int, tw, th, pad int32, pix []byte, bw int) error {
	fr := &w.fr
	if d.src == 0 { // trimmed
		if d.src = openSource(fr, d.data); d.src == 0 {
			return errDecodeAnim
		}
	}
	w.i32 = max(tw, th)
	num := fr.Reset().Arg(0).Arg(cfNumberSInt32).ArgPtr(unsafe.Pointer(&w.i32)).Call(iio.cfNumCreate).R0()
	w.keys = [4]uintptr{iio.optFromAlways, iio.optMaxSize, iio.optCacheNow}
	w.vals = [4]uintptr{iio.boolTrue, num, iio.boolTrue}
	opts := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&w.keys[0])).ArgPtr(unsafe.Pointer(&w.vals[0])).Arg(3).
		Arg(iio.dictKeyCB).Arg(iio.dictValCB).Call(iio.cfDictCreate).R0()
	fr.Call1(iio.cfRelease, num)
	thumb := fr.Call3(iio.srcThumb, d.src, uintptr(i), opts)
	fr.Call1(iio.cfRelease, opts)
	if thumb == 0 {
		return errDecodeAnim
	}
	defer fr.Call1(iio.imgRelease, thumb)
	if !w.draw(fr, thumb, pix, bw, int(th+2*pad), pad, tw, th) {
		return errDecodeAnim
	}
	if pad > 0 {
		padEdges(pix, int(tw), int(th))
	}
	return nil
}
