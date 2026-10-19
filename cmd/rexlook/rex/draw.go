package rex

import (
	"math"

	"gonativeui/theme"
	"gonativeui/ui"
)

// draw builds the whole frame in tree order: background, panes, titlebar (with
// the traffic lights, if the scene draws them). Every box is a floating child
// of its container with a whole-pixel position and size.
func (r *Rex) draw(c *ui.Context) {
	r.th.Chrome.Background(c, &r.th)
	r.drawPanes(c)
	r.drawTitlebar(c)
}

// ---- titlebar ----

// inactiveOpacity is how much of the titlebar's content shows while the
// window is not key.
const inactiveOpacity = 0.55

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
	y0, y1 := theme.Snap(16*k), theme.Snap(76*k)

	// The titlebar band moves the window: a drag area under the tabs and
	// buttons, which keep their presses (ui.Context.DragRegions). The traffic
	// lights are part of it when the scene draws them.
	buttons := theme.ButtonsNone
	if r.Traffic {
		buttons = theme.ButtonsTraffic
	}
	r.th.Chrome.Titlebar(c, &r.th, theme.Titlebar{Width: W, Buttons: buttons})
	capsule := [4]float32{theme.Snap(capsuleL * k), theme.Snap(12 * k), theme.Snap(tabX(n) + tabInset*k), theme.Snap(80 * k)}
	r.capsule = capsule
	for i := 0; i < n; i++ {
		r.tabRects[i] = [4]float32{theme.Snap(tabX(i)), y0, theme.Snap(tabX(i + 1)), y1}
	}

	// The window's key state (animated) dims the band while another window
	// has the keyboard, as AppKit's titlebar content does: quieter titles and
	// icons, a flatter pill.
	keyTgt := float32(0)
	if c.Input().Focused {
		keyTgt = 1
	}
	kw := c.AnimateValue(keyKeyW, keyTgt, 12)
	r.keyW = kw

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
		pill[0], pill[2] = theme.Snap(sx0/sw), theme.Snap(sx1/sw)
	}
	r.pill = pill

	// Capsule: a translucent group that clips what is inside it (the pill's
	// shadows stay within).
	theme.At(c, capsule[0], capsule[1], capsule[2]-capsule[0], capsule[3]-capsule[1])
	capTop, capBot := r.col(0xE8DCE9, 0x2B2C3B), r.col(0xE2DCE2, 0x282937)
	if r.backdrop {
		// Over the material the group is the capsule's own tint, half
		// covering: the material shows through it, and it pulls what shows
		// toward the tint, which keeps the tab titles' contrast (labelAlpha).
		capTop, capBot = r.colA(0xE8DCE9, 0x2B2C3B, capsuleAlpha[0], capsuleAlpha[1]), r.colA(0xE2DCE2, 0x282937, capsuleAlpha[0], capsuleAlpha[1])
	}
	c.NextBG4(capTop, capTop, capBot, capBot)
	c.NextRadius(34 * k)
	cap := c.BoxKeyed(ui.DrawBackground|ui.Clip|fl, ui.KeyN(keyBar, 1), nil)
	c.Push(cap)
	ox, oy := capsule[0], capsule[1]

	// Pill, with the edge ring around the two end caps only.
	theme.At(c, pill[0]-ox, pill[1]-oy, pill[2]-pill[0], pill[3]-pill[1])
	pillTop, pillBot := r.col(0xFFFAFF, 0x4A4D66), r.col(0xFCF8FC, 0x434559)
	c.NextBG4(pillTop, pillTop, pillBot, pillBot)
	c.NextRadius(30 * k)
	sh := theme.Lerp(0.45, 1, kw)
	c.NextShadows(ui.Shadow{OffY: 2 * k, Sigma: 2.5 * k, Color: r.colA(0x3A2A4A, 0x000000, 0.13*sh, 0.40*sh)},
		ui.Shadow{OffY: 0, Sigma: 1.5 * k, Color: r.colA(0x3A2A4A, 0x000000, 0.14*sh, 0.45*sh)})
	c.NextBorder(r.col(0xBEB9C0, 0x5C5F7A), max(1, k))
	c.NextBorderSides(ui.SideLeft | ui.SideRight)
	c.BoxKeyed(ui.DrawBackground|ui.DrawShadow|ui.DrawBorder|fl, ui.KeyN(keyBar, 2), nil)

	// Separators between tabs that are both inactive.
	for i := 0; i+1 < n; i++ {
		a := (1 - act[i]) * (1 - act[i+1]) * 0.45
		if a < 0.004 {
			continue
		}
		b := tabX(i + 1)
		theme.At(c, theme.Snap(b-k)-ox, theme.Snap(28*k)-oy, theme.Snap(b+k)-theme.Snap(b-k), theme.Snap(63*k)-theme.Snap(28*k))
		c.NextBG(r.colA(0x8C8290, 0xB9B3CC, a, 0.6*a))
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(keyBar, uint64(10+i)), nil)
	}

	for i := 0; i < n; i++ {
		r.drawTab(c, i, act[i], tabX(i), tabW)
	}
	c.Pop()

	// Host block: device glyph, host name over machine name.
	kg := keyGen{Base: ui.KeyN(keyBar, 3)}
	c.PushOpacity(theme.Lerp(inactiveOpacity, 1, kw))
	devCol, hostCol, machCol := r.col(0x5F5E63, 0xA3A7B8), r.col(0x5F5D64, 0xB8BBCB), r.col(0x87848B, 0x8D91A3)
	if r.backdrop {
		devCol = r.label(0.62)
	}
	r.IconAt(c, &kg, icDevice, 209*k, 43*k, devCol)
	if r.backdrop {
		// Over the material the text dims by its colour, not by opacity,
		// down to floors (labelAlpha).
		c.PopOpacity()
		hostCol, machCol = r.bandLabel(labelTitle), r.bandLabel(labelSecondary)
	}
	r.Put(c, &kg, r.th.Font.UISemi, "rex-snake", 245*k, 42*k, hostCol, 0, 0)
	r.Put(c, &kg, r.th.Font.Small, "Mac Studio", 245*k, 66*k, machCol, 0, 0)
	if !r.backdrop {
		c.PopOpacity()
	}

	// Right edge: ⌘ and +, the window's own when it has native buttons.
	if r.btns != nil || r.shellShot {
		if r.btns != nil {
			r.btns.SetButtonEnabled(cmdPlus, r.nTabs < maxTabs)
		}
		return
	}
	hit := 28 * k
	r.IconButton(c, keyCmd, icCommand, W-115.5*k, 45*k, hit, r.tone(0x5F5D62, 0xA3A7B8), r.tone(0x1E2227, 0xECEDF3), true)
	r.IconButton(c, keyPlus, icPlus, W-46.5*k, 45*k, hit, r.tone(0x5F5D62, 0xA3A7B8), r.tone(0x1E2227, 0xECEDF3), r.nTabs < maxTabs)
}

