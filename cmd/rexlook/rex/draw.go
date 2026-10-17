package rex

import (
	"math"

	"gonativeui/text"
	"gonativeui/ui"
)

const fl = ui.FloatingX | ui.FloatingY

// draw builds the whole frame in tree order: background, panes, titlebar,
// traffic lights. Every box is a floating child of its container with a
// whole-pixel position and size.
func (r *Rex) draw(c *ui.Context) {
	r.drawBackground(c)
	r.drawPanes(c)
	r.drawTitlebar(c)
	if r.Traffic {
		r.drawTrafficLights(c)
	}
}

// at positions and sizes the next box.
func at(c *ui.Context, x, y, w, h float32) {
	c.NextPos(x, y)
	c.NextSize(ui.Px(w), ui.Px(h))
}

func (r *Rex) drawBackground(c *ui.Context) {
	kg := keyGen{base: keyBG}
	f := &r.fld
	for i := 0; i < f.ncells; i++ {
		cell := &f.cells[i]
		at(c, cell.r[0], cell.r[1], cell.r[2]-cell.r[0], cell.r[3]-cell.r[1])
		c.NextBG4(cell.c[0], cell.c[1], cell.c[2], cell.c[3])
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
	}
}

// put places the text s with its pen origin at (x, baseline) in the
// coordinates of the current parent. width > 0 fixes the box width (text
// wider than that fades out, with ui.FadeOverflowX in extra).
func (r *Rex) put(c *ui.Context, kg *keyGen, f text.FontKey, s string, x, baseline float32, color uint32, width float32, extra ui.Flags) {
	fm := r.fm[f]
	ix := float32(math.Floor(float64(x)))
	frac := x - ix
	c.NextFont(f)
	c.NextTextColor(color)
	c.NextPos(ix, baseline-fm.off)
	c.NextPadding(frac, 0, 0, 0)
	if width > 0 {
		c.NextSize(ui.Px(width+frac), ui.Px(fm.h))
	} else {
		c.NextSize(ui.TextSize(0), ui.Px(fm.h))
	}
	c.BoxKeyed(ui.DrawText|fl|extra, kg.next(), strBytes(s))
}

// iconAt builds a box that shows icon id centred at (cx, cy) in the parent's
// coordinates, in colour col (RGBA).
func (r *Rex) iconAt(c *ui.Context, kg *keyGen, id iconID, cx, cy float32, color uint32) {
	flags, disp := r.ic.set(c, id)
	var w, h float32
	if flags == ui.DrawImage {
		sr := r.ic.rects[id]
		w, h = float32(sr.w), float32(sr.h)
	} else {
		w, h = 20*r.k, r.fm[r.fSym].h
		c.NextFont(r.fSym)
		c.NextAlign(ui.AlignCenter)
	}
	c.NextTextColor(color)
	at(c, snap(cx-w/2), snap(cy-h/2), w, h)
	c.BoxKeyed(flags|fl, kg.next(), disp)
}

// iconButton builds a square hit box of side hit centred at (cx, cy) with
// icon id centred in it. The icon goes from colour base to hover as the
// pointer comes in, over a faint rounded background. A disabled button is
// drawn faded and takes no input.
func (r *Rex) iconButton(c *ui.Context, key ui.Key, id iconID, cx, cy, hit float32, base, hover uint32, enabled bool) {
	t := c.HotTOf(key)
	pressed := c.ActiveTOf(key)
	flags := ui.DrawBackground | fl
	bx, by := snap(cx-hit/2), snap(cy-hit/2)
	if enabled {
		flags |= ui.Clickable | ui.HotAnimation | ui.ActiveAnimation
		c.NextBG(colA(0x2A2030, 0.08*t+0.07*pressed))
	} else {
		c.PushOpacity(0.4)
	}
	at(c, bx, by, hit, hit)
	c.NextRadius(6 * r.scale)
	btn := c.BoxKeyed(flags, key, nil)
	if !enabled {
		c.PopOpacity()
	}
	if enabled {
		r.addCursor(key, CursorPointer)
	}
	// The icon is placed against the parent's grid, not the button's.
	c.Push(btn)
	kg := keyGen{base: ui.KeyN(key, 1000)}
	if !enabled {
		c.PushOpacity(0.4)
	}
	r.iconAt(c, &kg, id, cx-bx, cy-by, col(mixRGB(base, hover, max(t, pressed))))
	if !enabled {
		c.PopOpacity()
	}
	c.Pop()
}

