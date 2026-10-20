package winstate

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"gonativeui/app/prefs"
	"gonativeui/platform"
)

// A laptop display with a menu bar (24 high) and a Dock at the bottom (60 high),
// and an external display to its right and, optionally, one above.
func laptop() Screen {
	return Screen{ID: "laptop", Frame: Rect{0, 0, 1440, 900}, Visible: Rect{0, 24, 1440, 816}, Scale: 2}
}
func external() Screen {
	return Screen{ID: "external", Frame: Rect{1440, -180, 2560, 1440}, Visible: Rect{1440, -180, 2560, 1440}, Scale: 1}
}

var cfg = Config{Width: 900, Height: 640, MinWidth: 480, MinHeight: 320}

func state(f Rect, on Screen) State { return State{Frame: f, Screen: on} }

func TestApplyKeepsAFrameThatFits(t *testing.T) {
	f := Rect{200, 150, 900, 640}
	pl := Apply(cfg, state(f, laptop()), []Screen{laptop(), external()})
	want := Placement{Restored: true, Frame: f, HasPosition: true, Screen: 0}
	if pl != want {
		t.Errorf("%+v, want %+v", pl, want)
	}
}

func TestApplyMovesAFrameBackIntoTheVisibleArea(t *testing.T) {
	for name, c := range map[string]struct{ in, want Rect }{
		"under the menu bar": {Rect{200, 0, 900, 640}, Rect{200, 24, 900, 640}},
		"over the Dock":      {Rect{200, 400, 900, 640}, Rect{200, 200, 900, 640}},
		"past the right":     {Rect{1200, 100, 900, 640}, Rect{540, 100, 900, 640}},
		"past the left":      {Rect{-300, 100, 900, 640}, Rect{0, 100, 900, 640}},
		"larger than screen": {Rect{0, 0, 3000, 2000}, Rect{0, 24, 1440, 816}},
	} {
		pl := Apply(cfg, state(c.in, laptop()), []Screen{laptop()})
		if pl.Frame != c.want || !pl.Adjusted || !pl.Restored || pl.Screen != 0 {
			t.Errorf("%s: %+v, want frame %v adjusted", name, pl, c.want)
		}
	}
}

func TestApplyScreenDisappeared(t *testing.T) {
	// Saved on the external display, which is gone: centred on the laptop's visible area.
	f := Rect{2000, 100, 1200, 800}
	pl := Apply(cfg, state(f, external()), []Screen{laptop()})
	want := Rect{120, 24 + 8, 1200, 800} // (1440-1200)/2 and 24+(816-800)/2
	if !pl.Restored || pl.Frame != want || pl.Screen != 0 || !pl.Adjusted || !pl.HasPosition {
		t.Errorf("%+v, want frame %v on screen 0", pl, want)
	}
	// ... and when the saved screen is still there but the frame is not on any
	// screen, it is centred on that screen, not on the primary.
	pl = Apply(cfg, state(Rect{9000, 9000, 900, 640}, external()), []Screen{laptop(), external()})
	if pl.Screen != 1 || pl.Frame.X != 1440+(2560-900)/2 {
		t.Errorf("%+v, want it centred on the saved external screen", pl)
	}
	// Saved on a screen the new list does not name, frame off every screen: the primary.
	pl = Apply(cfg, state(Rect{9000, 9000, 900, 640}, Screen{ID: "other"}), []Screen{laptop(), external()})
	if pl.Screen != 0 || pl.Frame.X != (1440-900)/2 {
		t.Errorf("%+v, want it centred on the primary", pl)
	}
}

func TestApplyFrameOnTheSecondScreenStays(t *testing.T) {
	f := Rect{2000, 0, 1000, 700}
	pl := Apply(cfg, state(f, external()), []Screen{laptop(), external()})
	if pl.Screen != 1 || pl.Frame != f || pl.Adjusted {
		t.Errorf("%+v", pl)
	}
	// The same frame with the external screen gone is no longer on any screen.
	pl = Apply(cfg, state(f, external()), []Screen{laptop()})
	if pl.Screen != 0 || !pl.Adjusted || pl.Frame.X < 0 || pl.Frame.X+pl.Frame.W > 1440 {
		t.Errorf("%+v", pl)
	}
}

func TestApplyScreensLeftOfAndAboveThePrimary(t *testing.T) {
	left := Screen{ID: "left", Frame: Rect{-1920, -100, 1920, 1080}, Visible: Rect{-1920, -100, 1920, 1080}}
	f := Rect{-1500, 200, 800, 600}
	pl := Apply(cfg, state(f, left), []Screen{laptop(), left})
	if pl.Screen != 1 || pl.Frame != f || pl.Adjusted {
		t.Errorf("%+v", pl)
	}
}

