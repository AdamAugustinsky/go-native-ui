//go:build darwin || windows || linux

package text

// RunCapacity reports retained run, glyph and text-byte capacities. It does
// not include native font objects or paragraph caches. Call on the UI thread.
func (s *System) RunCapacity() (runs, glyphs, textBytes int) {
	c := s.runs
	if c == nil {
		return
	}
	return cap(c.ents), cap(c.glyph) + cap(c.over.glyph), cap(c.text) + cap(c.over.text)
}
