// Package winstate remembers where a window was and puts it back: its frame,
// whether it was maximised or full screen, the screen it was on, and a few
// bytes the application wants kept with it (the open tab, the sidebar width).
//
//	st, err := winstate.Load(appID)            // before the window exists; reads one small file
//	// err reports an unreadable or corrupt file; st still works, on the defaults.
//	defer st.Close()                           // at quit: writes what is pending
//
//	pl := winstate.Apply(winstate.Config{Width: 900, Height: 640, MinWidth: 480, MinHeight: 320},
//		st.State(), screens)                   // screens from the platform, see Screen
//	// open the window with pl.Frame (when pl.Restored), then maximise or enter full screen
//
//	st.Save(winstate.State{Frame: frame, Maximised: max, Fullscreen: full, Screen: here, App: mine})
//	// on every move, resize and change: debounced, written on a worker
//
// The state is the framework's own file ($config/window-main.json, written
// atomically by app/prefs), not NSWindow's frame autosave: the same rule on
// every OS, readable before the window exists, carrying the application's bytes
// with it, and with a clamp to the screens that exist now.
//
// # Coordinates
//
// Rectangles are in the units of the platform's window frame, in one space for
// all screens, with the origin at the top-left of the primary screen and y
// growing downwards. The unit differs by OS:
//
//   - macOS: points (the logical units of Config.Width and Height);
//   - Linux: points too (no screens are read yet, so only the size is kept);
//   - Windows: physical pixels (the process is DPI-aware), which are points
//     times the display's scale.
//
// The frame is the whole window, title bar included. platform.Screens returns
// the screens in this form.
//
// App code that has lengths in points (app.App.ContentSize, Config.Width and
// Height) converts them with FromPoints and ToPoints, which know the unit of the
// OS, and never multiplies by a scale by hand: at 200 % on Windows a size
// saved in points is half the window.
//
// The frame is never in a window's zoomed ui points: with app zoom
// (app.App.SetZoom) the window keeps its size and only its content scales, so
// the frame is unchanged by the zoom, which is kept in State.Zoom. Derive a
// frame from app.App.ContentSize, not from the ui's extent.
//
// # What Apply does
//
//   - A frame that is on a screen stays where it was; one that sticks out of the
//     screen's visible area (past the menu bar, the Dock, the taskbar) is moved
//     in, and one larger than the visible area is made to fit.
//   - A frame that is no longer on any screen (a monitor was unplugged, the
//     resolution fell) is centred on the screen it was saved on, if that still
//     exists, and otherwise on the primary screen (screens[0]).
//   - A maximised or full-screen window is reported on the screen it was saved
//     on, if that exists, with the frame kept as the size to go back to.
//   - A state that is missing, from an older format, or not sensible (no size, not
//     a number) is ignored: Placement.Restored is false and the application uses its
//     own defaults.
//   - With no screens known (Linux today) the saved size is kept and the position
//     is left to the system (Placement.HasPosition is false).
//
// # Delivery
//
// Nothing here runs per frame and nothing blocks the UI thread: Load is one file
// read at startup, Save changes memory and schedules a write on a worker (prefs),
// and Apply is arithmetic. With the multi-window work (F4), a window's name
// selects its file (Options.Name) and the screens come from the application
// object.
package winstate

import (
	"errors"
	"math"
	"runtime"
)

// Rect is a rectangle: top-left corner and size.
type Rect struct {
	// X and Y are the top-left corner.
	X float64 `json:"x"`
	Y float64 `json:"y"`
	// W and H are the width and the height.
	W float64 `json:"w"`
	H float64 `json:"h"`
}

func (r Rect) right() float64  { return r.X + r.W }
func (r Rect) bottom() float64 { return r.Y + r.H }