// ---- titlebar ----

const (
	capsuleL   = 402 // reference px
	capsuleR   = 380 // distance of the capsule's right end from the window's right edge
	tabInset   = 3
	maxTabWRef = 520
)

func (r *Rex) drawTitlebar(c *ui.Context) {
	k := r.k
	W := float32(r.lastW)
	n := r.nTabs

	// Geometry: the tabs share the capsule equally, up to a maximum width.
	tabW := (W/k - capsuleR - capsuleL - 2*tabInset) / float32(n)
	tabW = max(min(tabW, maxTabWRef), 40)
	tabX := func(i int) float32 { return (capsuleL + tabInset + float32(i)*tabW) * k }
	y0, y1 := snap(16*k), snap(76*k)
	capsule := [4]float32{snap(capsuleL * k), snap(12 * k), snap(tabX(n) + tabInset*k), snap(80 * k)}
	r.capsule = capsule
	for i := 0; i < n; i++ {
		r.tabRects[i] = [4]float32{snap(tabX(i)), y0, snap(tabX(i + 1)), y1}
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
		pill[0], pill[2] = snap(sx0/sw), snap(sx1/sw)
	}
	r.pill = pill

	// Capsule: a translucent group that clips what is inside it (the pill's
	// shadows stay within).
	at(c, capsule[0], capsule[1], capsule[2]-capsule[0], capsule[3]-capsule[1])
	c.NextBG4(col(0xE8DCE9), col(0xE8DCE9), col(0xE2DCE2), col(0xE2DCE2))
	c.NextRadius(34 * k)
	cap := c.BoxKeyed(ui.DrawBackground|ui.Clip|fl, ui.KeyN(keyBar, 1), nil)
	c.Push(cap)
	ox, oy := capsule[0], capsule[1]

	// Pill, with the edge ring around the two end caps only.
	at(c, pill[0]-ox, pill[1]-oy, pill[2]-pill[0], pill[3]-pill[1])
	c.NextBG4(col(0xFFFAFF), col(0xFFFAFF), col(0xFCF8FC), col(0xFCF8FC))
	c.NextRadius(30 * k)
	c.NextShadows(ui.Shadow{OffY: 2 * k, Sigma: 2.5 * k, Color: colA(0x3A2A4A, 0.13)},
		ui.Shadow{OffY: 0, Sigma: 1.5 * k, Color: colA(0x3A2A4A, 0.14)})
	c.NextBorder(col(0xBEB9C0), max(1, k))
	c.NextBorderSides(ui.SideLeft | ui.SideRight)
	c.BoxKeyed(ui.DrawBackground|ui.DrawShadow|ui.DrawBorder|fl, ui.KeyN(keyBar, 2), nil)

	// Separators between tabs that are both inactive.
	for i := 0; i+1 < n; i++ {
		a := (1 - act[i]) * (1 - act[i+1]) * 0.45
		if a < 0.004 {
			continue
		}
		b := tabX(i + 1)
		at(c, snap(b-k)-ox, snap(28*k)-oy, snap(b+k)-snap(b-k), snap(63*k)-snap(28*k))
		c.NextBG(colA(0x8C8290, a))
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(keyBar, uint64(10+i)), nil)
	}

	for i := 0; i < n; i++ {
		r.drawTab(c, i, act[i], tabX(i), tabW)
	}
	c.Pop()

	// Host block: device glyph, host name over machine name.
	kg := keyGen{base: ui.KeyN(keyBar, 3)}
	r.iconAt(c, &kg, icDevice, 209*k, 43*k, col(0x5F5E63))
	r.put(c, &kg, r.fUISemi, "rex-snake", 245*k, 42*k, col(0x5F5D64), 0, 0)
	r.put(c, &kg, r.fSub, "Mac Studio", 245*k, 66*k, col(0x87848B), 0, 0)

	// Right edge: ⌘ and +.
	hit := 28 * k
	r.iconButton(c, keyCmd, icCommand, W-115.5*k, 45*k, hit, 0x5F5D62, 0x1E2227, true)
	r.iconButton(c, keyPlus, icPlus, W-46.5*k, 45*k, hit, 0x5F5D62, 0x1E2227, r.nTabs < maxTabs)
}