func TestApplyMaximisedAndFullscreen(t *testing.T) {
	st := state(Rect{100, 100, 900, 640}, external())
	st.Maximised = true
	// The frame (a restore size) lies on the laptop, but the window was maximised
	// on the external display: that is where it goes.
	pl := Apply(cfg, st, []Screen{laptop(), external()})
	if !pl.Maximised || pl.Fullscreen || pl.Screen != 1 {
		t.Errorf("%+v, want maximised on screen 1", pl)
	}
	// With that display gone, the primary, and the restore frame is on screen there.
	pl = Apply(cfg, st, []Screen{laptop()})
	if !pl.Maximised || pl.Screen != 0 || pl.Frame.X < 0 {
		t.Errorf("%+v", pl)
	}
	st.Maximised, st.Fullscreen = false, true
	if pl := Apply(cfg, st, []Screen{laptop(), external()}); !pl.Fullscreen || pl.Maximised || pl.Screen != 1 {
		t.Errorf("%+v", pl)
	}
}

func TestApplyMinimumSize(t *testing.T) {
	pl := Apply(cfg, state(Rect{100, 100, 100, 50}, laptop()), []Screen{laptop()})
	if pl.Frame.W != 480 || pl.Frame.H != 320 {
		t.Errorf("%+v", pl.Frame)
	}
}

func TestApplyIgnoresNonsense(t *testing.T) {
	for name, st := range map[string]State{
		"zero":     {},
		"no width": state(Rect{0, 0, 0, 500}, laptop()),
		"negative": state(Rect{0, 0, -5, 500}, laptop()),
		"NaN":      state(Rect{math.NaN(), 0, 500, 500}, laptop()),
		"Inf":      state(Rect{0, 0, math.Inf(1), 500}, laptop()),
		"huge":     state(Rect{0, 0, 1e12, 500}, laptop()),
	} {
		pl := Apply(cfg, st, []Screen{laptop()})
		if pl.Restored || pl.Screen != -1 || pl.HasPosition {
			t.Errorf("%s: %+v", name, pl)
		}
	}
}

func TestApplyWithoutScreensKeepsTheSize(t *testing.T) {
	pl := Apply(cfg, state(Rect{5000, 5000, 700, 500}, laptop()), nil)
	if !pl.Restored || pl.HasPosition || pl.Frame.W != 700 || pl.Frame.H != 500 || pl.Screen != -1 {
		t.Errorf("%+v", pl)
	}
	// Screens with no usable area are the same as none.
	pl = Apply(cfg, state(Rect{0, 0, 700, 500}, laptop()), []Screen{{ID: "x"}})
	if pl.HasPosition || pl.Screen != -1 {
		t.Errorf("%+v", pl)
	}
}

// ---- the file -------------------------------------------------------------------

