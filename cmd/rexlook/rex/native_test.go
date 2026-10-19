package rex

import (
	"math"
	"testing"

	"gonativeui/gpu"
	"gonativeui/ui"
)

// fakeButtons records what the scene asks of the window's native buttons.
type fakeButtons struct{ plus, set bool }

func (b *fakeButtons) SetButtonEnabled(id uint16, enabled bool) {
	if id == cmdPlus {
		b.plus, b.set = enabled, true
	}
}

// With the native shell the scene paints no field and no ⌘ and + of its own,
// keeps the panes and its tabs where they were, and follows the + button.
func TestNativeShellScene(t *testing.T) {
	drawn := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	btns := &fakeButtons{}
	h.x.backdrop, h.x.btns = true, btns
	h.settle()

	if drawn.x.disp != h.x.disp || drawn.x.tabRects != h.x.tabRects || drawn.x.capsule != h.x.capsule {
		t.Errorf("geometry moved: drawn panes %v tabs %v, native panes %v tabs %v", drawn.x.disp, drawn.x.tabRects, h.x.disp, h.x.tabRects)
	}
	if n, want := len(h.list.Quads), len(drawn.list.Quads); n >= want-100 {
		t.Errorf("native shell builds %d quads, the drawn scene %d: want the field mesh gone", n, want)
	}
	if !btns.set || !btns.plus {
		t.Errorf("+ enabled %v (set %v), want enabled", btns.plus, btns.set)
	}
	h.in.PushCommand(cmdPlus)
	h.settle()
	if h.x.nTabs != 4 || h.x.active != 3 {
		t.Errorf("+ command: %d tabs, active %d", h.x.nTabs, h.x.active)
	}
	for h.x.nTabs < maxTabs {
		h.in.PushCommand(cmdPlus)
		h.settle()
	}
	if btns.plus {
		t.Error("+ stays enabled with the most tabs open")
	}
}

// Tabs behave like AppKit's: chosen on the press with the content there in
// the same frame, darkened while held, the arrow cursor, the system's tab
// shortcuts, and quieter while the window is not key.
func TestTabsFeelNative(t *testing.T) {
	h := newHarness(t, WindowWidthPt, WindowHeightPt, 2)
	cx, cy := centre(h.tabRect(1))
	h.move(cx, cy)
	h.settle()
	if got := h.cursor(); got != ui.CursorArrow {
		t.Errorf("cursor over a tab %v, want the arrow", got)
	}
	fades := h.x.switchN
	h.in.SetButton(ui.ButtonLeft, true, 1)
	h.frame()
	if h.x.active != 1 {
		t.Fatalf("active tab %d after pressing tab 2, want 1 on the press", h.x.active)
	}
	if h.x.switchN != fades {
		t.Error("the switch fades the new tab's panes in instead of showing them at once")
	}
	h.frames(10)
	press := false
	for _, q := range h.list.Quads {
		if q.Rect == h.tabRect(1) && q.Color[0]&^255 == 0x3A2A4A00 {
			press = true
		}
	}
	if !press {
		t.Error("a held tab shows no press")
	}
	h.in.SetButton(ui.ButtonLeft, false, 0)
	h.settle()

	key := func(code ui.KeyCode, mods ui.Mods) {
		h.in.PushKey(ui.KeyEvent{Code: code, Mods: mods, Down: true})
		h.frame()
	}
	key('3', ui.ModSuper)
	if h.x.active != 2 {
		t.Errorf("⌘3: active %d", h.x.active)
	}
	key(ui.KeyTab, ui.ModCtrl)
	if h.x.active != 0 {
		t.Errorf("⌃Tab from the last tab: active %d, want 0 (wraps)", h.x.active)
	}
	key(ui.KeyLeftBracket, ui.ModSuper|ui.ModShift)
	if h.x.active != 2 {
		t.Errorf("⌘⇧[ from the first tab: active %d, want 2", h.x.active)
	}
	key('T', ui.ModSuper)
	if h.x.nTabs != 4 || h.x.active != 3 {
		t.Errorf("⌘T: %d tabs, active %d", h.x.nTabs, h.x.active)
	}

	h.settle()
	if h.x.keyW != 1 {
		t.Fatalf("key state %v in a focused window", h.x.keyW)
	}
	h.in.Focused = false
	h.settle()
	if h.x.keyW != 0 {
		t.Errorf("key state %v once the window lost focus, want 0", h.x.keyW)
	}
}

