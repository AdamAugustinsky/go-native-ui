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
	FinalPx       [2]int32 // the surface size after the drag
	// PacedFramesAfter counts the normal-path frames drawn after the drag
	// (20 wanted): the CAMetalDisplayLink is attached again.
	PacedFramesAfter int
}
