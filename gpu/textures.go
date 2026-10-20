package gpu

// Texture ids and their lifetime, shared by the three backends.
//
// A TextureID is an index into the renderer's texture table in its low
// texIndexBits bits and the generation of that index in the bits above.
// FreeTexture bumps the generation and puts the index on a free list, so a
// later NewTexture can reuse the index under a new id while every copy of
// the old id stops resolving: Upload and FreeTexture ignore it and Encode
// draws its quads with an empty (transparent) texture. An index freed under
// its last generation is never reused, so no id repeats (a table index lasts
// 4096 frees; the 2^20 indices, about 4×10^9). Ids of a renderer
// that never frees are 0, 1, 2, ... as before generations existed.
//
// The GPU object of a freed texture is not released at once. It joins the
// renderer's retiring list, tagged with the serial of the last frame Encode
// recorded (every frame that may sample it has a serial at most that), and
// is released once the GPU has finished every frame up to that serial. How a
// backend learns that is its own (ring-slot retirement):
//
//   - Metal: each ring slot holds the command buffer of its frame. A slot
//     retires when Encode reuses it (after the wait that paces frames today),
//     or earlier when its command buffer reports Completed; a command buffer
//     that was never committed retires only at that reuse, never earlier, so
//     an encoded frame that is still to be committed keeps what it samples.
//   - Direct3D 11 and OpenGL: each ring slot carries a GPU fence (an event
//     query; a sync object) issued after its frame's draws and read without
//     waiting. Both APIs run a context's commands in order, so a signalled
//     fence also completes every earlier frame.
//
// Nothing waits for the GPU to free a texture and nothing is freed on a timer:
// a GPU that never finishes leaves the texture on the retiring list, counted
// by TextureCounts. Destroy releases everything after its own wait.

const (
	texIndexBits = 20
	texIndexMask = 1<<texIndexBits - 1
	texGenMask   = 1<<(32-texIndexBits) - 1
)

func (t TextureID) index() int  { return int(uint32(t) & texIndexMask) }
func (t TextureID) gen() uint32 { return uint32(t) >> texIndexBits }

// texTable is the generation-checked id table of a renderer's textures. The
// backend keeps its own per-index slices (objects, kinds, sizes) beside it.
type texTable struct {
	gen  []uint32 // current generation of each index
	live []bool
	free []int32 // freed indices, reused last-in first-out
	n    int     // live textures
}

// add takes an index for a new texture (a setup path: the table may grow)
// and returns it with the id that names it.
func (t *texTable) add() (int, TextureID) {
	var i int
	if k := len(t.free); k > 0 {
		i = int(t.free[k-1])
		t.free = t.free[:k-1]
	} else {
		i = len(t.gen)
		if i > texIndexMask {
			panic("gpu: too many textures")
		}
		t.gen = append(t.gen, 0)
		t.live = append(t.live, false)
	}
	t.live[i] = true
	t.n++
	return i, TextureID(uint32(i) | t.gen[i]<<texIndexBits)
}

// lookup returns the index of a live texture's id; false for a freed or
// unknown id.
func (t *texTable) lookup(id TextureID) (int, bool) {
	i := id.index()
	if i >= len(t.gen) || !t.live[i] || t.gen[i] != id.gen() {
		return 0, false
	}
	return i, true
}

// remove frees index i: its id stops resolving and the index may be reused.
// An index whose generations are used up is retired for good instead: a new
// generation would repeat an old id, which might still be held.
func (t *texTable) remove(i int) {
	t.live[i] = false
	t.n--
	if t.gen[i] == texGenMask {
		return
	}
	t.gen[i]++
	t.free = append(t.free, int32(i))
}
