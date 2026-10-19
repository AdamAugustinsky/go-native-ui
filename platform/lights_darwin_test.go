//go:build darwin

package platform

import (
	"fmt"
	"math"
	"testing"
	"time"

	"gonativeui/internal/objc"
)

// lightsSeen is what AppKit says about the window buttons after an event, in
// window coordinates from the top left.
type lightsSeen struct {
	centre   [3][2]float64
	union    [4]float64 // the three buttons' frames: x0, y0, x1, y1
	sys      [4]float32 // SystemButtons
	hover    [4]float64 // the theme frame's hover area for the button group
	hoverOK  bool       // found, and active whether or not the window is key
	zoomArea [2]float64 // centre of the zoom button's own tracking area (its tiling menu)
	hit      [3]bool    // AppKit's hit test at each centre finds the button
	drag     [3]bool    // a press at each centre would drag the window (our drag map)
	inGroup  [2]int     // _isWindowPointInStandardWindowButtonGroup: at the close button / at AppKit's default place, -1 unknown
	fs       bool
}

var (
	selTrackingAreas = objc.Sel("trackingAreas")
	selTrackRect     = objc.Sel("rect")
	selTrackOwner    = objc.Sel("owner")
	selTrackOptions  = objc.Sel("options")
	selTestCount     = objc.Sel("count")
	selTestObjectAt  = objc.Sel("objectAtIndex:")
	selCell          = objc.Sel("cell")
	selInGroup       = objc.Sel("_isWindowPointInStandardWindowButtonGroup:") // private, checked
)

func seeLights(w *Window) (s lightsSeen) {
	wf := objc.SendRect(w.win, selFrame)
	s.fs = objc.Send(w.win, selStyleMask)&(1<<14) != 0
	s.sys = w.SystemButtons()
	s.union = [4]float64{math.MaxFloat64, math.MaxFloat64, -math.MaxFloat64, -math.MaxFloat64}
	var btn [3]objc.ID
	for i := range btn {
		btn[i] = objc.Send1(w.win, selStdButton, uintptr(i))
		f := objc.SendRect(btn[i], selFrame)
		s.centre[i][0], s.centre[i][1] = w.buttonCentre(btn[i])
		x0, y0 := s.centre[i][0]-f.W/2, s.centre[i][1]-f.H/2
		s.union = [4]float64{min(s.union[0], x0), min(s.union[1], y0), max(s.union[2], x0+f.W), max(s.union[3], y0+f.H)}
	}
	frame := objc.Send(w.content, selSuperview)
	areas := objc.Send(frame, selTrackingAreas)
	for i := 0; i < int(objc.Send(areas, selTestCount)); i++ {
		a := objc.Send1(areas, selTestObjectAt, uintptr(i))
		r := objc.SendRect(a, selTrackRect)
		opts, owner := uintptr(objc.Send(a, selTrackOptions)), objc.Send(a, selTrackOwner)
		switch {
		case owner == frame && opts&0x01 != 0: // NSTrackingMouseEnteredAndExited: the group's hover
			s.hover = [4]float64{r.X, wf.H - r.Y - r.H, r.X + r.W, wf.H - r.Y}
			s.hoverOK = opts&0x80 != 0 // NSTrackingActiveAlways: inactive windows show the glyphs too
		case owner == objc.Send(btn[2], selCell):
			s.zoomArea = [2]float64{r.X + r.W/2, wf.H - r.Y - r.H/2}
		}
	}
	for i := range btn {
		x, y := s.centre[i][0], wf.H-s.centre[i][1]
		hit := objc.ID(objc.F.Reset().Arg(uintptr(frame)).Arg(uintptr(selHitTest)).ArgPoint(x, y).Call(objc.MsgSend).R0())
		for v := hit; v != 0; v = objc.Send(v, selSuperview) {
			if v == btn[i] {
				s.hit[i] = true
			}
		}
		s.drag[i] = w.DebugDragAt(float32(s.centre[i][0])*w.scale, float32(s.centre[i][1])*w.scale)
	}
	s.inGroup = [2]int{-1, -1}
	if objc.SendBool1(w.win, selRespondsTo, uintptr(selInGroup)) {
		for i, p := range [2][2]float64{s.centre[0], {10, 10}} {
			if objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selInGroup)).ArgPoint(p[0], wf.H-p[1]).Call(objc.MsgSend).Bool() {
				s.inGroup[i] = 1
			} else {
				s.inGroup[i] = 0
			}
		}
	}
	return s
}

