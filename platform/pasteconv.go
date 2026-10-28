package platform

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/draw"
	"image/png"
	"slices"
	"unicode/utf16"
	"unicode/utf8"
)

// The image and file conversions of the clipboard (see paste.go). They have no
// build tag so that they are tested on every OS; each OS uses the ones it
// needs (the DIB and CF_HDROP ones: Windows; the URI list: Linux).

var pngSig = [8]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

// pngHeader reads the size of a PNG from its IHDR chunk. ok is false when b
// does not start like a PNG.
func pngHeader(b []byte) (w, h uint32, ok bool) {
	if len(b) < 8+8+13 || !bytes.Equal(b[:8], pngSig[:]) {
		return 0, 0, false
	}
	if binary.BigEndian.Uint32(b[8:]) != 13 || string(b[12:16]) != "IHDR" {
		return 0, 0, false
	}
	w, h = binary.BigEndian.Uint32(b[16:]), binary.BigEndian.Uint32(b[20:])
	return w, h, w > 0 && h > 0
}

// pngEnd returns the length of the PNG file at the start of b, up to the end
// of its IEND chunk; 0 if b does not start with a PNG whose chunks run to an
// IEND. (Clipboard memory is rounded up in size, so the file is shorter than
// what the system reports.) It looks at the chunk framing only; a corrupt
// image is found when it is decoded.
func pngEnd(b []byte) int {
	if _, _, ok := pngHeader(b); !ok {
		return 0
	}
	for i := 8; i+12 <= len(b); {
		n := int(binary.BigEndian.Uint32(b[i:]))
		if n < 0 || n > len(b) { // a 32-bit length on a 32-bit int, or plainly too long
			return 0
		}
		end := i + 12 + n
		if end > len(b) || end < i {
			return 0
		}
		if string(b[i+4:i+8]) == "IEND" {
			return end
		}
		i = end
	}
	return 0
}

// appendPath appends the path p to dst, ended by a NUL. An empty path, or one
// with a NUL inside (it would split in two), is skipped.
func appendPath(dst []byte, p []byte) ([]byte, bool) {
	if len(p) == 0 || bytes.IndexByte(p, 0) >= 0 {
		return dst, false
	}
	return append(append(dst, p...), 0), true
}

// ---- text/uri-list (Linux; also what a file manager copies) ----

// appendURIListPaths appends to dst the file paths of a text/uri-list (RFC
// 2483: one URI per line, # starts a comment line), each ended by a NUL, and
// returns the number of paths. Only file: URIs on this host (no host, or
// localhost) are paths; percent-escapes are decoded. The output is never
// longer than the input, so converting in place (dst = list[:0]) is safe.
func appendURIListPaths(dst, list []byte) ([]byte, int) {
	n := 0
	for len(list) > 0 {
		line := list
		if i := bytes.IndexAny(list, "\r\n"); i >= 0 {
			line, list = list[:i], list[i+1:]
		} else {
			list = nil
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] == '#' {
			continue
		}
		var ok bool
		if dst, ok = appendFileURI(dst, line); ok {
			n++
		}
	}
	return dst, n
}

// appendFileURI appends the path of a file: URI, NUL-ended.
func appendFileURI(dst, uri []byte) ([]byte, bool) {
	const scheme = "file://"
	if len(uri) < len(scheme) || !bytes.EqualFold(uri[:len(scheme)], []byte(scheme)) {
		return dst, false
	}
	rest := uri[len(scheme):]
	if len(rest) == 0 { // "file://" and nothing after it
		return dst, false
	}
	if rest[0] != '/' { // a host: only this one's files are paths
		i := bytes.IndexByte(rest, '/')
		if i < 0 {
			return dst, false
		}
		host := rest[:i]
		if !bytes.EqualFold(host, []byte("localhost")) {
			return dst, false
		}
		rest = rest[i:]
	}
	start := len(dst)
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		switch {
		case c == '?' || c == '#':
			i = len(rest)
		case c == '%' && i+2 < len(rest) && unhex(rest[i+1]) >= 0 && unhex(rest[i+2]) >= 0:
			dst = append(dst, byte(unhex(rest[i+1])<<4|unhex(rest[i+2])))
			i += 2
		default:
			dst = append(dst, c)
		}
	}
	if len(dst) == start || bytes.IndexByte(dst[start:], 0) >= 0 {
		return dst[:start], false
	}
	return append(dst, 0), true
}

