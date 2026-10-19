//go:build !darwin && !windows && !linux

package text

import "errors"

// System is the text system. Only the macOS backend exists so far; the
// Windows (DirectWrite) and Linux (go-text + fontconfig) backends come
// later, behind this same API.
type System struct{}

// New always fails on this OS.
func New() (*System, error) { return nil, errors.New("text: backend not implemented on this OS yet") }

// Font returns the zero FontKey on this OS.
func (s *System) Font(FontDesc) FontKey { return 0 }

// Metrics returns zero Metrics on this OS.
func (s *System) Metrics(FontKey) Metrics { return Metrics{} }

// Shape returns the invalid RunRef on this OS.
func (s *System) Shape(FontKey, []byte) RunRef { return 0 }

// Run returns an empty RunView on this OS.
func (s *System) Run(RunRef) RunView { return RunView{} }

// Glyph returns an empty GlyphEntry on this OS.
func (s *System) Glyph(FaceID, uint16, uint8) GlyphEntry { return GlyphEntry{} }

// RunGeneration is always 0 on this OS.
func (s *System) RunGeneration() uint32 { return 0 }

// AtlasGeneration is always 0 on this OS.
func (s *System) AtlasGeneration() uint32 { return 0 }

// Pages is always 0 on this OS.
func (s *System) Pages() int { return 0 }

// PageInfo returns zero values on this OS.
func (s *System) PageInfo(int) (w, h int, color bool) { return 0, 0, false }

// ClearDirty does nothing on this OS.
func (s *System) ClearDirty(int) {}

// Close does nothing on this OS.
func (s *System) Close() {}

// ReserveRuns does nothing on this OS.
func (s *System) ReserveRuns(runs, glyphs, textBytes int) {}

// Dirty reports no dirty region on this OS.
func (s *System) Dirty(int) (x, y, w, h int, pixels []byte, stride int, ok bool) {
	return
}

// Layout returns the invalid ParaRef on this OS.
func (s *System) Layout(*Para) ParaRef { return 0 }

// Paragraph returns an empty ParaView on this OS.
func (s *System) Paragraph(ParaRef) ParaView { return ParaView{} }

// ParaGeneration is always 0 on this OS.
func (s *System) ParaGeneration() uint32 { return 0 }

// ParaStats returns zero counters on this OS.
func (s *System) ParaStats() ParaStats { return ParaStats{} }

// ReserveParas does nothing on this OS.
func (s *System) ReserveParas(b ParaBudget) {}
