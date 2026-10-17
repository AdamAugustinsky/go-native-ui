package rex

import (
	"math"

	"gonativeui/cmd/internal/rexkit"
	"gonativeui/ui"
)

const fl = rexkit.Fl

// draw builds the whole frame in tree order: background, panes, titlebar,
// traffic lights. Every box is a floating child of its container with a
// whole-pixel position and size.
func (r *Rex) draw(c *ui.Context) {
	r.DrawBackground(c)
	r.drawPanes(c)
	r.drawTitlebar(c)
	if r.Traffic {
		r.DrawTrafficLights(c)
	}
}

// ---- titlebar ----

const (
	capsuleL   = 402 // reference px
	capsuleR   = 380 // distance of the capsule's right end from the window's right edge
	tabInset   = 3
	maxTabWRef = 520
)

func (r *Rex) drawTitlebar(c *ui.Context) {
	k := r.K
	W := float32(r.lastW)
	n := r.nTabs

	// Geometry: the tabs share the capsule equally, up to a maximum width.
	tabW := (W/k - capsuleR - capsuleL - 2*tabInset) / float32(n)
	tabW = max(min(tabW, maxTabWRef), 40)
	tabX := func(i int) float32 { return (capsuleL + tabInset + float32(i)*tabW) * k }
	y0, y1 := rexkit.Snap(16*k), rexkit.Snap(76*k)

	// The titlebar band moves the window: a drag area under the tabs and
	// buttons, which keep their presses (ui.Context.DragRegions).
	rexkit.At(c, 0, 0, W, rexkit.Snap(TitlebarPt*r.Scale))
	c.BoxKeyed(ui.WindowDrag|fl, ui.KeyN(keyBar, 99), nil)
	capsule := [4]float32{rexkit.Snap(capsuleL * k), rexkit.Snap(12 * k), rexkit.Snap(tabX(n) + tabInset*k), rexkit.Snap(80 * k)}
	r.capsule = capsule
	for i := 0; i < n; i++ {
		r.tabRects[i] = [4]float32{rexkit.Snap(tabX(i)), y0, rexkit.Snap(tabX(i + 1)), y1}
	}

	// Activeness of each tab (0..1) drives the pill, the title colours and
	// the separators.
	var act [maxTabs]float32
	var sw, sx0, sx1 float32
	for i := 0; i < n; i++ {
		tgt := float32(0)
		if i == r.active {
			tgt = 1
		}
		act[i] = c.AnimateValue(ui.KeyN(keyTabT, uint64(r.tabs[i].uid)), tgt, 26)
		sw += act[i]
		sx0 += act[i] * r.tabRects[i][0]
		sx1 += act[i] * r.tabRects[i][2]
	}
	pill := [4]float32{r.tabRects[r.active][0], y0, r.tabRects[r.active][2], y1}
	if sw > 0.001 {
		pill[0], pill[2] = rexkit.Snap(sx0/sw), rexkit.Snap(sx1/sw)
	}
	r.pill = pill

	// Capsule: a translucent group that clips what is inside it (the pill's
	// shadows stay within).
	rexkit.At(c, capsule[0], capsule[1], capsule[2]-capsule[0], capsule[3]-capsule[1])
	c.NextBG4(rexkit.Col(0xE8DCE9), rexkit.Col(0xE8DCE9), rexkit.Col(0xE2DCE2), rexkit.Col(0xE2DCE2))
	c.NextRadius(34 * k)
	cap := c.BoxKeyed(ui.DrawBackground|ui.Clip|fl, ui.KeyN(keyBar, 1), nil)
	c.Push(cap)
	ox, oy := capsule[0], capsule[1]

	// Pill, with the edge ring around the two end caps only.
	rexkit.At(c, pill[0]-ox, pill[1]-oy, pill[2]-pill[0], pill[3]-pill[1])
	c.NextBG4(rexkit.Col(0xFFFAFF), rexkit.Col(0xFFFAFF), rexkit.Col(0xFCF8FC), rexkit.Col(0xFCF8FC))
	c.NextRadius(30 * k)
	c.NextShadows(ui.Shadow{OffY: 2 * k, Sigma: 2.5 * k, Color: rexkit.ColA(0x3A2A4A, 0.13)},
		ui.Shadow{OffY: 0, Sigma: 1.5 * k, Color: rexkit.ColA(0x3A2A4A, 0.14)})
	c.NextBorder(rexkit.Col(0xBEB9C0), max(1, k))
	c.NextBorderSides(ui.SideLeft | ui.SideRight)
	c.BoxKeyed(ui.DrawBackground|ui.DrawShadow|ui.DrawBorder|fl, ui.KeyN(keyBar, 2), nil)

	// Separators between tabs that are both inactive.
	for i := 0; i+1 < n; i++ {
		a := (1 - act[i]) * (1 - act[i+1]) * 0.45
		if a < 0.004 {
			continue
		}
		b := tabX(i + 1)
		rexkit.At(c, rexkit.Snap(b-k)-ox, rexkit.Snap(28*k)-oy, rexkit.Snap(b+k)-rexkit.Snap(b-k), rexkit.Snap(63*k)-rexkit.Snap(28*k))
		c.NextBG(rexkit.ColA(0x8C8290, a))
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(keyBar, uint64(10+i)), nil)
	}

	for i := 0; i < n; i++ {
		r.drawTab(c, i, act[i], tabX(i), tabW)
	}
	c.Pop()

	// Host block: device glyph, host name over machine name.
	kg := keyGen{Base: ui.KeyN(keyBar, 3)}
	r.IconAt(c, &kg, icDevice, 209*k, 43*k, rexkit.Col(0x5F5E63))
	r.Put(c, &kg, r.FUISemi, "rex-snake", 245*k, 42*k, rexkit.Col(0x5F5D64), 0, 0)
	r.Put(c, &kg, r.FSub, "Mac Studio", 245*k, 66*k, rexkit.Col(0x87848B), 0, 0)

	// Right edge: ⌘ and +.
	hit := 28 * k
	r.IconButton(c, keyCmd, icCommand, W-115.5*k, 45*k, hit, 0x5F5D62, 0x1E2227, true)
	r.IconButton(c, keyPlus, icPlus, W-46.5*k, 45*k, hit, 0x5F5D62, 0x1E2227, r.nTabs < maxTabs)
}

