package img

import "image"

// Decode decodes the first frame of the encoded image data, turned to its
// EXIF orientation and scaled down (never up) so that neither side is
// longer than maxSide pixels (0: full size), into a new *image.RGBA
// (premultiplied, sRGB, its bounds at the origin). It decodes as the cache's
// workers do (see Decoding in the package documentation): with ImageIO on
// macOS, so JPEG, PNG, GIF, HEIC, WebP and whatever else the system reads;
// with the standard library's PNG, JPEG and GIF decoders elsewhere. It may
// be called from any goroutine, and blocks: call it from a worker, never
// from the UI thread. It fails with ErrUnsupported, ErrDecode or
// ErrTooLarge (more than 100 megapixels, or a side over 16384 pixels).
func Decode(data []byte, maxSide int) (image.Image, error) {
	if len(data) == 0 {
		return nil, ErrDecode
	}
	m, code := decodeImage(data, int32(min(max(maxSide, 0), maxSide32)))
	if code != errNone {
		return nil, errOf(code)
	}
	return m, nil
}

// maxSide32 bounds Decode's maxSide: anything larger is the same as no limit
// short of maxSide.
const maxSide32 = 1 << 30