func near(a, b float64) bool { return math.Abs(a-b) <= 0.5 }

// checkLights reports how s differs from buttons centred on want (the close
// button; the others at AppKit's spacing), or "" if it does not.
func checkLights(s lightsSeen, want [2]float64, space float64) string {
	if s.fs {
		if s.sys != [4]float32{} {
			return fmt.Sprintf("in full screen SystemButtons is %v, want empty", s.sys)
		}
		return ""
	}
	for i, c := range s.centre {
		if !near(c[0], want[0]+float64(i)*space) || !near(c[1], want[1]) {
			return fmt.Sprintf("button centres %v, want (%v, %v) spaced %v", s.centre, want[0], want[1], space)
		}
	}
	for i := range s.sys {
		if !near(float64(s.sys[i]), s.union[i]) {
			return fmt.Sprintf("SystemButtons %v, the buttons are at %v", s.sys, s.union)
		}
	}
	for i := range s.hover {
		if !near(s.hover[i], s.union[i]) {
			return fmt.Sprintf("the group's hover area is %v, the buttons are at %v: the glyphs would show elsewhere", s.hover, s.union)
		}
	}
	if !s.hoverOK {
		return "the group's hover area is missing or works only in the key window"
	}
	if !near(s.zoomArea[0], s.centre[2][0]) || !near(s.zoomArea[1], s.centre[2][1]) {
		return fmt.Sprintf("the zoom button's tracking area is centred at %v, the button at %v", s.zoomArea, s.centre[2])
	}
	if s.hit != [3]bool{true, true, true} || s.drag != [3]bool{} {
		return fmt.Sprintf("at the buttons' centres AppKit's hit test finds them %v and our drag map drags %v", s.hit, s.drag)
	}
	if outside := s.union[0] > 10 || s.union[1] > 10; s.inGroup[0] == 0 || (outside && s.inGroup[1] == 1) {
		return fmt.Sprintf("AppKit's button group contains the close button's centre: %v, an empty spot at (10, 10): %v", s.inGroup[0] == 1, s.inGroup[1] == 1)
	}
	return ""
}

// pumpFor runs the window's event loop for d.
func pumpFor(w *Window, d time.Duration) {
	for end := time.Now().Add(d); time.Now().Before(end); {
		w.Wait(int64(10 * time.Millisecond))
	}
}

// pumpUntil runs the event loop until ok or the timeout, and reports ok.
func pumpUntil(w *Window, timeout time.Duration, ok func() bool) bool {
	for end := time.Now().Add(timeout); time.Now().Before(end); {
		if ok() {
			return true
		}
		w.Wait(int64(10 * time.Millisecond))
	}
	return ok()
}

