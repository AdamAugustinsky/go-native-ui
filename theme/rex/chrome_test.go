package rex

import (
	"os"
	"path/filepath"
	"testing"

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