func unhex(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

// ---- CF_HDROP (Windows) ----

// buildDropFiles appends to dst a DROPFILES block (CF_HDROP) of paths: the
// 20-byte header with fWide set, then each path in UTF-16 ended by a NUL
// unit, then an empty path.
func buildDropFiles(dst []byte, paths []string) []byte {
	dst = binary.LittleEndian.AppendUint32(dst, 20) // pFiles
	dst = append(dst, make([]byte, 12)...)          // pt, fNC
	dst = binary.LittleEndian.AppendUint32(dst, 1)  // fWide
	for _, p := range paths {
		for _, u := range utf16.Encode([]rune(p)) {
			dst = binary.LittleEndian.AppendUint16(dst, u)
		}
		dst = append(dst, 0, 0)
	}
	return append(dst, 0, 0)
}

// appendDropFiles appends to dst the paths of a DROPFILES block (CF_HDROP),
// as UTF-8, each ended by a NUL, and returns how many. max bounds the
// appended bytes: it reports false, with dst as it was, when they would be
// more. The paths of an ANSI block (fWide 0) are in the system code page:
// ansi appends one of them to dst as UTF-8 (Windows: MultiByteToWideChar with
// CP_ACP); nil reads them as Latin-1.
func appendDropFiles(dst, blk []byte, max int, ansi func(dst, path []byte) []byte) (out []byte, n int, ok bool) {
	// DROPFILES: DWORD pFiles; POINT pt; BOOL fNC; BOOL fWide  (20 bytes)
	if len(blk) < 20 {
		return dst, 0, true
	}
	off := int(binary.LittleEndian.Uint32(blk))
	wide := binary.LittleEndian.Uint32(blk[16:]) != 0
	if off < 20 || off > len(blk) {
		return dst, 0, true
	}
	list := blk[off:]
	start := len(dst)
	if !wide {
		if ansi == nil {
			ansi = latin1ToUTF8
		}
		for len(list) > 0 && list[0] != 0 {
			i := bytes.IndexByte(list, 0)
			if i < 0 {
				i = len(list)
			}
			before := len(dst)
			dst = ansi(dst, list[:i])
			if len(dst) > before && bytes.IndexByte(dst[before:], 0) < 0 {
				dst = append(dst, 0)
				n++
			} else {
				dst = dst[:before]
			}
			if len(dst)-start > max {
				return dst[:start], 0, false
			}
			if i >= len(list) {
				break
			}
			list = list[i+1:]
		}
		return dst, n, true
	}
	// UTF-16, each path ended by a NUL unit and the list by an empty path.
	for i := 0; i+1 < len(list); {
		before := len(dst)
		for i+1 < len(list) {
			c := rune(binary.LittleEndian.Uint16(list[i:]))
			i += 2
			if c == 0 {
				break
			}
			switch {
			case c >= 0xD800 && c < 0xDC00:
				c = utf8.RuneError
				if i+1 < len(list) {
					if c2 := rune(binary.LittleEndian.Uint16(list[i:])); c2 >= 0xDC00 && c2 < 0xE000 {
						c = 0x10000 + (rune(binary.LittleEndian.Uint16(list[i-2:]))-0xD800)<<10 + c2 - 0xDC00
						i += 2
					}
				}
			case c >= 0xDC00 && c < 0xE000:
				c = utf8.RuneError
			}
			dst = utf8.AppendRune(dst, c)
		}
		if len(dst) == before { // the empty path that ends the list
			break
		}
		dst = append(dst, 0)
		n++
		if len(dst)-start > max {
			return dst[:start], 0, false
		}
	}
	return dst, n, true
}

// latin1ToUTF8 appends p, read as Latin-1, to dst as UTF-8.
func latin1ToUTF8(dst, p []byte) []byte {
	for _, c := range p {
		dst = utf8.AppendRune(dst, rune(c))
	}
	return dst
}

// ---- DIB (Windows) ----

// Errors of the DIB conversions.
var (
	errDIBUnsupported = errors.New("platform: unsupported DIB")
	errPNGTooBig      = errors.New("platform: PNG over MaxPaste")
)

// limitBuf is a bytes buffer that fails once it would hold more than max
// bytes.
type limitBuf struct {
	b   []byte
	max int
}

func (l *limitBuf) Write(p []byte) (int, error) {
	if len(l.b)+len(p) > l.max {
		return 0, errPNGTooBig
	}
	l.b = append(l.b, p...)
	return len(p), nil
}

// dibToPNG appends to dst the PNG file of the device-independent bitmap dib (a
// BITMAPINFOHEADER, V4 or V5 header, then the color masks and table, then
// the pixels: CF_DIB and CF_DIBV5). The PNG is limited to max bytes
// (errPNGTooBig). Uncompressed 1, 4, 8, 16, 24 and 32 bit bitmaps are read;
// others are errDIBUnsupported. Alpha is used where the bitmap says it has
// it (a mask in the V4/V5 header or BI_BITFIELDS), or where a 32-bit bitmap
// without masks has any non-zero fourth byte.
func dibToPNG(dst, dib []byte, max int) ([]byte, error) {
	const (
		biRGB, biBitfields, biAlphaBitfields = 0, 3, 6
	)
	if len(dib) < 40 {
		return dst, errDIBUnsupported
	}
	hdr := int(binary.LittleEndian.Uint32(dib))
	if hdr != 40 && hdr != 108 && hdr != 124 || hdr > len(dib) {
		return dst, errDIBUnsupported
	}
	w := int(int32(binary.LittleEndian.Uint32(dib[4:])))
	h := int(int32(binary.LittleEndian.Uint32(dib[8:])))
	bpp := int(binary.LittleEndian.Uint16(dib[14:]))
	comp := binary.LittleEndian.Uint32(dib[16:])
	used := int(binary.LittleEndian.Uint32(dib[32:]))
	topDown := h < 0
	if topDown {
		h = -h
	}
	if w <= 0 || h <= 0 || w > 1<<16 || h > 1<<16 || w*h > maxClipPixels || used < 0 || used > 256 {
		return dst, errDIBUnsupported
	}
	if comp != biRGB && comp != biBitfields && comp != biAlphaBitfields {
		return dst, errDIBUnsupported
	}
	switch bpp {
	case 1, 4, 8, 16, 24, 32:
	default:
		return dst, errDIBUnsupported
	}
	off := hdr
	var masks [4]uint32 // R, G, B, A
	switch {
	case hdr >= 108:
		if comp == biBitfields || comp == biAlphaBitfields {
			for i := range masks {
				masks[i] = binary.LittleEndian.Uint32(dib[40+4*i:])
			}
		}
	case comp == biBitfields || comp == biAlphaBitfields:
		nm := 3
		if comp == biAlphaBitfields {
			nm = 4
		}
		if off+4*nm > len(dib) {
			return dst, errDIBUnsupported
		}
		for i := 0; i < nm; i++ {
			masks[i] = binary.LittleEndian.Uint32(dib[off+4*i:])
		}
		off += 4 * nm
	}
	if comp == biRGB {
		switch bpp {
		case 16:
			masks = [4]uint32{0x7C00, 0x03E0, 0x001F, 0}
		case 32:
			masks = [4]uint32{0xFF0000, 0xFF00, 0xFF, 0}
		}
	}
	var pal [256][4]byte
	if bpp <= 8 {
		n := used
		if n == 0 {
			n = 1 << bpp
		}
		if off+4*n > len(dib) {
			return dst, errDIBUnsupported
		}
		for i := 0; i < n; i++ {
			pal[i] = [4]byte{dib[off+4*i+2], dib[off+4*i+1], dib[off+4*i], 0xff} // RGBA from BGRx
		}
		off += 4 * n
	} else {
		off += 4 * used // the color table a true-color bitmap may have too (biClrUsed counts it, whatever the header or the compression)
		if off > len(dib) {
			return dst, errDIBUnsupported
		}
	}
	stride := (w*bpp + 31) / 32 * 4
	if off > len(dib) || len(dib)-off < stride*h {
		return dst, errDIBUnsupported
	}
	if (bpp == 16 || bpp == 32) && (masks[0]|masks[1]|masks[2]) == 0 {
		return dst, errDIBUnsupported
	}
	px := dib[off:]
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	haveAlpha := masks[3] != 0
	anyAlpha := false
	for y := 0; y < h; y++ {
		sy := y
		if !topDown {
			sy = h - 1 - y
		}
		row := px[sy*stride : sy*stride+stride]
		out := img.Pix[y*img.Stride : y*img.Stride+4*w]
		switch bpp {
		case 1, 4, 8:
			for x := 0; x < w; x++ {
				var idx int
				switch bpp {
				case 8:
					idx = int(row[x])
				case 4:
					idx = int(row[x/2]>>(4*uint(1-x%2))) & 0xf
				default:
					idx = int(row[x/8]>>(7-uint(x%8))) & 1
				}
				copy(out[4*x:], pal[idx][:])
			}
		case 24:
			for x := 0; x < w; x++ {
				out[4*x], out[4*x+1], out[4*x+2], out[4*x+3] = row[3*x+2], row[3*x+1], row[3*x], 0xff
			}
		default: // 16 or 32 bits through the masks
			for x := 0; x < w; x++ {
				var v uint32
				if bpp == 16 {
					v = uint32(binary.LittleEndian.Uint16(row[2*x:]))
				} else {
					v = binary.LittleEndian.Uint32(row[4*x:])
				}
				out[4*x] = maskedByte(v, masks[0])
				out[4*x+1] = maskedByte(v, masks[1])
				out[4*x+2] = maskedByte(v, masks[2])
				switch {
				case haveAlpha:
					out[4*x+3] = maskedByte(v, masks[3])
				case bpp == 32 && comp == biRGB:
					a := byte(v >> 24)
					out[4*x+3] = a
					anyAlpha = anyAlpha || a != 0
				default:
					out[4*x+3] = 0xff
				}
			}
		}
	}
	if bpp == 32 && comp == biRGB && !anyAlpha { // the fourth byte is unused
		for i := 3; i < len(img.Pix); i += 4 {
			img.Pix[i] = 0xff
		}
	}
	lb := limitBuf{b: dst, max: len(dst) + max}
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&lb, img); err != nil {
		return dst, err
	}
	return lb.b, nil
}

