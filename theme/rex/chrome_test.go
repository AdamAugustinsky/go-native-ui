package rex

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"gonativeui/gpu"
	"gonativeui/text"
	"gonativeui/theme"
	"gonativeui/ui"
)

// put places s with its top-left at (x, y) in the current parent, set in font f.
func put(c *ui.Context, kg *theme.KeyGen, r *theme.Resolved, f text.FontKey, s string, x, y float32, col uint32) {
	putMet(c, kg, f, r.Met(f), s, x, y, col)
}

func putMet(c *ui.Context, kg *theme.KeyGen, f text.FontKey, fm theme.FontMet, s string, x, y float32, col uint32) {
	c.NextFont(f)
	c.NextTextColor(col)
	c.NextPos(x, y)
	c.NextSize(ui.TextSize(0), ui.Px(fm.H))
	c.BoxKeyed(ui.DrawText|theme.Floating, kg.Next(), strBytes(s))
}

// box places a filled, bordered rounded box at (x, y).
func box(c *ui.Context, kg *theme.KeyGen, x, y, w, h, rad float32, fill, border uint32) {
	theme.At(c, x, y, w, h)
	flags := ui.DrawBackground | theme.Floating
	c.NextBG(fill)
	c.NextRadius(rad)
	if border != 0 {
		flags |= ui.DrawBorder
		c.NextBorder(border, 1)
	}
	c.BoxKeyed(flags, kg.Next(), nil)
}

// legibility fills a pane with the text roles on the surface they sit on:
// headings, body and muted text, terminal colours, a primary button, a
// selected row, a text field and the state colours.
func legibility(grid *Grid) func(c *ui.Context, r *theme.Resolved, i int, kg *theme.KeyGen) {
	return func(c *ui.Context, r *theme.Resolved, i int, kg *theme.KeyGen) {
		p := &r.Pal
		s := r.Scale
		x, y := 20*s, 18*s
		line := func(f text.FontKey, str string, col uint32) {
			put(c, kg, r, f, str, x, y, col)
			y += r.Met(f).H + 6*s
		}
		if i == 0 {
			line(r.Font.UISemi, "Focused pane", p.Text)
		} else {
			line(r.Font.UISemi, "Unfocused pane (translucent glass)", p.Text)
		}
		line(r.Font.UI, "The quick brown fox jumps over the lazy dog 0123456789", p.Text)
		line(r.Font.UI, "Muted text: hints, captions and units", p.TextMuted)
		line(r.Font.Small, "Small caption in the muted colour, 10.8 pt", p.TextMuted)
		line(r.Font.UI, "Disabled text (exempt from the minimums)", p.TextDisabled)
		y += 10 * s

		focus := float32(1)
		if i == 1 {
			focus = 0
		}
		t := TerminalAt(r.T, focus)
		for _, row := range []struct {
			s string
			c uint32
		}{
			{"$ ls -la  # foreground", t.FG}, {"total 48  # dim", t.Dim}, {"blue    teal    green", t.Blue},
			{"red     purple  orange", t.Red}, {"teal    green   purple", t.Teal},
		} {
			putMet(c, kg, grid.Mono, grid.Met, row.s, x, y, theme.Col(row.c))
			y += grid.RowPitch
		}
		y += 10 * s

		// A primary button, a default button, a text field and a selected row.
		bh := r.Px.ControlH
		box(c, kg, x, y, 110*s, bh, r.Px.RadiusMedium, p.Accent, 0)
		put(c, kg, r, r.Font.UI, "Primary", x+18*s, y+(bh-r.Met(r.Font.UI).H)/2, p.OnAccent)
		box(c, kg, x+122*s, y, 110*s, bh, r.Px.RadiusMedium, theme.Over(p.ControlFill, p.Surface), p.ControlBorder)
		put(c, kg, r, r.Font.UI, "Default", x+140*s, y+(bh-r.Met(r.Font.UI).H)/2, p.Text)
		y += bh + 10*s
		box(c, kg, x, y, 232*s, bh, r.Px.RadiusMedium, theme.Over(p.ControlFill, p.Surface), p.ControlBorder)
		put(c, kg, r, r.Font.UI, "Text field", x+10*s, y+(bh-r.Met(r.Font.UI).H)/2, p.Text)
		y += bh + 10*s
		box(c, kg, x, y, 232*s, r.Px.RowH, r.Px.RadiusSmall, p.Selection, 0)
		put(c, kg, r, r.Font.UI, "Selected row", x+10*s, y+(r.Px.RowH-r.Met(r.Font.UI).H)/2, p.SelectionText)
		y += r.Px.RowH + 14*s

		for _, st := range []struct {
			name string
			c    uint32
		}{{"Danger", p.Danger}, {"Warning", p.Warning}, {"Success", p.Success}, {"Info", p.Info}} {
			box(c, kg, x, y+3*s, 10*s, 10*s, 5*s, st.c, 0)
			put(c, kg, r, r.Font.UI, st.name, x+18*s, y, p.Text)
			y += r.Met(r.Font.UI).H + 4*s
		}
	}
}

