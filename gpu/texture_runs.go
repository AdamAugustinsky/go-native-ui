package gpu

// textureBatch preserves painter order while sharing up to eight textures.
// It contains ids, not native pointers, so its reused slab is pointer-free.
type textureBatch struct {
	first, count int
	textures     [runTextures]TextureID
	n            int
}

// textureBatches rewrites only the renderer's instance copy, mapping texture
// ids to slots. The backdrops in quads have already become fallback shapes.
func textureBatches(dst []textureBatch, quads []Quad) []textureBatch {
	dst = dst[:0]
	b := textureBatch{}
next:
	for i := range quads {
		q := &quads[i]
		if q.Mode == ModeShape || q.Mode == ModeCapsule {
			continue
		}
		for slot := range b.n {
			if b.textures[slot] == q.Tex {
				q.Tex = TextureID(slot)
				continue next
			}
		}
		if b.n == runTextures {
			b.count = i - b.first
			dst = append(dst, b)
			b = textureBatch{first: i}
		}
		b.textures[b.n] = q.Tex
		q.Tex = TextureID(b.n)
		b.n++
	}
	if b.first < len(quads) {
		b.count = len(quads) - b.first
		dst = append(dst, b)
	}
	return dst
}