// intersect is the overlap of two rectangles; the zero Rect when they do not
// overlap.
func (r Rect) intersect(o Rect) Rect {
	x, y := math.Max(r.X, o.X), math.Max(r.Y, o.Y)
	w, h := math.Min(r.right(), o.right())-x, math.Min(r.bottom(), o.bottom())-y
	if w <= 0 || h <= 0 {
		return Rect{}
	}
	return Rect{x, y, w, h}
}

func (r Rect) finite() bool {
	for _, v := range [...]float64{r.X, r.Y, r.W, r.H} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return false
		}
	}
	return true
}

// Screen is one display as the caller found it.
type Screen struct {
	// ID names the display so a saved state can find it again ("" if the
	// platform cannot tell displays apart).
	ID string `json:"id,omitempty"`
	// Frame is the whole display.
	Frame Rect `json:"frame"`
	// Visible is the part windows can use: the frame without the menu bar, the
	// Dock or the taskbar.
	Visible Rect `json:"visible"`
	// Scale is the display's pixels per unit (2 on a Retina display); it is
	// informational here.
	Scale float64 `json:"scale,omitempty"`
}

// State is what is remembered about a window.
type State struct {
	// Frame is the window's frame when it is neither maximised nor full
	// screen: the size to go back to.
	Frame Rect `json:"frame"`
	// Maximised and Fullscreen say how the window was when it was saved.
	Maximised  bool `json:"maximised,omitempty"`
	Fullscreen bool `json:"fullscreen,omitempty"`
	// Screen is the display the window was on.
	Screen Screen `json:"screen"`
	// App is kept with the window and returned unchanged: the application's
	// own bytes (at most MaxApp).
	App []byte `json:"app,omitempty"`
	// Zoom is the window's app zoom factor (app.App.Zoom); 0 means 1. Frame is
	// in screen points whatever the zoom: it is never multiplied or divided
	// by it, so a window restored at 150 % opens at the size it was saved at.
	Zoom float64 `json:"zoom,omitempty"`
}

// FromPoints converts a length in points (app.App.ContentSize, the Width and
// Height of app.Config) to the unit of Frame on this OS, for a display of the
// given scale (the pixels per point app.App.Size reports): the same number on
// macOS and Linux, the number of physical pixels on Windows.
func FromPoints(pt, scale float64) float64 { return pt * unitsPerPoint(runtime.GOOS, scale) }

// ToPoints is the inverse of FromPoints: a length of Frame (a Placement's) as
// points for app.Config.
func ToPoints(units, scale float64) float64 { return units / unitsPerPoint(runtime.GOOS, scale) }

// unitsPerPoint is how many units of Frame make a point on goos at a display scale.
func unitsPerPoint(goos string, scale float64) float64 {
	if goos == "windows" && scale > 0 && !math.IsInf(scale, 0) {
		return scale
	}
	return 1
}

// ZoomFactor is Zoom as a factor to give app.App.SetZoom: 1 when none is saved
// or the saved one is not a positive number.
func (s State) ZoomFactor() float64 {
	if s.Zoom > 0 && !math.IsInf(s.Zoom, 0) {
		return s.Zoom
	}
	return 1
}

// MaxApp is the most application bytes a State may carry.
const MaxApp = 256 << 10

// ErrInvalid is returned by Save for a state that cannot be a window's (no
// size, a coordinate that is not a number, a size beyond any display).
var ErrInvalid = errors.New("winstate: not a usable window state")

// ErrTooLarge is returned by Save when State.App is longer than MaxApp.
var ErrTooLarge = errors.New("winstate: application bytes too large")

// maxExtent bounds coordinates and sizes: far beyond any display, small enough
// that arithmetic on them is exact.
const maxExtent = 1 << 20

// Valid reports whether the state describes a window: a frame with a positive
// size and finite numbers within limits. Apply ignores a state that is not.
func (s State) Valid() bool {
	f := s.Frame
	return !math.IsNaN(s.Zoom) && !math.IsInf(s.Zoom, 0) && f.finite() && f.W > 0 && f.H > 0 && f.W <= maxExtent && f.H <= maxExtent &&
		math.Abs(f.X) <= maxExtent && math.Abs(f.Y) <= maxExtent
}