// maskedByte extracts the field of v selected by mask and scales it to 8 bits.
func maskedByte(v, mask uint32) byte {
	if mask == 0 {
		return 0
	}
	shift := 0
	for mask>>uint(shift)&1 == 0 {
		shift++
	}
	f := (v & mask) >> uint(shift)
	bits := 0
	for m := mask >> uint(shift); m != 0; m >>= 1 {
		bits++
	}
	switch {
	case bits >= 8:
		return byte(f >> uint(bits-8))
	case bits == 0:
		return 0
	}
	return byte(f * 255 / (1<<uint(bits) - 1))
}

// pngToDIBV5 appends to dst a CF_DIBV5 (a BITMAPV5HEADER and 32-bit BGRA pixels,
// bottom-up, with the alpha channel) for the PNG file. An image over
// maxClipPixels, or one that does not decode, is an error.
func pngToDIBV5(dst, pngData []byte) ([]byte, error) {
	w, h, ok := pngHeader(pngData)
	if !ok || uint64(w)*uint64(h) > maxClipPixels {
		return dst, errDIBUnsupported
	}
	src, err := png.Decode(bytes.NewReader(pngData))
	if err != nil {
		return dst, err
	}
	b := src.Bounds()
	W, H := b.Dx(), b.Dy()
	size := 124 + 4*W*H
	start := len(dst)
	dst = slices.Grow(dst, size)[:start+size]
	clear(dst[start:])
	out := dst[start:]
	le := binary.LittleEndian
	le.PutUint32(out[0:], 124)
	le.PutUint32(out[4:], uint32(W))
	le.PutUint32(out[8:], uint32(H)) // positive: bottom-up
	le.PutUint16(out[12:], 1)
	le.PutUint16(out[14:], 32)
	le.PutUint32(out[16:], 3) // BI_BITFIELDS
	le.PutUint32(out[20:], uint32(4*W*H))
	le.PutUint32(out[40:], 0x00FF0000)
	le.PutUint32(out[44:], 0x0000FF00)
	le.PutUint32(out[48:], 0x000000FF)
	le.PutUint32(out[52:], 0xFF000000)
	le.PutUint32(out[56:], 0x73524742) // LCS_sRGB
	le.PutUint32(out[108:], 4)         // LCS_GM_IMAGES
	nrgba, isN := src.(*image.NRGBA)
	if !isN { // the generic path un-premultiplies correctly
		nrgba = image.NewNRGBA(image.Rect(0, 0, W, H))
		draw.Draw(nrgba, nrgba.Bounds(), src, b.Min, draw.Src)
		b = nrgba.Bounds()
	}
	for y := 0; y < H; y++ {
		row := out[124+4*W*(H-1-y):]
		for x := 0; x < W; x++ {
			p := nrgba.Pix[nrgba.PixOffset(b.Min.X+x, b.Min.Y+y):]
			r, g, bl, a := p[0], p[1], p[2], p[3]
			row[4*x], row[4*x+1], row[4*x+2], row[4*x+3] = bl, g, r, a
		}
	}
	return dst, nil
}
