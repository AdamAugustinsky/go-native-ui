package rexparity

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"gonativeui/cmd/internal/rexkit"
	"gonativeui/cmd/rexlook/rex/rexrig"
	"gonativeui/theme"
	"gonativeui/theme/rex"
	"gonativeui/ui"
)

// The same chrome scene (background, a focused and an unfocused pane with
// their shadows, the divider, the titlebar band, the traffic lights) is built
// once through rexkit and once through theme/rex, offscreen on the OS's real
// renderer, and the pixels must be identical in the light mode.

const divKey = ui.Key(0xD1D1)

// geometry is the work area, the two panes and the gutter between them.
func geometry(w, h int, scale float32) (l, gut, r, work [4]float32) {
	W, H := float32(w), float32(h)
	work = [4]float32{theme.Snap(7.5 * scale), theme.Snap(45.5 * scale), W - theme.Snap(7.5*scale), H - theme.Snap(7.5*scale)}
	gw := theme.Snap(7 * scale)
	lw := theme.Snap((work[2] - work[0] - gw) * 0.5)
	l = [4]float32{work[0], work[1], work[0] + lw, work[3]}
	gut = [4]float32{l[2], work[1], l[2] + gw, work[3]}
	r = [4]float32{gut[2], work[1], work[2], work[3]}
	return
}

type kitScene struct {
	k     *rexkit.Kit
	focus [2]float32
}

func (s *kitScene) Build(c *ui.Context) {
	w, h := c.Size()
	k := s.k
	k.Fld.Build(w, h, k.K)
	k.DrawBackground(c)
	l, gut, r, work := geometry(w, h, k.Scale)
	rects := [2][4]float32{l, r}
	for i := range rects {
		k.DrawShadow(c, uint32(i+1), rects[i], s.focus[i], 1)
	}
	for i := range rects {
		kg := k.BeginPane(c, rexkit.PaneKey(uint32(i+1)), i, rects[i], s.focus[i], 1, true)
		k.EndPane(c, &kg, rects[i], s.focus[i])
	}
	k.Divider(c, divKey, ui.AxisX, work, gut)
	k.TitlebarDrag(c, float32(w))
	k.DrawTrafficLights(c)
}

type themeScene struct {
	r     *theme.Resolved
	focus [2]float32
}

func (s *themeScene) Build(c *ui.Context) {
	w, h := c.Size()
	r := s.r
	ch := r.Chrome
	ch.Background(c, r)
	l, gut, rr, work := geometry(w, h, r.Scale)
	rects := [2][4]float32{l, rr}
	var surf [2]theme.Surface
	for i := range surf {
		surf[i] = theme.Surface{Key: rexkit.PaneKey(uint32(i + 1)), Slot: i, Rect: rects[i], Focus: s.focus[i], Opacity: 1, Top: true}
		ch.SurfaceShadow(c, r, surf[i])
	}
	for i := range surf {
		kg := ch.BeginSurface(c, r, surf[i])
		ch.EndSurface(c, r, surf[i], kg)
	}
	ch.Divider(c, r, divKey, ui.AxisX, work, gut)
	ch.Titlebar(c, r, theme.Titlebar{Width: float32(w), Buttons: theme.ButtonsTraffic})
}

// render builds the scene made by mk until it settles, with the pointer at
// (mx, my), and returns the pixels.
func render(t *testing.T, w, h int, scale float32, mx, my float32, mk func(g *rexrig.Rig) rexrig.Scene) []byte {
	t.Helper()
	g, err := rexrig.New(w, h, scale)
	if errors.Is(err, rexrig.ErrNoText) || errors.Is(err, rexrig.ErrNoDevice) {
		if os.Getenv("CI") != "" {
			t.Fatalf("no offscreen rendering on CI: %v", err)
		}
		t.Skipf("no offscreen rendering: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	s := mk(g)
	g.Move(mx, my)
	g.Frames(s, 3) // the pointer is hit-tested against the previous frame's layout
	if n := g.Settle(s, 600); n > 600 {
		t.Fatal("scene did not settle")
	}
	return g.RGBA()
}

func TestLightChromeMatchesRexkit(t *testing.T) {
	for _, tc := range []struct {
		name  string
		w, h  int
		scale float32
		focus [2]float32
		onDiv bool // the pointer rests on the divider, which then shows its handle
	}{
		{"1x focused+unfocused", 760, 480, 1, [2]float32{1, 0}, false},
		{"2x focused+unfocused", 1000, 640, 2, [2]float32{1, 0}, false},
		{"2x focus mid-animation", 1000, 640, 2, [2]float32{0.35, 0.65}, false},
		{"2x divider handle", 1000, 640, 2, [2]float32{0, 1}, true},
		{"1x tiny window (flat fills)", 300, 200, 1, [2]float32{1, 0}, false},
		{"1.5x", 801, 501, 1.5, [2]float32{0.6, 0.2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mx, my := float32(-100), float32(-100)
			if _, gut, _, _ := geometry(tc.w, tc.h, tc.scale); tc.onDiv {
				mx, my = (gut[0]+gut[2])/2, (gut[1]+gut[3])/2
			}
			want := render(t, tc.w, tc.h, tc.scale, mx, my, func(g *rexrig.Rig) rexrig.Scene {
				k := &rexkit.Kit{}
				k.Init(g.TS, nil, 2, tc.scale)
				return &kitScene{k: k, focus: tc.focus}
			})
			got := render(t, tc.w, tc.h, tc.scale, mx, my, func(g *rexrig.Rig) rexrig.Scene {
				r := rex.Theme().Resolve(g.TS, tc.scale, theme.Light)
				return &themeScene{r: &r, focus: tc.focus}
			})
			// The scene is not blank: it holds the white of a focused pane, the
			// background field and the traffic lights, and (with the pointer on
			// the divider) the handle.
			colours := map[[3]byte]bool{}
			for i := 0; i < len(want); i += 4 {
				colours[[3]byte{want[i], want[i+1], want[i+2]}] = true
			}
			if len(colours) < 40 {
				t.Fatalf("the rexkit scene has only %d colours", len(colours))
			}
			if tc.onDiv {
				idle := render(t, tc.w, tc.h, tc.scale, -100, -100, func(g *rexrig.Rig) rexrig.Scene {
					k := &rexkit.Kit{}
					k.Init(g.TS, nil, 2, tc.scale)
					return &kitScene{k: k, focus: tc.focus}
				})
				if string(idle) == string(want) {
					t.Fatal("the divider shows no handle under the pointer")
				}
			}
			if len(got) != len(want) {
				t.Fatalf("sizes differ: %d vs %d", len(got), len(want))
			}
			n, first := 0, -1
			for i := 0; i < len(got); i += 4 {
				if got[i] != want[i] || got[i+1] != want[i+1] || got[i+2] != want[i+2] || got[i+3] != want[i+3] {
					if first < 0 {
						first = i / 4
					}
					n++
				}
			}
			if n > 0 {
				x, y := first%tc.w, first/tc.w
				t.Errorf("%d of %d pixels differ; first at (%d, %d): rexkit %v, theme/rex %v", n, len(got)/4, x, y,
					fmt.Sprintf("%x", want[first*4:first*4+4]), fmt.Sprintf("%x", got[first*4:first*4+4]))
			}
		})
	}
}