// drawTab builds tab i: hover background, the stacked-card icon and the title.
func (r *Rex) drawTab(c *ui.Context, i int, act, absX, tabW float32) {
	k := r.K
	t := &r.tabs[i]
	rc := r.tabRects[i]
	key := tabKey(t.uid)
	hot := c.HotTOf(key)
	ox, oy := r.capsule[0], r.capsule[1]

	rexkit.At(c, rc[0]-ox, rc[1]-oy, rc[2]-rc[0], rc[3]-rc[1])
	c.NextBG(rexkit.ColA(0xFFFFFF, 0.55*hot*(1-act)))
	c.NextRadius(30 * k)
	c.NextCursor(ui.CursorPointer)
	tb := c.BoxKeyed(ui.DrawBackground|ui.Clip|ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
	c.Push(tb)
	defer c.Pop()
	tx, ty := rc[0], rc[1]

	// Stacked cards: coloured cards peek out behind the front card.
	st := &t.style
	kg := keyGen{Base: ui.KeyN(key, 7)}
	nb := 0
	for nb < len(st.backs) && st.backs[nb] != 0 {
		nb++
	}
	x0 := absX + st.iconX*k
	for j := nb; j >= 1; j-- {
		fj := float32(j)
		rexkit.At(c, rexkit.Snap(x0+st.step*fj*k)-tx, rexkit.Snap((27+3*fj)*k)-ty, rexkit.Snap(x0+(st.step*fj+45)*k)-rexkit.Snap(x0+st.step*fj*k), rexkit.Snap((65-3*fj)*k)-rexkit.Snap((27+3*fj)*k))
		c.NextBG(rexkit.Col(st.backs[j-1]))
		c.NextRadius(8 * k)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}
	fx0, fy0, fx1, fy1 := rexkit.Snap(x0), rexkit.Snap(27*k), rexkit.Snap(x0+45*k), rexkit.Snap(65*k)
	flags, disp := r.IC.Set(c, st.icon)
	if flags == ui.DrawText {
		c.NextFont(r.FSym)
		c.NextAlign(ui.AlignCenter)
	}
	c.NextTextColor(rexkit.Col(st.symbol))
	rexkit.At(c, fx0-tx, fy0-ty, fx1-fx0, fy1-fy0)
	c.NextBG(st.front)
	c.NextRadius(8 * k)
	c.NextBorder(st.border, max(1, k))
	c.NextShadow(ui.Shadow{OffY: 1.5 * k, Sigma: 2 * k, Color: 0x00000030})
	c.BoxKeyed(flags|ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|fl, kg.Next(), disp)

	// Title: the name, then the lighter path; text that does not fit fades out.
	nameCol := rexkit.Col(rexkit.MixRGB(0x5A5A60, 0x1E2227, act))
	pathCol := rexkit.Col(rexkit.MixRGB(0x928F95, 0x1E2227, act))
	x := absX + st.textX*k
	end := rc[2] - 10*r.Scale // right limit of the title
	base := 55 * k
	room := end - x
	if room <= 4 {
		return
	}
	nw := r.Width(r.FUI, t.name)
	r.titleRun(c, &kg, t.name, x-tx, base-ty, nameCol, min(nw, room), nw > room)
	if px := x + nw; px < end-4 {
		pw := r.Width(r.FUI, t.path)
		r.titleRun(c, &kg, t.path, px-tx, base-ty, pathCol, min(pw, end-px), pw > end-px)
	}
}

// titleRun puts a tab title run of advance adv (or the room left, when it
// overflows and fades).
func (r *Rex) titleRun(c *ui.Context, kg *keyGen, s string, x, baseline float32, color uint32, room float32, over bool) {
	w := float32(math.Ceil(float64(room))) + 2
	var fx ui.Flags
	if over {
		w, fx = room, ui.FadeOverflowX
	}
	r.Put(c, kg, r.FUI, s, x, baseline, color, w, fx)
}
