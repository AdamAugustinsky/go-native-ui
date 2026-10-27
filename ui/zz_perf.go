package ui

// THROWAWAY perf-survey counters (never commit).

import (
	"fmt"

	"gonativeui/core"
)

type perfCounters struct {
	frames, boxes, newBoxes, probes, getCalls, layoutVisits, childVisits, emitVisits, quads, hitVisits, glyphs, textBoxes, shapes, auditCalls, signals, peekSignals, tOf uint64
}

var perf perfCounters

func PerfReset() { perf = perfCounters{}; core.PerfGets, core.PerfProbes = 0, 0 }

func PerfReport(frames int) string {
	f := float64(max(frames, 1))
	p := &perf
	p.getCalls, p.probes = core.PerfGets, core.PerfProbes
	return fmt.Sprintf("frames=%d endFrames=%d boxes/f=%.1f newBoxes/f=%.2f tableGets/f=%.1f probes/f=%.1f sizeChildWalks/f=%.1f layoutOrderVisits/f=%.1f emitVisits/f=%.1f hitVisits/f=%.1f quads/f=%.1f glyphQuads/f=%.1f textBoxes/f=%.1f shapes/f=%.1f signals/f=%.1f peek/f=%.1f tOf/f=%.1f",
		frames, p.frames, float64(p.boxes)/f, float64(p.newBoxes)/f, float64(p.getCalls)/f, float64(p.probes)/f, float64(p.childVisits)/f, float64(p.layoutVisits)/f, float64(p.emitVisits)/f, float64(p.hitVisits)/f, float64(p.quads)/f, float64(p.glyphs)/f, float64(p.textBoxes)/f, float64(p.shapes)/f, float64(p.signals)/f, float64(p.peekSignals)/f, float64(p.tOf)/f)
}
