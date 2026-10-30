//go:build darwin || windows || linux

package text

// ShapedRun owns a worker's output, independently of its run cache. Its bulk
// storage (text, glyphs, positions, clusters and face records) is pointer-free.
// Native faces are retained until Close; no mutable shaping state is shared.
// Transfer exclusive ownership between goroutines. The zero value is ready
// for use. Do not copy a ShapedRun after use.
type ShapedRun struct {
	desc    FontDesc
	slabs   runSlabs
	faces   []shapedFace
	names   []byte
	ids     []FaceID
	mapped  []FaceID
	advance float32
	valid   bool
}

// Close releases the retained faces and resets the result, keeping its Go
// buffers for reuse. It can run on either the producing or consuming thread.
func (r *ShapedRun) Close() {
	for i := range r.faces {
		releaseShapedFace(&r.faces[i])
	}
	r.faces, r.names = r.faces[:0], r.names[:0]
	r.slabs.truncate()
	r.desc, r.advance, r.valid = FontDesc{}, 0, false
}

func (r *ShapedRun) reserve(n int) {
	r.slabs.grow(n, n, n, n)
	grow(&r.mapped, n)
	if cap(r.faces) < 8 {
		r.faces = make([]shapedFace, 0, 8)
		r.ids = make([]FaceID, 0, 8)
		r.names = make([]byte, 0, 1024)
	}
}

// Shaper shapes on its calling goroutine, with its own System. It has no
// AppKit dependency and never rasterizes. Each pool worker needs its own
// Shaper; NewPrefetcher manages that ownership for file-list prefetching.
type Shaper struct {
	s    *System
	desc FontDesc
	font FontKey
	set  bool
}

// NewShaper creates a shaper on the calling worker. The small cache is bounded
// independently of the UI's working set; it does not reserve a paragraph cache.
func NewShaper() (*Shaper, error) {
	s, err := New()
	if err != nil {
		return nil, err
	}
	s.runs.maxRuns, s.runs.maxGlyphs, s.runs.maxText = 256, 32<<10, 64<<10
	s.runs.reserve(256, 32<<10, 64<<10)
	s.reserveWorkerScratch()
	return &Shaper{s: s}, nil
}

// Shape copies the shaped output to out. It releases out's previous faces.
// Only the current scale's fonts are retained: a description at a new scale
// releases the previous scale's, bounding fonts and fallback state when a
// window changes scale. Fonts of one scale (regular, semibold, a list's body
// font) stay, so alternating between them reuses their faces and runs.
func (w *Shaper) Shape(d FontDesc, b []byte, out *ShapedRun) bool {
	out.Close()
	if w.s == nil {
		return false
	}
	d = normalizeDesc(d)
	if !w.set || w.desc != d {
		if w.set && w.desc.Scale != d.Scale {
			w.s.ReleaseScale(w.desc.Scale)
		}
		w.font = w.s.Font(d)
		w.desc, w.set = d, true
	}
	if actual, ok := w.s.FontDescription(w.font); !ok || actual != d {
		return false
	}
	ref := w.s.Shape(w.font, b)
	if ref == 0 {
		return false
	}
	v := w.s.Run(ref)
	out.slabs.add(0, 0, v.Text, v.Glyphs, v.X, nil, v.Clusters, v.Advance)
	// Faces in the result index its own retained face records, never a
	// worker's mutable face table. Consecutive glyphs usually share a face.
	var prev FaceID
	var index FaceID
	for i, id := range v.Faces {
		if i == 0 || id != prev {
			j := 0
			for j < len(out.faces) && out.faces[j].source != id {
				j++
			}
			if j == len(out.faces) {
				out.faces = append(out.faces, w.s.exportFace(id, &out.names))
			}
			prev, index = id, FaceID(j)
		}
		out.slabs.faces = append(out.slabs.faces, index)
	}
	grow(&out.ids, len(out.faces))
	grow(&out.mapped, len(v.Glyphs))
	out.ids, out.mapped = out.ids[:len(out.faces)], out.mapped[:len(v.Glyphs)]
	out.desc, out.advance, out.valid = d, v.Advance, true
	return true
}

// Close releases the worker's state. Results already produced remain valid.
func (w *Shaper) Close() {
	if w.s != nil {
		w.s.Close()
		w.s = nil
	}
}

// FontDescription returns the normalized description of a live font. A
// released key is invalid until Font reuses it.
func (s *System) FontDescription(f FontKey) (FontDesc, bool) {
	if s.closed || int(f) >= len(s.fonts) || s.fonts[f].gone {
		return FontDesc{}, false
	}
	return s.fonts[f].desc, true
}

// CachedRun looks up a run without shaping. Like Shape, a hit marks the run
// recently used. The ref has the usual RunGeneration lifetime.
func (s *System) CachedRun(f FontKey, b []byte) (RunRef, bool) {
	if _, ok := s.FontDescription(f); !ok {
		return 0, false
	}
	return s.runs.lookup(f, b)
}

// ImportRun inserts a worker result without shaping. It rejects a released or
// reused font key whose description no longer matches. Call it on the System's
// thread before building a frame: insertion can evict runs (including the
// oversized slot), changing RunGeneration and invalidating earlier views.
// It copies the result, which may then be reused or closed. Zero means invalid
// input or a face that could not be imported; Shape remains the fallback.
func (s *System) ImportRun(f FontKey, r *ShapedRun) RunRef {
	d, ok := s.FontDescription(f)
	if !ok || r == nil || !r.valid || r.desc != d {
		return 0
	}
	if ref, ok := s.runs.lookup(f, r.slabs.text); ok {
		return ref
	}
	for i := range r.faces {
		id, ok := s.importFace(&r.faces[i], r.names)
		if !ok {
			return 0
		}
		r.ids[i] = id
	}
	for i, id := range r.slabs.faces {
		r.mapped[i] = r.ids[id]
	}
	return s.runs.insert(f, r.slabs.text, r.slabs.glyph, r.slabs.xs, r.mapped, r.slabs.clus, r.advance)
}
