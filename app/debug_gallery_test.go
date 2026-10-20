package app_test

import (
	"os"
	"testing"

	"gonativeui/app"
	"gonativeui/cmd/gallery/gallery"
	"gonativeui/platform"
)

// stepUntilIdle steps the loop until a step draws nothing and returns the
// frames that took.
func stepUntilIdle(t *testing.T, o *app.Offscreen) int {
	t.Helper()
	for n := 0; n < 1000; n++ {
		if !o.Step(16e6) {
			return n
		}
	}
	t.Fatal("the app never went idle")
	return 0
}

func causeNames(r app.FrameRecord) []string {
	var s []string
	for _, c := range r.Causes[:r.NCauses] {
		s = append(s, c.Kind.String())
	}
	return s
}

// The idle rule (ARCHITECTURE §3), checked through the overlay's repaint
// causes: the gallery at rest draws no frames, and what wakes it records why,
// each frame with a cause.
func TestIdleGalleryRecordsNoFrames(t *testing.T) {
	g, err := gallery.New(gallery.Options{Synthetic: true, Still: true, Rows: 1000})
	if err != nil {
		t.Fatal(err)
	}
	o, err := app.NewOffscreen(gallery.Config(), 2*gallery.WindowWidthPt, 2*gallery.WindowHeightPt, 2, g)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer o.Close()
	o.EnableDebug(true)

	if first := stepUntilIdle(t, o); first == 0 {
		t.Fatal("the first frame was not drawn")
	}
	if r, ok := o.DebugFrame(int(o.DebugFrames()) - 1); ok {
		first := false
		for _, c := range r.Causes[:r.NCauses] {
			first = first || c.Kind == app.CauseFirst
		}
		if !first {
			t.Errorf("the first frame's causes are %v", causeNames(r))
		}
	}
	recorded := o.DebugFrames()
	for i := 0; i < 600; i++ { // ten seconds of nothing
		if o.Step(16e6) {
			r, _ := o.DebugFrame(0)
			t.Fatalf("the idle gallery drew a frame (step %d, causes %v)", i, causeNames(r))
		}
	}
	if o.DebugFrames() != recorded {
		t.Errorf("%d frames recorded while idle", o.DebugFrames()-recorded)
	}

	// Each kind of wake draws and says why; then it is idle again.
	wake := func(name string, want app.Cause, ev platform.Event) {
		t.Helper()
		before := o.DebugCauseCount(want)
		if !o.Step(0, ev) {
			t.Fatalf("%s: no frame", name)
		}
		if o.DebugCauseCount(want) != before+1 {
			r, _ := o.DebugFrame(0)
			t.Errorf("%s: the frame's causes are %v, want %v", name, causeNames(r), want)
		}
		stepUntilIdle(t, o)
	}
	wake("pointer", app.CauseInput, platform.Event{Kind: platform.EvPointerMove, X: 900, Y: 500})
	wake("pointer again", app.CauseInput, platform.Event{Kind: platform.EvPointerMove, X: 1400, Y: 700})
	wake("task", app.CauseWake, platform.Event{Kind: platform.EvWake})
	wake("visibility", app.CauseVisible, platform.Event{Kind: platform.EvVisible, Flags: platform.FlagDown})
	wake("resize", app.CauseResize, platform.Event{Kind: platform.EvResize, W: 2400, H: 1500, Scale: 2})

	if n := o.DebugUnexplained(); n != 0 {
		t.Errorf("%d frames ran with no cause", n)
	}
	recorded = o.DebugFrames()
	for i := 0; i < 300; i++ {
		if o.Step(16e6) {
			t.Fatal("the gallery drew a frame after settling")
		}
	}
	if o.DebugFrames() != recorded {
		t.Error("frames recorded while idle again")
	}
}

// A steady-state frame of the gallery allocates nothing with the overlay on,
// the pointer resting over a button so that the outlines, the card and the
// inspector panel are all drawn. (The windowed gate, gallery -bench -gate with
// GNUI_DEBUG=overlay, runs the whole scripted session; its paste and its
// dialogs need a window.)
func TestGalleryFramesAllocateNothingWithTheOverlayOn(t *testing.T) {
	g, err := gallery.New(gallery.Options{Synthetic: true, Still: true, Rows: 1000})
	if err != nil {
		t.Fatal(err)
	}
	o, err := app.NewOffscreen(gallery.Config(), gallery.WindowWidthPt, gallery.WindowHeightPt, 1, g)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("offscreen rendering on CI: %v", err)
		}
		t.Skipf("offscreen rendering: %v", err)
	}
	defer o.Close()
	o.EnableDebug(true)
	o.SetInspectPanel(true)
	stepUntilIdle(t, o)
	o.Step(0, platform.Event{Kind: platform.EvPointerMove, X: 1100, Y: 200})
	stepUntilIdle(t, o)
	for i := 0; i < 120; i++ {
		o.Frame()
	}
	if n := testing.AllocsPerRun(30, func() { o.Frame() }); n != 0 {
		t.Errorf("%.1f allocations per frame of the gallery with the overlay on", n)
	}
}
