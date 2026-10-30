//go:build darwin || windows || linux

package text

// ShapeInfo shapes utf8 and returns its run, advance and cache generation.
// It avoids building a RunView when a caller needs only the width.
func (s *System) ShapeInfo(f FontKey, utf8 []byte) (RunRef, float32, uint32) {
	r := s.Shape(f, utf8)
	if r == 0 || int(r) > len(s.runs.ents) {
		return r, 0, s.runs.gen
	}
	return r, s.runs.ents[r-1].advance, s.runs.gen
}
