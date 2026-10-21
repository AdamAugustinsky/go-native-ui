package img

import (
	"errors"
	"fmt"

	"gonativeui/internal/videoframe"
)

// VideoPoster is the first frame of the local video file at path. On macOS it
// honours the preferred track transform and Key's decode box and Cover flag,
// without enlarging or creating a player. Animate has no effect. The app chooses
// a Key.Hash identifying the file and its revision, distinct from its FileIcon.
// Elsewhere the cache reports Failed with an error wrapping errors.ErrUnsupported.
func VideoPoster(path string) Source { return Source{path: path, kind: srcVideoPoster} }

var errVideoUnsupported = fmt.Errorf("img: video poster: %w", errors.ErrUnsupported)

func (w *worker) decodeVideoPoster(c *Cache, j *job) result {
	pix, pw, ph, err := videoframe.Read(j.src.path, func(ow, oh int) (int, error) {
		if ow <= 0 || oh <= 0 {
			return 0, ErrDecode
		}
		if int64(ow)*int64(oh) > maxPixels {
			return 0, ErrTooLarge
		}
		tw, th := scaled(int32(ow), int32(oh), fitScale(int32(ow), int32(oh), j.w, j.h, j.flags&Cover != 0))
		if tw > maxSide || th > maxSide {
			return 0, ErrTooLarge
		}
		return int(max(tw, th)), nil
	})
	if err != nil {
		code := uint8(errDecode)
		if errors.Is(err, errors.ErrUnsupported) {
			code = errVideo
		}
		if errors.Is(err, ErrTooLarge) {
			code = errTooLarge
		}
		return result{buf: -1, err: code}
	}
	pad := atlasPad(int32(pw), int32(ph))
	bw, bh := pw+2*int(pad), ph+2*int(pad)
	buf := c.bufs.get(bw*bh*4, &w.fr)
	if buf < 0 {
		return result{buf: -1, err: errTooLarge}
	}
	dst := c.bufs.bytes(buf, bw*bh*4)
	for y := 0; y < ph; y++ {
		copy(dst[((y+int(pad))*bw+int(pad))*4:], pix[y*pw*4:(y+1)*pw*4])
	}
	if pad > 0 {
		padEdges(dst, pw, ph)
	}
	return result{buf: buf, w: int32(pw), h: int32(ph), pad: pad, frames: 1}
}
