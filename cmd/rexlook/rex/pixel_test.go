package rex

import (
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gonativeui/cmd/rexlook/rex/rexrig"
	"gonativeui/theme"
	"gonativeui/ui"
)

// Offscreen pixel tests: the real text system and renderer of the OS (Metal on
// macOS; Direct3D 11 / WARP and OpenGL / llvmpipe on the CI runners). Fonts
// differ per OS, so assertions sample places that hold no text, or ink in
// general; the exact reference match runs on macOS only (TestRexcompare).

func newRig(t *testing.T, wPt, hPt int, scale float32) (*rexrig.Rig, *Rex) {
	t.Helper()
	g, err := rexrig.New(int(float32(wPt)*scale), int(float32(hPt)*scale), scale)
	if errors.Is(err, rexrig.ErrNoText) {
		t.Skipf("no text system: %v", err)
	}
	if errors.Is(err, rexrig.ErrNoDevice) {
		if os.Getenv("CI") != "" {
			t.Fatalf("no offscreen renderer on CI: %v", err)
		}
		t.Skipf("no offscreen renderer: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(g.Close)
	x := New(g.TS, scale)
	x.AttachRenderer(g.R)
	if n := g.Settle(x, 600); n > 600 {
		t.Fatal("scene did not settle")
	}
	return g, x
}

type shot struct {
	g   *rexrig.Rig
	pix []byte
}

func (s shot) at(x, y float32) [3]int {
	o := (int(y)*s.g.W + int(x)) * 4
	return [3]int{int(s.pix[o]), int(s.pix[o+1]), int(s.pix[o+2])}
}

func (s shot) isWhite(x, y float32) bool { return s.at(x, y) == [3]int{255, 255, 255} }

// ink counts the pixels of a rectangle that are clearly darker than a light card.
func (s shot) ink(r [4]float32) int {
	n := 0
	for y := int(r[1]); y < int(r[3]); y++ {
		for x := int(r[0]); x < int(r[2]); x++ {
			if c := s.at(float32(x), float32(y)); c[0]+c[1]+c[2] < 3*140 {
				n++
			}
		}
	}
	return n
}

func (s shot) save(dir, name string) {
	if dir == "" {
		return
	}
	if err := rexrig.WritePNG(filepath.Join(dir, name+".png"), s.pix, s.g.W, s.g.H, false, nil); err != nil {
		fmt.Fprintln(os.Stderr, "rex test:", err)
	}
}

func snapshot(g *rexrig.Rig) shot { return shot{g, g.RGBA()} }

func (r *Rex) paneRect(kind paneKind) [4]float32 {
	t := &r.tabs[r.active]
	for i := range t.panes {
		if t.panes[i].used && t.panes[i].kind == kind {
			return r.disp[i]
		}
	}
	return [4]float32{}
}

// The initial state at the reference size, as the window loop draws it.
func TestRenderInitial(t *testing.T) {
	g, x := newRig(t, WindowWidthPt, WindowHeightPt, 2)
	x.Traffic = true
	g.Frames(x, 2)
	s := snapshot(g)
	k := x.K

	agent, git, snake := x.paneRect(kindAgent), x.paneRect(kindGit), x.paneRect(kindSnake)
	// The focused pane is opaque white where it has no text; the others are tinted.
	if !s.isWhite(agent[2]-30*k, agent[3]-30*k) {
		t.Errorf("focused pane at its blank corner = %v, want white", s.at(agent[2]-30*k, agent[3]-30*k))
	}
	for name, r := range map[string][4]float32{"git": git, "snake": snake} {
		c := s.at(r[2]-30*k, r[3]-30*k)
		if c == [3]int{255, 255, 255} || c[0] < 200 || c[2] > 252 {
			t.Errorf("unfocused %s pane at its blank corner = %v: want a tinted, light fill", name, c)
		}
	}
	// The gutters show the background (pink at the top, sand lower), not white.
	if c := s.at((agent[2]+snake[0])/2, 300*k); c == [3]int{255, 255, 255} || c[0] < 150 {
		t.Errorf("gutter = %v", c)
	}
	if top, bottom := s.at(agent[2]+7*k, 91-7*k/2), s.at(agent[2]+7*k, float32(g.H)-4*k); !(top[2] > bottom[2]) {
		t.Errorf("background not lavender at the top and sand at the bottom: %v vs %v", top, bottom)
	}
	// The pill is near-white at the left end of the active tab, the capsule darker.
	pill := x.pill
	if c := s.at(pill[0]+8*k, (pill[1]+pill[3])/2); c[0] < 250 || c[1] < 245 {
		t.Errorf("pill = %v", c)
	}
	cx, _ := centre(x.tabRects[1])
	if c := s.at(cx, x.capsule[1]+2*k); c[0] > 244 || c[0] < 200 { // the band above the tabs
		t.Errorf("capsule = %v", c)
	}
	// Text is drawn: terminal rows in the agent pane, titles in the tabs, the
	// header buttons of the focused pane, the host block.
	if n := s.ink([4]float32{agent[0] + 20*k, agent[1] + 90*k, agent[2] - 20*k, agent[1] + 130*k}); n < 200 {
		t.Errorf("agent pane first row has %d ink pixels", n)
	}
	for i := 0; i < 3; i++ {
		r := x.tabRects[i]
		if n := s.ink([4]float32{r[0] + 60*k, r[1] + 8*k, r[2] - 40*k, r[3] - 8*k}); n < 40 {
			t.Errorf("tab %d title has %d ink pixels", i, n)
		}
	}
	for b := 0; b < numBtns && runtime.GOOS == "darwin"; b++ { // elsewhere the icons are font glyphs, whose coverage varies
		cx, cy := x.btnCentre(agent, b)
		if n := s.ink([4]float32{cx - 10*k, cy - 10*k, cx + 10*k, cy + 10*k}); n == 0 {
			// glyphs are #96999B: lighter than the ink threshold, so look for any non-white
			found := false
			for yy := int(cy - 10*k); yy < int(cy+10*k); yy++ {
				for xx := int(cx - 10*k); xx < int(cx+10*k); xx++ {
					if !s.isWhite(float32(xx), float32(yy)) {
						found = true
					}
				}
			}
			if !found {
				t.Errorf("header button %d draws nothing", b)
			}
		}
	}
	// The unfocused panes show no header buttons.
	cx, cy := x.btnCentre(git, btnMax)
	for yy := int(cy - 10*k); yy < int(cy+10*k); yy++ {
		for xx := int(cx - 10*k); xx < int(cx+10*k); xx++ {
			if c := s.at(float32(xx), float32(yy)); c[0] < 200 {
				t.Fatalf("unfocused pane shows a button glyph at %d,%d: %v", xx, yy, c)
			}
		}
	}
	// The traffic lights are drawn offscreen: red, yellow, green.
	if c := s.at(46*k, 46*k); !(c[0] > 200 && c[1] < 150 && c[2] < 140) {
		t.Errorf("red light = %v", c)
	}
	if c := s.at(138*k, 46*k); !(c[1] > 180 && c[0] < 190) {
		t.Errorf("green light = %v", c)
	}
	s.save(os.Getenv("REX_SHOTS"), "initial")
}

// light counts the pixels of a rectangle that are light enough to read on a
// dark ground.
func (s shot) light(r [4]float32) int {
	n := 0
	for y := int(r[1]); y < int(r[3]); y++ {
		for x := int(r[0]); x < int(r[2]); x++ {
			if c := s.at(float32(x), float32(y)); c[0] > 150 && c[1] > 150 && c[2] > 150 {
				n++
			}
		}
	}
	return n
}

// The dark mode (forced, once the crossfade has settled): the focused pane is the
// dark surface, the capsule and the window are dark, and every kind of text the
// scene sets in its own colours is light enough to read on them.
func TestRenderDark(t *testing.T) {
	g, x := newRig(t, WindowWidthPt, WindowHeightPt, 2)
	x.Traffic = true
	x.SetPreference(theme.ForceDark)
	if n := g.Settle(x, 600); n > 600 {
		t.Fatal("the crossfade did not settle")
	}
	s := snapshot(g)
	k := x.K

	agent, git := x.paneRect(kindAgent), x.paneRect(kindGit)
	if c, want := s.at(agent[2]-30*k, agent[3]-30*k), [3]int{0x2A, 0x2C, 0x3A}; c != want {
		t.Errorf("focused pane at its blank corner = %v, want the dark surface %v", c, want)
	}
	if c := s.at(git[2]-30*k, git[3]-30*k); c[0] > 90 || c[1] > 90 || c[2] > 100 {
		t.Errorf("unfocused pane at its blank corner = %v: want a dark glass", c)
	}
	if c := s.at((agent[2]+x.paneRect(kindSnake)[0])/2, 300*k); c[0] > 60 || c[1] > 60 || c[2] > 70 {
		t.Errorf("gutter = %v: want the dark window background", c)
	}
	cx, _ := centre(x.tabRects[1])
	if c := s.at(cx, x.capsule[1]+2*k); c[0] > 90 { // the band above the tabs
		t.Errorf("capsule = %v: want it dark", c)
	}
	pill := x.pill
	if c, cap := s.at(pill[0]+8*k, (pill[1]+pill[3])/2), s.at(cx, x.capsule[1]+2*k); c[0] <= cap[0] {
		t.Errorf("the pill (%v) is not lighter than the capsule (%v)", c, cap)
	}
	for name, r := range map[string][4]float32{
		"focused pane title":   {agent[0] + 62*k, agent[1] + 22*k, agent[0] + 400*k, agent[1] + 52*k},
		"unfocused pane title": {git[0] + 62*k, git[1] + 22*k, git[0] + 400*k, git[1] + 52*k},
		"agent first row":      {agent[0] + 20*k, agent[1] + 90*k, agent[2] - 20*k, agent[1] + 130*k},
		"host name":            {245 * k, 14 * k, 330 * k, 50 * k},
		"active tab title":     {x.tabRects[0][0] + 100*k, x.tabRects[0][1] + 8*k, x.tabRects[0][2] - 30*k, x.tabRects[0][3] - 8*k},
		"inactive tab title":   {x.tabRects[1][0] + 60*k, x.tabRects[1][1] + 8*k, x.tabRects[1][2] - 60*k, x.tabRects[1][3] - 8*k},
	} {
		if n := s.light(r); n < 40 {
			t.Errorf("%s has only %d light pixels on the dark ground", name, n)
		}
	}
	s.save(os.Getenv("REX_SHOTS"), "dark-initial")
}

// RenderShot draws the mode asked for from the first frame, and the metrics
// file says which.
func TestRenderShotModes(t *testing.T) {
	dir := t.TempDir()
	mean := map[string]float64{}
	for _, m := range []struct {
		name string
		pref theme.Preference
		sys  ui.Appearance
	}{
		{"light", theme.ForceLight, ui.Appearance{Dark: true, Known: true}},
		{"dark", theme.ForceDark, ui.Appearance{}},
		{"system-dark", theme.System, ui.Appearance{Dark: true, Known: true}},
		{"system-unknown", theme.System, ui.Appearance{}},
	} {
		path := filepath.Join(dir, m.name+".png")
		_, _, err := RenderShot(path, ShotOptions{Width: 600, Height: 400, Scale: 1, Preference: m.pref, System: m.sys})
		if errors.Is(err, rexrig.ErrNoDevice) || (err != nil && strings.Contains(err.Error(), "renderer")) {
			if os.Getenv("CI") != "" {
				t.Fatalf("no offscreen renderer on CI: %v", err)
			}
			t.Skipf("no offscreen renderer: %v", err)
		}
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		var sum, n float64
		for y := 0; y < 400; y += 4 {
			for x := 0; x < 600; x += 4 {
				r, g, b, _ := img.At(x, y).RGBA()
				sum += float64(r+g+b) / 3 / 0xFFFF
				n++
			}
		}
		mean[m.name] = sum / n
		meta, err := os.ReadFile(path + ".metrics.json")
		if err != nil {
			t.Fatal(err)
		}
		var md struct{ Mode string }
		if err := json.Unmarshal(meta, &md); err != nil {
			t.Fatal(err)
		}
		if want := map[string]string{"light": "light", "dark": "dark", "system-dark": "dark", "system-unknown": "light"}[m.name]; md.Mode != want {
			t.Errorf("%s: the metrics say mode %q, want %q", m.name, md.Mode, want)
		}
	}
	if !(mean["dark"] < 0.3 && mean["light"] > 0.6) {
		t.Errorf("mean brightness: light %.2f, dark %.2f", mean["light"], mean["dark"])
	}
	if mean["system-dark"] != mean["dark"] || mean["system-unknown"] != mean["light"] {
		t.Errorf("system preference: dark system %.3f (want %.3f), unknown system %.3f (want %.3f)",
			mean["system-dark"], mean["dark"], mean["system-unknown"], mean["light"])
	}
}

// Hover, focus, tab, split, maximise and drag change what is on screen.
func TestRenderInteractionStates(t *testing.T) {
	dir := os.Getenv("REX_SHOTS")
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	g, x := newRig(t, WindowWidthPt, WindowHeightPt, 2)
	x.Traffic = true
	k := x.K
	click := func(cx, cy float32) {
		g.Click(x, cx, cy)
		if n := g.Settle(x, 900); n > 900 {
			t.Fatal("did not settle")
		}
	}

	initial := snapshot(g)

	// Hover over the second tab: a light fill appears there and nowhere else changes much.
	rc := x.tabRects[1]
	g.Move((rc[0]+rc[2])/2, rc[1]+4*k)
	g.Settle(x, 200)
	hover := snapshot(g)
	if a, b := initial.at(rc[0]+8*k, (rc[1]+rc[3])/2), hover.at(rc[0]+8*k, (rc[1]+rc[3])/2); a == b {
		t.Errorf("hovering a tab changed nothing at %v", rc)
	}
	hover.save(dir, "hover-tab")

	// Hover over the maximise button of the focused pane.
	agent := x.paneRect(kindAgent)
	bx, by := x.btnCentre(agent, btnMax)
	g.Move(bx, by)
	g.Settle(x, 200)
	hb := snapshot(g)
	if hb.ink([4]float32{bx - 8*k, by - 8*k, bx + 8*k, by + 8*k}) <= initial.ink([4]float32{bx - 8*k, by - 8*k, bx + 8*k, by + 8*k}) && runtime.GOOS == "darwin" {
		t.Error("the hovered button glyph does not darken")
	}
	hb.save(dir, "hover-button")

	// Focus the snake pane: it turns white, the agent pane dims.
	snake := x.paneRect(kindSnake)
	g.Click(x, snake[2]-30*k, snake[3]-30*k)
	g.Move(5, 5)
	g.Frames(x, 4)
	if !g.C.Animating() {
		t.Error("the dimming is not animating a few frames after the click")
	}
	mid := snapshot(g)
	if c := mid.at(snake[2]-30*k, snake[3]-30*k); c == [3]int{255, 255, 255} || c == initial.at(snake[2]-30*k, snake[3]-30*k) {
		t.Errorf("mid-animation fill %v is neither the dimmed nor the focused colour", c)
	}
	mid.save(dir, "focus-mid-animation")
	g.Settle(x, 100)
	focus := snapshot(g)
	if !focus.isWhite(snake[2]-30*k, snake[3]-30*k) || focus.isWhite(agent[2]-30*k, agent[3]-30*k) {
		t.Error("focus did not move to the snake pane")
	}
	focus.save(dir, "focus-switched")

	// Split the focused pane side by side.
	bx, by = x.btnCentre(snake, btnSplitX)
	click(bx, by)
	g.Move(5, 5)
	g.Settle(x, 100)
	split := snapshot(g)
	if x.cur().numPanes() != 4 {
		t.Fatalf("%d panes", x.cur().numPanes())
	}
	split.save(dir, "split")

	// Maximise the new (focused) pane.
	sh := x.paneRect(kindShell)
	bx, by = x.btnCentre(sh, btnMax)
	click(bx, by)
	g.Move(5, 5)
	g.Settle(x, 100)
	max := snapshot(g)
	w := x.work()
	if !max.isWhite((w[0]+w[2])/2, w[3]-30*k) || !max.isWhite(w[0]+30*k, w[3]-30*k) {
		t.Error("the maximised pane does not fill the work area")
	}
	max.save(dir, "maximised")
	bx, by = x.btnCentre(x.paneRect(kindShell), btnMax)
	click(bx, by)

	// Mid-drag: the vertical divider dragged 150 px to the right.
	gut := x.cur().nodes[x.cur().root].gut
	cx, cy := (gut[0]+gut[2])/2, (gut[1]+gut[3])/2
	g.Move(cx, cy)
	g.Frames(x, 2)
	g.Press()
	g.Frame(x)
	g.Move(cx+150, cy)
	g.Frames(x, 2)
	drag := snapshot(g)
	drag.save(dir, "mid-drag")
	g.Release()
	g.Frame(x)

	// Another tab, and the same window at 1× and a small size.
	tx, ty := centre(x.tabRects[1])
	g.Click(x, tx, ty)
	g.Move(5, 5)
	g.Frames(x, 3)
	snapshot(g).save(dir, "tab-switch-mid-animation")
	g.Settle(x, 200)
	snapshot(g).save(dir, "tab-2")
}

// Small and large windows at 1×, 2× and 3×: the scene fills the target without
// holes (every pixel is covered by the background mesh) and every pane has a
// fill.
func TestRenderSizes(t *testing.T) {
	dir := os.Getenv("REX_SHOTS")
	for _, c := range []struct {
		w, h  int
		scale float32
	}{{600, 400, 1}, {600, 400, 2}, {3000, 1800, 1}, {1057, 639, 1}, {900, 1200, 2}} {
		t.Run(fmt.Sprintf("%dx%d@%v", c.w, c.h, c.scale), func(t *testing.T) {
			g, x := newRig(t, c.w, c.h, c.scale)
			g.Clear = [4]float32{1, 0, 1, 1}
			g.Frame(x)
			s := snapshot(g)
			// A hole would show the clear colour, magenta.
			holes := 0
			for y := 0; y < g.H; y += 3 {
				for xx := 0; xx < g.W; xx += 3 {
					if s.at(float32(xx), float32(y)) == [3]int{255, 0, 255} {
						holes++
					}
				}
			}
			if holes > 0 {
				t.Errorf("%d uncovered samples", holes)
			}
			for slot := range x.cur().panes {
				if !x.cur().panes[slot].used {
					continue
				}
				r := x.disp[slot]
				if p := s.at((r[0]+r[2])/2, r[3]-3*x.K*2); p[0] > 250 && p[1] > 250 && p[2] > 250 && x.cur().focus != x.cur().panes[slot].uid {
					t.Errorf("unfocused pane %v is pure white", r)
				}
			}
			s.save(dir, fmt.Sprintf("size-%dx%d@%v", c.w, c.h, c.scale))
		})
	}
}

// The initial state against the reference screenshot (macOS only; the
// screenshot is not in the repository): go run ./tools/rexcompare must pass
// every check.
func TestRexcompare(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the reference match needs the macOS fonts")
	}
	ref, err := filepath.Abs("../../../docs/design/ref/rex-screenshot@2x.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ref); err != nil {
		t.Skip("reference screenshot absent (docs/design/ref is not committed)")
	}
	root, _ := filepath.Abs("../../..")
	g, x := newRig(t, WindowWidthPt, WindowHeightPt, 2)
	x.Traffic = true
	g.Frames(x, 2)
	out := filepath.Join(t.TempDir(), "rex.png")
	meta := map[string]any{
		"cellAdvancePt": x.CellAdvancePt(), "rowPitchPt": x.RowPitchPt(), "monoFace": x.MonoFace(), "monoSizePt": x.MonoSizePt(),
	}
	if err := rexrig.WritePNG(out, g.RGBA(), g.W, g.H, true, meta); err != nil {
		t.Fatal(err)
	}
	if dir := os.Getenv("REX_SHOTS"); dir != "" {
		_ = rexrig.WritePNG(filepath.Join(dir, "initial-corners.png"), g.RGBA(), g.W, g.H, true, meta)
	}
	gobin := filepath.Join(runtime.GOROOT(), "bin", "go")
	cmd := exec.Command(gobin, "run", "./tools/rexcompare", "-ref", ref, "-regions", "docs/design/rex-regions.json", "-cand", out)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rexcompare failed: %v\n%s", err, b)
	}
	if !strings.Contains(string(b), "0 failed") {
		t.Fatalf("rexcompare: %s", b)
	}
}