// drawTab builds tab i: hover background, the stacked-card icon and the title.
func (r *Rex) drawTab(c *ui.Context, i int, act, absX, tabW float32) {
	k := r.K
	t := &r.tabs[i]
	rc := r.tabRects[i]
	key := tabKey(t.uid)
	hot := c.HotTOf(key)
	ox, oy := r.capsule[0], r.capsule[1]

	theme.At(c, rc[0]-ox, rc[1]-oy, rc[2]-rc[0], rc[3]-rc[1])
	c.NextBG(theme.ColA(0xFFFFFF, theme.Lerp(0.55, 0.10, r.th.T)*hot*(1-act)))
	c.NextRadius(30 * k)
	// The arrow, as over AppKit's tabs and segments: no pointing hand.
	tb := c.BoxKeyed(ui.DrawBackground|ui.Clip|ui.Clickable|ui.HotAnimation|ui.ActiveAnimation|fl, key, nil)
	c.Push(tb)
	defer c.Pop()
	tx, ty := rc[0], rc[1]

	// Held down: the tab darkens under the pointer (a segment's press), on
	// the pill as on an inactive tab. It selects on the press already.
	if p := c.ActiveTOf(key); p > 0.004 {
		theme.At(c, 0, 0, rc[2]-rc[0], rc[3]-rc[1])
		c.NextBG(r.colA(0x3A2A4A, 0xFFFFFF, 0.09*p, 0.08*p))
		c.NextRadius(30 * k)
		c.BoxKeyed(ui.DrawBackground|fl, ui.KeyN(key, 6), nil)
	}
	c.PushOpacity(theme.Lerp(inactiveOpacity, 1, r.keyW))
	dimmed := true
	defer func() {
		if dimmed {
			c.PopOpacity()
		}
	}()

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
		theme.At(c, theme.Snap(x0+st.step*fj*k)-tx, theme.Snap((27+3*fj)*k)-ty, theme.Snap(x0+(st.step*fj+45)*k)-theme.Snap(x0+st.step*fj*k), theme.Snap((65-3*fj)*k)-theme.Snap((27+3*fj)*k))
		c.NextBG(theme.Col(st.backs[j-1]))
		c.NextRadius(8 * k)
		c.BoxKeyed(ui.DrawBackground|fl, kg.Next(), nil)
	}
	fx0, fy0, fx1, fy1 := theme.Snap(x0), theme.Snap(27*k), theme.Snap(x0+45*k), theme.Snap(65*k)
	flags, disp := r.icons.Prepare(c, st.icon, theme.Col(st.symbol))
	theme.At(c, fx0-tx, fy0-ty, fx1-fx0, fy1-fy0)
	c.NextBG(st.front)
	c.NextRadius(8 * k)
	c.NextBorder(st.border, max(1, k))
	c.NextShadow(ui.Shadow{OffY: 1.5 * k, Sigma: 2 * k, Color: 0x00000030})
	c.BoxKeyed(flags|ui.DrawBackground|ui.DrawBorder|ui.DrawShadow|fl, kg.Next(), disp)

	// Title: the name, then the lighter path; text that does not fit fades out.
	nameCol := r.col(theme.MixRGB(0x5A5A60, 0x1E2227, act), theme.MixRGB(0xA9ACBB, 0xF1F2F8, act))
	pathCol := r.col(theme.MixRGB(0x928F95, 0x1E2227, act), theme.MixRGB(0x8D91A8, 0xF1F2F8, act))
	if r.backdrop {
		// Over the material: label colours whose alpha keeps AppKit's
		// contrast floors, dimmed by colour rather than opacity.
		c.PopOpacity()
		dimmed = false
		nameCol = r.label(theme.Lerp(r.labelAlpha(labelTab), r.labelAlpha(labelTitle), act))
		pathCol = r.label(theme.Lerp(r.labelAlpha(labelSecondary), r.labelAlpha(labelTitle), act))
	}
	x := absX + st.textX*k
	end := rc[2] - 10*r.Scale // right limit of the title
	base := 55 * k
	room := end - x
	if room <= 4 {
		return
	}
	nw := r.Width(r.th.Font.UI, t.name)
	r.titleRun(c, &kg, t.name, x-tx, base-ty, nameCol, min(nw, room), nw > room)
	if px := x + nw; px < end-4 {
		pw := r.Width(r.th.Font.UI, t.path)
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
	r.Put(c, kg, r.th.Font.UI, s, x, baseline, color, w, fx)
}

