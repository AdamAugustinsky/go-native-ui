package mdview

import (
	"bytes"
	"hash/maphash"
	"math"

	"gonativeui/ui"
	"gonativeui/widget/base"
)

const maxLayoutBytes = 8 << 20

type codeState struct {
	scroll                 base.ScrollState
	seen                   uint32
	hash                   uint64
	made                   bool
	offsets                []uint32
	stride, lines, columns int
}

func (v *MarkdownView) blockState(c *ui.Context, key ui.Key) *codeState {
	if v.scroll == nil {
		v.scroll = make(map[ui.Key]*codeState)
		v.seed = maphash.MakeSeed()
	}
	if st := v.scroll[key]; st != nil {
		st.seen = c.Frame()
		return st
	}
	if len(v.scroll) >= maxEntries {
		for k, st := range v.scroll {
			if c.Frame()-st.seen > 120 {
				v.layoutBytes -= cap(st.offsets) * 4
				delete(v.scroll, k)
			}
		}
		if len(v.scroll) >= maxEntries {
			v.dropLayout(c.Frame(), 0)
		}
	}
	st := &codeState{seen: c.Frame()}
	v.scroll[key] = st
	return st
}

// dropLayout removes the oldest block other than keep.
func (v *MarkdownView) dropLayout(frame uint32, keep ui.Key) {
	var oldest ui.Key
	age := uint32(0)
	found := false
	for k, st := range v.scroll {
		if k != keep {
			if d := frame - st.seen; !found || d > age || d == age && k < oldest {
				oldest, age, found = k, d, true
			}
		}
	}
	if found {
		v.layoutBytes -= cap(v.scroll[oldest].offsets) * 4
		delete(v.scroll, oldest)
	}
}

// source caches layout on a content revision. Very large blocks use sparse
// checkpoints so even a source of empty lines cannot exceed the byte budget.
func (v *MarkdownView) source(c *ui.Context, key ui.Key, src []byte) *codeState {
	st := v.blockState(c, key)
	hash := maphash.Bytes(v.seed, src)
	if st.made && st.hash == hash {
		return st
	}
	st.made, st.hash = true, hash
	st.lines = bytes.Count(src, []byte("\n")) + 1
	st.columns = maxColumns(src)
	st.stride = 1
	for (st.lines+st.stride-1)/st.stride*4 > maxLayoutBytes {
		st.stride *= 2
	}
	n := (st.lines + st.stride - 1) / st.stride
	if cap(st.offsets) < n {
		v.layoutBytes -= cap(st.offsets) * 4
		st.offsets = nil
		for v.layoutBytes+n*4 > maxLayoutBytes {
			v.dropLayout(c.Frame(), key)
		}
		st.offsets = make([]uint32, n)
		v.layoutBytes += cap(st.offsets) * 4
	}
	st.offsets = st.offsets[:n]
	line := 0
	for at := 0; at <= len(src); line++ {
		if line%st.stride == 0 {
			st.offsets[line/st.stride] = uint32(at)
		}
		n := bytes.IndexByte(src[at:], '\n')
		if n < 0 {
			break
		}
		at += n + 1
	}
	return st
}

func (st *codeState) offset(src []byte, line int) int {
	if line >= st.lines {
		return len(src) + 1
	}
	at := int(st.offsets[line/st.stride])
	for i := line / st.stride * st.stride; i < line; i++ {
		at += bytes.IndexByte(src[at:], '\n') + 1
	}
	return at
}

func codeRange(c *ui.Context, id ui.ID, lines int, pitch, head float32) (first, last int) {
	r, shown := c.Rect(id), c.ShownRect(id)
	if r[3] <= r[1] {
		_, h := c.Size()
		return 0, min(lines, int(math.Ceil(float64(float32(h)/pitch)))+1)
	}
	if shown == [4]float32{} {
		return 0, 0
	}
	first = min(lines, max(0, int(math.Floor(float64((shown[1]-r[1]-head)/pitch)))-1))
	last = min(lines, max(first, int(math.Ceil(float64((shown[3]-r[1]-head)/pitch)))+1))
	return
}