// lightsEvents runs the events AppKit lays the titlebar out again on and
// calls check after each (on the main thread).
func lightsEvents(w *Window, check func(step string)) {
	pool := objc.F.Call0(objc.PoolPush)
	defer objc.F.Call1(objc.PoolPop, pool)
	app := objc.Send(objc.Cls("NSApplication"), selSharedApplication)
	center := objc.Send(objc.Cls("NSNotificationCenter"), objc.Sel("defaultCenter"))
	appKit, _ := loadFramework("/System/Library/Frameworks/AppKit.framework/AppKit")
	post := func(name string) {
		objc.Send2(center, objc.Sel("postNotificationName:object:"), uintptr(stringConstant(appKit, name)), uintptr(app))
	}
	appearance := func(name string) objc.ID {
		return objc.Send1(objc.Cls("NSAppearance"), objc.Sel("appearanceNamed:"), uintptr(objc.Str(name)))
	}
	isMini, demini, setAppearance := objc.Sel("isMiniaturized"), objc.Sel("deminiaturize:"), objc.Sel("setAppearance:")
	fullScreen := func() bool { return objc.Send(w.win, selStyleMask)&(1<<14) != 0 }
	step := func(name string, d time.Duration, f func()) {
		f()
		pumpFor(w, d)
		check(name)
	}
	step("open", 100*time.Millisecond, func() {})
	step("setTitle:", 50*time.Millisecond, func() { objc.Send1(w.win, selSetTitle, uintptr(objc.Str("another title"))) })
	step("resignKeyWindow", 50*time.Millisecond, func() { objc.Send(w.win, objc.Sel("resignKeyWindow")) })
	step("makeKeyAndOrderFront:", 50*time.Millisecond, func() { objc.Send1(w.win, selMakeKeyFront, 0) })
	step("app deactivated", 50*time.Millisecond, func() {
		objc.Send(app, objc.Sel("deactivate"))
		post("NSApplicationWillResignActiveNotification")
		post("NSApplicationDidResignActiveNotification")
	})
	step("app activated", 50*time.Millisecond, func() {
		post("NSApplicationWillBecomeActiveNotification")
		post("NSApplicationDidBecomeActiveNotification")
		objc.Send(app, selActivate)
	})
	step("dark appearance", 50*time.Millisecond, func() { objc.Send1(w.win, setAppearance, uintptr(appearance("NSAppearanceNameDarkAqua"))) })
	step("system appearance", 50*time.Millisecond, func() { objc.Send1(w.win, setAppearance, 0) })
	step("setContentSize:", 50*time.Millisecond, func() {
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetContentSize)).ArgPoint(640, 380).Call(objc.MsgSend)
	})
	step("live resize", 50*time.Millisecond, func() { w.SimulateLiveResize([][2]float64{{600, 360}, {560, 340}, {620, 370}}) })
	step("zoom:", 800*time.Millisecond, func() { objc.Send1(w.win, selZoom, 0) })
	step("zoom: back", 800*time.Millisecond, func() { objc.Send1(w.win, selZoom, 0) })
	objc.Send1(w.win, selMiniaturize, 0)
	if pumpUntil(w, 3*time.Second, func() bool { return objc.SendBool(w.win, isMini) }) {
		objc.Send1(w.win, demini, 0)
		pumpUntil(w, 3*time.Second, func() bool { return !objc.SendBool(w.win, isMini) })
		step("deminiaturize:", 300*time.Millisecond, func() {})
	} else {
		objc.Send1(w.win, demini, 0)
		step("miniaturize: (refused here)", 300*time.Millisecond, func() {})
	}
	// Full screen from the run loop's next turn, as a click would arrive. A
	// window that cannot go full screen here (a locked screen, an inactive
	// test process) fails to enter, which is a path of its own: AppKit
	// dropped the titlebar height when the attempt started.
	w.DebugToggleFullScreen()
	pumpFor(w, 3*time.Second)
	if fullScreen() {
		check("in full screen")
		w.DebugToggleFullScreen()
		pumpUntil(w, 5*time.Second, func() bool { return !fullScreen() && !w.fullScreen })
		step("out of full screen", 300*time.Millisecond, func() {})
	} else {
		check("failed full-screen entry")
	}
	step("setTitle: after all", 50*time.Millisecond, func() { objc.Send1(w.win, selSetTitle, uintptr(objc.Str("last title"))) })
}

// lightsNativeHere reports whether AppKit has the titlebar metrics the
// buttons are placed with.
func lightsNativeHere(w *Window) bool {
	for _, s := range []objc.SEL{selSetTitlebarHeight, selTitlebarHeight, selSetButtonGroupOffset, selButtonGroupOffset} {
		if !objc.SendBool1(w.win, selRespondsTo, uintptr(s)) {
			return false
		}
	}
	return true
}