// label is the text colour of the titlebar over a backdrop: black (light) or
// white (dark) at alpha a, as the system's label colours are.
func (r *Rex) label(a float32) uint32 {
	return theme.ColA(r.tone(0x000000, 0xFFFFFF), a)
}

// The titlebar's text over a backdrop must keep AppKit's contrast floors
// (measured on 27.2; TestBandTextContrastOverGlass):
//
//   - in the key window, the title block and the chosen tab at least
//     secondaryLabelColor's on the window background: 3.95:1 light
//     (black 0.498 over white), 5.84:1 dark (white 0.549 over #1E1E1E);
//   - the other tabs, and everything while the window is not key, at least
//     AppKit's own inactive window title: 1.98:1 light (#B8B8B8 on white),
//     2.25:1 dark (#5C5C5C on #262626; D0 captures, finder and chat).
//
// The background is the glass under the theme's scrim (and, for the tabs,
// the capsule's tint), whose range was measured from screen captures over a
// light and a dark window. Each role has a label alpha per look in the key
// window and one when it is not; the key state animates between them.
type labelRole uint8

const (
	labelTitle     labelRole = iota // the host name, the chosen tab
	labelTab                        // another tab's name
	labelSecondary                  // the machine name, a tab's path
)

var labelAlphas = [3]struct {
	key [2]float32
	off float32
}{
	labelTitle:     {key: [2]float32{0.85, 0.92}, off: 0.52},
	labelTab:       {key: [2]float32{0.78, 0.86}, off: 0.48},
	labelSecondary: {key: [2]float32{0.64, 0.84}, off: 0.44},
}

// capsuleAlpha is how much the tab capsule's tint covers the material,
// light and dark.
var capsuleAlpha = [2]float32{0.5, 0.55}

// labelAlpha is the alpha of role's text now: the key window's for the look
// (mixed light to dark), toward the not-key one as the window loses key.
func (r *Rex) labelAlpha(role labelRole) float32 {
	a := labelAlphas[role]
	return theme.Lerp(a.off, theme.Lerp(a.key[0], a.key[1], r.th.T), r.keyW)
}

// bandLabel is role's text colour now.
func (r *Rex) bandLabel(role labelRole) uint32 { return r.label(r.labelAlpha(role)) }