// Config is what the application wants when nothing is saved, and the limits
// it needs, in the same units as the frames.
type Config struct {
	// Width and Height are the default window size.
	Width, Height float64
	// MinWidth and MinHeight bound the restored size from below (the window
	// system enforces the exact minimum); 0 means no bound.
	MinWidth, MinHeight float64
}

// Placement is where to put the window.
type Placement struct {
	// Restored is true when a saved state was used. When false, open the
	// window with the application's defaults and let the system place it.
	Restored bool
	// Frame is the frame to open the window with: with the saved size, kept
	// on a screen. When Maximised or Fullscreen is set it is the frame to
	// go back to.
	Frame Rect
	// HasPosition is false when Frame.X and Frame.Y mean nothing (no screens
	// were known): use only the size.
	HasPosition bool
	// Maximised and Fullscreen repeat the saved state.
	Maximised, Fullscreen bool
	// Screen is the index in the screens given to Apply of the screen to put
	// the window on (and to maximise or fill), or -1 when none is known.
	Screen int
	// Adjusted is true when Frame differs from the saved one: it was moved,
	// shrunk or centred to fit the screens that exist.
	Adjusted bool
}

// Apply decides where a window goes: the saved state st, made to fit the
// screens now present (screens[0] is the primary one). See the package comment
// for the rules. It does no I/O.
func Apply(cfg Config, st State, screens []Screen) Placement {
	pl := Placement{Screen: -1}
	if !st.Valid() {
		return pl
	}
	pl.Restored = true
	pl.Maximised, pl.Fullscreen = st.Maximised, st.Fullscreen
	f := st.Frame
	f.W, f.H = math.Max(f.W, cfg.MinWidth), math.Max(f.H, cfg.MinHeight)

	// Screens that have somewhere to put a window.
	var idx []int
	for i, s := range screens {
		if v := s.Visible; v.finite() && v.W > 0 && v.H > 0 {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		pl.Frame = f
		pl.Adjusted = f != st.Frame
		return pl
	}
	pl.HasPosition = true

	saved := -1 // the screen the state was saved on, if it is still there
	if st.Screen.ID != "" {
		for _, i := range idx {
			if screens[i].ID == st.Screen.ID {
				saved = i
				break
			}
		}
	}
	// The screen the frame is mostly on.
	best, bestArea := -1, 0.0
	for _, i := range idx {
		o := f.intersect(screens[i].Visible)
		if a := o.W * o.H; a > bestArea || (a == bestArea && a > 0 && i == saved) {
			best, bestArea = i, a
		}
	}
	// "On a screen" means enough of the window is there to grab it.
	onScreen := false
	if best >= 0 {
		o := f.intersect(screens[best].Visible)
		onScreen = o.W >= math.Min(f.W, 160) && o.H >= math.Min(f.H, 100)
	}
	target := best
	switch {
	case !onScreen:
		target = saved
		if target < 0 {
			target = idx[0]
		}
	case (st.Maximised || st.Fullscreen) && saved >= 0:
		target = saved
	}
	vis := screens[target].Visible
	f.W, f.H = math.Min(f.W, vis.W), math.Min(f.H, vis.H)
	if onScreen {
		f.X = clamp(f.X, vis.X, vis.right()-f.W)
		f.Y = clamp(f.Y, vis.Y, vis.bottom()-f.H)
	} else {
		f.X, f.Y = vis.X+(vis.W-f.W)/2, vis.Y+(vis.H-f.H)/2
	}
	pl.Frame, pl.Screen = f, target
	pl.Adjusted = f != st.Frame
	return pl
}

func clamp(v, lo, hi float64) float64 {
	if hi < lo { // the window is larger than the room: pin to the top-left
		return lo
	}
	return math.Min(math.Max(v, lo), hi)
}
