package img

import (
	"math"

	"gonativeui/platform"
	"gonativeui/task"
)

// job is one decode, as submitted to the workers.
type job struct {
	src   Source
	copy  int32 // pooled copy of inline bytes, -1 if none
	copyN int32
	w, h  int32
	flags Flags
	frame int32 // the frame of an animated image (0 for now; C6 decodes the others)
}

// result is a finished decode (pointer-free: it crosses threads).
type result struct {
	buf          int32 // pooled pixels, -1 on failure
	w, h, pad    int32 // image size; pad is the replicated border around it (atlas images)
	frames, dlay int32 // frame count, first frame's delay in ms
	ns           int64 // worker time
	err          uint8
}

// maxSide is the largest decoded side: what every backend's textures take.
const maxSide = 16384

// worker is a decoding goroutine's private state: its C call frame (a
// Frame must not live on a stack) and the OS decoder's scratch.
type worker struct {
	fr cframe
	osDecoder
}

// decodeJob runs on a worker.
func (c *Cache) decodeJob(t task.Token, j job) result {
	w := c.takeWorker()
	defer c.putWorker(w)
	t0 := platform.Now()
	var data []byte
	switch j.src.kind {
	case srcBytes:
		data = c.bufs.bytes(j.copy, int(j.copyN))
	case srcLease:
		data = j.src.lease.Bytes()
	}
	var r result
	if t.Cancelled() {
		r = result{buf: -1, err: errDecode}
	} else {
		r = w.decode(c, &j, data)
	}
	if j.copy >= 0 {
		c.bufs.put(j.copy, &w.fr)
	}
	r.ns = platform.Now() - t0
	return r
}

func (c *Cache) takeWorker() *worker {
	c.wmu.Lock()
	w := c.workers[len(c.workers)-1]
	c.workers = c.workers[:len(c.workers)-1]
	c.wmu.Unlock()
	return w
}

func (c *Cache) putWorker(w *worker) {
	c.wmu.Lock()
	c.workers = append(c.workers, w)
	c.wmu.Unlock()
}

// fitScale is the scale that fits an ow×oh image inside (or, with cover,
// over) the box w×h, at most 1 (images are never enlarged). A side of 0 is
// free; both 0 is 1.
func fitScale(ow, oh, w, h int32, cover bool) float64 {
	sx, sy := math.Inf(1), math.Inf(1)
	if w > 0 {
		sx = float64(w) / float64(ow)
	}
	if h > 0 {
		sy = float64(h) / float64(oh)
	}
	s := math.Min(sx, sy)
	if cover && w > 0 && h > 0 {
		s = math.Max(sx, sy)
	}
	return math.Min(s, 1)
}

// scaled is the size of an ow×oh image at scale s, at least 1×1.
func scaled(ow, oh int32, s float64) (int32, int32) {
	return max(int32(math.Round(float64(ow)*s)), 1), max(int32(math.Round(float64(oh)*s)), 1)
}

// atlasPad is the replicated border an image of w×h gets: 1 pixel for an
// atlas image, so that linear filtering at its edges never reads a
// neighbour; none for an image with a texture of its own (clamped).
func atlasPad(w, h int32) int32 {
	if w <= AtlasMax && h <= AtlasMax {
		return 1
	}
	return 0
}

// padEdges copies the outermost pixels of the w×h image at (1, 1) of a
// (w+2)×(h+2) RGBA buffer into its one-pixel border.
func padEdges(pix []byte, w, h int) {
	stride := (w + 2) * 4
	for y := 1; y <= h; y++ {
		row := pix[y*stride:]
		copy(row[0:4], row[4:8])
		copy(row[(w+1)*4:(w+2)*4], row[w*4:(w+1)*4])
	}
	copy(pix[0:stride], pix[stride:2*stride])
	copy(pix[(h+1)*stride:(h+2)*stride], pix[h*stride:(h+1)*stride])
}

// sniff reports formats the decoders recognise by their first bytes, for
// telling an unsupported format from a corrupt file.
func sniff(b []byte) string {
	switch {
	case len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n":
		return "png"
	case len(b) >= 3 && b[0] == 0xFF && b[1] == 0xD8 && b[2] == 0xFF:
		return "jpeg"
	case len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a"):
		return "gif"
	case len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP":
		return "webp"
	case len(b) >= 12 && string(b[4:8]) == "ftyp":
		return "heif"
	}
	return ""
}
