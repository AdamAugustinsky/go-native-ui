//go:build !darwin && !windows && !linux

package text

import "errors"

// System is the text system. Only the macOS backend exists so far; the
// Windows (DirectWrite) and Linux (go-text + fontconfig) backends come
// later, behind this same API.
type System struct{}

// New always fails on this OS.
func New() (*System, error) { return nil, errors.New("text: backend not implemented on this OS yet") }

func (s *System) Font(FontDesc) FontKey                   { return 0 }
func (s *System) Metrics(FontKey) Metrics                 { return Metrics{} }
func (s *System) Shape(FontKey, []byte) RunRef            { return 0 }
func (s *System) Run(RunRef) RunView                      { return RunView{} }
func (s *System) Glyph(FaceID, uint16, uint8) GlyphEntry  { return GlyphEntry{} }
func (s *System) RunGeneration() uint32                   { return 0 }
func (s *System) AtlasGeneration() uint32                 { return 0 }
func (s *System) Pages() int                              { return 0 }
func (s *System) PageInfo(int) (w, h int, color bool)     { return 0, 0, false }
func (s *System) ClearDirty(int)                          {}
func (s *System) Close()                                  {}
func (s *System) ReserveRuns(runs, glyphs, textBytes int) {}
func (s *System) Dirty(int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return
}