// drawTab builds tab i: hover background, the stacked-card icon and the title.
func (r *Rex) drawTab(c *ui.Context, i int, act, absX, tabW float32) {
	k := r.k
	t := &r.tabs[i]
	rc := r.tabRects[i]
	key := tabKey(t.uid)
	hot := c.HotTOf(key)
	ox, oy := r.capsule[0], r.capsule[1]

	at(c, rc[0]-ox, rc[1]-oy, rc[2]-rc[0], rc[3]-rc[1])
	c.NextBG(colA(0xFFFFFF, 0.55*hot*(1-act)))
	c.NextRadius(30 * k)
	tb := c.BoxKeyed(ui.DrawBackground|ui.Clip|ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
	r.addCursor(key, CursorPointer)
	c.Push(tb)
	defer c.Pop()
	tx, ty := rc[0], rc[1]

	// Stacked cards: coloured cards peek out behind the front card.
	st := &t.style
	kg := keyGen{base: ui.KeyN(key, 7)}
	nb := 0
	for nb < len(st.backs) && st.backs[nb] != 0 {
		nb++
	}
	x0 := absX + st.iconX*k
	for j := nb; j >= 1; j-- {
		fj := float32(j)
		at(c, snap(x0+st.step*fj*k)-tx, snap((27+3*fj)*k)-ty, snap(x0+(st.step*fj+45)*k)-snap(x0+st.step*fj*k), snap((65-3*fj)*k)-snap((27+3*fj)*k))
		c.NextBG(col(st.backs[j-1]))
		c.NextRadius(8 * k)
		c.BoxKeyed(ui.DrawBackground|fl, kg.next(), nil)
	}
	fx0, fy0, fx1, fy1 := snap(x0), snap(27*k), snap(x0+45*k), snap(65*k)
	flags, disp := r.ic.set(c, st.icon)
	if flags == ui.DrawText {
		c.NextFont(r.fSym)
		c.NextAlign(ui.AlignCenter)
	}
	c.NextTextColor(col(st.symbol))
	at(c, fx0-tx, fy0-ty, fx1-fx0, fy1-fy0)
	c.NextBG(st.front)
	c.NextRadius(8 * k)
	c.NextBorder(st.border, max(1, k))
	c.NextShadow(ui.Shadow{OffY: 1.5 * k, Sigma: 2 * k, Color: 0x00000030})
	c.BoxKeyed(flags|ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|fl, kg.next(), disp)

	// Title: the name, then the lighter path; text that does not fit fades out.
	nameCol := col(mixRGB(0x5A5A60, 0x1E2227, act))
	pathCol := col(mixRGB(0x928F95, 0x1E2227, act))
	x := absX + st.textX*k
	end := rc[2] - 10*r.scale // right limit of the title
	base := 55 * k
	room := end - x
	if room <= 4 {
		return
	}
	nw := r.width(r.fUI, t.name)
	r.titleRun(c, &kg, t.name, x-tx, base-ty, nameCol, min(nw, room), nw > room)
	if px := x + nw; px < end-4 {
		pw := r.width(r.fUI, t.path)
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
	r.put(c, kg, r.fUI, s, x, baseline, color, w, fx)
}

// ---- traffic lights (offscreen output only: the window system draws them) ----

func (r *Rex) drawTrafficLights(c *ui.Context) {
	k := r.k
	kg := keyGen{base: ui.KeyN(keyBar, 4)}
	for _, l := range [...]struct {
		cx          float32
		top, bottom uint32
		ring        uint32
	}{
		{46, 0xEC766A, 0xE48980, 0xBF3A30},
		{92, 0xF4C653, 0xF2C85E, 0xB5701F},
		{138, 0x8ED064, 0x91D068, 0x3E8F30},
	} {
		d := 28 * k
		x0, y0 := snap(l.cx*k-d/2), snap(46*k-d/2)
		at(c, x0, y0, snap(l.cx*k+d/2)-x0, snap(46*k+d/2)-y0)
		c.NextBG4(col(l.top), col(l.top), col(l.bottom), col(l.bottom))
		c.NextRadius(d / 2)
		c.NextBorder(colA(l.ring, 0.85), max(1, 1.5*k))
		c.BoxKeyed(ui.DrawBackground|ui.DrawBorder|fl, kg.next(), nil)
	}
}