// The native shell's frames allocate nothing either: the pointer script with
// its light/dark round trip, tab shortcuts and key-state changes.
func TestZeroAllocNativeShell(t *testing.T) {
	h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{})
	h.x.backdrop, h.x.btns = true, &fakeButtons{}
	h.settle()
	s := &tscript{script: script{x: h.x, in: &h.in, modes: true}, frame: h.frame}
	n := 0
	run := func() {
		switch n++; n % 300 {
		case 100:
			h.in.PushKey(ui.KeyEvent{Code: ui.KeyTab, Mods: ui.ModCtrl, Down: true})
		case 150:
			h.in.Focused = false
		case 200:
			h.in.Focused = true
		}
		s.step()
	}
	zeroAlloc(t, "native shell: pointer script, tab keys, key state", run, h.c.Animating, h.c.Boxes, 1800, 900)
}

// The glass behind the titlebar, measured from screen captures on the
// MacBook (27.2, docs/design/native-shell-spike.md): the band's grey with a
// light (#FFFFFF) and a dark (#1E1E1E) window behind, key or not, widened a
// little.
var glassGrey = [2][2]float64{{150, 255}, {38, 115}} // light, dark: min, max

// AppKit's contrast floors (labelAlphas' comment): active text, and the rest
// or anything in a window that is not key; light, dark.
var floorActive, floorInactive = [2]float64{3.95, 5.84}, [2]float64{1.98, 2.25}

func srgbLum(c [3]float64) float64 {
	var l float64
	for i, w := range [3]float64{0.2126, 0.7152, 0.0722} {
		v := c[i] / 255
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		l += w * v
	}
	return l
}

func contrast(a, b [3]float64) float64 {
	la, lb := srgbLum(a), srgbLum(b)
	return (max(la, lb) + 0.05) / (min(la, lb) + 0.05)
}

// over is colour c (RGBA8) blended over bg, in sRGB as the layer blends.
func over(c uint32, bg [3]float64) [3]float64 {
	a := float64(c&255) / 255
	return [3]float64{
		a*float64(c>>24) + (1-a)*bg[0],
		a*float64(c>>16&255) + (1-a)*bg[1],
		a*float64(c>>8&255) + (1-a)*bg[2],
	}
}

func inRect(r [4]float32, x, y float32) bool { return x >= r[0] && x < r[2] && y >= r[1] && y < r[3] }

// Every glyph of the titlebar's text over the glass keeps AppKit's floors,
// light and dark, in a key window and in one that is not, at both ends of
// the glass's measured range. The background of a glyph is composed from the
// frame's own fills under its centre (the scrim, the capsule, the pill, the
// hover fills), in draw order, over the glass.
func TestBandTextContrastOverGlass(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, key := range []bool{true, false} {
			h := newHarnessRaw(t, WindowWidthPt, WindowHeightPt, 2, ui.Appearance{Dark: dark, Known: true})
			h.x.backdrop, h.x.btns = true, &fakeButtons{}
			h.in.Focused = key
			h.settle()
			look := 0
			if dark {
				look = 1
			}
			band, textX := float32(45*2), float32(240) // the band's height; the host name's left (after the device glyph)
			pill := h.x.pill
			glyphs, worst := 0, math.Inf(1)
			for i, q := range h.list.Quads {
				w := q.Rect[2] - q.Rect[0]
				rgb := q.Color[0] >> 8
				if q.Mode != gpu.ModeMaskR8 || q.Rect[3] > band || q.Rect[0] < textX || w > 12 || (rgb != 0 && rgb != 0xFFFFFF) {
					continue // not a glyph of the band's text (the fake glyphs are 6 px wide; icons are wider or coloured)
				}
				x, y := (q.Rect[0]+q.Rect[2])/2, (q.Rect[1]+q.Rect[3])/2
				if q.FadeX != [2]float32{} && x > q.FadeX[0] {
					continue // fading out where the title does not fit
				}
				floor := floorInactive[look]
				if key && (x < h.x.capsule[0] || inRect(pill, x, y)) {
					floor = floorActive[look]
				}
				for _, g := range glassGrey[look] {
					bg := [3]float64{g, g, g}
					for _, f := range h.list.Quads[:i] {
						if f.Mode != gpu.ModeShape || f.Soft != 0 || f.Border != 0 || !inRect(f.Rect, x, y) ||
							(f.Clip != [4]float32{} && !inRect(f.Clip, x, y)) {
							continue
						}
						bg = over(f.Color[0], bg)
					}
					c := contrast(bg, over(q.Color[0], bg))
					worst = min(worst, c/floor)
					if c < floor {
						t.Errorf("dark %v, key %v, glass %v: glyph at %.0f,%.0f (colour %08X) has %.2f:1 over %.0f, floor %.2f", dark, key, g, x, y, q.Color[0], c, bg[0], floor)
						break
					}
				}
				glyphs++
			}
			if glyphs < 40 {
				t.Errorf("dark %v, key %v: only %d glyphs of titlebar text found", dark, key, glyphs)
			}
			t.Logf("dark %v, key %v: %d glyphs, worst contrast %.2f× its floor", dark, key, glyphs, worst)
		}
	}
}