func load(t *testing.T, dir string, o Options) (*Store, error) {
	t.Helper()
	o.Dir = dir
	s, err := LoadWith("dev.gonativeui.test", o)
	if s == nil {
		t.Fatal(err)
	}
	return s, err
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := load(t, dir, Options{})
	if err != nil || s.State().Valid() {
		t.Fatalf("a missing file: %v, state %+v", err, s.State())
	}
	app := []byte{0, 1, 2, 0xff, 'x'}
	want := State{Frame: Rect{120.5, 80, 1000, 700}, Maximised: true, Screen: laptop(), App: append([]byte(nil), app...)}
	in := want
	in.App = app
	if err := s.Save(in); err != nil {
		t.Fatal(err)
	}
	app[0] = 99 // the store copied the bytes: this must not reach it
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if filepath.Base(s.Path()) != "window-main.json" {
		t.Errorf("file %s", s.Path())
	}
	s2, err := load(t, dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.State(); !reflect.DeepEqual(got, want) {
		t.Errorf("loaded\n%+v\nwant\n%+v", got, want)
	}
	// The slice is a copy: changing it does not change the store.
	got := s2.State()
	got.App[1] = 77
	if s2.State().App[1] != 1 {
		t.Error("State returned the store's own bytes")
	}
	// And the whole point: Apply puts it back.
	pl := Apply(cfg, s2.State(), []Screen{laptop()})
	if !pl.Restored || !pl.Maximised || pl.Frame.W != 1000 {
		t.Errorf("%+v", pl)
	}
}

func TestScreensFromPlatform(t *testing.T) {
	got := ScreensFromPlatform([]platform.ScreenInfo{
		{ID: "a", X: 0, Y: 0, W: 100, H: 80, VisibleX: 0, VisibleY: 10, VisibleW: 100, VisibleH: 60, Scale: 2},
		{ID: "b", X: 100, Y: -20, W: 50, H: 40, VisibleX: 100, VisibleY: -20, VisibleW: 50, VisibleH: 40, Scale: 1},
	})
	want := []Screen{
		{ID: "a", Frame: Rect{0, 0, 100, 80}, Visible: Rect{0, 10, 100, 60}, Scale: 2},
		{ID: "b", Frame: Rect{100, -20, 50, 40}, Visible: Rect{100, -20, 50, 40}, Scale: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%+v", got)
	}
	if ScreensFromPlatform(nil) != nil {
		t.Error("no screens should stay nil")
	}
}

func TestWindowsHaveFilesOfTheirOwn(t *testing.T) {
	dir := t.TempDir()
	a, _ := load(t, dir, Options{Name: "main"})
	b, _ := load(t, dir, Options{Name: "inspector"})
	a.Save(State{Frame: Rect{1, 2, 300, 400}})
	b.Save(State{Frame: Rect{5, 6, 700, 800}})
	a.Close()
	b.Close()
	a2, _ := load(t, dir, Options{Name: "main"})
	b2, _ := load(t, dir, Options{Name: "inspector"})
	if a2.State().Frame.W != 300 || b2.State().Frame.W != 700 {
		t.Errorf("%+v %+v", a2.State().Frame, b2.State().Frame)
	}
	for _, bad := range []string{"../x", "a b", "a/b", string(make([]byte, 65)), "é"} {
		if s, err := LoadWith("dev.gonativeui.test", Options{Dir: dir, Name: bad}); s != nil || err == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

func TestCorruptFileFallsBackToDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "window-main.json")
	for name, content := range map[string]string{
		"garbage":    "\x00\x01 not json",
		"truncated":  `{"version":1,"state":{"frame":{"X":1,`,
		"wrong type": `{"version":1,"state":{"frame":"big"}}`,
	} {
		os.Remove(path + ".corrupt")
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		s, err := load(t, dir, Options{})
		if !errors.Is(err, prefs.ErrCorrupt) {
			t.Errorf("%s: err = %v, want ErrCorrupt", name, err)
		}
		if s.State().Valid() {
			t.Errorf("%s: state %+v", name, s.State())
		}
		if pl := Apply(cfg, s.State(), []Screen{laptop()}); pl.Restored {
			t.Errorf("%s: Apply restored a corrupt file: %+v", name, pl)
		}
		if _, err := os.Stat(path + ".corrupt"); err != nil {
			t.Errorf("%s: the corrupt file was not kept: %v", name, err)
		}
		// The store works afterwards and replaces the file.
		if err := s.Save(State{Frame: Rect{0, 0, 500, 400}}); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if s2, err := load(t, dir, Options{}); err != nil || s2.State().Frame.W != 500 {
			t.Errorf("%s: after saving: %v %+v", name, err, s2.State())
		}
		os.Remove(path)
	}
}

func TestOtherVersionsAreIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "window-main.json")
	os.WriteFile(path, []byte(`{"version":99,"state":{"frame":{"x":1,"y":2,"w":300,"h":400},"screen":{"frame":{"X":0,"Y":0,"W":0,"H":0},"visible":{"X":0,"Y":0,"W":0,"H":0}}}}`), 0o600)
	s, err := load(t, dir, Options{})
	if err != nil || s.State().Valid() {
		t.Errorf("a file of version 99: %v %+v", err, s.State())
	}
}

