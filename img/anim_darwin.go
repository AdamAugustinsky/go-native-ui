//go:build darwin

package img

import (
	"unsafe"

	"gonativeui/internal/ffi"
)

// Which formats beyond GIF and APNG the system can animate: ImageIO decodes
// animated WebP (macOS 11+) and HEICS (macOS 10.15+) the same way.
const (
	osAnimWebP = true
	osAnimHEIF = true
)

// systemAutoplay reads the system's "Auto-play animated images" setting
// (System Settings, Accessibility, Motion; macOS 14+) with
// AXAnimatedImagesEnabled from Accessibility.framework, declared in
// AXSettings.h (API_AVAILABLE macos(14.0)) and never written. Before macOS
// 14, or when the symbol is missing, animations play. The framework is
// opened with the decoding frameworks by the first worker that decodes an
// image, which has happened before any animated image can be shown.
func systemAutoplay(fr *ffi.Frame) bool {
	if iio.axAnimated == 0 {
		return true
	}
	return fr.Call0(iio.axAnimated)&0xFF != 0
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
	if sniff(data) == "gif" {
		return openGoDecoder(data, false)
	}
	if err := loadImageIO(); err != nil || len(data) == 0 {
		return nil, errNotAnimated
	}
	fr := &w.fr
	cfd := fr.Reset().Arg(0).ArgPtr(unsafe.Pointer(&data[0])).Arg(uintptr(len(data))).Arg(iio.allocNull).Call(iio.cfDataNoCopy).R0()
	if cfd == 0 {
		return nil, errNotAnimated
	}
	src := fr.Call2(iio.srcWithData, cfd, 0) // retains the data
	fr.Call1(iio.cfRelease, cfd)
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