func modeName(m theme.Mode) string {
	if m == theme.Dark {
		return "dark"
	}
	return "light"
}

// TestPNG writes the chrome of both modes, bare and with text on the panes,
// for review: GNUI_THEME_PNG=<dir> go test ./theme/rex/ -run PNG
func TestPNG(t *testing.T) {
	dir := os.Getenv("GNUI_THEME_PNG")
	if dir == "" {
		t.Skip("set GNUI_THEME_PNG=<dir> to write the chrome renders")
	}
	const w, h = 1057, 639
	for _, mode := range []theme.Mode{theme.Light, theme.Dark} {
		for _, withText := range []bool{false, true} {
			g := newRig(t, 2*w, 2*h, 2)
			r := Theme().Resolve(g.ts, 2, mode)
			s := &scene{r: &r, focus: [2]float32{1, 0}, buttons: theme.ButtonsTraffic}
			name := "chrome-" + modeName(mode)
			if withText {
				grid := NewGrid(g.ts, 2)
				s.content = legibility(&grid)
				s.buttons, s.title = theme.ButtonsCaption, "Rex — window title"
				name = "text-" + modeName(mode)
			}
			g.settle(s)
			if err := writePNG(filepath.Join(dir, name+".png"), g.pixels(), g.w, g.h); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// The chrome follows the mode: the focused pane is the mode's surface colour,
// the corner of the window is in the mode's field, and a mix in between is
// between them.
func TestChromeFollowsTheMode(t *testing.T) {
	g := newRig(t, 800, 500, 1)
	r := Theme().Resolve(g.ts, 1, theme.Light)
	s := &scene{r: &r, focus: [2]float32{1, 0}}
	l, _, _, _ := s.rects(g.w, g.h)
	cx, cy := int(l[0]+l[2])/2, int(l[1]+l[3])/2
	var surf, corner [3]uint32
	for i, T := range []float32{0, 0.5, 1} {
		r.Mix(T)
		g.settle(s)
		pix := g.pixels()
		surf[i], corner[i] = g.at(pix, cx, cy), g.at(pix, g.w-3, g.h-3)
	}
	if surf[0] != light.Surface || surf[2] != dark.Surface {
		t.Errorf("focused pane colour: light %#x (want %#x), dark %#x (want %#x)", surf[0], light.Surface, surf[2], dark.Surface)
	}
	if !(theme.Luminance(corner[0]) > 0.5 && theme.Luminance(corner[2]) < 0.05) {
		t.Errorf("window corner: light %#x, dark %#x", corner[0], corner[2])
	}
	for _, v := range [][3]uint32{surf, corner} {
		if m := theme.Luminance(v[1]); !(m < theme.Luminance(v[0]) && m > theme.Luminance(v[2])) {
			t.Errorf("a mix of light and dark is not between them: %#x, %#x, %#x", v[0], v[1], v[2])
		}
	}
}

// A frame of the chrome allocates nothing once warm: at rest, with the pointer
// on the divider and the caption buttons, and while the mode crossfades.
func TestChromeFramesAllocateNothing(t *testing.T) {
	g := newRig(t, 900, 560, 2)
	r := Theme().Resolve(g.ts, 2, theme.Light)
	s := &scene{r: &r, focus: [2]float32{1, 0}, buttons: theme.ButtonsCaption, title: "A window title"}
	_, gut, _, _ := s.rects(g.w, g.h)
	g.in.MouseX, g.in.MouseY = (gut[0]+gut[2])/2, (gut[1]+gut[3])/2
	g.settle(s)
	for name, step := range map[string]func(){
		"rest":      func() { g.frame(s) },
		"crossfade": func() { r.Mix(r.T + 0.013); g.frame(s) },
		"focus":     func() { s.focus[0], s.focus[1] = s.focus[1]+0.01, s.focus[0]-0.01; g.frame(s) },
	} {
		for i := 0; i < 3; i++ {
			step() // warm: the fill meshes and the field are built
		}
		r.Mix(0)
		if n := testing.AllocsPerRun(50, step); n != 0 {
			t.Errorf("%s: a frame allocates %v times", name, n)
		}
	}
}

// dragAt reports whether a press at (x, y) of the last frame moves the window.
func dragAt(c *ui.Context, x, y float32) bool {
	drag := false
	for _, d := range c.DragRegions(nil) {
		if x >= d.Rect[0] && x < d.Rect[2] && y >= d.Rect[1] && y < d.Rect[3] {
			drag = !d.Block
		}
	}
	return drag
}

func TestTitlebar(t *testing.T) {
	g := newRig(t, 900, 560, 1)
	r := Theme().Resolve(g.ts, 1, theme.Light)
	s := &scene{r: &r, focus: [2]float32{1, 0}, buttons: theme.ButtonsCaption, title: "A window title"}
	g.settle(s)
	W := float32(g.w)
	free := s.barRes.Free
	if !(free[0] == 0 && free[1] < W-3*r.Px.WindowButtonW+1 && free[1] > W/2) {
		t.Errorf("free extent %v of a %v px band with three %v px buttons", free, W, r.Px.WindowButtonW)
	}
	bar := r.Px.TitlebarH

	// The band is a drag region, except where the buttons are, and under it.
	if !dragAt(g.c, W/2, bar/2) {
		t.Error("the middle of the titlebar does not move the window")
	}
	btn := func(i int) (x, y float32) {
		return free[1] + (float32(i)+0.5)*r.Px.WindowButtonW + r.Px.GapSmall/2, bar / 2
	}
	for i := 0; i < 3; i++ {
		if x, y := btn(i); dragAt(g.c, x, y) {
			t.Errorf("window button %d moves the window instead of taking the press", i)
		}
	}
	if dragAt(g.c, W/2, bar+20) {
		t.Error("the area under the titlebar moves the window")
	}

	// Buttons, left to right: minimise, maximise, close; a click reports each.
	for i, want := range []theme.WindowButton{theme.ButtonMinimise, theme.ButtonMaximise, theme.ButtonClose} {
		x, y := btn(i)
		g.in.MouseX, g.in.MouseY = x, y
		g.frame(s) // hover
		g.in.SetButton(ui.ButtonLeft, true, 1)
		g.frame(s)
		if s.barRes.Clicked != theme.ButtonNone {
			t.Errorf("button %d clicked on press", i)
		}
		g.in.SetButton(ui.ButtonLeft, false, 0)
		g.frame(s)
		if s.barRes.Clicked != want {
			t.Errorf("button %d: Clicked = %v, want %v", i, s.barRes.Clicked, want)
		}
		g.in.MouseX, g.in.MouseY = -100, -100
		g.settle(s)
	}

	// Hovering a button lights it: close turns red, the others take the overlay.
	closeX, y := btn(2)
	before := g.at(g.pixels(), int(closeX)-int(r.Px.WindowButtonW)/2+6, int(y))
	g.in.MouseX, g.in.MouseY = closeX, y
	g.frame(s)
	g.settle(s)
	after := g.at(g.pixels(), int(closeX)-int(r.Px.WindowButtonW)/2+6, int(y))
	if before == after || (after>>24)&0xFF < (after>>16)&0xFF || (after>>24)&0xFF < (after>>8)&0xFF {
		t.Errorf("hovered close button: %#x before, %#x after; want a red pill", before, after)
	}

	// The traffic lights are decoration: no clicks, free space starts to their right.
	s.buttons = theme.ButtonsTraffic
	g.in.MouseX, g.in.MouseY = 23, bar/2
	g.in.SetButton(ui.ButtonLeft, true, 1)
	g.frame(s)
	g.in.SetButton(ui.ButtonLeft, false, 0)
	g.frame(s)
	if s.barRes.Clicked != theme.ButtonNone || s.barRes.Free[0] < 76 {
		t.Errorf("traffic lights: %+v", s.barRes)
	}
	// An app that needs no buttons gets the whole band.
	s.buttons = theme.ButtonsNone
	g.frame(s)
	if s.barRes.Free != [2]float32{0, W} {
		t.Errorf("no buttons: free %v, want the full band", s.barRes.Free)
	}
}

// A live macOS window has the system draw the window buttons over the
// titlebar band (ui.Context.SystemButtons) and an offscreen render draws them
// itself (ButtonsTraffic): both leave the app the same part of the band, at
// every scale, so the layout of a window and of its screenshot cannot differ.
// And a band the system draws nothing over is free from end to end.
func TestTitlebarFreeIsTheSameForSystemAndDrawnButtons(t *testing.T) {
	for _, scale := range []float32{1, 1.5, 2, 3} {
		g := newRig(t, int(900*scale), int(560*scale), scale)
		r := Theme().Resolve(g.ts, scale, theme.Light)
		s := &scene{r: &r, focus: [2]float32{1, 0}}
		W := float32(g.w)

		s.buttons = theme.ButtonsTraffic
		g.c.SetSystemButtons([4]float32{})
		g.settle(s)
		drawn := s.barRes.Free

		s.buttons = theme.ButtonsNone
		g.c.SetSystemButtons(theme.TrafficRect(scale))
		g.settle(s)
		system := s.barRes.Free

		if drawn != system {
			t.Errorf("scale %v: free %v with drawn buttons, %v with the system's", scale, drawn, system)
		}
		if want := theme.Snap(76*scale + r.Px.Gap); drawn != [2]float32{want, W} {
			t.Errorf("scale %v: free %v, want [%v %v]", scale, drawn, want, W)
		}

		// buttons on the right of the band: the free part ends before them
		g.c.SetSystemButtons([4]float32{W - 80*scale, 10, W - 10*scale, 40})
		g.frame(s)
		if want := theme.Snap(W - 80*scale - r.Px.Gap); s.barRes.Free != [2]float32{0, want} {
			t.Errorf("scale %v: buttons on the right: free %v, want [0 %v]", scale, s.barRes.Free, want)
		}

		g.c.SetSystemButtons([4]float32{})
		g.frame(s)
		if s.barRes.Free != [2]float32{0, W} {
			t.Errorf("scale %v: no buttons over the band: free %v, want the full band", scale, s.barRes.Free)
		}
	}
}

// The drawn traffic lights are where the geometry says (theme.TrafficDiscs),
// which is where the system puts its own: the pixel under the centre of each
// is the button's colour.
func TestDrawnTrafficLightsSitAtTheSharedGeometry(t *testing.T) {
	g := newRig(t, 900, 560, 2)
	r := Theme().Resolve(g.ts, 2, theme.Light)
	s := &scene{r: &r, focus: [2]float32{1, 0}, buttons: theme.ButtonsTraffic}
	g.settle(s)
	pix := g.pixels()
	for i, d := range theme.TrafficDiscs(2) {
		x, y := int((d[0]+d[2])/2), int((d[1]+d[3])/2)
		c := g.at(pix, x, y)
		red, green := (c>>24)&0xFF, (c>>16)&0xFF
		switch i {
		case 0:
			if red < 200 || green > 160 {
				t.Errorf("close button centre %d,%d is %#x, want red", x, y, c)
			}
		case 1:
			if red < 200 || green < 160 {
				t.Errorf("minimise button centre %d,%d is %#x, want yellow", x, y, c)
			}
		case 2:
			if green < 160 || red > 200 {
				t.Errorf("zoom button centre %d,%d is %#x, want green", x, y, c)
			}
		}
	}
}

// A recoloured field (ThemeWithField) draws its own stops and glass, and
// leaves Rex's as they were.
func TestThemeWithFieldRecoloursTheField(t *testing.T) {
	fc := FieldColors{
		Light:     []FieldStop{{0, 0xDDEEE4, 0xDDEEE4, 0xDDEEE4}, {1278, 0xDDEEE4, 0xDDEEE4, 0xDDEEE4}},
		Dark:      []FieldStop{{0, 0x18201D, 0x18201D, 0x18201D}},
		DarkGlass: 0x26302C,
	}
	var own, stock Field
	own.look = newLook(fc)
	for _, m := range []struct {
		t    float32
		want uint32
	}{{0, 0xDDEEE4FF}, {1, 0x18201DFF}} {
		own.Build(800, 600, 0.5, m.t)
		if c := own.cells[own.ncells/2].Color[0]; c != m.want {
			t.Errorf("mix %v: the recoloured field is %08X, want %08X", m.t, c, m.want)
		}
		stock.Build(800, 600, 0.5, m.t)
		if c, rex := stock.cells[stock.ncells/2].Color[0], rexLook.fieldAt(m.t, 0, 0, 0); c == m.want || rex == m.want {
			t.Errorf("mix %v: Rex's own field took the recolouring", m.t)
		}
	}
	// the dark glass: half the glass over the field
	own.Build(800, 600, 0.5, 1)
	if g, want := own.fillAt(800, 600), theme.MixRGBA(0x18201DFF, 0x26302CFF, darkAlpha); g != want {
		t.Errorf("the dark glass is %08X, want %08X", g, want)
	}
	th := ThemeWithField(fc)
	if g := th.Roles[theme.Dark][theme.GlassTint]; g != theme.ColA(0x26302C, darkAlpha) {
		t.Errorf("GlassTint is %08X, want the dark glass", g)
	}
	if g := Theme().Roles[theme.Dark][theme.GlassTint]; g != theme.ColA(darkGlass, darkAlpha) {
		t.Errorf("Rex's own GlassTint changed to %08X", g)
	}
	// the scrim over a backdrop: the one given, Rex's for the other mode
	if sc := newLook(FieldColors{Scrim: [2]uint32{0xE6F2EA, 0}}).scrim; sc != [2]uint32{0xE6F2EA, scrimDark} || rexLook.scrim != [2]uint32{scrimLight, scrimDark} {
		t.Errorf("scrims %06X, Rex's %06X", sc, rexLook.scrim)
	}
}

// Replacing per-cell boxes keeps their snapped geometry, clip and opacity,
// including fractional pane origins and dimensions during a live resize.
func TestFillMeshMatchesCellBoxes(t *testing.T) {
	g := newRig(t, 900, 560, 2)
	r := Theme().Resolve(g.ts, 2, theme.Light)
	ch := &chrome{}
	var list gpu.DrawList
	for _, rect := range [][4]float32{{10, 30, 410, 430}, {10.25, 30.75, 410.8, 430.1}, {10.6, 30.1, 410.2, 430.7}} {
		for _, focus := range []float32{0, .4, .9} {
			s := theme.Surface{Rect: rect, Focus: focus, Slot: 0}
			build := func(mesh bool) {
				g.c.Begin(&g.in, g.w, g.h, g.scale, 1.0/60)
				ch.ensure(g.c, &r)
				g.c.PushOpacity(.37)
				theme.At(g.c, rect[0], rect[1], rect[2]-rect[0], rect[3]-rect[1])
				g.c.Push(g.c.BoxKeyed(fl|ui.Clip, 1, nil))
				kg := theme.KeyGen{Base: 2}
				if mesh {
					ch.drawFill(g.c, &r, s, &kg)
				} else {
					m := ch.meshes[0]
					for _, cell := range m.cells[:m.n] {
						theme.At(g.c, cell.r[0]-rect[0], cell.r[1]-rect[1], cell.r[2]-cell.r[0], cell.r[3]-cell.r[1])
						g.c.NextBG4(theme.MixRGBA(cell.c[0], r.Pal.Surface|255, focus), theme.MixRGBA(cell.c[1], r.Pal.Surface|255, focus), theme.MixRGBA(cell.c[2], r.Pal.Surface|255, focus), theme.MixRGBA(cell.c[3], r.Pal.Surface|255, focus))
						g.c.NextRadii(cell.radii[0], cell.radii[1], cell.radii[2], cell.radii[3])
						g.c.BoxKeyed(fl|ui.DrawBackground, kg.Next(), nil)
					}
				}
				g.c.Pop()
				g.c.PopOpacity()
				g.c.End(&list)
			}
			build(true)
			got := slices.Clone(list.Quads)
			build(false)
			if len(got) != len(list.Quads) {
				t.Fatalf("rect %v: %d mesh quads, %d cell quads", rect, len(got), len(list.Quads))
			}
			for i, q := range list.Quads {
				if got[i] != q {
					t.Fatalf("rect %v focus %v cell %d: mesh %+v, cells %+v", rect, focus, i, got[i], q)
				}
			}
		}
	}
}
