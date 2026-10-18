//go:build darwin

package app

import (
	"errors"
	"runtime"

	"gonativeui/platform"
)

// RunLiveResizeTest opens a real window and drags its size through a scripted
// sequence, entering and leaving AppKit's live-resize mode and setting each
// content size in turn exactly as a user dragging an edge does
// (platform.Window.SimulateLiveResize), so that every step reaches the app's
// frame handler. It first draws normal paced frames, then runs the drag, then
// checks that normal frames follow. The report says how many frames the
// handler drew and what they allocated. sizes are content sizes in points.
func RunLiveResizeTest(cfg Config, b Builder, sizes [][2]float64) (LiveResizeReport, error) {
	var st StartupReport
	a, err := openWindowed(cfg, b, &st)
	if err != nil {
		return LiveResizeReport{}, err
	}
	defer a.close()
	if len(sizes) == 0 {
		return LiveResizeReport{}, errors.New("app: no sizes")
	}

	var rep LiveResizeReport
	rep.Steps = len(sizes)
	inDrag := false
	var handlerNs, handlerMaxNs int64
	a.Win.SetFrameHandler(func() {
		t0 := platform.Now()
		before := a.frames
		a.frameHandler()
		if inDrag {
			rep.HandlerCalls++
			rep.Draws += a.frames - before
			if a.frames == before {
				rep.DrawFails++
			}
			d := platform.Now() - t0
			handlerNs += d
			handlerMaxNs = max(handlerMaxNs, d)
		}
	})

	const pacedWanted = 20
	rep.PacedWanted = pacedWanted
	phase, start := 0, platform.Now()
	var eventsAtPhase, lastFrames int
	a.hook = func() {
		if phase == 2 && a.frames > lastFrames { // frames drawn since the last pass: on a frame event or by the fallback
			if a.p.paced {
				rep.PacedFramesAfter += a.frames - lastFrames
			} else {
				rep.FallbackFramesAfter += a.frames - lastFrames
			}
		}
		lastFrames = a.frames
		switch phase {
		case 0: // paced frames first, so the CAMetalDisplayLink is attached
			if a.frames >= 12 || platform.Now()-start > int64(3e9) { // an occluded window gets no ticks
				phase = 1
				platform.Wake() // the loop may be blocked in Wait: come round again
				break
			}
			a.p.invalidate()
		case 1:
			rep.FrameEventsBefore = a.frameEvents
			var m0, m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m0)
			inDrag = true
			a.Win.SimulateLiveResize(sizes)
			inDrag = false
			runtime.ReadMemStats(&m1)
			rep.Mallocs, rep.TotalAlloc = m1.Mallocs-m0.Mallocs, m1.TotalAlloc-m0.TotalAlloc
			w, h, _ := a.Win.Size()
			rep.FinalPx = [2]int32{w, h}
			phase, eventsAtPhase, start = 2, a.frameEvents, platform.Now()
			lastFrames = a.frames
			a.p.invalidate()
		case 2: // frames after the drag must be paced again (the metal link is back)
			if rep.PacedFramesAfter >= pacedWanted || platform.Now()-start > int64(3e9) {
				rep.FrameEventsAfter = a.frameEvents - eventsAtPhase
				a.done = true
				break
			}
			a.p.invalidate()
		}
	}
	if err := a.loop(); err != nil {
		return rep, err
	}
	if rep.HandlerCalls > 0 {
		rep.HandlerMsMean = float64(handlerNs) / 1e6 / float64(rep.HandlerCalls)
		rep.HandlerMsMax = float64(handlerMaxNs) / 1e6
	}
	rep.Pacing = livePacingVerdict(rep.FrameEventsBefore, rep.PacedFramesAfter, pacedWanted)
	if rep.Draws > 0 {
		rep.MallocsPerDraw = float64(rep.Mallocs) / float64(rep.Draws)
		rep.BytesPerDraw = float64(rep.TotalAlloc) / float64(rep.Draws)
	}
	return rep, nil
}
