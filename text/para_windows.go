//go:build windows

package text

// Paragraphs on this backend use the basic layout (para_basic.go) over the
// System's own Shape: D42's fallback for this phase.

func (s *System) layouter() *BasicLayouter {
	if s.plays == nil {
		s.plays = NewBasicLayouter(s)
	}
	return s.plays
}

// Layout lays out a paragraph, or finds it in the paragraph cache (a hit
// allocates nothing), and returns its ref. This backend uses the basic layout
// (see BasicLayouter): style runs placed left to right, lines broken at the
// opportunities of UAX #14.
func (s *System) Layout(p *Para) ParaRef { return s.layouter().Layout(p) }

// Paragraph returns the layout r. The view is valid until the next eviction of
// the paragraph cache (see ParaGeneration).
func (s *System) Paragraph(r ParaRef) ParaView { return s.layouter().Paragraph(r) }

// ParaGeneration changes whenever the paragraph cache evicts.
func (s *System) ParaGeneration() uint32 { return s.layouter().ParaGeneration() }

// ParaStats returns the paragraph cache's counters.
func (s *System) ParaStats() ParaStats { return s.layouter().ParaStats() }

// ReserveParas pre-sizes the paragraph cache and the layout scratch; see the
// macOS System.ReserveParas.
func (s *System) ReserveParas(paras, glyphs, textBytes int) {
	s.layouter().ReserveParas(paras, glyphs, textBytes)
}