// The window buttons stay centred where Options.TrafficLights says, with
// their hover area, their hit test and SystemButtons on them, through every
// event AppKit lays the titlebar out again on. Before, the first such layout
// (a title change, key, appearance, a failed full-screen entry) put them back
// at AppKit's default or cut off at the top, and the hover area never left
// AppKit's default place.
func TestWindowButtonsHoldTheirPlace(t *testing.T) {
	want := [2]float64{21, 22}
	var opened, native bool
	var fails, steps []string
	var allocs float64
	onMain(func() {
		w, err := Open(Options{Title: "lights", Width: 500, Height: 300, Titlebar: 46, TrafficLights: [2]float32{21, 22}})
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		native = lightsNativeHere(w)
		w.SetDragRegions([]DragRegion{{Rect: [4]float32{0, 0, 500 * w.scale, 46 * w.scale}}}) // the whole band drags
		space := 0.0
		lightsEvents(w, func(step string) {
			steps = append(steps, step)
			s := seeLights(w)
			if space == 0 {
				space = s.centre[1][0] - s.centre[0][0]
				if !native {
					want = s.centre[0] // where AppKit put them: they must stay there
				}
			}
			if msg := checkLights(s, want, space); msg != "" {
				fails = append(fails, step+": "+msg)
			}
		})
		titles := [2]objc.ID{objc.Retain(objc.Str("one")), objc.Retain(objc.Str("two"))}
		n := 0
		allocs = testing.AllocsPerRun(20, func() { // a title change: AppKit's layout, the observer, layoutChrome
			n++
			objc.Send1(w.win, selSetTitle, uintptr(titles[n&1]))
			w.layoutChrome()
		})
		objc.Release(titles[0])
		objc.Release(titles[1])
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	t.Logf("checked after %q", steps)
	if !native {
		t.Logf("AppKit has no titlebar metrics here: the buttons stay at its default %v", want)
	}
	for _, f := range fails {
		t.Error(f)
	}
	if allocs != 0 {
		t.Errorf("keeping the buttons in place over a title change allocates %.1f times", allocs)
	}
}

// Where AppKit has no titlebar metrics, the buttons stay where AppKit puts
// them, working as AppKit's own, and SystemButtons follows them.
func TestWindowButtonsDefaultWhereNoMetrics(t *testing.T) {
	var opened bool
	var fails []string
	var at [2]float64
	onMain(func() {
		lightsForceDefault = true
		defer func() { lightsForceDefault = false }()
		w, err := Open(Options{Title: "lights default", Width: 400, Height: 260, Titlebar: 46, TrafficLights: [2]float32{21, 22}})
		if err != nil {
			return
		}
		defer w.Close()
		opened = true
		pumpFor(w, 100*time.Millisecond)
		s := seeLights(w)
		at = s.centre[0]
		space := s.centre[1][0] - s.centre[0][0]
		check := func(step string) {
			if msg := checkLights(seeLights(w), at, space); msg != "" {
				fails = append(fails, step+": "+msg)
			}
		}
		check("open")
		objc.Send1(w.win, selSetTitle, uintptr(objc.Str("another title")))
		pumpFor(w, 50*time.Millisecond)
		check("setTitle:")
		objc.F.Reset().Arg(uintptr(w.win)).Arg(uintptr(selSetContentSize)).ArgPoint(520, 300).Call(objc.MsgSend)
		pumpFor(w, 50*time.Millisecond)
		check("setContentSize:")
	})
	if !opened {
		t.Skip("cannot open a window here")
	}
	if near(at[0], 21) && near(at[1], 22) {
		t.Errorf("without the metrics the close button is still at (21, 22): the fallback moved it")
	}
	for _, f := range fails {
		t.Error(f)
	}
}