func TestSaveRefusesNonsense(t *testing.T) {
	s, _ := load(t, t.TempDir(), Options{})
	good := State{Frame: Rect{1, 1, 400, 300}}
	if err := s.Save(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []State{{}, {Frame: Rect{0, 0, math.NaN(), 5}}, {Frame: Rect{0, 0, 5, -1}}} {
		if err := s.Save(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Save(%+v) = %v", bad, err)
		}
	}
	big := good
	big.App = make([]byte, MaxApp+1)
	if err := s.Save(big); !errors.Is(err, ErrTooLarge) {
		t.Errorf("a large App: %v", err)
	}
	big.App = make([]byte, MaxApp)
	if err := s.Save(big); err != nil {
		t.Errorf("an App of exactly MaxApp: %v", err)
	}
	if s.State().Frame != good.Frame {
		t.Errorf("a refused Save changed the state: %+v", s.State().Frame)
	}
}

// Saves are debounced: nothing is written at once, a burst makes one write that
// has the last state, and Close writes what is pending without waiting.
func TestSaveIsDebounced(t *testing.T) {
	dir := t.TempDir()
	s, _ := load(t, dir, Options{Debounce: 250 * time.Millisecond})
	path := filepath.Join(dir, "window-main.json")
	for i := 1; i <= 50; i++ { // a window being dragged
		s.Save(State{Frame: Rect{float64(i), 10, 800, 600}})
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("written before the debounce: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Drop out the store from this process and read the file fresh.
	r, _ := load(t, dir, Options{})
	if got := r.State().Frame.X; got != 50 {
		t.Errorf("the file has x = %v, want the last of the burst (50)", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	// A pending save is written by Close at once, not after the debounce. The
	// debounce is an hour here so that the difference does not depend on the
	// disk: Windows flushes the file and retries the rename while a scanner
	// holds it (CI: Close took 240 ms against a 250 ms debounce and a 200 ms
	// bound).
	s2, _ := load(t, dir, Options{Debounce: time.Hour})
	s2.Save(State{Frame: Rect{777, 10, 800, 600}})
	start := time.Now()
	if err := s2.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Minute {
		t.Errorf("Close took %v: it should write at once, not after the debounce (1 h)", d)
	}
	r2, _ := load(t, dir, Options{})
	if got := r2.State().Frame.X; got != 777 {
		t.Errorf("after Close the file has x = %v, want 777", got)
	}
}

func TestSaveDoesNotBlockOnTheDisk(t *testing.T) {
	// The directory does not exist and is made by the worker; Save returns first.
	dir := filepath.Join(t.TempDir(), "not", "yet")
	s, _ := load(t, dir, Options{Debounce: time.Hour})
	start := time.Now()
	for i := 0; i < 1000; i++ {
		s.Save(State{Frame: Rect{float64(i), 0, 400, 300}})
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("1000 Saves took %v", d)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Save touched the disk: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "window-main.json")); err != nil {
		t.Errorf("Close wrote nothing: %v", err)
	}
}

// The zoom is kept beside the frame and never mixed into it: a window saved at
// 150 % comes back at the same frame and the same zoom, and a file from before
// the zoom existed means 100 %.
func TestZoomIsKeptApartFromTheFrame(t *testing.T) {
	dir := t.TempDir()
	s, _ := load(t, dir, Options{})
	f := Rect{40, 60, 900, 640}
	if err := s.Save(State{Frame: f, Screen: laptop(), Zoom: 1.5}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s2, _ := load(t, dir, Options{})
	got := s2.State()
	if got.Frame != f || got.Zoom != 1.5 || got.ZoomFactor() != 1.5 {
		t.Errorf("loaded frame %+v zoom %v", got.Frame, got.Zoom)
	}
	if pl := Apply(cfg, got, []Screen{laptop()}); pl.Frame != f {
		t.Errorf("Apply changed the frame of a zoomed window: %+v", pl.Frame)
	}
	if (State{Frame: f}).ZoomFactor() != 1 || (State{Zoom: -1}).ZoomFactor() != 1 || (State{Zoom: math.Inf(1)}).ZoomFactor() != 1 {
		t.Error("no zoom, a negative zoom and an infinite one are not 100 %")
	}
	if err := s2.Save(State{Frame: f, Zoom: math.NaN()}); err != ErrInvalid {
		t.Errorf("Save of a NaN zoom: %v", err)
	}
}

// A Windows frame is in physical pixels: the points an app has (its content
// size, Config.Width) are converted by the display's scale, and back, and the
// zoom is not part of it.
func TestFromPointsKnowsTheUnitOfTheOS(t *testing.T) {
	cases := []struct {
		goos  string
		scale float64
		want  float64
	}{
		{"darwin", 2, 1},
		{"linux", 1.5, 1},
		{"windows", 2, 2},
		{"windows", 1.5, 1.5},
		{"windows", 0, 1}, // a scale that is not known
	}
	for _, c := range cases {
		if got := unitsPerPoint(c.goos, c.scale); got != c.want {
			t.Errorf("%s at scale %v: %v units per point, want %v", c.goos, c.scale, got, c.want)
		}
	}
	// A 900×640 pt window on Windows at 200 % is saved as 1800×1280 and comes back as 900×640 pt.
	const w, h, scale = 900.0, 640.0, 2.0
	k := unitsPerPoint("windows", scale)
	f := Rect{X: 100, Y: 80, W: w * k, H: h * k}
	if f.W != 1800 || f.H != 1280 {
		t.Fatalf("frame %+v", f)
	}
	if f.W/k != w || f.H/k != h {
		t.Errorf("round trip %v×%v pt", f.W/k, f.H/k)
	}
	// On this OS the exported pair agrees with the table.
	if got := ToPoints(FromPoints(w, scale), scale); got != w {
		t.Errorf("ToPoints(FromPoints(%v)) = %v", w, got)
	}
	if FromPoints(w, scale) != w*unitsPerPoint(runtime.GOOS, scale) {
		t.Error("FromPoints does not use this OS's unit")
	}
}
