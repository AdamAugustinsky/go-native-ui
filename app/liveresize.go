package app

// LiveResizeReport is the result of RunLiveResizeTest.
type LiveResizeReport struct {
	Steps        int // sizes set
	HandlerCalls int // times the platform called the frame handler during the drag
	Draws        int // frames the handler drew
	DrawFails    int // handler calls that found no frame to draw into

	// Heap allocations over the whole drag (the handler's events, build, encode
	// and present, and the size changes), exact, and per frame drawn.
	Mallocs        uint64
	TotalAlloc     uint64
	MallocsPerDraw float64
	BytesPerDraw   float64

	HandlerMsMean float64  // mean time in the handler per call: events, build, draw
	HandlerMsMax  float64  // the slowest call (a starved drawable pool shows as ~1000 ms)
	FinalPx       [2]int32 // the surface size after the drag

	// Pacing after the drag. The platform's frame source (the
	// CAMetalDisplayLink) is replaced during a live resize and attached again
	// afterwards; "resumed" means it delivers frame events again. Frames drawn
	// by the loop's unpaced fallback (it draws without a frame event after a
	// second of silence: asleep display, occluded window) say nothing about
	// that, so they are counted apart.
	FrameEventsBefore   int // frame events during the warm-up before the drag
	FrameEventsAfter    int // frame events during the checking period after it
	PacedFramesAfter    int // frames drawn on a frame event after the drag (PacedWanted wanted)
	FallbackFramesAfter int // frames drawn by the unpaced fallback after the drag
	PacedWanted         int
	Pacing              string // PacingResumed, PacingFailed or PacingInconclusive
}

// Pacing verdicts of a LiveResizeReport.
const (
	PacingResumed      = "resumed"      // the frame source delivered PacedWanted frames after the drag
	PacingFailed       = "failed"       // it delivered frames before the drag but not enough after it
	PacingInconclusive = "inconclusive" // it never delivered a frame, even before the drag (asleep display, occluded window): the fallback drew everything
)

// LiveResizeMaxHandlerMs is the slowest handler call a live resize may have:
// one step must never stall the drag (a starved drawable pool blocks 1 s).
const LiveResizeMaxHandlerMs = 250

// livePacingVerdict decides the Pacing of a report from its counts.
func livePacingVerdict(before, pacedAfter, wanted int) string {
	switch {
	case before == 0:
		return PacingInconclusive
	case pacedAfter >= wanted:
		return PacingResumed
	}
	return PacingFailed
}
